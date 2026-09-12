# Recall project memory with your agent

Install the `memory-recall` skill for your local agent using the authenticated Event-driven Context CLI, or alongside a remote agent's MCP connection. It teaches your agent to explore a local Markdown notes folder, then retrieve source Events when it needs more detail. The CLI or MCP supplies access to records; the skill supplies the workflow. Connecting MCP does not install the skill automatically.

## Choose a skill

| Skill | Use it for |
| --- | --- |
| [`memory-recall`](../frontend/skills/memory-recall/SKILL.md) | Finding previous decisions, facts, people, goals, and history. Recall does not write remote records. |
| [`edc-recorder`](../frontend/skills/edc-recorder/SKILL.md) | Recording useful context during work, following the installed recording policy. |
| [`event-context`](../frontend/skills/event-context/SKILL.md) | General project context and saving decisions or progress when requested. |

Install the skills you need. Invoke `memory-recall` for focused retrieval; it does not need to load another skill. These are user-installed agent instructions, separate from server processors that generate notes or other derived content.

## Install

For Codex, put the distributed file in the working project's skill directory:

```sh
mkdir -p .agents/skills/memory-recall
cp /absolute/path/to/event-driven-context/frontend/skills/memory-recall/SKILL.md \
  .agents/skills/memory-recall/SKILL.md
```

Replace the source repository path. You can also download the file from the website's Skill setup section and save it at that destination. Review and merge an existing installation before replacing it. For other agents, use their supported skill directory and invocation mechanism. Reload the client's skills or restart it if the new skill is not visible.

The client exposes the skill's name and description for discovery and loads its instructions when selected. Explicit invocation makes the intended workflow clear:

```text
$memory-recall In project PROJECT_ID, what did we decide about the launch date,
and what is still unresolved? Cite the notes or Events you use.
```

The agent reuses the project selected for the task or the CLI directory binding. If the project is ambiguous, it asks you to choose. No project-specific edits to the skill are required.

## Local notes and source Events

For filesystem retrieval, the agent needs an authenticated `edc` CLI on its machine, using the same server and account as the MCP connection. A remote MCP login does not configure local CLI authentication. Complete CLI login privately in your terminal when needed.

By default, the skill syncs notes to `.context/notes/PROJECT_ID/` inside the working project. You can specify a different folder in your request. Use a separate folder per server and project, and exclude this local cache from version control in your working repository.

```sh
edc notes sync --project PROJECT_ID --output .context/notes/PROJECT_ID
```

The agent reads indexes and searches relevant files under `daily/`, `persons/`, `topics/`, and `goals/`, expanding only when needed. It leaves local edits alone and excludes preserved local-only files from published evidence.

When a note cites an Event, the agent can retrieve that Event through MCP or the CLI:

```sh
edc get --project PROJECT_ID EVENT_ID
edc query --project PROJECT_ID --limit 20
```

Global flags such as `--server` and `--config` precede the command. Event queries return chronological pages; the agent follows cursors or narrows filters as needed. Notes can lag behind recent Events, so syncing is not a guarantee of current coverage.

If there are no published notes, or the agent has only remote MCP access, it recalls directly from Events. Installing this skill does not create notes, install a notes indexer, or enable automatic recording. See [notes sync](notes-sync.md) for the local download contract.

## Check it works

Ask about a known past decision. The agent should cite actual note paths or Event IDs, retrieve supporting Events when needed, and distinguish unresolved information from confirmed decisions. You can verify a cited Event with `edc get --project PROJECT_ID EVENT_ID`. Recall should create no remote records; the local notes cache may be downloaded or refreshed.

For a fictional-data example using the real CLI and an independent agent, see the [local retrieval trial and reproduction steps](evals/memory-recall-local.md).
