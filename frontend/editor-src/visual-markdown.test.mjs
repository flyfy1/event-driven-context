import { test } from 'node:test';
import { Fragment } from 'prosemirror-model';
import assert from 'node:assert/strict';
import { parseVisualMarkdown, serializeVisualMarkdown, visualSchema } from './visual-markdown.mjs';
function append(parsed, content = 'New paragraph') {
  return parsed.doc.copy(parsed.doc.content.append(Fragment.from(visualSchema.nodes.paragraph.create(null, visualSchema.text(content)))));
}
test('switching modes without edits retains exact Markdown including CRLF metadata and whitespace', () => {
  const source = '---\r\nid: note_1\r\ntitle: "My note"\r\n---\r\n\r\n# Title\r\n\r\n*  one\r\n\r\n';
  const parsed = parseVisualMarkdown(source);
  assert.equal(serializeVisualMarkdown(parsed, parsed.doc), source);
  assert.ok(parsed.prefix.endsWith('---\r\n'));
});
test('visual edits preserve metadata bytes and evidence link targets', () => {
  const source = '---\nid: note_abc\ntitle: Test\n---\n# Test\n\n[source](edc-event://01abc) and [file](edc-file://file_123)\n';
  const parsed = parseVisualMarkdown(source), result = serializeVisualMarkdown(parsed, append(parsed));
  assert.ok(result.startsWith(parsed.prefix));
  assert.match(result, /edc-event:\/\/01abc/); assert.match(result, /edc-file:\/\/file_123/);
  assert.match(result, /New paragraph/);
});
test('HTML stays inert source instead of becoming live DOM', () => {
  const parsed = parseVisualMarkdown('<script>alert(1)</script>\n\nText <img src=x onerror=alert(1)> more');
  assert.equal(parsed.doc.firstChild.type.name, 'raw_block');
  assert.equal(parsed.hasSourceBlocks, true);
  const result = serializeVisualMarkdown(parsed, append(parsed));
  assert.match(result, /<script>alert\(1\)<\/script>/);
  assert.match(result, /<img src=x onerror=alert\(1\)>/);
  assert.equal(visualSchema.nodes.raw_block.spec.toDOM(parsed.doc.firstChild)[0], 'pre');
});
test('tables and task lists remain source blocks through unrelated edits', () => {
  const table = '| Name | Role |\n| --- | --- |\n| Alex | Lead |';
  const tasks = '- [x] Done\n- [ ] Pending';
  const parsed = parseVisualMarkdown(table + '\n\n' + tasks);
  assert.equal(parsed.doc.childCount, 2); assert.equal(parsed.doc.firstChild.type.name, 'raw_block');
  const result = serializeVisualMarkdown(parsed, append(parsed));
  assert.ok(result.includes(table)); assert.ok(result.includes(tasks));
});
test('footnote references and definitions are preserved rather than escaped', () => {
  const source = 'Evidence[^one]\n\n[^one]: footnote text';
  const parsed = parseVisualMarkdown(source), result = serializeVisualMarkdown(parsed, append(parsed));
  assert.match(result, /Evidence\[\^one\]/); assert.match(result, /\[\^one\]: footnote text/);
});
test('fenced code preserves its contents and language after rich edits', () => {
  const parsed = parseVisualMarkdown('```js\nconst value = "<html>";\n```\n');
  const result = serializeVisualMarkdown(parsed, append(parsed));
  assert.match(result, /```js\nconst value = "<html>";\n```/);
});
test('incomplete metadata cannot be silently interpreted as editable body', () => {
  assert.throws(() => parseVisualMarkdown('---\nid: unfinished\n'), /visualMetadataIncomplete/);
});
test('undoing to the original visual document restores the exact source spelling', () => {
  const source = '*a*   **b**\n\n'; const parsed = parseVisualMarkdown(source);
  assert.notEqual(serializeVisualMarkdown(parsed, append(parsed)), source);
  assert.equal(serializeVisualMarkdown(parsed, parsed.doc), source);
});
