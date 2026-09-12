# Backend

This directory is the Go module for the Event-driven Context V2 API, CLI, MCP server, capture hooks, and processor host. Run Go commands from this directory.

## Layout

- `cmd/edc-server`: V2 HTTP API and Streamable HTTP MCP at `/mcp`.
- `cmd/edc`: V2 CLI, Claude Code capture entry point, and stdio MCP proxy.
- `internal/v2`: append-only Event, File, versioned State, and plugin authorization rules.
- `internal/v2client`: typed client for the public V2 HTTP routes.
- `internal/capture`: directory bindings, setup previews, hook normalization, and the durable outbox.
- `internal/processorhost`: explicit one-pass host execution for installed agent and command processors.
- `internal/api`: HTTP and OAuth adapters plus real-route integration tests.
- `internal/mcpserver`: the 13 public V2 conversation-agent tools.
- `internal/core`: user, token, project, and membership identity storage.

SQLite stores identity and project membership. V2 Event, File, State, plugin, and run data live under the server `-data` directory.

## Run and verify

```sh
go test -race ./...
go vet ./...
go run ./cmd/edc-server -addr 127.0.0.1:8080 -db data/context.db -data data
```

The CLI uses `--server` and a private `--config` file. Global flags must precede the command:

```sh
go run ./cmd/edc --server http://127.0.0.1:8080 --config /private/path/edc.json login --username alice
go run ./cmd/edc --server http://127.0.0.1:8080 --config /private/path/edc.json project create --name demo
go run ./cmd/edc --server http://127.0.0.1:8080 --config /private/path/edc.json link prj_example
go run ./cmd/edc --server http://127.0.0.1:8080 --config /private/path/edc.json push --type note "A decision"
```

File pushes upload the bytes first and append the referencing Event only after the server confirms the digest. `file get` verifies size and SHA-256 in a temporary file before publishing a new destination atomically.

`edc setup claude-code` prints the proposed project-local hook, MCP, and recorder-skill changes without writing. Apply the exact preview explicitly:

```sh
edc --config /private/path/edc.json setup --apply claude-code
```

Shared projects also require `--enable-shared-hooks`. `edc status` reports the binding, hook state, and pending outbox count; `edc outbox list` and `edc outbox flush` inspect or retry queued Events. Hook network failures leave the Event queued and return promptly.

`edc mcp` exposes the same V2 data through stdio. It preserves per-item batch results and public error codes from the HTTP service. Plugin install output reports whether a token was returned but never prints the token.

Save the one-time plugin credential directly to a new private file, then run one local processor pass with the project binding or an explicit project ID:

```sh
edc plugin install --manifest backend/plugins/project-brief/manifest.json --token-file /private/path/project-brief.token
edc host run --plugin project-brief --plugin-dir backend/plugins --plugin-token-file /private/path/project-brief.token --agent-command /path/to/codex --once
```

The host reads the installation and configuration with the plugin credential, pulls Events after the plugin `_cursor`, publishes validated output, and advances `_cursor` only after the output succeeds. Command processors can use `--command` to select a local executable; no scheduler or background daemon is included in this build.

The repository-level `Makefile` builds the binaries into `bin/`.
