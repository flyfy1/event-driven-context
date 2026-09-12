# Event-driven Context

[English](README.md) | [简体中文](README.cn.md)

**Local first: your data and accumulated knowledge belong to you; you choose the models.**

Event-driven Context is a local-first, self-hostable open-source project. Anyone can deploy it on a platform they choose—including a personal computer, NAS, self-managed server, or cloud host—and retain control of records, original files, and access permissions without relying on the maintainers' hosted instance.

Information accumulated across tools and AI conversations should remain available for the long term in an environment the user controls. Models provide compute, while original records and accumulated context are stored independently. If you switch agents or stop using a model provider, existing records remain readable, backupable, and available to other tools.

The project currently provides a Go project-event storage backend, CLI, MCP interface, and controlled runner. Project members can jointly read and write data. Events, original files, and metadata are **append-only and cannot be modified or deleted**; media uploads, deterministic context queries, controlled transcription, and review results all retain their provenance relationships.

The first version directly uses `User → Project → Event` without introducing a separate Context entity. See the [MVP document](docs/mvp.md) for scope and acceptance criteria.

## Instructions for Development Agents

This project is currently in the **MCVP stage**. Agents working in this repository should follow these principles:

- **Do not preserve backward compatibility by default**: do not retain old APIs, compatibility layers, or transitional implementations solely to support existing versions.
- **Choose the best option for current conditions**: use the present requirements, actual runtime environment, and verification goals to select the most appropriate design and implementation.
- **Existing designs may change**: within the user-authorized task scope, refactor architecture, APIs, data formats, and configuration as needed. Do not treat the current implementation as immutable.
- **Advance sound decisions directly**: do not repeatedly request confirmation merely because a change is incompatible. Update the relevant documentation and verification together with the change.

## Local First and Data Control

- **Original data stays on the deployer's filesystem**: every event is an independent JSON file, and uploaded originals are stored separately. SQLite handles identity and authorization only; the deployer chooses the data path.
- **Core capabilities do not depend on a cloud model**: recording, reading, and conditional querying do not call a model. Once built and started locally, these capabilities are available through the local CLI or HTTP API without a model account or externally hosted service.
- **Tools access the same records through shared interfaces**: CLI, HTTP, and MCP use the same project permissions and storage. Users can connect different agents without binding existing data to one conversation client.
- **Backup and migration stay under deployer control**: a consistent backup of the identity database and complete data directory can be moved to another environment under the deployer's control. See "Verification and Current Boundaries" below for consistency requirements.
- **Fixed Skills and the runner are separated**: P1 includes two repository-pinned Skills, server-side scheduling state, and an independent runner. The runner receives only the inputs authorized for one run and does not hold a full-access user token. Results carry platform-controlled provenance. Open third-party plugin installation remains future work.

Here, "local" means that data storage and execution run in an environment controlled by the deployer; a cloud host selected and controlled by the deployer also counts as self-hosting. Local first does not mean data can never leave the device when a cloud model is invoked: content supplied to an external agent through MCP or another tool may enter that provider's model service. Manage persistence location and model-call data flow as separate concerns.

## Product and Design Documents

- [Memory Recall user guide](docs/memory-recall.md): install the retrieval Skill so an Agent can read local Notes on demand and retrieve their source Events.
- [Complete product design](docs/product.md): continuous capture, on-demand context extraction, Skill-based input processing, and scheduled services.
- [Technical design](docs/technical-design.md): data contracts, rules, runner behavior, retrieval, permissions, and failure recovery.
- [User journeys and page flows](docs/ux-flows.md): six user journeys, information architecture, page actions, and error feedback.
- [Mobile app capture flow](docs/mobile-capture-ux.md): automatic save, upload, organization, and offline recovery after recording—the revised primary daily entry point.
- [Agent-organized Notes design](docs/notes-design.md): daily, persons, topics, and goals document views for the same Events, plus organization rules and the local sync contract.
- [Original first-version scope](docs/mvp.md): the storage, authorization, and conditional-query baseline; the current implementation additionally includes media, context, and fixed automated processing flows.

