# Notes export and local sync

For an agent workflow over the downloaded folder, install the [memory-recall skill](memory-recall.md). It uses ordinary filesystem reads and retrieves source Events through the CLI or MCP when needed.

Status: implemented read-only sync. The CLI pulls the organizer's published Markdown tree for local reading. It never uploads local content or changes remote notes. User editing, backlinks, and history UI remain future work.

## Published storage

Each project has immutable complete generations:

```text
data/v2/projects/<project-id>/
├── notes -> notes-system/revisions/<revision>-<random>/notes
└── notes-system/revisions/<revision>-<random>/
    ├── publication.json
    └── notes/...
```

The notes indexer writes and validates a complete generation plus its checkpoint metadata, then atomically switches `notes` to it. Metadata records revision, publishing actor and time, and organizer checkpoint. The existing V2 writer lock and service mutex serialize publication. Direct edits to server files are unsupported.

Only root `index.md` and `.md` files below `daily/`, `persons/`, `topics/`, and `goals/` are exported. Only the four base folders may contain `organization.md`. The service rejects invalid or escaping paths, symlinks, non-UTF-8 content, files over 1 MiB, more than 10,000 files, and exports over 16 MiB encoded.

## HTTP contract

`GET /v1/projects/{project_id}/notes/export`

The route uses existing V2 user authentication, read scope, and project membership; it does not accept plugin credentials. Separate plugin-only organizer routes publish validated generations internally.

```json
{
  "project_id": "prj_example",
  "revision": 7,
  "through_sequence": 193,
  "files": [{
    "path": "goals/priorities.md",
    "content": "# Priorities\n",
    "sha256": "b4f..."
  }]
}
```

Paths are slash-separated relative to `notes/` and bytewise sorted. `sha256` is lowercase hexadecimal SHA-256 of the exact UTF-8 bytes in `content`. `revision` and `through_sequence` come from the same atomic publication. A project with no published notes returns both as zero and `files: []`. The public API has no notes write endpoint.

## CLI contract

```text
edc notes sync --project PROJECT_ID --output DIRECTORY
```

The command uses existing `--server`, `--config`, authentication, and directory binding. `--project` may be omitted only when the existing binding resolves one project. `--output` is required.

The output directory contains `.edc-notes-sync.json` with version, canonical server origin, project ID, last observed revision/through-sequence pair, and hashes of remote files successfully mirrored locally. Metadata from another server or project is rejected. Writes use temporary files in the destination directory, and local symlinks are never followed.

The CLI fetches one complete export and preflights every path before writing:

- Missing local file: create it from the remote copy.
- Local content equal to remote: adopt it without rewriting.
- Tracked local content equal to its prior hash while remote changed: replace it with the remote copy.
- Local content different from both its prior hash and remote: report a conflict.
- Same path without prior metadata and different contents: report a conflict.
- Local `.md` path absent remotely: preserve it, report it in `preserved_local`, and exclude it from the new remote baseline.

Any conflict exits nonzero and performs no note or metadata writes. A conflict never overwrites the local edit. After a conflict-free run, the CLI installs remote changes and replaces metadata last. It never deletes local files, propagates local deletions, uploads local files, or calls a remote write endpoint. Output JSON reports revision, through-sequence, downloaded count, restored paths, and `preserved_local` paths.

## Project collection sync

This accepted extension is in progress and not yet claimed deployed or validated. The aggregate command keeps notes, attachment metadata, and optional bytes together:

```sh
edc sync --project PROJECT_ID --output COLLECTION
edc sync --project PROJECT_ID --output COLLECTION --files all
```

```text
COLLECTION/
├── notes/...
├── files/FILE_ID/{metadata.json,original-SAFE_NAME.ext}
└── manifest.json
```

Default sync pulls notes and every page of one frozen file catalog. `--files all` also downloads all catalog bytes. The self-checksummed manifest binds canonical server origin and project ID and records three independent completion states: notes revision plus `through_sequence`, catalog through-sequence/cursor, and bytes through-sequence. Counts belong in command output. Catalog completion never implies notes coverage; metadata completion never implies bytes are local.

Notes retain the conflict behavior above. Machine metadata uses verified temporary writes and atomic rename; each verified byte inode is installed with a no-clobber hardlink. Interrupted work may retain verified files but leaves the relevant completion false for retry. Existing `edc notes sync` remains supported. Recall defaults new collections to `.context/projects/PROJECT_ID/`; sync never migrates, modifies, or deletes old `.context/notes/` directories.

One attachment can be cached on demand:

```sh
edc file get --project PROJECT_ID --cache COLLECTION FILE_ID
```

Flags precede `FILE_ID`; `--cache` and `-o` are mutually exclusive. The command freezes a fresh catalog boundary, verifies server size and SHA-256, and atomically registers the cached file and per-file metadata. Bytes use a fixed `original-` prefix plus a safe, deterministically shortened display name; metadata retains the exact filename. The command skips a matching cache, refuses to overwrite mismatched bytes, and does not mark the whole catalog or bytes set complete.

Acceptance covers authorization, atomic notes coverage, project isolation, frozen catalog pagination, export limits, traversal and symlinks, local-edit conflicts, preserved local-only files, separate completion states, cache verification, interrupted resume, and retry. See [the attachment plan](notes-files-plan.md) for catalog and evidence semantics.
