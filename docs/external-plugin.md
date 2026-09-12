# Run a Plugin on Your Machine (External Plugin)

English | [简体中文](external-plugin.cn.md)

When the installation page shows a one-time Plugin token, the Plugin is intended to run on a machine you control. Save the token securely, then start `edc host run`. Installation only creates the authorization and configuration; it does not start a processor. The Plugin processes new Events only while its processor host is running.

> `evidence` is the exception: it is a Skill for use by a conversation agent, has no background processor, and does not need a host. If the generic installation API still returns a token, do not configure that token in a processor host.

## End-to-end flow

```text
Install a Plugin in a Project
        │
        ├─ Save installation, manifest, configuration, and permissions
        └─ Return a one-time edcp_... token
                     │
                     ▼
             edc host on the user's machine
                     │
        ┌────────────┼────────────┐
        │            │            │
  Read authorized   Run a local   Start a local
  Events + _cursor  command/model Codex CLI process
        │            │            │
        └────────────┼────────────┘
                     ▼
       Write authorized derived Events / State
                     │
                     ▼
            Advance _cursor after success
```

For a remote deployment, the host only needs to make outbound connections to the Context API. It does not need a public IP, port forwarding, or a publicly exposed listening port. Production deployments should use HTTPS, for example `https://context-api.integ.life`. A host may also use `http://127.0.0.1:...` when connecting to a self-hosted service on the same machine.

## Which Plugins need a host

| Plugin | Execution mode | Local dependencies | Main output |
| --- | --- | --- | --- |
| `project-brief` | Agent processor | Installed and authenticated Codex CLI | `project-brief/current` State |
| `daily-review` | Scheduled Agent processor | Installed and authenticated Codex CLI | `daily-review/YYYY-MM-DD` State |
| `audio-transcribe` | Command processor | Adapter, Python + `mlx_audio`, local Qwen3-ASR model, FFmpeg, FFprobe | Source-linked transcript Event |
| `notes-indexer` | Built-in bounded Agent protocol | Installed and authenticated Codex CLI | Project Notes tree |
| `evidence` | Skill inside a conversation agent | An agent integration that supports the Skill | Immediate evidence answer; no background output |

`audio-transcribe` also has a server-managed path. A built-in installation enabled through that path does not receive a bearer token. Use the processor-host flow on this page when installing through the generic manifest flow and running it on your own machine.

## 1. Prepare the CLI and Plugin package

The following commands assume you are at the root of a trusted Event-driven Context source checkout:

```sh
make build

export EDC_SERVER="https://context-api.integ.life"
export PROJECT_ID="prj_replace_me"
export PLUGIN_DIR="$PWD/backend/plugins"
```

For self-hosting, replace `EDC_SERVER` with your API address. `PLUGIN_DIR` may point to a directory containing multiple Plugin subdirectories or directly to one Plugin directory. The selected directory must contain `manifest.json` and the local resources referenced by the manifest.

Run only Plugin packages you trust and have reviewed. A Plugin token limits what the Plugin can access on the Context server; it is not a local-machine sandbox. A Command processor runs as the current operating-system user and has the local permissions that user already has.

## 2. Save the one-time token

The server stores only the token's SHA-256 digest and cannot display the plaintext again. Do not put the token in a repository, command argument, screenshot, chat message, or ordinary log.

If you install from the Web, copy the token from the page and write it to a private file through hidden input. The following example is for the default zsh on macOS; the token itself does not enter shell history or terminal output:

```zsh
export PLUGIN_ID="project-brief"
export TOKEN_FILE="$HOME/.config/event-driven-context/$PLUGIN_ID.token"

install -d -m 700 "${TOKEN_FILE:h}"
umask 077
IFS= read -r -s 'PLUGIN_TOKEN?Paste the one-time Plugin token, then press Enter: '
printf '\n' >&2
printf '%s\n' "$PLUGIN_TOKEN" > "$TOKEN_FILE"
unset PLUGIN_TOKEN
chmod 600 "$TOKEN_FILE"
```

If you install with the CLI, prefer having the CLI create the private file directly so the token never appears in output:

```sh
export PLUGIN_ID="project-brief"
export TOKEN_FILE="$HOME/.config/event-driven-context/$PLUGIN_ID.token"

./bin/edc --server "$EDC_SERVER" plugin install \
  --project "$PROJECT_ID" \
  --manifest "$PLUGIN_DIR/$PLUGIN_ID/manifest.json" \
  --token-file "$TOKEN_FILE"
```