## Getting Started

Requires Go 1.26.5 or later. SQLite uses a pure-Go driver and does not depend on an external database or CGO. `make check` also uses Node.js's built-in runtime to validate frontend locale resources; no npm dependencies need to be installed.

```sh
make build
./bin/edc-server -skill-root backend/skills
./bin/edc-runner help
```

By default, the server listens on `127.0.0.1:8080`. The SQLite identity database is stored at `data/context.db`; event manifests, original files, and the automation journal are written under the Git-ignored `data/` directory. `-skill-root` must explicitly point to the repository's fixed Skill directory to enable the automation API; leaving it empty disables those routes. To change the address or paths:

```sh
./bin/edc-server -addr 127.0.0.1:8090 -db data/context.db -data data -skill-root backend/skills
./bin/edc --server http://127.0.0.1:8090 help
```

`GET /healthz` is used for liveness checks. `SIGINT` / `SIGTERM` stop accepting new requests and wait for in-flight requests to complete.

### Deploy on Your Own Platform

Self-hosting does not depend on an `integ.life` domain or the maintainers' production host. Build and run `edc-server` in the target environment, use `-db` and `-data` to select persistence locations you manage, and connect the CLI to your instance through `--server` or `EDC_SERVER`. The current storage model assumes a single writing service process; do not run multiple service processes against the same data directory.

For local use, the startup commands above are sufficient. For remote access, place an HTTPS reverse proxy in front of the service. For browser access or OAuth, also configure the permitted web origin and your public API address explicitly:

```sh
./bin/edc-server -addr 127.0.0.1:8090 \
  -db data/context.db -data data \
  -skill-root backend/skills \
  -allowed-origins https://context.example.com \
  -public-base-url https://context-api.example.com
```

Replace the example domains with your own. When the reverse proxy connects to the example upstream, set the upstream Host to `127.0.0.1:8090`. Clients connect to `https://context-api.example.com`, and that instance's MCP endpoint is `/mcp`. Purely local use does not require a public OAuth address.

Serve `frontend/` with any static file server. The frontend currently retains the maintainers' default API address; for self-hosting, either change the default in `frontend/app.js` or explicitly select your API with `https://context.example.com/workspace.html?api=https%3A%2F%2Fcontext-api.example.com`. The workspace's web origin must appear in the backend's `-allowed-origins`. When publishing under your own domain, also replace or remove the maintainers' domain in `frontend/CNAME`.

The administration dashboard is available at `/admin/`. Set `EDC_ADMIN_USERS` to a comma-separated list of user IDs, usernames, or verified email addresses. Those users can then use a first-party website session to view registered users, all projects, project statistics, sharing relationships, and installed plugins, and can grant or remove project access or change owner/member roles. Every project must retain at least one owner. Leaving the variable empty disables all administrative access; OAuth and MCP access tokens do not inherit administrator privileges. Example:

```sh
EDC_ADMIN_USERS=alice,owner@example.com ./bin/edc-server ...
```

The `integ-prod` host, `integ.life` domains, and `make deploy-prod` / `make deploy-frontend` commands below describe the maintainers' current deployment workflow. Those scripts contain environment-specific configuration and are neither prerequisites nor general-purpose self-hosting commands.

## CLI: Shared Recording Workflow

Registration and login read the password from the terminal without echoing it; registration also requires a unique email address. Usernames must contain 3–64 lowercase letters, numbers, `_ . -`; passwords must be 12–72 bytes. Passwords cannot be supplied as command-line arguments. Automation can provide passwords through `--password-stdin`.

```sh
./bin/edc register --username alice --email alice@example.invalid
./bin/edc login --username alice
./bin/edc project create --name "Backend Development" --description "Team-shared raw records"
```

Copy the returned project `id` and use it as `PROJECT_ID` in the following commands.

