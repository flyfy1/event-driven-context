# Landing page

## Scope

- Target user: an individual moving a project forward across multiple AI conversations.
- User job: understand Context’s value, inspect a concrete example, and enter the existing workspace.
- Riskiest assumption: readers can distinguish the existing recording foundation from the planned context and skill workflows.
- P1 loop: product introduction → switch an illustrative question → inspect its source → open the workspace in the selected language.
- Success proof: this path works in the browser on desktop and narrow mobile layouts.
- No-gos: implementing the product roadmap, adding real generated results, pricing promises, new authentication, or production deployment.
- Appetite: one static landing page using the existing frontend, with no new dependencies.

## Design and implementation

The page follows the product document’s central promise: a new conversation should not require repeating the project’s background. A light surface, violet emphasis, and a dark conversation example make the transition from original records to task-specific context visible.

The example is explicitly illustrative. Three question buttons change the answer and highlight the relevant original records. Links jump to the corresponding source. Existing capabilities, the next validation loop, and later exploration are labeled separately.

`frontend/index.html` is the public entry point. The previous page is preserved in `frontend/workspace.html`, with its original scripts and styles. Landing content lives in `landing-copy.js`; language resolution and the preference key are shared with the workspace. Workspace links explicitly carry locale and any local API override.

## Local validation

- Browser interactions at 1440px and 390px for English, Simplified Chinese, Malay, and Hindi: language switching, example selection, reload persistence, workspace navigation, and localized empty-login validation.
- All four locales checked at 320px without horizontal overflow.
- Source link navigates to the corresponding original record.
- Desktop and mobile visual inspection; no browser warnings or errors observed.
- JavaScript syntax, locale resource coverage, local asset references, and `git diff --check` pass.

This validates the landing page and its workspace entry, not registration, authenticated project actions, OAuth, backend availability, or the planned product capabilities. No production deployment was performed.

## CLI, MCP and Agent Skill setup

- Target user: someone connecting their terminal or agent to their own Context project.
- User job: install the CLI, authenticate, connect MCP, then reuse project context through an Agent Skill.
- Riskiest assumption: copied examples match the current CLI and MCP implementation and let both tools access the same project.
- P1 loop: homepage setup link → CLI record/query → MCP project discovery/read → Skill-guided read, followed by an explicitly requested append.
- Success proof: real local CLI/MCP round trip plus desktop and narrow-mobile setup interactions.
- Scope: onboarding content, accessible disclosure sections, copy controls and a downloadable Agent Skill. No deployment or changes to personal agent configuration.

The `#connect` section has direct links to `#connect-cli`, `#connect-mcp` and `#connect-skill`. Opening a step link expands its instructions. Code blocks can be copied, with visible status and manual-selection fallback if the Clipboard API fails. Existing four-language support is retained. The Skill download lives in `frontend/skills/event-context/SKILL.md`; its inline preview must match the downloadable file.

Examples start with a self-hosted loopback server, explicitly select a local login config, and reuse that config in the stdio MCP process. Remote HTTPS/Bearer and OAuth requirements are explained separately, without assuming availability of the maintainer's public endpoint. The Agent Skill is separate from the fixed server automation packages.

Validation: built the current CLI and server in a temporary directory; registered and logged in with a temporary account; executed the page's record/query example; launched stdio with the page's JSON configuration and no EDC environment overrides; discovered tools, listed the project, queried events/context, appended through MCP, and read the saved event back through CLI. Temporary backend processes and data were cleaned up. Frontend checks and Skill validation pass. Browser checks cover step links, copying JSON and the localized prompt, Skill download, and all four locales at 390px and 320px without page overflow. This verifies local integration and onboarding, not production availability or an actual model's Skill selection.

Codex client instructions were checked against the [official MCP documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli) and [Skill documentation](https://learn.chatgpt.com/docs/build-skills).