`--token-file` uses create-new semantics and creates a mode `0600` file; it will not overwrite an existing file. `edc host run` also rejects a token file that is readable or writable by group or other users. For a long-running host, use `--plugin-token-file` instead of persisting the token in the `EDC_PLUGIN_TOKEN` environment variable.

If the token is lost, there is currently no reveal or rotate operation. Remove and reinstall the Plugin to obtain a new token.

## 3. Run one pass first

Use `--once` for initial setup. A successful result is printed as JSON; when there is no new work, it returns `"noop": true`.

### Agent processor: Project brief

```sh
export PLUGIN_ID="project-brief"
export TOKEN_FILE="$HOME/.config/event-driven-context/$PLUGIN_ID.token"
export CODEX_BIN="$(command -v codex)"

./bin/edc --server "$EDC_SERVER" host run \
  --project "$PROJECT_ID" \
  --plugin "$PLUGIN_ID" \
  --plugin-dir "$PLUGIN_DIR" \
  --plugin-token-file "$TOKEN_FILE" \
  --agent-command "$CODEX_BIN" \
  --once
```

`project-brief` and `daily-review` both load a fixed Skill from the Plugin package, combine authorized Events, the current configuration, and any required previous State, then start the local Codex CLI. The current implementation uses a fixed model and structured output schema, disables Web search, and requires a validated State as the only output. Every cited Event ID must come from the authorized input.

The `daily-review` command has the same structure; only change the ID and token file:

```sh
export PLUGIN_ID="daily-review"
export TOKEN_FILE="$HOME/.config/event-driven-context/$PLUGIN_ID.token"
export CODEX_BIN="$(command -v codex)"

./bin/edc --server "$EDC_SERVER" host run \
  --project "$PROJECT_ID" \
  --plugin "$PLUGIN_ID" \
  --plugin-dir "$PLUGIN_DIR" \
  --plugin-token-file "$TOKEN_FILE" \
  --agent-command "$CODEX_BIN" \
  --once
```

Daily review interprets the configuration's `time` in the Project's IANA timezone; the default is `21:00`. A one-pass run checks the most recent review period that is already due. Installing the Plugin does not create a server-side scheduled job.

### Command processor: Audio transcription

Build the adapter, then provide every local ASR path:

```sh
go -C backend build \
  -o ../bin/audio-transcribe-adapter \
  ./plugins/audio-transcribe/cmd/audio-transcribe-adapter

export PLUGIN_ID="audio-transcribe"
export TOKEN_FILE="$HOME/.config/event-driven-context/$PLUGIN_ID.token"
export EDC_RUNNER_SKILL_ROOT="$PWD/backend/skills"
export EDC_RUNNER_PYTHON="/absolute/path/to/python"
export EDC_RUNNER_ASR_SCRIPT="$PWD/backend/internal/runner/qwen_asr.py"
export EDC_RUNNER_ASR_MODEL="/absolute/path/to/local-qwen3-asr-model"
export EDC_RUNNER_FFMPEG="/absolute/path/to/ffmpeg"
export EDC_RUNNER_FFPROBE="/absolute/path/to/ffprobe"

./bin/edc --server "$EDC_SERVER" host run \
  --project "$PROJECT_ID" \
  --plugin "$PLUGIN_ID" \
  --plugin-dir "$PLUGIN_DIR" \
  --plugin-token-file "$TOKEN_FILE" \
  --command "$PWD/bin/audio-transcribe-adapter" \
  --once
```

The Python environment must be able to import `mlx_audio`, and the model must be a local directory. The host downloads only an audio File that this Plugin is authorized to read, writes it to a private temporary directory, and verifies its SHA-256. The adapter uses FFprobe to check its duration, then transcribes it with the local model. Temporary directories are removed after success, failure, or timeout. The current implementation rejects audio longer than 600 seconds or larger than 20 MiB.

The current command protocol implements the single-audio input and derived-transcript output required by `audio-transcribe`. It is not a general runtime for arbitrary third-party commands.

## 4. Keep it running with watch

After `--once` succeeds, replace the final argument with `--watch`:

```sh
./bin/edc --server "$EDC_SERVER" host run \
  --project "$PROJECT_ID" \
  --plugin "$PLUGIN_ID" \
  --plugin-dir "$PLUGIN_DIR" \
  --plugin-token-file "$TOKEN_FILE" \
  --agent-command "$CODEX_BIN" \
  --watch \
  --interval 30s
```

For a Command processor, replace `--agent-command` with its `--command`. The host checks once immediately after startup and then at the configured interval. The default interval is 30 seconds, or 15 seconds for `notes-indexer`; the minimum is 1 second. SIGINT or SIGTERM cancels the active child process, removes its temporary directory, and exits normally.

