(function () {
  const { buildTree, visibleRows } = window.ContextFileTree;
  const rowHeight = 32;
  window.createFilesWorkspace = function ({ request, t }) {
    const $ = selector => document.querySelector(selector);
    const sessions = new Map();
    let session = null, editor = null, epoch = 0, rows = [];
    const treeElement = $('#files-tree'), spacer = $('#files-tree-spacer');
    function makeSession(id) {
      return { id, revision: 0, loaded: false, busy: false, request: 0, docs: new Map(), expanded: new Set(), selected: '', focused: '', filter: '', scroll: 0, message: '', tree: buildTree([]) };
    }
    function errorMessage(error) { return error.code === "service_unavailable" ? t("filesUnavailable") : error.message; }
    function dirty(doc) { return doc.content !== doc.base; }
    function stash() {
      if (session && editor && session.docs.has(session.selected)) session.docs.get(session.selected).editorState = editor.snapshot();
      if (session) session.scroll = treeElement.scrollTop;
    }
    function ensureEditor() {
      if (!editor) {
        if (!window.ContextCodeEditor) throw new Error(t('filesEditorUnavailable'));
        editor = window.ContextCodeEditor.create($('#files-editor'), content => {
          const doc = session && session.docs.get(session.selected);
          if (!doc) return;
          doc.content = content;
          renderStatus(); renderRows();
        }, save);
      }
    }
    function open(path) {
      const doc = session.docs.get(path);
      if (!doc) return;
      stash(); session.selected = path; session.focused = path;
      let parent = session.tree.nodes.get(path)?.parent;
      while (parent) { session.expanded.add(parent); parent = session.tree.nodes.get(parent)?.parent; }
      try { ensureEditor(); editor.open(doc.content, doc.editorState); }
      catch (error) { session.message = error.message; }
      renderTree();
    }
    function merge(s, snapshot) {
      if (!snapshot || snapshot.project_id !== s.id || !Array.isArray(snapshot.files) || !Number.isSafeInteger(snapshot.revision)) throw new Error(t('filesInvalidResponse'));
      const next = new Map();
      for (const file of snapshot.files) {
        const old = s.docs.get(file.path);
        if (old && dirty(old)) {
          old.conflict = old.base !== file.content && old.content !== file.content;
          // A refresh also recovers an acknowledged-late or lost save response.
          if (old.content === file.content) { old.base = file.content; old.conflict = false; }
          old.remote = file.content;
          next.set(file.path, old);
        } else if (old && old.content === file.content) {
          old.conflict = false; old.remote = file.content; next.set(file.path, old);
        } else next.set(file.path, { content: file.content, base: file.content, remote: file.content, conflict: false });
      }
      for (const [path, doc] of s.docs) if (!next.has(path) && dirty(doc)) {
        doc.conflict = true; doc.remote = null; next.set(path, doc);
      }
      s.docs = next; s.revision = snapshot.revision; s.loaded = true;
      s.tree = buildTree(Array.from(next.keys(), path => ({ path })));
      if (!next.has(s.selected)) s.selected = '';
    }
    async function refresh() {
      const s = session;
      if (!s || s.busy) return;
      stash(); s.busy = true; s.message = ''; const generation = epoch, ticket = ++s.request;
      renderStatus();
      try {
        const snapshot = await request('/v1/projects/' + encodeURIComponent(s.id) + '/notes/export');
        if (epoch !== generation || ticket !== s.request) return;
        if (s === session) stash();
        merge(s, snapshot);
        if (!s.selected && s.docs.size) {
          s.selected = s.docs.has('index.md') ? 'index.md' : s.docs.keys().next().value;
          let parent = s.tree.nodes.get(s.selected).parent;
          while (parent) { s.expanded.add(parent); parent = s.tree.nodes.get(parent).parent; }
        }
      } catch (error) { if (epoch === generation) s.message = errorMessage(error); }
      finally {
        if (epoch === generation) {
          s.busy = false;
          if (s === session) { renderTree(); if (s.selected) showSelected(); renderStatus(); }
        }
      }
    }
    function showSelected() {
      const doc = session.docs.get(session.selected);
      if (!doc) return;
      try { ensureEditor(); editor.open(doc.content, doc.editorState); }
      catch (error) { session.message = error.message; }
    }
    async function save() {
      const s = session, path = s && s.selected, doc = s && s.docs.get(path);
      if (!doc || s.busy || doc.conflict || !dirty(doc)) return;
      if (new TextEncoder().encode(doc.content).length > 1048576) { s.message = t('filesTooLarge'); renderStatus(); return; }
      const content = doc.content, generation = epoch;
      s.busy = true; s.message = ''; renderStatus();
      try {
        const snapshot = await request('/v1/projects/' + encodeURIComponent(s.id) + '/notes/sync', {
          method: 'POST', body: { expected_revision: s.revision, files: [{ path, content }] }
        });
        if (epoch !== generation) return;
        if (s === session) stash();
        // Edits made while saving remain dirty against the version just saved.
        doc.base = content;
        merge(s, snapshot); s.message = t('filesSaved');
      } catch (error) {
        if (epoch === generation) s.message = error.code === 'notes_revision_mismatch' ? t('filesStale') : errorMessage(error);
      } finally {
        if (epoch === generation) {
          s.busy = false;
          if (s === session) { renderTree(); renderStatus(); }
        }
      }
    }
    function renderStatus() {
      const s = session, doc = s && s.docs.get(s.selected);
      $('#files-path').textContent = doc ? s.selected : t('filesChoose');
      $('#files-editor').classList.toggle('hidden', !doc || !editor);
      $('#files-placeholder').classList.toggle('hidden', Boolean(doc && editor));
      $('#files-placeholder').textContent = s && s.busy && !s.loaded ? t('filesLoading') : s && s.loaded && !s.docs.size ? t('filesEmpty') : t('filesChoose');
      $('#files-refresh').disabled = !s || s.busy;
      $('#files-save').disabled = !doc || s.busy || doc.conflict || !dirty(doc);
      $('#files-discard').disabled = !doc || s.busy || (!dirty(doc) && !doc.conflict);
      $('#files-download').disabled = !doc;
      $('#files-status').textContent = !s ? '' : t('filesCount', { count: s.docs.size, revision: s.revision }) + (doc ? ' · ' + t(doc.conflict ? 'filesConflict' : dirty(doc) ? 'filesUnsaved' : 'filesClean') : '');
      $('#files-message').textContent = s ? (doc && doc.conflict ? t('filesConflictHint') : s.message) : '';
      $('#files-tree-empty').textContent = s && s.loaded && !rows.length ? t(s.filter ? 'filesNoMatches' : 'filesEmpty') : '';
    }
    function renderTree() {
      if (!session) { rows = []; spacer.replaceChildren(); return; }
      rows = visibleRows(session.tree, session.expanded, session.filter);
      if (!rows.some(row => row.path === session.focused)) session.focused = rows.find(row => row.path === session.selected)?.path || rows[0]?.path || '';
      spacer.style.height = rows.length * rowHeight + 'px';
      renderRows(); renderStatus();
    }
    function renderRows() {
      spacer.replaceChildren();
      if (!session) return;
      const start = Math.max(0, Math.floor(treeElement.scrollTop / rowHeight) - 6);
      const end = Math.min(rows.length, start + Math.ceil((treeElement.clientHeight || 480) / rowHeight) + 12);
      const activeIndex = rows.findIndex(row => row.path === session.focused);
      const indexes = new Set(Array.from({ length: Math.max(0, end - start) }, (_, i) => i + start));
      // Keep the active descendant mounted even when scrolled outside the viewport.
      if (activeIndex >= 0) indexes.add(activeIndex);
      for (const i of indexes) {
        const row = rows[i], item = document.createElement('div');
        item.id = 'file-row-' + i; item.className = 'file-tree-row'; item.setAttribute('role', 'treeitem');
        item.setAttribute('aria-level', row.depth); item.setAttribute('aria-posinset', row.pos); item.setAttribute('aria-setsize', row.size);
        item.setAttribute('aria-selected', String(session.selected === row.path));
        if (row.folder) item.setAttribute('aria-expanded', String(Boolean(session.filter.trim()) || session.expanded.has(row.path)));
        item.classList.toggle('focused', session.focused === row.path);
        item.style.top = i * rowHeight + 'px'; item.style.paddingLeft = 10 + (row.depth - 1) * 16 + 'px';
        item.title = row.path;
        const icon = document.createElement('span'); icon.className = 'file-tree-icon'; icon.setAttribute('aria-hidden', 'true');
        icon.textContent = row.folder ? (session.filter.trim() || session.expanded.has(row.path) ? '▾' : '▸') : '≡';
        const label = document.createElement('span'); label.className = 'file-tree-name'; label.textContent = row.name;
        item.append(icon, label);
        const doc = session.docs.get(row.path);
        if (doc && dirty(doc)) { const marker = document.createElement('span'); marker.textContent = '●'; marker.setAttribute('aria-label', t('filesUnsaved')); item.append(marker); }
        item.addEventListener('click', () => { session.focused = row.path; treeElement.focus(); activate(row); });
        spacer.append(item);
      }
      if (activeIndex >= 0) treeElement.setAttribute('aria-activedescendant', 'file-row-' + activeIndex);
      else treeElement.removeAttribute('aria-activedescendant');
    }
    function activate(row) {
      if (row.folder) { if (session.expanded.has(row.path)) session.expanded.delete(row.path); else session.expanded.add(row.path); renderTree(); }
      else open(row.path);
    }
    function focusRow(index) {
      const row = rows[Math.max(0, Math.min(rows.length - 1, index))];
      if (!row) return;
      session.focused = row.path;
      const top = rows.indexOf(row) * rowHeight;
      if (top < treeElement.scrollTop) treeElement.scrollTop = top;
      else if (top + rowHeight > treeElement.scrollTop + treeElement.clientHeight) treeElement.scrollTop = top + rowHeight - treeElement.clientHeight;
      renderRows();
    }
    let typed = '', typedAt = 0;
    treeElement.addEventListener('keydown', event => {
      if (!session || !rows.length) return;
      const index = rows.findIndex(row => row.path === session.focused), row = rows[Math.max(0, index)];
      if (['ArrowDown', 'ArrowUp', 'ArrowRight', 'ArrowLeft', 'Home', 'End', 'Enter', ' '].includes(event.key)) event.preventDefault();
      if (event.key === 'ArrowDown') focusRow(index + 1);
      else if (event.key === 'ArrowUp') focusRow(index - 1);
      else if (event.key === 'Home') focusRow(0);
      else if (event.key === 'End') focusRow(rows.length - 1);
      else if (event.key === 'Enter' || event.key === ' ') activate(row);
      else if (event.key === 'ArrowRight' && row.folder) {
        if (!session.expanded.has(row.path) && !session.filter.trim()) { session.expanded.add(row.path); renderTree(); } else focusRow(index + 1);
      } else if (event.key === 'ArrowLeft') {
        if (row.folder && session.expanded.has(row.path) && !session.filter.trim()) { session.expanded.delete(row.path); renderTree(); }
        else { const parent = rows.findIndex(item => item.path === row.parent); if (parent >= 0) focusRow(parent); }
      } else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
        typed = Date.now() - typedAt < 700 ? typed + event.key.toLowerCase() : event.key.toLowerCase(); typedAt = Date.now();
        for (let offset = 1; offset <= rows.length; offset++) { const next = (index + offset) % rows.length; if (rows[next].name.toLowerCase().startsWith(typed)) { focusRow(next); break; } }
      }
    });
    treeElement.addEventListener('scroll', renderRows);
    new ResizeObserver(() => { renderRows(); if (editor) editor.measure(); }).observe(treeElement);
    $('#files-filter').addEventListener('input', event => { if (!session) return; session.filter = event.target.value; treeElement.scrollTop = 0; renderTree(); });
    $('#files-refresh').addEventListener('click', refresh);
    $('#files-save').addEventListener('click', save);
    $('#files-discard').addEventListener('click', () => {
      const doc = session && session.docs.get(session.selected);
      if (!doc || session.busy || !window.confirm(t('filesDiscardConfirm'))) return;
      if (doc.remote === null) { session.docs.delete(session.selected); session.selected = ''; session.tree = buildTree(Array.from(session.docs.keys(), path => ({ path }))); }
      else { doc.content = doc.base = doc.remote; doc.conflict = false; doc.editorState = null; showSelected(); }
      session.message = ''; renderTree(); renderStatus();
    });
    $('#files-download').addEventListener('click', () => {
      const doc = session && session.docs.get(session.selected); if (!doc) return;
      const url = URL.createObjectURL(new Blob([doc.content], { type: 'text/markdown;charset=utf-8' }));
      const link = document.createElement('a'); link.href = url; link.download = session.selected.split('/').pop(); link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
    });
    window.addEventListener('beforeunload', event => { if (Array.from(sessions.values()).some(s => Array.from(s.docs.values()).some(dirty))) { event.preventDefault(); event.returnValue = ''; } });
    return {
      hasDrafts() { return Array.from(sessions.values()).some(s => Array.from(s.docs.values()).some(dirty)); },
      select(id, load = true) {
        stash();
        session = id ? (sessions.get(id) || makeSession(id)) : null;
        if (session) sessions.set(id, session);
        $('#files-filter').value = session ? session.filter : '';
        renderTree(); treeElement.scrollTop = session ? session.scroll : 0;
        if (session && session.selected) showSelected();
        renderStatus();
        if (load && session && !session.loaded) refresh();
      },
      show() { renderTree(); if (editor) editor.measure(); if (session && !session.loaded) refresh(); },
      translate() { renderStatus(); renderRows(); },
      clear() { epoch++; session = null; sessions.clear(); if (editor) { editor.destroy(); editor = null; } renderTree(); renderStatus(); }
    };
  };
})();
