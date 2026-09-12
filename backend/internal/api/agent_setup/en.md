# Connect your local coding agent to Event-driven Context

## Target project

`{{.Project}}`

{{if .HasProject}}The project ID above is explicit. Use it in every project command, even if this document was downloaded without its URL. This guide does not prove that the project exists or that the user can access it; verify access with the CLI.{{else}}No project was selected. Stop and ask the user for an existing project before configuring or reading context. PROJECT_ID below is a placeholder, not a real project. Do not create a project implicitly.{{end}}

## Goal

Connect local Codex or Claude Code through the authenticated `edc` CLI, install the official recorder Skill, and verify the exact project with a real CLI read. Do not configure MCP for a local coding agent.

- API server: `{{.APIURL}}`
- Recorder Skill: `{{.SkillURL}}`

The CLI reads its own private login config. Never open that file, copy or print its token, or ask the user to paste a token into the conversation. Inspect existing project files before changing them and preserve unrelated content.

## 1. Locate and verify the CLI

Use an existing `edc` on `PATH`, or the repository's executable `./bin/edc`. Do not download and execute an unknown binary. Confirm the command and existing login without exposing credentials:

```sh
edc help
edc --server {{.APIURL}} whoami
```

If `whoami` is not authenticated, pause and ask the user to complete `edc --server {{.APIURL}} login --username USERNAME` privately in their terminal. If they use a non-default config, reuse the exact `--config /absolute/path/to/config.json` they provide on every command. Do not inspect the config to recover the token.

## 2. Bind the directory and verify the project

Run these commands from the working directory:

```sh
edc --server {{.APIURL}} project list
edc --server {{.APIURL}} link {{.Project}}
edc --server {{.APIURL}} status
```

If the project is missing or access is denied, check the logged-in account and membership. Do not switch projects or create one as a workaround.

## 3. Install the recorder Skill

Read the exact Skill from the URL above. Compare an existing target before replacing it and preserve user edits.

- Codex: save it as `.agents/skills/edc-recorder/SKILL.md`.
- Claude Code: preview `edc --server {{.APIURL}} setup claude-code`; after reviewing the exact changes, run `edc --server {{.APIURL}} setup --apply claude-code`. This installs the Skill and optional capture hooks; it does not add MCP. Shared-project hooks require separate explicit confirmation.

Never commit CLI credentials, private config, or client-local state.

## 4. Prove the direct CLI connection

Read the selected project through `edc`, not through MCP:

```sh
edc --server {{.APIURL}} query --project {{.Project}} --limit 5
edc --server {{.APIURL}} state list --project {{.Project}}
```

Report the actual project name, returned Event UUIDs, and any State lag or coverage limitation. Empty history is a valid result; do not invent an Event. Setup is complete only after a real CLI read succeeds. Do not write a test Event unless the user explicitly asks for a write test.

## Optional: install Memory Recall

For focused retrieval, read `{{.RecallSkillURL}}` and save it as `.agents/skills/memory-recall/SKILL.md` (Codex) or `.claude/skills/memory-recall/SKILL.md` (Claude Code), preserving existing edits. Invoke `$memory-recall` with project `{{.Project}}` and your question. It syncs published notes into a local folder, reads relevant files selectively, and retrieves source Events with the CLI when needed. Recall does not enable recording hooks or write remote records.
