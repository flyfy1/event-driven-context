import { Schema } from 'prosemirror-model';
import { schema as commonSchema, defaultMarkdownParser, defaultMarkdownSerializer, MarkdownParser, MarkdownSerializer } from 'prosemirror-markdown';

// HTML is displayed as source, never mounted into the editable document as HTML.
export const visualSchema = new Schema({
  nodes: commonSchema.spec.nodes.append({
    raw_block: {
      group: 'block', atom: true, attrs: { source: {} },
      toDOM: node => ['pre', { class: 'visual-source-block', contenteditable: 'false' }, ['code', node.attrs.source]]
    },
    raw_inline: {
      group: 'inline', inline: true, atom: true, attrs: { source: {} },
      toDOM: node => ['code', { class: 'visual-source-inline', contenteditable: 'false' }, node.attrs.source]
    }
  }),
  marks: commonSchema.spec.marks
});

// Use the maintained CommonMark tokenizer. Keep extensions outside its visual
// schema as opaque source blocks, rather than silently rewriting their meaning.
const tokenizer = defaultMarkdownParser.tokenizer;
tokenizer.set({ html: true });
tokenizer.enable('table');
tokenizer.core.ruler.after('inline', 'preserve_extended_blocks', state => {
  const lines = state.src.split('\n'), output = [];
  for (let i = 0; i < state.tokens.length; i++) {
    const token = state.tokens[i];
    if (token.level !== 0 || !token.map || token.nesting === -1) { output.push(token); continue; }
    const source = lines.slice(token.map[0], token.map[1]).join('\n');
    const preserve = token.type === 'table_open' || /\[\^[^\]]+\]/.test(source) || /^\s*[-*+] \[[ xX]\]/m.test(source) || /^\s*:::/m.test(source);
    if (!preserve) { output.push(token); continue; }
    const raw = new state.Token('preserved_block', '', 0); raw.content = source;
    output.push(raw);
    if (token.nesting === 1) {
      let nesting = 1;
      while (nesting && i + 1 < state.tokens.length) { i++; nesting += state.tokens[i].nesting; }
    }
  }
  state.tokens = output;
});

export const visualParser = new MarkdownParser(visualSchema, tokenizer, {
  ...defaultMarkdownParser.tokens,
  preserved_block: { node: 'raw_block', getAttrs: token => ({ source: token.content }) },
  html_block: { node: 'raw_block', getAttrs: token => ({ source: token.content.replace(/\n$/, '') }) },
  html_inline: { node: 'raw_inline', getAttrs: token => ({ source: token.content }) }
});
export const visualSerializer = new MarkdownSerializer({
  ...defaultMarkdownSerializer.nodes,
  raw_block(state, node) { state.write(node.attrs.source); state.closeBlock(node); },
  raw_inline(state, node) { state.write(node.attrs.source); }
}, defaultMarkdownSerializer.marks);

export function parseVisualMarkdown(source) {
  // Preserve front matter including its exact delimiters and line endings.
  const frontMatter = source.match(/^(?:\uFEFF)?---[^\S\r\n]*\r?\n[\s\S]*?\r?\n(?:---|\.\.\.)[^\S\r\n]*(?:\r?\n|$)/);
  const prefix = frontMatter ? frontMatter[0] : '';
  if (/^(?:\uFEFF)?---[^\S\r\n]*\r?\n/.test(source) && !prefix) throw new Error('visualMetadataIncomplete');
  const body = source.slice(prefix.length);
  const doc = visualParser.parse(body);
  let hasSourceBlocks = false;
  doc.descendants(node => { if (node.type.name.startsWith('raw_')) hasSourceBlocks = true; });
  return { source, prefix, doc, newline: source.includes('\r\n') ? '\r\n' : '\n', trailing: /\r?\n$/.test(source), hasSourceBlocks };
}
export function serializeVisualMarkdown(parsed, doc) {
  // Viewing, switching modes, and undoing to the original document are lossless.
  if (doc.eq(parsed.doc)) return parsed.source;
  const body = visualSerializer.serialize(doc).replace(/\n/g, parsed.newline);
  return parsed.prefix + body + (parsed.trailing && body ? parsed.newline : '');
}