```sh
# A second user uses a separate config file; they can also register and log in from another computer.
./bin/edc --config "$HOME/.config/event-driven-context/bob.json" register --username bob --email bob@example.invalid
./bin/edc --config "$HOME/.config/event-driven-context/bob.json" login --username bob

# Any owner can add a registered member by username or email; the web UI can promote members to owners.
./bin/edc project add-member --project PROJECT_ID --username bob
./bin/edc project add-member --project PROJECT_ID --email bob@example.invalid
./bin/edc project members --project PROJECT_ID

# Bob appends text; the author identity is taken from Bob's authenticated session.
./bin/edc --config "$HOME/.config/event-driven-context/bob.json" record \
  --project PROJECT_ID --text 'Events cannot be modified or deleted' \
  --metadata '{"source":"discussion","tags":["backend","decision"]}' \
  --idempotency-key decision-001

# Raw text file: the type must be declared explicitly.
./bin/edc record --project PROJECT_ID --file ./notes.txt --type text/plain \
  --metadata '{"description":"Meeting notes","document_type":"meeting-notes"}'

# Query by half-open time interval and metadata.
./bin/edc query --project PROJECT_ID \
  --from 2026-09-08T00:00:00+08:00 --to 2026-09-09T00:00:00+08:00 \
  --metadata '{"source":"discussion"}' --exists tags

./bin/edc get --event EVENT_ID
./bin/edc metadata --project PROJECT_ID
./bin/edc metadata --project PROJECT_ID --key source
./bin/edc file --id FILE_ID --output ./downloaded-notes.txt
./bin/edc whoami
./bin/edc logout
```

Global `--server` and `--config` options must appear before the command. Command results are written to stdout; errors and password prompts are written to stderr. File downloads output JSON/base64 by default; `--output -` writes raw bytes to stdout. When a file path is specified, the CLI refuses to overwrite an existing file.

Login stores a token valid for 30 days in `event-driven-context/config.json` under the system user configuration directory, with permissions `0600`. Passwords are not stored and tokens are not printed. The server stores only the SHA256 hash of the token. `logout` immediately revokes the current token; a running stdio process can no longer use it either.

`EDC_SERVER`, `EDC_CONFIG`, and `EDC_TOKEN` can be set. A token stored in a config file is only used for the server it is bound to; switching `--server` will not send the old server's token to the new server. The CLI only permits plaintext HTTP for loopback addresses. Remote addresses require HTTPS, and automatic redirects are rejected.

### CLI Version and Updates

```sh
edc version
edc update --check
edc update
```

The V2 server publishes the recommended CLI version, minimum compatible version, and official GitHub Release location at the unauthenticated `GET /.well-known/edc-cli` endpoint. `edc status` reports the installed version, compatibility, and available update under `cli`. Interactive commands warn on stderr at most once per day without changing their JSON stdout; hook, MCP, and long-running host commands never perform an update check. Set `EDC_UPDATE_CHECK=off` to disable reminders from ordinary commands; explicit `version`, `status`, and `update` commands remain available.

`edc update` never installs silently. Only an explicit invocation downloads the release asset for the current OS and architecture, verifies its SHA-256 using the GitHub release digest or `checksums.txt`, retains a no-clobber backup beside the current executable, and atomically replaces it with a same-directory rename. Source builds use `make build`, which embeds the Git commit and build time. Tagged releases use `.github/workflows/release-cli.yml` to cross-compile CLI assets for `darwin/arm64`, `darwin/amd64`, `linux/amd64`, and `linux/arm64`.

## Data Contract

Example event:

```json
{
  "id": "evt_...",
  "project_id": "prj_...",
  "actor_user_id": "usr_...",
  "actor_username": "bob",
  "recorded_at": "2026-09-08T14:00:00.000000000Z",
  "occurred_at": "2026-09-07T09:00:00.000000000Z",
  "content": {"kind": "text", "text": "Original information"},
  "metadata": {"source": "meeting", "tags": ["decision"]},
  "provenance": {"kind": "original"},
  "relations": {}
}
```

