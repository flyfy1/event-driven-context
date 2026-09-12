# Connect your coding agent to Event-driven Context

## Target project

`{{.Project}}`

{{if .HasProject}}The project ID above is explicit. Use it in every project-scoped call, even if this document was downloaded without its URL. This guide does not prove that the project exists or that the user can access it; verify membership after OAuth.{{else}}No project was selected. Stop and ask the user for an existing project before configuring or reading context. PROJECT_ID below is a placeholder, not a real project. Do not create a project implicitly.{{end}}

## Goal

Connect this working directory to the user’s existing account through remote MCP, install the official recorder Skill, and verify the exact project with a real read. Configuration files alone are not proof.

- MCP endpoint: `{{.MCPURL}}`
- Recorder Skill: `{{.SkillURL}}`

Never ask the user to paste an access token into a conversation. Use OAuth in their browser. Inspect existing configuration, preserve unrelated entries, and do not enable automatic capture hooks without a separate request.

## 1. Configure remote MCP

Choose the client running this task. If an event-context entry already exists, check its endpoint and preserve unrelated settings; do not silently overwrite an entry pointing to another service.

### Codex

Inspect first. Only add the server if the entry is missing:

```sh
codex mcp get event-context
codex mcp add event-context \
  --url {{.MCPURL}} \
  --oauth-resource {{.MCPURL}}
```

Complete OAuth with both scopes. Open the authorization page for the user when needed and wait for confirmation from the command:

```sh
codex mcp login event-context --scopes context:read,context:write
```

### Claude Code

Inspect first. Only add the server if the entry is missing:

```sh
claude mcp get event-context
claude mcp add --transport http --scope local \
  event-context {{.MCPURL}}
```

Open /mcp inside Claude Code, choose event-context, and complete OAuth in the browser. Use protected-resource discovery and dynamic registration; do not insert static tokens or client secrets.

## 2. Install the recorder Skill

Read and download the exact Skill from the URL above. Save it in the current project at the path for this client:

- Codex: `.agents/skills/edc-recorder/SKILL.md`
- Claude Code: `.claude/skills/edc-recorder/SKILL.md`

Compare any existing file before replacing it; preserve user edits. Never commit OAuth credentials or private client state.

## 3. Verify the real connection

Reconnect MCP or open a fresh agent session so the server and Skill are loaded. Call list_projects and confirm the target project ID is present. Then call query_events with exactly:

```json
{
  "project_id": "{{.Project}}",
  "limit": 5
}
```

If available, call list_state for the same project; read the brief and report its lag. Preserve source references. Report the project name and the Event UUIDs actually returned. Empty history is a valid result; do not invent an Event.

If access is denied or the project is missing, check the OAuth account and membership. Do not switch projects or create one as a workaround. Finish only after a real read succeeds, and do not write a test Event unless explicitly requested.
