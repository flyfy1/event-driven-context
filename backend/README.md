# Backend

This directory is the Go module for the Event-driven Context API, CLI, and MCP server. The module path remains `event-driven-context` so moving the source does not change package imports.

## Layout

- `cmd/edc-server`: HTTP and MCP server entry point.
- `cmd/edc`: CLI and stdio MCP entry point.
- `cmd/edc-runner`: bounded worker for fixed automation skills.
- `internal/core`: shared domain rules, authorization metadata, and file-backed event storage.
- `internal/api`: HTTP server, client, OAuth support, and integration tests.
- `internal/mcpserver`: MCP tools and transports.
- `internal/automation`: durable installations, runs, leases, inbox, and recovery.
- `internal/runner`: fixed-input transcription and structured review adapters.
- `skills`: the two versioned P1 skill snapshots; the server and runner verify their digests.

Run Go commands from this directory:

```sh
go test -race ./...
go vet ./...
go run ./cmd/edc-server -skill-root skills
go run ./cmd/edc-runner -server http://127.0.0.1:8080 -token-file /private/path/runner-token -state-dir /private/path/runner-state -skill-root skills tick
```

`-skill-root` is explicit: omit it to disable automation routes, or point it at this module's `skills/` directory to load only the fixed `audio-transcribe` and `daily-review` snapshots. Runtime state remains under the server's configured `-data` directory; no user directories are scanned.

The runner token comes from `EDC_RUNNER_TOKEN` or a mode-`0600` token file, never a command-line token. Audio transcription additionally requires explicit Python, ASR adapter/model, ffmpeg, and ffprobe paths; daily review requires an explicit Codex executable path. Run `edc-runner help` for the corresponding flags and environment variables.

The root `Makefile` wraps these commands. `make build` writes `edc`, `edc-server`, and `edc-runner` to the repository-level `bin/` directory so existing CLI and MCP configuration paths stay stable.
