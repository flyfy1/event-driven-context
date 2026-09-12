(function (root) {
  // Paths, not row indexes or filenames, are identities. No recursion: deep paths
  // and a 10,000-file export cannot overflow the stack.
  function buildTree(files) {
    const nodes = new Map();
    for (const file of files) {
      const parts = file.path.split('/');
      for (let i = 0; i < parts.length; i++) {
        const path = parts.slice(0, i + 1).join('/');
        const folder = i < parts.length - 1;
        if (!nodes.has(path)) nodes.set(path, { path, name: parts[i], parent: parts.slice(0, i).join('/'), folder, depth: i + 1, children: [] });
      }
    }
    const roots = [];
    for (const node of nodes.values()) (node.parent ? nodes.get(node.parent).children : roots).push(node);
    const compare = (a, b) => Number(b.folder) - Number(a.folder) || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
    roots.sort(compare);
    for (const node of nodes.values()) node.children.sort(compare);
    return { nodes, roots };
  }
  function visibleRows(tree, expanded, filter = '') {
    const query = filter.trim().toLowerCase();
    const included = new Set();
    if (query) for (const node of tree.nodes.values()) {
      if (!node.folder && node.path.toLowerCase().includes(query)) {
        let current = node;
        while (current && !included.has(current.path)) { included.add(current.path); current = tree.nodes.get(current.parent); }
      }
    }
    const rows = [], stack = tree.roots.map((node, i, all) => ({ node, pos: i + 1, size: all.length })).reverse();
    while (stack.length) {
      const row = stack.pop(), node = row.node;
      if (query && !included.has(node.path)) continue;
      rows.push({ ...node, pos: row.pos, size: row.size });
      if (node.folder && (query || expanded.has(node.path))) {
        for (let i = node.children.length - 1; i >= 0; i--) stack.push({ node: node.children[i], pos: i + 1, size: node.children.length });
      }
    }
    return rows;
  }
  const api = { buildTree, visibleRows };
  if (typeof module === 'object' && module.exports) module.exports = api;
  root.ContextFileTree = api;
})(typeof window === 'object' ? window : globalThis);
