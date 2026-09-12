import { basicSetup } from 'codemirror';
import { EditorState } from '@codemirror/state';
import { EditorView, keymap } from '@codemirror/view';
import { markdown } from '@codemirror/lang-markdown';

// Keep one EditorState per open document: switching files retains selection and undo.
export function create(parent, onChange, onSave) {
  const extensions = [basicSetup, markdown(), EditorView.lineWrapping,
    keymap.of([{ key: 'Mod-s', run: () => { onSave(); return true; } }]),
    EditorView.updateListener.of(update => {
      if (update.docChanged) onChange(update.state.doc.toString());
    }),
    EditorView.contentAttributes.of({ 'aria-label': 'File contents' }),
    EditorView.theme({ '&': { height: '100%', fontSize: '13px' }, '.cm-scroller': { overflow: 'auto', fontFamily: 'var(--mono)' }, '.cm-content': { padding: '16px 0' }, '.cm-gutters': { background: '#f8faf7', borderRight: '1px solid #d8ddd5' } })];
  const view = new EditorView({ parent, state: EditorState.create({ extensions }) });
  return {
    snapshot: () => ({ state: view.state, scroll: view.scrollDOM.scrollTop }),
    open: (content, saved) => {
      view.setState(saved ? saved.state : EditorState.create({ doc: content, extensions }));
      view.scrollDOM.scrollTop = saved ? saved.scroll : 0;
      view.requestMeasure();
    },
    measure: () => view.requestMeasure(),
    destroy: () => view.destroy()
  };
}