- **Sharing**: The creator adds members. All members can read the complete event, file, and metadata history and append data under their own identity. Only the creator can add members. Other projects are inaccessible by default. The first version does not support member removal or project deletion.

- **Identity and time**: `actor_user_id`, `actor_username`, and `recorded_at` are server-generated fields; supplying them in input is rejected. `occurred_at` is an optional user-declared timestamp and is stored by the server in UTC. It must not be treated as a trusted audit timestamp.

- **Append-only**: HTTP/MCP exposes no edit or delete operations. Each event is stored as a non-overwritable JSON manifest inside the project directory; uploaded source files are stored separately and are also non-overwritable. SQLite stores only users, HTTP/OAuth tokens and authorization transactions, projects, and membership authorization. It does not store events, metadata, or file bytes. An administrator with filesystem write access can still modify or delete files; this is not a tamper-proof ledger.

- **Idempotency**: `idempotency_key` is isolated by `project + author`. Reusing the same key with identical input returns the original event; using the same key with different input returns a conflict. Metadata object key order and whitespace do not affect comparison. The CLI generates a key by default; for retries across separate commands, explicitly pass the same key. Metadata serves as identifying labels and does not enforce uniqueness.

- **Files**: `record_event` accepts base64-encoded `text/*` files up to 1 MiB that are valid UTF-8 and contain no NUL bytes. `POST /v1/media-events` accepts multipart AAC M4A, MP3, or WAV files up to 20 MiB. Both paths first save non-overwritable original bytes, then publish an event that references their ID, type, filename, size, and SHA256. Small files can be retrieved as base64 through `get_file`; every file can be streamed and verified again through the authenticated `/v1/files/{id}/content` endpoint. File type is never silently inferred from the extension, and server-side file paths supplied by callers are never loaded.

- **Limits**: Direct text is limited to 1 MiB; metadata is limited to 32 KiB and 128 top-level fields, with keys of 1–128 bytes. Ordinary HTTP requests are limited to 2 MiB and media requests to 21 MiB. Data is never silently truncated.

### Query Semantics

`query_events` requires `project_id`. By default, filtering uses the server-generated `recorded_at`; `time_field="occurred_at"` may be specified instead. `from` is inclusive and `to` is exclusive. RFC3339 timestamps with timezone information are required. Events without `occurred_at` do not match occurrence-time boundaries.

Results are ordered by append sequence in ascending order. `limit` defaults to 50 and has a maximum of 100. Each page also has a 4 MiB JSON budget for events, while guaranteeing at least one complete event, so the actual number of results may be lower than `limit`. The first page pins the project's current maximum sequence number. Use the returned `next_cursor` as `cursor`, while retaining all other parameters, to read the same snapshot without mixing in subsequently appended events. Starting a new query will include new events.

`metadata` performs exact JSON matching on top-level keys, with conditions combined using AND:

```json
{
  "project_id": "prj_...",
  "metadata": {"source": "meeting", "verified": true},
  "metadata_exists": ["description"],
  "limit": 50
}
```

The string `"1"` differs from the number `1`; `null` differs from a missing value. Object key order is ignored, while array order is significant. JSON numbers preserve their original representation and precision; in the first version, `1` and `1.0` are treated as different values. Arbitrary JSON metadata can be stored, but the first version does not support nested paths, array containment, fuzzy matching, or numeric range filters.

`list_metadata` without a `key` lists top-level fields that have actually appeared, together with their types and event counts. With a `key`, it lists actual JSON values observed for that field and their counts. `limit` / `offset` are supported, and `has_more` indicates whether another page exists. There is no separate metadata registration process. Recommended fields include `description`, `source`, `tags`, and `document_type`. Formal project ownership is always determined by `project_id`.

