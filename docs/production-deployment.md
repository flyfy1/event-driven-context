# Production Deployment and Raspberry Pi Migration

English | [简体中文](production-deployment.cn.md)

## Target Topology

- Backend host: `songyy-pi`, Ubuntu 24.04 ARM64 on NVMe.
- Service: systemd `event-context.service`, running as `songyy:service-admins` on `127.0.0.1:8401`.
- Public API, OAuth, and MCP: Cloudflare Tunnel `integ-pi` routes `context-api.integ.life` directly to `http://localhost:8401`.
- Frontend: GitHub Pages continues to serve `context.integ.life`; its public API address does not change.
- Persistence: `/var/lib/event-driven-context/context.db` stores identity and authorization. `/var/lib/event-driven-context/data/` stores append-only project Events, State, original files, notes revisions, and plugin state.
- Releases: `/opt/event-driven-context/releases/<timestamp>-<commit>` with `/opt/event-driven-context/current` as the active symlink.
- Secrets: `/etc/event-context.env`, root-readable only. Never copy its values into Git, logs, worklogs, or chat.

The core has a single-writer filesystem contract. Never run the Pi and GCE services as simultaneous writers after cutover.

## Normal Release

Run from a clean, pushed commit:

```sh
make deploy-prod
```

The deploy script runs `make check`, builds static Linux ARM64 `edc-server` and `edc` binaries with commit metadata, uploads the release through SSH target `pi`, takes a stopped-service SQLite backup and complete data archive when data already exists, installs the immutable release, and verifies loopback health. Set `EDC_DEPLOY_SSH_TARGET` only to choose another SSH alias for the same Pi.

After every release, verify all of the following independently:

1. `event-context.service` is active and enabled, its process is the expected release, and only `127.0.0.1:8401` is listening.
2. `https://context-api.integ.life/healthz` returns the expected version and a request ID, and that request ID appears in the Pi journal.
3. OAuth discovery and the unauthenticated MCP challenge still name `https://context-api.integ.life`.
4. The public Web flow restores a real signed-in user's project list, an existing Event and source identity, project sharing, and a public State source link.
5. A controlled append or idempotent replay is visible on Pi storage and absent from the stopped GCE copy. Clean up only the temporary verification record or token if the product contract permits it; append-only Events are retained.

## Initial Cutover

1. Record source release, service identity, SQLite integrity, V2 counts, data size, and hashes without exposing content or secret values.
2. Install the ARM64 release and a preliminary data copy on Pi while GCE remains the public writer. Validate Pi loopback behavior and exact counts.
3. Put the public API into a short maintenance window by stopping GCE `event-context.service`. Stop its automatic notes worker in the same transaction.
4. Create the final SQLite snapshot with the SQLite backup API. Archive the complete `data/` directory after the writer has stopped. Hash both outputs and stream them to a root-only Pi staging directory.
5. Install `/etc/event-context.env` on Pi with mode `0600`, restore the final database and data directory with the runtime ownership, start Pi `event-context.service`, and compare integrity, hashes, counts, and loopback API behavior.
6. Change the Cloudflare Tunnel published application for `context-api.integ.life` to `http://localhost:8401`. Keep the hostname and public URLs unchanged.
7. Verify public health, routing identity, OAuth, MCP, and the real browser flow. Prove a new request in the Pi journal.
8. Disable the GCE app and proxy units and confirm GCE port 8401 and direct proxy listeners are closed. Retain its unit files, environment, releases, original data, and final snapshots as rollback material.

## Backup and Rollback

The application database and `data/` directory are one consistency boundary. A usable backup includes a SQLite backup API output plus the entire stopped-writer data tree. Verify SQLite integrity and SHA-256 after every transfer.

After Pi has accepted a production write, never roll back by starting GCE against its old copy. Freeze Pi writes, take a fresh consistent Pi backup, restore that newest backup to GCE, validate it locally, then move the Tunnel or DNS route and enable only the chosen writer. The legacy release command is deliberately guarded:

```sh
make deploy-legacy-gce
```

This target sets the required `ALLOW_LEGACY_GCE_DEPLOY=1` flag. Its existence is rollback capacity, not permission to run two production writers.
