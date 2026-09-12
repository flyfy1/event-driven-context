import 'prosemirror-view/style/prosemirror.css';
import { EditorState } from 'prosemirror-state';
import { EditorView } from 'prosemirror-view';
import { baseKeymap, toggleMark, setBlockType, wrapIn, lift, exitCode } from 'prosemirror-commands';
import { history, undo, redo } from 'prosemirror-history';
import { keymap } from 'prosemirror-keymap';
import { wrapInList, splitListItem, liftListItem, sinkListItem } from 'prosemirror-schema-list';
import { visualSchema as schema, parseVisualMarkdown, serializeVisualMarkdown } from './visual-markdown.mjs';

export function createVisualEditor(parent, onChange, onSave, t) {
  const toolbar = document.createElement('div'); toolbar.className = 'visual-format-toolbar'; toolbar.setAttribute('role', 'toolbar');
  const note = document.createElement('p'); note.className = 'visual-editor-note';
  const surface = document.createElement('div'); surface.className = 'visual-editor-surface';
  parent.append(toolbar, note, surface);
  let parsed, view, buttons = [];
  const plugins = [history(), keymap({
    'Mod-s': () => { onSave(); return true; }, 'Mod-z': undo, 'Mod-y': redo, 'Shift-Mod-z': redo,
    'Mod-b': toggleMark(schema.marks.strong), 'Mod-i': toggleMark(schema.marks.em),
    Enter: splitListItem(schema.nodes.list_item), Tab: sinkListItem(schema.nodes.list_item), 'Shift-Tab': liftListItem(schema.nodes.list_item),
    'Mod-Enter': exitCode
  }), keymap(baseKeymap)];
  function updateToolbar() {
    if (!view) return;
    for (const { element, command, mark } of buttons) {
      element.disabled = !command(view.state);
      if (mark) element.setAttribute('aria-pressed', String((view.state.storedMarks || view.state.selection.$from.marks()).some(active => active.type === mark)));
    }
  }
  function addButton(labelKey, text, command, mark) {
    const button = document.createElement('button'); button.type = 'button'; button.className = 'visual-format-button'; button.textContent = text;
    button.addEventListener('mousedown', event => event.preventDefault());
    button.addEventListener('click', () => { command(view.state, view.dispatch, view); view.focus(); updateToolbar(); });
    buttons.push({ element: button, command, mark, labelKey }); toolbar.append(button);
  }
  addButton('visualBold', 'B', toggleMark(schema.marks.strong), schema.marks.strong);
  addButton('visualItalic', 'I', toggleMark(schema.marks.em), schema.marks.em);
  addButton('visualHeading', 'H2', setBlockType(schema.nodes.heading, { level: 2 }));
  addButton('visualParagraph', '¶', setBlockType(schema.nodes.paragraph));
  addButton('visualBulletList', '• ≡', wrapInList(schema.nodes.bullet_list));
  addButton('visualOrderedList', '1. ≡', wrapInList(schema.nodes.ordered_list));
  addButton('visualQuote', '❝', wrapIn(schema.nodes.blockquote));
  addButton('visualCode', '</>', setBlockType(schema.nodes.code_block));
  addButton('visualLift', '⇤', lift);
  addButton('visualUndo', '↶', undo);
  addButton('visualRedo', '↷', redo);
  view = new EditorView(surface, {
    state: EditorState.create({ schema, plugins }),
    dispatchTransaction(transaction) {
      const next = view.state.apply(transaction); view.updateState(next);
      if (transaction.docChanged && parsed) onChange(serializeVisualMarkdown(parsed, next.doc));
      updateToolbar();
    },
    handleDOMEvents: { click(_, event) { if (event.target.closest('a')) { event.preventDefault(); return true; } return false; } },
    attributes: { role: 'textbox', 'aria-multiline': 'true', 'aria-label': t('visualContents') }
  });
  function translate() {
    toolbar.setAttribute('aria-label', t('visualFormatting'));
    buttons.forEach(({ element, labelKey }) => { element.title = t(labelKey); element.setAttribute('aria-label', t(labelKey)); });
    note.textContent = t('visualPreserved');
    view.setProps({ attributes: { role: 'textbox', 'aria-multiline': 'true', 'aria-label': t('visualContents') } });
  }
  translate();
  return {
    open(content, saved) {
      const nextParsed = saved?.parsed || parseVisualMarkdown(content);
      const nextState = saved?.state || EditorState.create({ schema, doc: nextParsed.doc, plugins });
      parsed = nextParsed; view.updateState(nextState);
      surface.scrollTop = saved?.scroll || 0;
      note.hidden = !parsed.prefix && !parsed.hasSourceBlocks;
      updateToolbar();
    },
    snapshot: () => ({ parsed, state: view.state, scroll: surface.scrollTop }),
    translate,
    focus: () => view.focus(),
    destroy: () => { view.destroy(); parent.replaceChildren(); }
  };
}
