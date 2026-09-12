# Workspace code editor

CodeMirror 6 is bundled locally for the static workspace. It provides Markdown
highlighting, line numbers, undo/redo, folding, search, and a Cmd/Ctrl-S binding.
Each open file retains its own editor state while the browser session is alive.

Rebuild the committed browser bundle and its dependency license notices:

```sh
cd frontend/editor-src
npm ci --ignore-scripts
npm run build
```

Commit `package-lock.json`, `../vendor/code-editor.js`, and
`../vendor/code-editor.LICENSE.txt` together with changes to the source. The site
needs no runtime package manager, external CDN, or build server. CodeMirror
packages must resolve to one copy of `@codemirror/state` and `@codemirror/view`;
check `npm ls @codemirror/state @codemirror/view` after updates.

The framework-free file tree is in `../file-tree.js`; the controller in
`../workspace-files.js` renders a bounded viewport, retains expansion by full
path, and implements tree keyboard navigation. Run `node --test frontend/*.test.js`
from the repository root for frontend checks.