`query_context` / `POST /v1/context/query` retrieves text that matches keywords from an immutable project snapshot, then follows server-controlled source and supersedes relationships to include related records. The response separates evidence, suggestions, and explicit update forks, and provides sequence coverage, pending-audio counts, and stable warning codes. `max_output_bytes` is capped at 24000 while preserving valid UTF-8. The query does not write events and does not treat arbitrary metadata as provenance.

## HTTP API

Except for registration, login, and healthz, all endpoints require `Authorization: Bearer <token>`. JSON requests use `Content-Type: application/json`.

| Method and Path | Input / Purpose |
|---|---|
| `POST /v1/auth/register` | `{username,password}`, returns the user |
| `POST /v1/auth/login` | `{username,password}`, returns the user, token, and expiration time |
| `POST /v1/auth/logout` | Revokes the current token |
| `GET /v1/me` | Current identity |
| `POST /v1/projects` | `{name,description?}`, creates a project |
| `GET /v1/projects` | Projects belonging to the current user |
| `GET /v1/projects/{project_id}/members` | Lists members and their `member` / `owner` roles |
| `POST /v1/projects/{project_id}/members` | An owner adds exactly one registered member by `{username}` or `{email}` |
| `PATCH /v1/projects/{project_id}/members/{user_id}` | An owner changes another member to `{role:"owner"}` or `{role:"member"}`; at least one owner must remain |
| `POST /v1/members` | Adds an exact member by `{project_id,username}` or `{project_id,email}` |
| `POST /v1/members/query` | `{project_id}`, lists members |
| `POST /v1/events` | `{project_id,content,metadata?,occurred_at?,idempotency_key?}` |
| `POST /v1/media-events` | Atomically uploads multipart M4A/MP3/WAV media up to 20 MiB |
| `GET /v1/events/{id}` | Full event |
| `POST /v1/events/query` | `{project_id,from?,to?,time_field?,metadata?,metadata_exists?,limit?,cursor?}` |
| `POST /v1/context/query` | Read-only keyword retrieval, explicit relationship closure, provenance, and coverage |
| `POST /v1/metadata/query` | `{project_id,key?,limit?,offset?}` |
| `GET /v1/files/{id}` | File information and `data_base64` |
| `GET /v1/files/{id}/content` | Authenticated streaming and verification of original file bytes |
| `GET /v1/inbox`, `POST /v1/inbox/{id}/read` | Current user's processing results and read state |
| `/v1/automation/*`, `/v1/runner/*` | Installation and run state plus the fencing-token-protected runner protocol |
| `/mcp` | Standard MCP Streamable HTTP with Bearer authentication |

Business errors use:

```json
{"error":{"code":"...","message":"..."}}
```

Status codes include 400 (invalid input), 401 (unauthenticated), 403 (permission denied), 404 (not found or no read access), 409 (conflict), 413 (too large), and 429 (authentication rate limiting). Errors never expose passwords, tokens, or internal database information.

## MCP Integration

The V2 API dynamically provides local Agent setup instructions for the workspace at `GET /agent-setup.md?project=prj_...&locale=en`. It returns `text/markdown` with the project ID, API address, and official Skill address embedded in the document, and supports `en`, `zh-CN`, `ms`, and `hi`. Local Codex and Claude Code agents invoke an authenticated `edc` CLI directly; the CLI reads its access token from private configuration, while the Agent neither reads nor prints the token. This public endpoint uses only the supplied project ID and does not query the project name, membership, or content; actual access still requires CLI login and project membership. Omitting the project returns general instructions to select one first; invalid or duplicate parameters return 400. Generated addresses use `-public-base-url` and `EDC_WEB_BASE_URL`—or the first allowed origin when unset—not the request Host. The static site's `/agent-setup.md` remains a general guide only.

