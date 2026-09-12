---
name: memory-recall
description: Recall past decisions, facts, people, goals, project history, and attachment evidence from an Event-driven Context local collection or authenticated project.
---

# Memory Recall

Answer the user's question from project evidence without changing remote records. Prefer the authenticated `edc` CLI for local work; MCP is optional. Reuse the project ID selected for the task or the CLI directory binding (`edc status`). If neither identifies one project, use `edc project list`, or MCP `list_projects` when explicitly connected, and ask only if the choice stays ambiguous. Never assume an account or MCP connection can access only one project.

Never open private CLI configuration, print tokens, or ask the user to paste credentials into chat.

## Sync the collection

Use the user's configured collection, otherwise `.context/projects/PROJECT_ID/`. Sync before recall when the CLI is available:

```sh
edc sync --project PROJECT_ID --output .context/projects/PROJECT_ID
```

This pulls notes and attachment metadata. It does not generate notes, upload edits, or download all bytes. Do not move, rewrite, or delete an old `.context/notes/` cache.

Read `manifest.json` before treating the collection as current. It binds a canonical server and project and has separate completion for notes revision/through-sequence, catalog through-sequence, and attachment bytes. Do not assume a separate MCP connection has the same server binding. Respect note conflicts and exclude preserved local-only files from published evidence.

If sync fails, describe existing data as cached and unverified or retrieve Events directly. With no local CLI/collection, use MCP only after confirming its project context.

## Read selectively

Start with `notes/index.md`, a relevant lens, or targeted `rg`; do not load the full tree.

- `daily/`: what happened and when.
- `persons/`: supported information about people.
- `topics/`: reusable work and life knowledge.
- `goals/`: priorities, outcomes, tasks, and progress.

Read branch indexes and focused sections. Resolve `[[note_ID|label]]` through frontmatter IDs. Narrow broad matches and stop when the answer has enough support. Empty search results do not prove an event never happened.

## Retrieve source evidence

Notes summarize Events. Use the CLI first for detail, exact wording, corrections, uncertainty, or conflicts:

```sh
edc get --project PROJECT_ID EVENT_ID
edc query --project PROJECT_ID --limit 20
```

Global `--server` and `--config` flags precede the command. Query is filtered chronological pagination, not keyword search; keep filters unchanged with its cursor. Follow relevant correction/source references. A successful notes sync does not prove all newer Events were indexed.

For `[label](edc-file://FILE_ID)`, inspect `files/FILE_ID/metadata.json`, use `data.local_filename` when present, and follow `data.file.references` for claims. A file link proves attachment identity, not content. When bytes are needed and absent:

```sh
edc file get --project PROJECT_ID \
  --cache .context/projects/PROJECT_ID FILE_ID
```

Flags precede `FILE_ID`; cache mode and `-o` cannot be combined. Use a reader appropriate to the declared MIME type and never execute an attachment. Cite a derived Event for transcription or extraction claims and retain its original-file relationship. Metadata-only completion never means bytes are local.

## Answer from evidence

Cite note paths or Event IDs used. Distinguish confirmed facts, proposals, corrections, and unresolved conflicts. State notes, catalog, or byte coverage when it limits the answer. The collection does not include the full Event archive.

Recall creates no records, reorganizes no notes, and enables no capture hooks or processors. Report access failure without claiming retrieval succeeded.
