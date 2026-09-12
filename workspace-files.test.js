const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const { translations } = require('./i18n');

function harness(request) {
  const elements = new Map();
  class Element {
    constructor() { this.listeners = {}; this.style = {}; this.classList = { toggle() {} }; this.children = []; this.scrollTop = 0; this.clientHeight = 480; this.value = ''; }
    addEventListener(name, fn) { this.listeners[name] = fn; }
    setAttribute() {} removeAttribute() {} focus() {}
    append(...items) { this.children.push(...items); }
    replaceChildren(...items) { this.children = items; }
  }
  const element = id => { if (!elements.has(id)) elements.set(id, new Element()); return elements.get(id); };
  let current = '', change;
  const window = {
    ContextFileTree: require('./file-tree'),
    ContextCodeEditor: { create(parent, onChange) {
      change = onChange;
      return { snapshot: () => ({ content: current }), open: (content, saved) => { current = saved ? saved.content : content; }, measure() {}, destroy() { current = ''; } };
    } },
    addEventListener() {}, confirm: () => true
  };
  const context = { window, document: { querySelector: element, createElement: () => new Element() }, ResizeObserver: class { observe() {} }, TextEncoder, setTimeout };
  vm.runInNewContext(fs.readFileSync(require.resolve('./workspace-files'), 'utf8'), context);
  const workspace = window.createFilesWorkspace({ request, t: (key, vars = {}) => (translations.en[key] || key).replace(/\{(\w+)\}/g, (_, name) => vars[name] ?? name) });
  return { workspace, element, text: () => current, edit: content => { current = content; change(content); }, click: id => element(id).listeners.click() };
}
const flush = () => new Promise(resolve => setImmediate(resolve));
const snapshot = (id = 'a', content = '# Original', revision = 1) => ({ project_id: id, revision, files: [{ path: 'index.md', content }] });
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };

test('late project read cannot replace the current project and cached drafts survive project switches', async () => {
  const late = deferred();
  const h = harness(path => path.includes('/a/') ? late.promise : Promise.resolve(snapshot('b', '# Birch')));
  h.workspace.select('a'); h.workspace.select('b'); await flush();
  assert.equal(h.text(), '# Birch');
  late.resolve(snapshot()); await flush();
  assert.equal(h.text(), '# Birch');
  h.edit('# Birch draft'); h.workspace.select('a'); assert.equal(h.text(), '# Original');
  h.workspace.select('b'); assert.equal(h.text(), '# Birch draft'); assert.equal(h.workspace.hasDrafts(), true);
});

test('refresh preserves dirty files and rejects same-file remote changes until discarded', async () => {
  let remote = snapshot();
  const h = harness(async () => remote);
  h.workspace.select('a'); await flush(); h.edit('# My draft');
  remote = snapshot('a', '# Other author', 2);
  await h.click('#files-refresh');
  assert.equal(h.text(), '# My draft'); assert.equal(h.element('#files-save').disabled, true);
  assert.match(h.element('#files-message').textContent, /changed remotely/);
  h.click('#files-discard'); assert.equal(h.text(), '# Other author'); assert.equal(h.workspace.hasDrafts(), false);
});

test('failed refresh and failed save retain the draft and allow retry', async () => {
  let fail = false;
  const h = harness(async () => { if (fail) throw new Error('Offline'); return snapshot(); });
  h.workspace.select('a'); await flush(); h.edit('# My draft'); fail = true;
  await h.click('#files-refresh'); assert.equal(h.text(), '# My draft');
  await h.click('#files-save'); assert.equal(h.text(), '# My draft');
  assert.equal(h.element('#files-save').disabled, false); assert.equal(h.element('#files-message').textContent, 'Offline');
});

test('typing during save retains newer content as dirty and subsequent save uses the new revision', async () => {
  const pending = deferred(); const inputs = [];
  const h = harness(async (path, options) => {
    if (!options) return snapshot();
    inputs.push(options.body);
    return inputs.length === 1 ? pending.promise : snapshot('a', options.body.files[0].content, 3);
  });
  h.workspace.select('a'); await flush(); h.edit('# Sent');
  const save = h.click('#files-save'); h.edit('# Newer');
  pending.resolve(snapshot('a', '# Sent', 2)); await save;
  assert.equal(h.text(), '# Newer'); assert.equal(h.element('#files-save').disabled, false);
  await h.click('#files-save');
  assert.equal(inputs[1].expected_revision, 2); assert.equal(inputs[1].files[0].content, '# Newer');
  assert.equal(h.workspace.hasDrafts(), false);
});

test('refresh preserves a removed dirty file until explicit discard', async () => {
  let remote = snapshot(); const h = harness(async () => remote);
  h.workspace.select('a'); await flush(); h.edit('# Keep me');
  remote = { project_id: 'a', revision: 2, files: [] }; await h.click('#files-refresh');
  assert.equal(h.text(), '# Keep me'); assert.equal(h.element('#files-save').disabled, true);
  h.click('#files-discard'); assert.equal(h.workspace.hasDrafts(), false);
  assert.match(h.element('#files-placeholder').textContent, /No published notes/);
});

test('sign-out clears drafts and ignores outstanding responses from the old session', async () => {
  const pending = deferred(); const h = harness(() => pending.promise);
  h.workspace.select('a'); h.workspace.clear(); pending.resolve(snapshot()); await flush();
  assert.equal(h.workspace.hasDrafts(), false); assert.equal(h.text(), '');
  assert.equal(h.element('#files-save').disabled, true);
});

test('unrelated remote edits do not block a dirty file after refresh', async () => {
  let remote = snapshot(); const h = harness(async () => remote);
  h.workspace.select('a'); await flush(); h.edit('# Local');
  remote = { ...snapshot('a', '# Original', 2), files: [...snapshot().files, { path: 'other.md', content: '# Other' }] };
  await h.click('#files-refresh');
  assert.equal(h.text(), '# Local'); assert.equal(h.element('#files-save').disabled, false);
});


test('file service failures never use an audio-specific message', async () => {
  const h = harness(async () => { throw Object.assign(new Error('Audio transcription is not configured'), { code: 'service_unavailable' }); });
  h.workspace.select('a'); await flush();
  assert.match(h.element('#files-message').textContent, /Files are temporarily unavailable/);
});
