# Connect Claude Code or Codex to Event-driven Context

This is a generic, static setup guide. Query parameters on this static file do not change its contents. For a guide with the target project ID and language written into the Markdown, use `https://context-api.integ.life/agent-setup.md?project=YOUR_PROJECT_ID&locale=en`, replacing `YOUR_PROJECT_ID` with the existing project ID. The workspace's Agent setup link opens this dynamic guide automatically.

If you only have this generic guide, obtain the target project ID explicitly from the user before proceeding. Do not infer a project or create one implicitly.

## Goal

Connect the current working directory to the user's existing Event-driven Context account through remote MCP, install the official recorder Skill, and prove access to the target project with a real read. Configuration files alone are not proof.

Use these fixed service resources:

- MCP endpoint: `https://context-api.integ.life/mcp`
- Recorder Skill: `https://context.integ.life/skills/edc-recorder/SKILL.md`

Never ask the user to paste an access token into the conversation. Authentication uses OAuth in the user's browser. Inspect existing configuration before changing it, preserve unrelated entries, and do not enable automatic capture hooks unless the user separately asks for them.

## 1. Configure remote MCP

Choose the instructions for the client that is running this task.

### Codex

First inspect any existing entry with `codex mcp get event-context`. If it is missing, add the remote server:

```sh
codex mcp add event-context \
  --url https://context-api.integ.life/mcp \
  --oauth-resource https://context-api.integ.life/mcp
```

Then start OAuth with both project scopes:

```sh
codex mcp login event-context --scopes context:read,context:write
```

Open the browser authorization page for the user when needed. Do not claim authentication succeeded until the command confirms it.

### Claude Code

First inspect any existing entry with `claude mcp get event-context`. If it is missing, add the remote server for the current working project:

```sh
claude mcp add --transport http --scope local \
  event-context https://context-api.integ.life/mcp
```

Open `/mcp` inside Claude Code, choose `event-context`, and complete the OAuth flow in the user's browser. The server uses standard protected-resource discovery and dynamic client registration; do not add a static token or client secret.

## 2. Install the recorder Skill

Read the official Skill before installing it:

`https://context.integ.life/skills/edc-recorder/SKILL.md`

Save that exact file in the current project at the client-specific path:

- Codex: `.agents/skills/edc-recorder/SKILL.md`
- Claude Code: `.claude/skills/edc-recorder/SKILL.md`

If a file already exists at the target path, compare it first. Preserve user changes or ask before replacing a materially different Skill. Do not commit OAuth credentials or other private client state to the repository.

## 3. Verify the real connection

Reconnect the MCP server or start a fresh agent session so the new MCP and Skill are loaded. Then:

1. Call `list_projects` and confirm the explicitly selected project ID is present.
2. Call `query_events` for that exact project ID with a small limit.
3. If the project has State, call `list_state` and report any `lag`; State does not replace the source Events.
4. Report the project name, the newest Event UUID returned, and any access or coverage limitation.

If the project is missing, check that OAuth used the same Integ.Life account shown in the web workspace and that the account is a project member. Do not create another project as a workaround.

Setup is complete only after the real MCP read succeeds. Do not write a test Event unless the user explicitly asks for a write test.
