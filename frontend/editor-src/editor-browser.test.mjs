import { before, after, test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { chromium } from 'playwright';

let browser;
before(async () => { browser = await chromium.launch({ headless: true }); });
after(async () => { await browser?.close(); });
const bundle = await readFile(new URL('../vendor/code-editor.js', import.meta.url), 'utf8');
const source = '---\nid: note_test\n---\n\n# Title\n\nA **bold** paragraph with [evidence](edc-event://123).\n';
async function fixture(t) {
  const page = await browser.newPage(); const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  t.after(async () => { await page.close(); assert.deepEqual(errors, []); });
  await page.setContent('<div id="editor" style="height:500px;display:flex;flex-direction:column"></div><style>[hidden]{display:none!important}.file-editor-pane{flex:1;min-height:0}.ProseMirror{min-height:150px;white-space:pre-wrap}</style>');
  await page.addScriptTag({ content: bundle });
  await page.evaluate(source => {
    window.changes = []; window.saved = [];
    window.editor = ContextCodeEditor.create(document.getElementById('editor'), value => changes.push(value), () => saved.push(editor.snapshot().content));
    editor.open(source);
  }, source);
  return page;
}

test('real browser: tab changes alone are lossless and formatting commands run', async t => {
  const page = await fixture(t);
  await page.getByRole('tab', { name: 'fileEditorVisual' }).click();
  assert.equal(await page.locator('.ProseMirror h1').innerText(), 'Title');
  assert.deepEqual(await page.evaluate(() => changes), []);
  await page.locator('.ProseMirror p').click();
  await page.getByRole('button', { name: 'visualHeading', exact: true }).click();
  assert.equal(await page.locator('.ProseMirror h2').count(), 1);
  assert.match(await page.evaluate(() => changes.at(-1)), /## A \*\*bold\*\* paragraph/);
  await page.getByRole('button', { name: 'visualUndo', exact: true }).click();
  assert.equal(await page.evaluate(() => changes.at(-1)), source);
  await page.getByRole('tab', { name: 'fileEditorRaw' }).click();
  assert.equal(await page.evaluate(() => editor.snapshot().content), source);
});

test('real browser: raw and visual edits share content and save shortcut', async t => {
  const page = await fixture(t);
  await page.locator('.cm-content').fill('# Raw change\n\nBody');
  await page.getByRole('tab', { name: 'fileEditorVisual' }).click();
  assert.equal(await page.locator('.ProseMirror h1').innerText(), 'Raw change');
  await page.locator('.ProseMirror p').click(); await page.keyboard.press('End'); await page.keyboard.type(' visual');
  await page.keyboard.press('ControlOrMeta+s');
  assert.match(await page.evaluate(() => saved.at(-1)), /Body visual/);
  await page.getByRole('tab', { name: 'fileEditorRaw' }).click();
  assert.match(await page.locator('.cm-content').innerText(), /Body visual/);
});

test('real browser: a saved snapshot restores its own document after file switches', async t => {
  const page = await fixture(t);
  await page.getByRole('tab', { name: 'fileEditorVisual' }).click();
  await page.locator('.ProseMirror h1').click(); await page.keyboard.press('End'); await page.keyboard.type(' draft');
  await page.evaluate(() => { window.first = editor.snapshot(); editor.open('# Second file'); });
  assert.equal(await page.locator('.ProseMirror h1').innerText(), 'Second file');
  await page.evaluate(() => editor.open(first.content, first));
  assert.equal(await page.locator('.ProseMirror h1').innerText(), 'Title draft');
});

test('real browser: invalid metadata falls back without losing content', async t => {
  const page = await fixture(t);
  await page.locator('.cm-content').fill('---\nid: incomplete\n');
  await page.getByRole('tab', { name: 'fileEditorVisual' }).click();
  assert.equal(await page.getByRole('tab', { name: 'fileEditorRaw' }).getAttribute('aria-selected'), 'true');
  assert.equal(await page.locator('.file-editor-mode-message').textContent(), 'visualMetadataIncomplete');
  assert.equal(await page.evaluate(() => editor.snapshot().content), '---\nid: incomplete\n');
});
