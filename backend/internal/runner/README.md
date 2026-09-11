# Runner adapter

Execute(ctx, Task, Config) runs one already-authorized task and returns a
validated candidate. It does not dispatch work, renew leases, append events, or
install a scheduler.

The coordinator supplies absolute local paths through Config:

- SkillRoot: this repository's fixed backend/skills directory.
- PythonPath, ASRScriptPath, and ASRModelPath: a Python environment with
  mlx_audio, this package's qwen_asr.py, and a local Qwen3-ASR model.
- FFmpegPath and FFprobePath: explicit executables used for audio decoding and
  to reject audio over 600 seconds.
- CodexPath: an authenticated Codex CLI executable.
- WorkRoot: an optional private parent for per-run temporary directories.

Paths are configuration, not source defaults. A deployment can map environment
variables such as EDC_RUNNER_PYTHON, EDC_RUNNER_ASR_MODEL,
EDC_RUNNER_FFMPEG, EDC_RUNNER_FFPROBE, and EDC_RUNNER_CODEX into Config.

Audio is copied into a mode-0700 temporary directory while its SHA-256 is
verified, then transcribed without truncation. Codex runs ephemerally with a
read-only sandbox, a fixed output schema, web search disabled, and all available
tool surfaces disabled. Child processes receive a small allowlist of ordinary
runtime environment variables; API keys and runner/backend tokens are not
forwarded. Timeouts kill the complete process group and the temporary directory
is removed after every outcome.

The current adapter supports the reviewed audio-transcribe and daily-review
packages at version 1.0.0. It is intentionally not a general third-party skill
runtime.

## Minimal CLI

`cmd/edc-runner` provides `tick` and a finite `watch`. The runner token comes
only from `EDC_RUNNER_TOKEN` or a private token file; it is never accepted on
the command line. Configure fixed local executable and asset paths with the
`EDC_RUNNER_*` variables listed by `edc-runner -help`.

The CLI uses a private state directory, with separate locks and pending
candidate queues for each server and irreversible runner-token fingerprint.
It streams granted inputs to mode-0600 attempt files, verifies claimed size and
SHA-256, and removes them after the attempt. Heartbeats run independently of
model execution and cancel it when the lease is lost. A candidate is synced to
local state before submission, so an uncertain submit can replay the same
attempt without rerunning the model. The server fencing token decides whether
that replay is still valid. `watch` requires a positive maximum runtime and
does not install cron or any other long-lived scheduler.
