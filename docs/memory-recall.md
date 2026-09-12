# Recall project memory with your agent

Install the `memory-recall` skill for a local agent with an authenticated Event-driven Context CLI. It teaches selective reading of notes and attachment metadata, then source Event retrieval when needed. MCP is optional and does not install the skill.

## Install

For Codex, copy the distributed skill into the working project:

```sh
mkdir -p .agents/skills/memory-recall
cp /absolute/path/to/event-driven-context/frontend/skills/memory-recall/SKILL.md \
  .agents/skills/memory-recall/SKILL.md
```

Review and merge an existing installation before replacing it, then reload the client's skills. Other agents use their supported skill directory and invocation mechanism.

Invoke it with a project ID or a directory already bound to one:

```text
$memory-recall In project PROJECT_ID, what did we decide about launch timing?
```

If no binding selects one project, the agent lists accessible projects and asks only when the choice remains ambiguous.

## Local collection

The default collection is `.context/projects/PROJECT_ID/` in the working repository:

```text
.context/projects/PROJECT_ID/
├── notes/
├── files/FILE_ID/{metadata.json,original-SAFE_NAME.ext}
└── manifest.json
```

Exclude it from version control. Sync notes and attachment metadata before recall:

```sh
edc sync --project PROJECT_ID --output .context/projects/PROJECT_ID
```

Add `--files all` only when all catalog attachment bytes should be available offline. For one attachment:

```sh
edc file get --project PROJECT_ID \
  --cache .context/projects/PROJECT_ID FILE_ID
```

Command flags precede `FILE_ID`. Cache mode verifies the original size and SHA-256 and cannot be combined with `-o`.

The collection manifest binds a canonical server origin and project ID. The agent uses that binding with the CLI and does not assume a separate MCP connection points to the same server or project. A remote MCP login does not configure local CLI authentication. Complete CLI login privately; never paste credentials into chat.

Notes, catalog, and bytes have separate completion states. A newer catalog does not make notes current, and metadata does not mean bytes are local. An old `.context/notes/` directory is a separate legacy cache: the workflow never moves, rewrites, or deletes it.

## Retrieval workflow

Start from `notes/index.md`, a relevant lens, or a targeted `rg`. Read branch indexes and focused sections rather than the whole tree:

- `daily/`: what happened and when;
- `persons/`: supported information about people;
- `topics/`: reusable work and life knowledge;
- `goals/`: priorities, outcomes, tasks, and progress.

Resolve `[[note_ID|label]]` by frontmatter ID. Only files represented by the current collection manifest are published notes; treat preserved local-only Markdown as user material, not published evidence.

Use the authenticated CLI first for source evidence:

```sh
edc get --project PROJECT_ID EVENT_ID
edc query --project PROJECT_ID --limit 20
```

Global `--server` and `--config` flags precede the command. Query pages are chronological and filtered rather than keyword search; follow cursors with unchanged filters when needed. An explicitly connected MCP can be a fallback only after confirming its project and server context.

For `[label](edc-file://FILE_ID)`, inspect `files/FILE_ID/metadata.json`, follow `data.file.references` for claims, and use `data.local_filename` when bytes are present. If bytes are absent, use cache mode above. Select a reader appropriate to the declared MIME type; never execute an attachment. A file link proves identity, not its content. Derived transcription or extraction remains Event evidence linked to the original file.

## Answer from evidence

Cite note paths or Event IDs. Distinguish facts, proposals, corrections, and unresolved conflicts. Report notes `through_sequence`, catalog coverage, and local byte availability when they limit the answer. Sync does not generate notes or cache the full Event archive.

Recall creates no remote records and does not reorganize notes, install processors, or enable capture hooks. If CLI access fails, describe existing data as cached and unverified. If no local collection exists, use a confirmed MCP connection when available; otherwise report that source retrieval is unavailable.
