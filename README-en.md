# Event-driven Context

A Go-based project event storage backend and CLI. Project members can jointly read and write data. Events, original files, and metadata are **append-only and cannot be modified or deleted**. Original information can later be processed into context by independent logic.

The first version directly uses `User → Project → Event` without introducing a separate Context entity. See the [MVP document](docs/mvp.md) for scope and acceptance criteria.

## Getting Started

Requires Go 1.26.5 or later. SQLite uses a pure-Go driver and does not depend on an external database or CGO.

```sh
make build
./bin/edc-server
```

By default, the server listens on `127.0.0.1:8080`. The SQLite identity database is stored at `data/context.db`; event manifests and original files are written under the Git-ignored `data/` directory. To change the address or paths:

```sh
./bin/edc-server -addr 127.0.0.1:8090 -db data/context.db -data data
./bin/edc --server http://127.0.0.1:8090 help
```

`GET /healthz` is used for liveness checks. `SIGINT` / `SIGTERM` stop accepting new requests and wait for in-flight requests to complete.

## CLI: Shared Recording Workflow

Registration and login read the password from the terminal without echoing it. Usernames must contain 3–64 lowercase letters, numbers, `_ . -`; passwords must be 12–72 bytes. Passwords cannot be supplied as command-line arguments. Automation can provide passwords through `--password-stdin`.

```sh
./bin/edc register --username alice
./bin/edc login --username alice
./bin/edc project create --name "Backend Development" --description "Team-shared raw records"
```

Copy the returned project `id` and use it as `PROJECT_ID` in the following commands.

```sh
# A second user uses a separate config file; they can also register and log in from another computer.
./bin/edc --config "$HOME/.config/event-driven-context/bob.json" register --username bob
./bin/edc --config "$HOME/.config/event-driven-context/bob.json" login --username bob

# The creator adds an already-registered member.
./bin/edc project add-member --project PROJECT_ID --username bob
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
  "metadata": {"source": "meeting", "tags": ["decision"]}
}
```

- **Sharing**: The creator adds members. All members can read the complete event, file, and metadata history and append data under their own identity. Only the creator can add members. Other projects are inaccessible by default. The first version does not support member removal or project deletion.

- **Identity and time**: `actor_user_id`, `actor_username`, and `recorded_at` are server-generated fields; supplying them in input is rejected. `occurred_at` is an optional user-declared timestamp and is stored by the server in UTC. It must not be treated as a trusted audit timestamp.

- **Append-only**: HTTP/MCP exposes no edit or delete operations. Each event is stored as a non-overwritable JSON manifest inside the project directory; uploaded source files are stored separately and are also non-overwritable. SQLite stores only users, HTTP/OAuth tokens and authorization transactions, projects, and membership authorization. It does not store events, metadata, or file bytes. An administrator with filesystem write access can still modify or delete files; this is not a tamper-proof ledger.

- **Idempotency**: `idempotency_key` is isolated by `project + author`. Reusing the same key with identical input returns the original event; using the same key with different input returns a conflict. Metadata object key order and whitespace do not affect comparison. The CLI generates a key by default; for retries across separate commands, explicitly pass the same key. Metadata serves as identifying labels and does not enforce uniqueness.

- **Files**: A single `record_event` atomically writes both the file and event. Input format is `content={kind:"file",file:{filename,media_type,data_base64}}`; output replaces the raw data with the file ID, declared type, filename, size, and SHA256. Original bytes are retrieved through `get_file`. The first version accepts `text/*`, requires UTF-8 with no NUL bytes, and supports files up to 1 MiB. Non-text MIME types return an explicit error. Future versions may allow additional types within the same byte envelope. File type is never silently inferred from the extension, and server-side file paths supplied by callers are never loaded.

- **Limits**: Direct text is limited to 1 MiB; metadata is limited to 32 KiB and 128 top-level fields, with keys of 1–128 bytes. HTTP requests are limited to 2 MiB. Data is never silently truncated.

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
| `POST /v1/members` | `{project_id,username}`, adds a member |
| `POST /v1/members/query` | `{project_id}`, lists members |
| `POST /v1/events` | `{project_id,content,metadata?,occurred_at?,idempotency_key?}` |
| `GET /v1/events/{id}` | Full event |
| `POST /v1/events/query` | `{project_id,from?,to?,time_field?,metadata?,metadata_exists?,limit?,cursor?}` |
| `POST /v1/metadata/query` | `{project_id,key?,limit?,offset?}` |
| `GET /v1/files/{id}` | File information and `data_base64` |
| `/mcp` | Standard MCP Streamable HTTP with Bearer authentication |

Business errors use:

```json
{"error":{"code":"...","message":"..."}}
```

Status codes include 400 (invalid input), 401 (unauthenticated), 403 (permission denied), 404 (not found or no read access), 409 (conflict), 413 (too large), and 429 (authentication rate limiting). Errors never expose passwords, tokens, or internal database information.

