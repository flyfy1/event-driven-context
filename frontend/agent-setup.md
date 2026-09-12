# Connect local Codex or Claude Code to Event-driven Context

This static guide is generic. For a guide with the selected project and language embedded, open `https://context-api.integ.life/agent-setup.md?project=YOUR_PROJECT_ID&locale=en`. If no project ID is available, stop and ask the user for it. Do not infer or create a project.

## Goal

Use the authenticated `edc` CLI directly from local Codex or Claude Code. Do not add an MCP server for a local coding agent. The CLI reads its own private login configuration and calls the Event-driven Context API.

- API server: `https://context-api.integ.life`
- Recorder Skill: `https://context.integ.life/skills/edc-recorder/SKILL.md`

Never open the private CLI configuration, print its access token, or ask the user to paste a token into chat. If login is required, ask the user to run `edc login` privately in their terminal.

## 1. Verify the CLI login

Use the same `edc` executable and configuration the user logged in with:

```sh
edc --server https://context-api.integ.life whoami
edc --server https://context-api.integ.life project list
```

If `whoami` is not authenticated, stop and ask the user to run:

```sh
edc --server https://context-api.integ.life login
```

Do not inspect the credential file yourself.

## 2. Bind the working directory and install the Skill

```sh
edc --server https://context-api.integ.life link YOUR_PROJECT_ID
```

Install the official Skill after reading and comparing any existing file:

- Codex: `.agents/skills/edc-recorder/SKILL.md`
- Claude Code: `.claude/skills/edc-recorder/SKILL.md`

For Claude Code, `edc setup claude-code` previews the hook and Skill changes. It does not add MCP. Apply only after reviewing the preview:

```sh
edc --server https://context-api.integ.life setup claude-code
edc --server https://context-api.integ.life setup --apply claude-code
```

The setup command also removes a legacy Event-driven Context entry from `.mcp.json` if one exists, while preserving unrelated MCP entries. Automatic shared-context hooks require separate user confirmation.

## 3. Verify real access

```sh
edc --server https://context-api.integ.life query --project YOUR_PROJECT_ID --limit 5
edc --server https://context-api.integ.life state list --project YOUR_PROJECT_ID
```

Report the actual project, newest Event UUID, and any State lag or access limitation. Configuration alone is not proof. Do not write a test Event unless the user explicitly asks for one.

Remote ChatGPT remains a separate integration and uses the HTTPS MCP endpoint with OAuth.

## Optional: install Memory Recall

For focused retrieval, read `https://context.integ.life/skills/memory-recall/SKILL.md` and save it as `.agents/skills/memory-recall/SKILL.md` (Codex) or `.claude/skills/memory-recall/SKILL.md` (Claude Code), preserving existing edits. Invoke `$memory-recall` with project `YOUR_PROJECT_ID` and your question. It syncs published notes into a local folder, reads relevant files selectively, and retrieves source Events with the CLI when needed. Recall does not enable recording hooks or write remote records.
