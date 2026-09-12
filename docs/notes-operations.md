# Notes worker operations

Status: operating contract for the notes and attachment work in progress; it does not claim deployment or production validation.

The API and worker are separate processes. Install the notes plugin as project owner, then run one worker per project. Codex CLI must already be authenticated as the worker's Unix user; its bundled MCP support must be available. Shell, browser, app, plugin discovery, and external action tools are disabled for organizer sessions.

## Install

With a project-owner CLI session:

```sh
edc plugin install --project PROJECT_ID \
  --manifest backend/plugins/notes-indexer/manifest.json \
  --token-file /var/lib/event-driven-context/notes-indexer.token
```

Keep the token private (0600). It belongs to this project and grants event reads and validated note publication, without event or State writes. Existing installations are not silently replaced.

Copy `deploy/production/event-context-notes.service` to the system unit directory. Create `/etc/event-context-notes.env` with:

```sh
EDC_NOTES_PROJECT=PROJECT_ID
EDC_NOTES_CODEX=/home/yycy/.local/bin/codex
```

The service expects `edc`, `plugins/notes-indexer/manifest.json`, and the API binary in the current release. Enable with `systemctl enable --now event-context-notes`. The deployment script packages these artifacts and restarts an already-running worker after API deployment. Existing API authentication and proxy configuration remain separate.

## Verify and recover

- Inspect `journalctl -u event-context-notes`. Successful runs log Event count, through-sequence, and notes revision. Idle checks are quiet. Failures retry with bounded backoff.
- Use `edc sync --project PROJECT_ID --output .context/projects/PROJECT_ID` to read notes and attachment metadata. Add `--files all` only when all catalog bytes are wanted. A regular authenticated CLI session is required; the worker credential is not a user export credential.
- Inspect `manifest.json` separately for atomic notes revision/through-sequence, frozen catalog through-sequence, and bytes completion. Never infer one from another.
- Cache one attachment with `edc file get --project PROJECT_ID --cache COLLECTION FILE_ID`; all flags precede the ID. The command verifies size and SHA-256. It cannot be combined with `-o`.
- A run publishes only after Codex calls `finish` successfully and exits successfully. Failed runs keep the last published notes and checkpoint. Restarting the worker is safe; a lease from a disconnected worker expires after twelve minutes.
- Attachment prompt version 2 automatically backfills old file Events. Each run handles at most 20 Events, using up to ten historical file Events and remaining slots for new Events. `attachment_backfill_through_sequence` advances atomically with the notes tree; missing means zero. The organizer reads metadata and references only, never attachment bytes.
- Stop the worker before API rollback. Retain the current immutable notes generations and data backups. A previous API binary may not expose organizer routes; leave the worker stopped until compatible code is restored.
- Event references use `edc-event://UUID`; wiki links use stable note IDs. A viewer must resolve these IDs; raw Obsidian installation does not automatically resolve them by filename.

The service targets one configured project. Enabling another project requires its owner to install the plugin and an independently configured worker. Before deployment, verify catalog project isolation and frozen pagination, reference pages beyond five previews, atomic main/backfill checkpoints, checksum failure, interrupted cache resume, and all three completion states.
