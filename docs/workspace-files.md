# Project Files tab

The project workspace's **Files** tab (`#files`) browses and edits the published
Markdown notes tree. It does not represent a local repository or original record
attachments. Empty projects show an empty state until notes are published.

- Filter by path; folders expand with click or arrow keys. Up/Down, Home/End,
  Enter/Space and typing a filename navigate the tree. Only the visible rows and
  the focused row mount, including for the 10,000-file notes limit.
- CodeMirror provides line numbers, Markdown highlighting, search, folding and
  undo/redo. Save or Cmd/Ctrl-S saves only the selected file.
- Drafts, undo history, selection, filter and folder expansion survive file and
  project switches in the current session. Reload/close warns about unsaved
  changes. Sign-out asks before discarding drafts. Drafts are not persisted to
  browser storage; download them for a durable local copy.
- Refresh retains dirty files. If the same file changed remotely or was removed,
  saving is blocked; download the draft and discard to use the fetched version.
  A failed refresh or save keeps the current document available. Edits typed
  during a save remain dirty after that save completes.

## API

`GET /v1/projects/{project_id}/notes/export` returns the current snapshot.
`POST /v1/projects/{project_id}/notes/sync` accepts:

```json
{"expected_revision": 12, "files": [{"path": "topics/example.md", "content": "# Example\n"}]}
```

The write requires an authenticated user with write scope and project membership.
It patches only submitted paths through the existing notes service lock and
validation. The response is the full export with its new revision. Stale writes
return HTTP 409 with `notes_revision_mismatch`. No force overwrite or deletion
route is added. Notes organizer publications use the same revision boundary.

Deploy both backend and frontend for remote editing. The UI retains drafts if
used with an older backend lacking the write endpoint. Editor bundle rebuild
instructions are in `frontend/editor-src/README.md`.
