# Workspace code editor

CodeMirror 6 and ProseMirror are bundled locally for the static workspace.
The Raw Markdown and Visual editor tabs share a draft. CodeMirror provides Markdown
highlighting, line numbers, undo/redo, folding, search, and a Cmd/Ctrl-S binding.
Each open file retains its own editor state while the browser session is alive.

Rebuild the committed browser bundle and its dependency license notices:

```sh
cd frontend/editor-src
npm ci --ignore-scripts
npm run build
npm run test:browser
```

Commit `package-lock.json`, `../vendor/code-editor.js`,
`../vendor/code-editor.css`, and `../vendor/code-editor.LICENSE.txt` together
with changes to the source. The site
needs no runtime package manager, external CDN, or build server. CodeMirror
packages must resolve to one copy of `@codemirror/state` and `@codemirror/view`;
check `npm ls @codemirror/state @codemirror/view` after updates.

The framework-free file tree is in `../file-tree.js`; the controller in
`../workspace-files.js` renders a bounded viewport, retains expansion by full
path, and implements tree keyboard navigation. Run `node --test frontend/*.test.js`
from the repository root for frontend checks.

The build runs Markdown round-trip tests before generating assets. Browser tests
use Playwright; if Chromium is not installed, run `npx playwright install chromium`.
They exercise the actual bundle, including mode switches, formatting commands,
snapshots, and the save shortcut.

Visual editing supports CommonMark headings, emphasis, links, lists, quotes and
code. YAML front matter stays byte-for-byte unchanged. Tables, task lists,
footnotes, directives and HTML are preserved as source blocks for raw editing.
Unsupported nested extensions fall back to raw mode without changing the draft.
Switching modes without editing retains the exact source. Visual edits can
normalize Markdown spelling/spacing; undoing to the original visual document
restores its original source. Switching files retains both modes' state; editing
in one mode invalidates the other mode's stale state so it reparses the new draft.

The frontend deployment excludes `editor-src/` and `node_modules/`; only the
locally built vendor assets are required by the published page.
