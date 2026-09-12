# Agent-organized project notes

Status: implemented initial organizer. One project-level agent maintains all four lenses in one draft and publication. Users can export notes through the read-only API and CLI; user editing and history UI remain future work. See [notes-sync.md](notes-sync.md).

## Document collection

Notes are readable views over immutable Events; one Event may support several lenses. Every ordinary note has stable frontmatter and a visible summary:

```markdown
---
id: note_hierarchical_retrieval
title: Hierarchical retrieval
---
# Hierarchical retrieval
> Summary: Agents narrow the tree before reading detailed evidence.

- Selective reads keep context bounded. [source](edc-event://019f...)
- Related: [[note_notes_architecture|Notes architecture]]
```

IDs remain stable when paths or titles change. Event links name a UUID in the same project. The publisher rejects missing or duplicate IDs, unresolved wiki links, unknown citations, missing titles or summaries, and loss of an existing ID. It requires each ordinary file to contain fewer than 1,000 whitespace-delimited words and stay below 1 MiB.

Each `index.md` summarizes its branch and links immediate children. The organizer updates affected ancestors. Direct search can jump to a matching note without traversing every index.

## Four lenses

```text
notes/
├── index.md
├── daily/<year>/<month>/<date>.md
├── persons/<person-id>/index.md
├── topics/{work,life}/<subject>/index.md
└── goals/
    ├── priorities.md
    └── <goal-id>/{index,tasks,todos,resources,timeline}.md
```

- `daily/` records what happened and when. It prefers the source's effective date, then `occurred_at`, then `recorded_at`, in project time. Corrections and late Events revise the relevant day.
- `persons/` records supported facts by stable identity and then useful facets. Similar names are not merged without evidence.
- `topics/` explains reusable knowledge under `work/` or `life/`. A cross-cutting subject has one primary home and links from the other category.
- `goals/` describes desired outcomes and delivery state. `priorities.md` ranks current attention. A goal can add tasks, immediate todos, resources, timeline, and nested subgoals. Large goals split into independently understandable outcomes without inventing commitments.

The organizer seeds the root and base indexes, `topics/work`, `topics/life`, `goals/priorities.md`, and four policies on its first non-noop run. It does not create empty person, topic, or goal folders merely to fill the structure.

## Editable organization policy

Each base folder has `organization.md` with exactly this frontmatter, substituting its own lens:

```yaml
---
schema_version: 1
lens: daily
body_style: bullet_points
organization_word_limit: 499
default_note_word_limit: 999
---
```

Additional keys or changed invariant values are rejected. The remaining nonblank lines must be flat `- ` bullets, with fewer than 500 words in the complete file. The organizer follows these bullets as lower-priority guidance and may adapt them to observed data. They cannot change authorization, tools, citations, validation, or publication. Event content and ordinary note bodies remain reference data, not instructions.

The files are visible through export. Local sync is pull-only, so user edits are not published. A validated notes editor is still planned.

## Organizer run

The `notes-indexer` plugin has `organize_notes` permission and read access to all `note`, `derived`, and `log` Events. A host runs one Codex `gpt-5.6-luna` process at medium reasoning for one project. Watch mode polls every 15 seconds; failures back off to five minutes. Each run has a five-minute default timeout, a twelve-minute server lease, and at most 20 new Events in append-sequence order. The server prevents a concurrent organizer for the same project.

Codex runs ephemerally in a read-only sandbox with user rules, shell, web search, apps, plugins, hooks, memories, browser/computer use, and subagents disabled. Its fixed embedded prompt is versioned separately from policy files. A random bearer secret protects a loopback-only MCP server holding the draft in memory.

The agent can:

- list the 20 Event previews and immediate note children;
- search up to 20 note matches;
- read an outline or at most 120 lines/12,000 characters of one note;
- read one Event in chunks of at most 8,000 characters;
- create, replace, or move draft notes;
- mark every batch Event `used` or `irrelevant`, then validate with `finish`.

A used Event must first be opened with `read_event`; previews are routing hints. The agent starts from the root and four policies, then reads relevant material on demand rather than receiving the whole collection as prompt context. Tools allow at most 500 calls per run.

## Publication and checkpoint

Plugin-only organizer endpoints begin, publish, or cancel a leased run. Ordinary user credentials cannot call them. Begin returns the current tree, checkpoint, timezone, and bounded Event previews. Publish rechecks plugin installation, permission, configuration revision, lease, base notes revision, source access, complete Event accounting, and the entire document tree.

The complete tree and one project-wide checkpoint are written into the same immutable generation before its pointer becomes current. The checkpoint records through-sequence, prompt version, policy hash, and run ID, so a crash cannot advance past unpublished notes. A prompt or policy change causes a run even without new Events. Revision conflicts preserve the current tree and require a fresh run.

Code ownership is split across `internal/notes` for generations and validation, `internal/v2/notes_organizer.go` for authorization and leases, `internal/noteindexer` for the bounded runtime, `internal/processorhost/notes.go` for once/watch operation, `plugins/notes-indexer` for its manifest, and separate public export and plugin-only API handlers. `deploy/production/event-context-notes.service` runs the 15-second project watcher.