`--watch` is only a long-running foreground process. The project does not automatically install launchd, systemd, a container, cron, or another process supervisor. Use your own supervisor for production operation.

## What happens in each pass

1. The host uses the token to read its own installation, current manifest, configuration revision, and Project timezone.
2. The host loads the local `manifest.json` and requires the local Plugin ID and version to exactly match the installed version on the server.
3. For a cursor-driven processor, it reads the private State `<plugin-id>/_cursor`, then pulls only Events after that cursor whose types are allowed by `read_events`.
4. An Agent processor starts Codex; a Command processor starts a local executable. Each receives only the data required for that pass.
5. The host validates processor output, source references, and write permissions.
6. On success, it writes a `derived` Event or State inside the Plugin's own namespace.
7. For an ordinary cursor-driven processor, `_cursor` advances only after every output succeeds. Daily review first records the day's attempt in cursor data, then marks it complete after publication succeeds.

A token is bound to one installation, one Project, and one Plugin. The server enforces the manifest's `read_events`, `write_events`, and `write_state` permissions. A Plugin may write only `derived` Events and only the granted State names inside its own namespace. It may download a File only when a readable Event references that File. The Plugin token is not a user login token and does not grant Project member management or access to another Project's data.

## Failures, retries, and repeated execution

If reading, processor execution, output validation, or publication fails, an ordinary cursor-driven host does not advance `_cursor`. The next `--watch` pass retries from the same position. Treat this as at-least-once execution, not exactly-once execution.

There is a failure window in which the server has accepted output but the host has not successfully advanced the cursor. The current audio Command processor generates deterministic IDs for derived Events, so the server handles the same result as a duplicate on retry. A custom processor must also handle replay safely. State writes and cursor writes use expected versions; competing hosts may encounter revision conflicts. Coordinating multiple hosts is not currently a supported deployment mode.

`daily-review` also records the day's attempt in `_cursor` and is limited by the manifest's `max_runs_per_day`. Network failures while reading Events do not consume an attempt. A date that has already been published is not generated again.

## Configuration and lifecycle

- **Change configuration**: the server increments the configuration revision; the host reads the new value on its next pass without replacing the token.
- **Pause**: token authentication returns `plugin_paused`, so the host cannot continue reading or writing.
- **Resume**: the original token works again, and the host continues from the existing `_cursor`.
- **Remove**: the installation is marked removed, its token digest is deleted, and the original token is permanently invalid. Existing Events, State, and Notes are not automatically deleted.

Configuration can be changed in the Web UI or with an optimistic CLI revision update:

```sh
./bin/edc --server "$EDC_SERVER" plugin config \
  --project "$PROJECT_ID" \
  --plugin "$PLUGIN_ID" \
  --expected-revision 1 \
  --config '{"language":"zh-CN","prompt":"Keep the result concise."}'
```

Use the installation page's field documentation for the selected Plugin. Different Plugins accept different configuration fields.

## Data boundary: running locally does not mean data stays local

- The Context Server remains the persistence location for Events, Files, State, installations, and cursors. With a remote Server, that data travels between the Server and host over HTTPS.
- `audio-transcribe` can perform inference with local Python, FFmpeg, and a local model without sending audio to a cloud model. The original audio is still already stored on the Context Server, and the transcript is written back to that Server.
- Although the local machine starts the Codex CLI for `project-brief`, `daily-review`, and `notes-indexer`, authorized Project content enters a Codex model request and is therefore sent to the model service. Evaluate this model data flow separately from where data is persisted.
- Only the host should read the Plugin token. Do not put it in a Plugin prompt, processor stdin, configuration, or model input.

## Current limitations

- Web installation creates only authorization and configuration; it does not install the CLI, Plugin package, Codex, Python, model, FFmpeg, or FFprobe.
- The Web UI cannot currently prove that a processor host is online and has no heartbeat, last-seen status, or last-successful-processing time.
- `--watch` does not install a system service. Processing stops while the machine sleeps or the process is not running.
- Running multiple hosts for one installation is not yet a supported capability.
- A manual rerun request can be persisted to `_requests`, but the current processor host does not consume those requests.
- The current host supports the fixed Agent and Command protocols defined in this repository. It is neither a general Plugin marketplace nor a security sandbox for third-party code.
- `evidence` has no host processor; it must be used by a conversation agent with the corresponding Skill integration.

When troubleshooting, run `--once` first and keep the complete error. Then check the API address, Project ID, token file permissions, local manifest version, Plugin status, Codex authentication, and absolute dependency paths for a Command processor.
