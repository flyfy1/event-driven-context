# Files in notes and local recall

Status: accepted implementation contract. Work and validation are underway in an isolated worktree; this document does not claim deployment. It extends [notes design](notes-design.md), [sync](notes-sync.md), and [memory recall](memory-recall.md).

## Coverage model

Three independent facts must stay visible:

- Notes coverage is the atomically published pair `notes.revision` and `notes.through_sequence`.
- Catalog coverage is the independent `catalog.through_sequence`, frozen when pagination starts. It can be newer than notes.
- Bytes coverage says which catalog files are verified locally. Metadata-only sync is never complete bytes coverage.

An attachment available in the catalog is not necessarily described in notes or downloaded. `--files all` covers attachment bytes for one catalog snapshot, not the complete Event archive.

## Notes and file evidence

Notes can place `[Recording](edc-file://FILE_ID)` beside `[source](edc-event://EVENT_ID)`. File identity comes from `FILE_ID`; display filenames never determine storage paths. One file can appear in daily, person, topic, and goal views without duplicating bytes.

A file link identifies an attachment but does not prove its content. Claims cite the Event containing evidence. Extracted or transcribed claims cite the derived Event, whose references preserve the original attachment chain. Filename and MIME type alone support no content claim.

The organizer may link any same-project file referenced by a readable Event at or below its run `through_sequence`, including historical Events outside the current batch. It must link every file Event's attachment, even when usable extraction is still pending. Publication rejects foreign, unattached, unreadable, or newer file references. The organizer gets metadata through bounded `list_files`, `file_metadata`, and `file_references` tools; it never receives or reads attachment bytes.

Prompt version 2 starts automatic backfill for projects whose notes predate attachment support. Missing `attachment_backfill_through_sequence` means zero. A run contains at most 20 Events: up to ten historical file Events, then new Events in the remaining slots; with no historical candidates, all 20 slots can be new. Backfill scans through the prior main checkpoint, then advances with new batches once caught up. Historical Events enrich notes without rewinding main coverage. Both checkpoints advance only in the same atomic publication and use the same lease, accounting, and failure rules.

## File catalog API

All routes require existing V2 read authentication: user callers need project membership, while plugin callers stay project-bound and see only granted Event types.

```text
GET /v1/projects/{project_id}/files/catalog
GET /v1/projects/{project_id}/files/{file_id}/metadata
GET /v1/projects/{project_id}/files/{file_id}/references
```

The first catalog page fixes `through_sequence`; its signed cursor carries that boundary and query identity across later pages. A client can explicitly supply the same boundary for metadata and reference reads. The catalog contains files attached to readable Events through the boundary; unattached uploads are excluded.

Each catalog entry includes existing `FileInfo`, `reference_count`, the latest five `references`, and `references_complete`. A reference has Event ID, sequence, type, and relation (`attachment` or `derived_from`). When previews are incomplete, `/references` returns deterministic historical sequence-ordered pages with the same frozen boundary. `/metadata` returns one scoped entry without file bytes. Cursors cannot widen project access or move the snapshot boundary.

## Local collection and commands

```text
COLLECTION/
├── notes/                         # Published Markdown tree
├── files/FILE_ID/
│   ├── metadata.json              # Exact name, MIME, size, hash, references
│   └── original-SAFE_NAME.ext     # Optional verified original bytes
└── manifest.json                  # Binding and three completion records
```

`manifest.json` is a self-checksummed envelope binding schema version, canonical server origin, and project ID. It separately records notes revision/through-sequence/completion, catalog through-sequence/cursor/completion, and bytes through-sequence/completion. Per-file self-checksummed metadata records the catalog entry, frozen boundary, local filename, download state, and verified hash. The local name has a fixed `original-` prefix; the display basename is sanitized and deterministically truncated with a hash when needed, while metadata retains the exact original filename. Counts such as downloaded, cached, unavailable, conflicting, and pending belong in command output, not the growing manifest. Machine JSON is outside Markdown word limits.

```sh
edc sync --project PROJECT_ID --output COLLECTION
edc sync --project PROJECT_ID --output COLLECTION --files all
edc file get --project PROJECT_ID --cache COLLECTION FILE_ID
```

Default sync updates notes plus the complete frozen catalog and metadata. `--files all` also caches every catalog byte. `file get --cache` downloads one file on demand, verifies server size and SHA-256, and atomically registers its metadata and cached status. Its fresh catalog lookup does not mark whole-catalog or whole-bytes completion. Flags must precede `FILE_ID`; `--cache` and `-o` are mutually exclusive.

The collection rejects a different server/project binding, traversal, symlinks, and unsafe file IDs. Notes use whole-run conflict preflight and preserve local edits. Catalog metadata uses atomic rename; verified bytes use an atomic install that cannot overwrite an existing file. Existing verified bytes are skipped; existing mismatched bytes are not overwritten. Interrupted runs retain individually verified files but leave the affected completion record false, so retry can resume.

Existing `edc notes sync` remains pull-only. The new collection defaults to `.context/projects/PROJECT_ID/` in recall instructions. It never moves, rewrites, or deletes an old `.context/notes/` cache.

## Implementation acceptance

Implementation spans scoped V2 catalog queries, organizer file tools and validation, typed client methods, and local collection/cache logic. Verify project isolation, frozen pagination during new Events, references beyond five, shared-file associations, historical organizer references, backfill retries, stale notes versus newer catalog, safe cache paths, checksum failure, interrupted resume, three separate completion states, metadata-only sync, on-demand caching, and a synthetic `--files all` run. Deployment and production evidence are recorded only after these checks pass.
