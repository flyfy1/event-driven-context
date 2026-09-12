# Notes worker operations

The API and the worker are separate processes. Install the notes plugin as the project owner, then run one worker per project. Codex CLI must already be authenticated as the worker's Unix user. The tested server version is 0.153.4; its sibling `codex-code-mode-host` is required for MCP tools. Shell, browser, app, plugin discovery and external action tools are disabled for organizer sessions.

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
- Use `edc notes sync --project PROJECT_ID --output DIRECTORY` to read a published tree. This requires a regular authenticated CLI session; the worker credential is not a user export credential.
- A run publishes only after Codex calls `finish` successfully and exits successfully. Failed runs keep the last published notes and checkpoint. Restarting the worker is safe; a lease from a disconnected worker expires after twelve minutes.
- Stop the worker before API rollback. Retain the current immutable notes generations and data backups. A previous API binary may not expose organizer routes; leave the worker stopped until compatible code is restored.
- Event references use `edc-event://UUID`; wiki links use stable note IDs. A viewer must resolve these IDs; raw Obsidian installation does not automatically resolve them by filename.

The initial service targets one configured project. Enabling other projects requires their owner to install the plugin and an additional independently configured worker.