Uses the [official Go SDK](https://github.com/modelcontextprotocol/go-sdk) v1.7.0. Supports standard initialization, tool discovery, tool invocation, JSON Schema, and read/write annotations. Business errors are returned through `isError`. All tools return structured JSON and also include JSON-formatted text content.

Tools: `create_project`, `list_projects`, `add_project_member`, `list_project_members`, `record_event`, `get_event`, `query_events`, `query_context`, `list_metadata`, `get_file`.

### Local Codex / Claude Code: Use the CLI Directly

Run `edc login` privately in a terminal. A local Agent then runs `edc` commands directly without registering MCP or opening the CLI credentials file:

```sh
/absolute/path/to/event-driven-context/bin/edc whoami
/absolute/path/to/event-driven-context/bin/edc project list
/absolute/path/to/event-driven-context/bin/edc query --project PROJECT_ID --limit 5
/absolute/path/to/event-driven-context/bin/edc state list --project PROJECT_ID
```

Add `--config /absolute/path/to/config.json` to these commands when a fixed configuration path is needed. The CLI loads the access token and sends it as an HTTP Bearer credential; do not copy it into prompts, repositories, or Agent configuration.

Recording Skills live at `.agents/skills/edc-recorder/SKILL.md` for Codex and `.claude/skills/edc-recorder/SKILL.md` for Claude Code. Claude Code can preview and then apply the hook and Skill:

```sh
edc setup claude-code
edc setup --apply claude-code
```

Setup does not add MCP. If the project's `.mcp.json` contains a legacy `event-driven-context` or `event-context` entry, setup removes that entry while preserving other MCP services.

### Remote Streamable HTTP: ChatGPT OAuth, OpenAI API, and Generic MCP Clients

The server URL is:

```text
https://YOUR_HOST/mcp
```

The client sends a Bearer token. `GET` / `DELETE /mcp` do not carry sessions and return 405. The service uses stateless Streamable HTTP, with identity independently verified on every request.

The [official OpenAI Responses API documentation](https://developers.openai.com/api/docs/guides/tools-connectors-mcp) supports remote Streamable HTTP and provides an `authorization` field for sending an access token. The MCP tool configuration in a request can be written as:

```json
{
  "type": "mcp",
  "server_label": "event_context",
  "server_url": "https://YOUR_HOST/mcp",
  "authorization": "<EDC access token>",
  "require_approval": "always"
}
```

The production MCP endpoint is:

```text
https://context-api.integ.life/mcp
```

It continues to support the static Bearer-token access described above and also provides OAuth 2.1 Authorization Code + PKCE, accepting only S256, for browser-based clients such as ChatGPT:

- Protected Resource Metadata: `https://context-api.integ.life/.well-known/oauth-protected-resource/mcp` (the root-path version is also supported). The `WWW-Authenticate` header returned by unauthenticated `/mcp` requests points here.
- Authorization Server Metadata: `https://context-api.integ.life/.well-known/oauth-authorization-server`.
- Authorization, token exchange, and dynamic client registration: `/oauth/authorize`, `/oauth/token`, and `/oauth/register`. Dynamic registration accepts only public clients without a client secret and only exact HTTPS or loopback callback URLs.
- `resource` must exactly equal `https://context-api.integ.life/mcp` in both the authorization request and token exchange. Authorization codes expire after 5 minutes and are single-use. OAuth access tokens expire after 1 hour.

Minimal scope model:

| Scope | MCP Tools |
|---|---|
| `context:read` | `list_projects`, `list_project_members`, `get_event`, `query_events`, `query_context`, `list_metadata`, `get_file` |
| `context:write` | `create_project`, `add_project_member`, `record_event` |

Scopes do not replace project permissions. Even with `context:read` or `context:write`, callers can access only projects permitted by their existing membership. Adding members remains restricted to the project creator. The service does not allow anonymous writes. Tool `readOnlyHint` annotations and descriptions identify read/write behavior and required scopes.

#### ChatGPT Custom Connector

In ChatGPT Developer Mode, create a new custom connector named `Event-driven Context`. Set the URL to:

```text
https://context-api.integ.life/mcp
```

Choose OAuth authentication and use server discovery / Dynamic Client Registration. Do not provide a static API token or client secret.

ChatGPT currently generates or submits an exact callback URL in the connector draft, typically:

```text
https://chatgpt.com/connector/oauth/<callback_id>
```

Older connectors may use:

```text
https://chatgpt.com/connector_platform_oauth_redirect
```

The service registers the complete URL from the DCR request exactly as provided. The authorization request must match it character-for-character. Wildcards are not supported.

ChatGPT first receives a 401 challenge, then discovers the two well-known JSON documents, registers a public client, and redirects to the login page with the `resource`, scope, and S256 challenge.

Users can log in using an existing Event-driven Context username and password, or create an account from the same OAuth page according to the existing username and password rules. A new account receives only a user identity and is not automatically granted access to any existing project.

OAuth login, registration, error messages, and authorization confirmation pages automatically use English, Simplified Chinese, Bahasa Melayu, or हिन्दी according to the browser language, with manual language switching that does not lose the authorization transaction. The main site and OAuth preserve the user's selection through a locale-only `.integ.life` cookie; it contains no account information, token, or other identity data.

The user then verifies the client, resource, and scopes on the authorization page and confirms authorization.

ChatGPT itself also displays a warning indicating that the custom MCP has not been reviewed by OpenAI. Confirming authorization allows the third-party MCP to read from or append to projects that the user is authorized to access. Proceed only after verifying the URL, tools, and scopes.

## Web Frontend and Deployment

`frontend/` is a static website with no build dependencies. Its production domain is `https://context.integ.life`, and it calls `https://context-api.integ.life` by default. The public home page introduces the V2 product; the workspace provides central sign-in, project recording, member sharing, files and citations, versioned State, integrations, and plugin configuration.

The public entry point at `frontend/index.html` shows the landing page when signed out. A valid central session or compatible token enters `frontend/workspace.html`. The workspace's "About" link opens `?page=about`; signed-in users can still view it and return to their original project and section. Successful sign-out returns to the home page. Project deep links, central-login returns, and language selection preserve the current route. The landing page's interactive example performs no inference and writes no data. See [Landing page](docs/landing-page.md) for the V2 comparison and verification boundary. Deployment preserves the home page and no longer overwrites `index.html` with the workspace.

The complete current UI for both the main site and OAuth supports English, Simplified Chinese, Bahasa Melayu, and हिन्दी, including dynamic status, form validation, and error messages. Without a manual selection, it uses the system language reported by the browser; if that language is unavailable or unsupported, it falls back to English. A manual selection persists across refreshes, login, and both subdomains.

Development preview:

```sh
python3 -m http.server 4173 --directory frontend
go -C backend run ./cmd/edc-server -addr 127.0.0.1:8401 -db /tmp/event-context-dev.db -skill-root skills \
  -allowed-origins http://127.0.0.1:4173
```

When visiting `http://127.0.0.1:4173`, the page automatically points to the local API. The production page points only to `context-api.integ.life`. The frontend stores the short-lived access token in the browser's localStorage. Logging out immediately revokes it. Do not remain logged in using a shared browser profile.

Before production deployment, first commit a clean working tree, then run:

```sh
make deploy-prod       # Build linux/amd64, upload to integ-prod, install systemd service
make deploy-frontend   # Push frontend/ to gh-pages
```

The service runs on `integ-prod` at `127.0.0.1:8401` and is exposed as `https://context-api.integ.life` through a dedicated Caddy `event-context-proxy.service`. It does not share the machine's global Caddy instance. The identity and authorization database is stored at `/var/lib/event-driven-context/context.db`; event manifests and original files are stored under `/var/lib/event-driven-context/data/projects/<project-id>/{events,files}/`. Static deployment uses `frontend/CNAME` to specify `context.integ.life`. When a new version starts for the first time, it exports legacy SQLite event/file tables into files under the data directory and then removes the old tables.

### integ-prod Operations

When production SSH access is available, use `EDC_DEPLOY_SSH_TARGET=user@host make deploy-prod`; if it is unset, deployment continues to use gcloud/IAP. Both connection methods run the same validation, backup, release, and rollback flow.

The service process and SQLite data use the Linux account `yycy`. The shared group is `context-admins`. Both `yycy` and `songyy` belong to this group. Deployment directories remain group-readable and group-writable, and subsequent deployment directories inherit this group.

The SQLite driver tightens the database file so that it is private to the runtime account. Collaborators access data through the service interface.

The systemd unit remains root-managed. `yycy` can manage the Context service only through the following restricted commands and is not granted general-purpose sudo:

```sh
sudo context-service-admin status
sudo context-service-admin health
sudo context-service-admin logs
sudo context-service-admin restart
sudo context-service-admin start
sudo context-service-admin stop
```

The deployment script installs this helper and its sudo rules. It also validates, starts, or reloads the dedicated `event-context-proxy.service`; it does not take control of the machine's global `caddy.service`.

After stopping the application, the deployment process backs up the identity database using the SQLite backup API and archives the complete `data/` directory. It retains the previous release symlink target for rollback if health checks fail.

See [proxy-setup.md](deploy/production/proxy-setup.md) for the independent HTTPS proxy configuration and first-installation checks for the public API.

## Verification and Current Boundaries

```sh
make check
make build
```

Tests cover real CLI and stdio MCP subprocesses, the official MCP HTTP client, OAuth, cross-project isolation, media idempotency and original bytes, UTF-8 context budgets and explicit relationships, automation fencing/recovery, and validation of the fixed runner's inputs and outputs.

By default, the service binds only to loopback.

`-public-base-url` accepts only an HTTPS origin without a path. If left empty, OAuth discovery and endpoints are disabled, while static Bearer MCP access remains available.

Browser origins are rejected by default. An explicit allowlist can be configured using:

```text
-allowed-origins https://YOUR_HOST
```

The OAuth public origin is automatically added to the allowed list.

The MCP SDK enables loopback Host validation by default. If a reverse proxy connects to a loopback upstream, the proxy must set the upstream Host header to that upstream address.

The application rate-limits concurrent password authentication and global authentication attempts. DCR volume is also limited. The public entry point should still apply per-client abuse controls.

SQLite is used only for identity and authorization, including OAuth clients, transactions, codes, and tokens.

Event queries scan the project's data directory, which is appropriate for the first version's small-team use case.

Backups must contain both:

1. A consistent SQLite backup.
2. The complete `data/` directory.

At runtime, do not copy only the main SQLite file when using WAL mode. Likewise, do not back up only event files while omitting the authorization database.

Production deployment stops the service and writes both the SQLite backup API output and a tar archive of `data/` under:

```text
/var/lib/event-driven-context/backups/
```

Ordinary writes and context queries do not call a model or execute event contents. Only a fixed Skill that has been explicitly installed and run sends that run's authorized inputs to the independent runner. Current retrieval is based on deterministic keywords and explicit relationships; it does not claim semantic conflict detection or vector retrieval.

## Directory Structure

```text
app/                         Native mobile capture client and local upload queue
frontend/                    Static website, workspace, and multilingual UI
backend/cmd/edc-server/       HTTP + MCP service entry point
backend/cmd/edc/              CLI and stdio MCP entry point
backend/cmd/edc-runner/       Independent executor CLI for fixed Skills
backend/internal/core/        Shared business rules, SQLite authorization, and file-based event storage
backend/internal/api/         HTTP service, client, and end-to-end tests
backend/internal/mcpserver/   Standard MCP tools and transports
backend/internal/automation/  Installation, run, lease, inbox, and recovery state machine
backend/internal/runner/      Fixed-input execution, media transcription, and structured-review adapters
backend/skills/               Version-pinned Skill snapshots whose digests are checked at startup
docs/                        Product, technical, and interaction design documents
scripts/                     Build and deployment helpers
```