## MCP Integration

Uses the [official Go SDK](https://github.com/modelcontextprotocol/go-sdk) v1.7.0. Supports standard initialization, tool discovery, tool invocation, JSON Schema, and read/write annotations. Business errors are returned through `isError`. All tools return structured JSON and also include JSON-formatted text content.

Tools:

`create_project`, `list_projects`, `add_project_member`, `list_project_members`, `record_event`, `get_event`, `query_events`, `list_metadata`, `get_file`.

### Local stdio: CLI, Codex, Claude Desktop / Claude Code

Run `edc login` first. A standard MCP client only needs to launch:

```sh
/absolute/path/to/event-driven-context/bin/edc mcp
```

This is a stdio proxy that connects to the HTTP backend. It does not open SQLite directly; every tool call is revalidated by the backend for identity and project permissions. stdout contains only MCP protocol output.

A generic client configuration example is available at [examples/mcp-stdio.json](examples/mcp-stdio.json); replace the absolute path. According to the [Claude Code documentation](https://code.claude.com/docs/en/mcp), it can be registered as follows:

```sh
claude mcp add --transport stdio event-context -- /absolute/path/to/event-driven-context/bin/edc mcp
```

Codex uses the same standard stdio command configuration. The repository does not automatically modify personal client settings.

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
| `context:read` | `list_projects`, `list_project_members`, `get_event`, `query_events`, `list_metadata`, `get_file` |
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

OAuth login, registration, error messages, and authorization confirmation pages automatically use English, Simplified Chinese, Bahasa Melayu, or हिन्दी according to the browser language, with manual language switching that does not lose the authorization transaction.

The user then verifies the client, resource, and scopes on the authorization page and confirms authorization.

ChatGPT itself also displays a warning indicating that the custom MCP has not been reviewed by OpenAI. Confirming authorization allows the third-party MCP to read from or append to projects that the user is authorized to access. Proceed only after verifying the URL, tools, and scopes.

## Web Frontend and Deployment

`frontend/` contains static pages with no build dependencies. The production domain is:

```text
https://context.integ.life
```

By default, it calls:

```text
https://context-api.integ.life
```

The frontend supports registration, login, project creation and selection, appending text and text files, metadata browsing, metadata filtering, event pagination, and downloading original files.

Project member management remains in the CLI/MCP because the first version only allows the creator to add already-registered members.

Development preview:

```sh
python3 -m http.server 4173 --directory frontend

go run ./cmd/edc-server -addr 127.0.0.1:8401 -db /tmp/event-context-dev.db \
  -allowed-origins http://127.0.0.1:4173
```

When visiting:

```text
http://127.0.0.1:4173
```

the page automatically points to the local API. The production page points only to `context-api.integ.life`.

The frontend stores the short-lived access token in the browser's localStorage. Logging out immediately revokes it. Do not remain logged in using a shared browser profile.

Before production deployment, first commit a clean working tree, then run:

```sh
make deploy-prod       # Build linux/amd64, upload to integ-prod, install systemd service
make deploy-frontend   # Push frontend/ to gh-pages
```

The service runs on `integ-prod` at:

```text
127.0.0.1:8401
```

and is exposed as:

```text
https://context-api.integ.life
```

through a dedicated `event-context-proxy.service` using Caddy. It does not share the machine's global Caddy instance.

The identity and authorization database is stored at:

```text
/var/lib/event-driven-context/context.db
```

Event manifests and original files are stored under:

```text
/var/lib/event-driven-context/data/projects/<project-id>/{events,files}/
```

Static deployment uses `frontend/CNAME` to specify `context.integ.life`.

When a new version starts for the first time, it exports legacy SQLite event/file tables into files under the data directory and then removes the old tables.

### integ-prod Operations

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

Tests cover:

- Real CLI subprocesses and stdio MCP subprocesses
- The official MCP HTTP client
- OAuth discovery
- DCR
- Login
- Authorization
- PKCE
- Single-use authorization codes
- Resource validation
- Scope handling
- Token expiration
- MCP `initialize`
- `tools/list`
- Negotiation with older protocol versions
- Cross-user sharing
- Denial of cross-project access
- Rejection of forged authorship
- Arbitrary metadata
- Lossless handling of large integers
- File byte round trips
- Concurrent idempotent retries
- Pagination snapshots
- Persistence after reopening file-based event storage
- Automatic migration of legacy SQLite events
- Token revocation

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

Independent consumers, parsing, summarization, and retrieval should only be added later when actually needed.

The current write path does not call any model and does not execute event contents.

## Directory Structure

```text
cmd/edc-server/       HTTP + MCP service entry point
cmd/edc/              CLI and stdio MCP entry point
internal/core/        Shared business rules, SQLite authorization, and file-based event storage
internal/api/         HTTP service, client, and end-to-end tests
internal/mcpserver/    Standard MCP tools and transports
docs/mvp.md            Confirmed first-version scope
```