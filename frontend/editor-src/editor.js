import { basicSetup } from 'codemirror';
import { EditorState } from '@codemirror/state';
import { EditorView, keymap } from '@codemirror/view';
import { markdown } from '@codemirror/lang-markdown';
import { createVisualEditor } from './visual-editor';

export function create(parent, onChange, onSave, t = key => key) {
  const tabs = document.createElement('div'); tabs.className = 'file-editor-tabs'; tabs.setAttribute('role', 'tablist');
  const rawTab = document.createElement('button'), visualTab = document.createElement('button');
  const rawPane = document.createElement('div'), visualPane = document.createElement('div');
  const message = document.createElement('p'); message.className = 'file-editor-mode-message'; message.setAttribute('role', 'status'); message.hidden = true;
  for (const [button, pane, mode] of [[visualTab, visualPane, 'visual'], [rawTab, rawPane, 'raw']]) {
    button.type = 'button'; button.setAttribute('role', 'tab'); button.id = 'file-editor-' + mode + '-tab';
    button.setAttribute('aria-controls', 'file-editor-' + mode + '-pane');
    pane.id = 'file-editor-' + mode + '-pane'; pane.className = 'file-editor-pane'; pane.setAttribute('role', 'tabpanel'); pane.setAttribute('aria-labelledby', button.id);
    button.addEventListener('click', () => switchMode(mode));
    tabs.append(button);
  }
  parent.replaceChildren(tabs, message, rawPane, visualPane);
  let current = null, preferredMode = 'raw', visual = null;
  function changed(content, mode) {
    if (!current) return;
    current.content = content;
    // The other mode must parse the changed Markdown, not reopen a stale snapshot.
    current[mode === 'raw' ? 'visual' : 'raw'] = null;
    onChange(content);
  }
  const extensions = [basicSetup, markdown(), EditorView.lineWrapping,
    keymap.of([{ key: 'Mod-s', run: () => { onSave(); return true; } }]),
    EditorView.updateListener.of(update => { if (update.docChanged) changed(update.state.doc.toString(), 'raw'); }),
    EditorView.contentAttributes.of({ 'aria-label': 'File contents' }),
    EditorView.theme({ '&': { height: '100%', fontSize: '13px' }, '.cm-scroller': { overflow: 'auto', fontFamily: 'var(--mono)' }, '.cm-content': { padding: '16px 0' }, '.cm-gutters': { background: '#f8faf7', borderRight: '1px solid #d8ddd5' } })];
  const rawView = new EditorView({ parent: rawPane, state: EditorState.create({ extensions }) });
  function stash() {
    if (!current) return;
    if (current.mode === 'visual' && visual) current.visual = visual.snapshot();
    else current.raw = { state: rawView.state, scroll: rawView.scrollDOM.scrollTop };
  }
  function render() {
    const mode = current?.mode || 'raw';
    rawPane.hidden = mode !== 'raw'; visualPane.hidden = mode !== 'visual';
    [[rawTab, 'raw'], [visualTab, 'visual']].forEach(([button, name]) => {
      button.setAttribute('aria-selected', String(name === mode)); button.tabIndex = name === mode ? 0 : -1;
    });
    rawView.requestMeasure();
  }
  function show(mode) {
    if (mode === 'visual') {
      if (!visual) visual = createVisualEditor(visualPane, value => changed(value, 'visual'), onSave, t);
      visual.open(current.content, current.visual);
    } else {
      rawView.setState(current.raw?.state || EditorState.create({ doc: current.content, extensions }));
      rawView.scrollDOM.scrollTop = current.raw?.scroll || 0;
    }
    current.mode = mode; message.hidden = true; render();
  }
  function openMode(mode) {
    try { show(mode); }
    catch (error) {
      show('raw'); message.textContent = t(error.message === 'visualMetadataIncomplete' ? 'visualMetadataIncomplete' : 'visualUnsupported'); message.hidden = false;
    }
  }
  function switchMode(mode) {
    if (!current || mode === current.mode) return;
    stash(); preferredMode = mode; openMode(mode);
    (current.mode === 'raw' ? rawTab : visualTab).focus();
  }
  tabs.addEventListener('keydown', event => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    switchMode(event.key === 'Home' ? 'visual' : event.key === 'End' ? 'raw' : current.mode === 'raw' ? 'visual' : 'raw');
  });
  function translate() {
    tabs.setAttribute('aria-label', t('fileEditorMode'));
    rawTab.textContent = t('fileEditorRaw'); visualTab.textContent = t('fileEditorVisual');
    if (visual) visual.translate();
  }
  translate(); render();
  return {
    snapshot() { stash(); return { ...current }; },
    open(content, saved) {
      current = saved && saved.content === content ? { ...saved } : { content, mode: preferredMode, raw: null, visual: null };
      openMode(preferredMode);
    },
    translate,
    measure: () => rawView.requestMeasure(),
    destroy() { rawView.destroy(); if (visual) visual.destroy(); parent.replaceChildren(); }
  };
}
