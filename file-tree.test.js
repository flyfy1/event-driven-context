const { test } = require('node:test');
const assert = require('node:assert/strict');
const { buildTree, visibleRows } = require('./file-tree');

test('tree uses full paths, keeps same-name files distinct and sorts directories first', () => {
  const tree = buildTree([{ path: 'index.md' }, { path: 'z/index.md' }, { path: 'a/index.md' }, { path: '__proto__/x.md' }]);
  const rows = visibleRows(tree, new Set(['a', 'z']));
  assert.deepEqual(rows.map(x => x.path), ['__proto__', 'a', 'a/index.md', 'z', 'z/index.md', 'index.md']);
  assert.equal(tree.nodes.get('a/index.md').depth, 2);
});
test('filter reveals matching ancestors without changing expansion and excludes unrelated siblings', () => {
  const expanded = new Set(['other']);
  const tree = buildTree([{ path: 'topics/nested/one.md' }, { path: 'topics/nested/two.md' }, { path: 'other/one.md' }]);
  assert.deepEqual(visibleRows(tree, expanded, 'NESTED/ONE').map(x => x.path), ['topics', 'topics/nested', 'topics/nested/one.md']);
  assert.deepEqual([...expanded], ['other']);
  assert.deepEqual(visibleRows(tree, expanded, 'missing'), []);
});
test('10,000 files retain stable order and collapsed folders do not render descendants', () => {
  const files = Array.from({ length: 10000 }, (_, i) => ({ path: `topics/${String(i).padStart(5, '0')}.md` }));
  const tree = buildTree(files.reverse());
  assert.equal(visibleRows(tree, new Set()).length, 1);
  const rows = visibleRows(tree, new Set(['topics']));
  assert.equal(rows.length, 10001);
  assert.equal(rows[1].path, 'topics/00000.md');
  assert.equal(rows[10000].path, 'topics/09999.md');
});
