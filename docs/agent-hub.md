# Agent data hub

[English](agent-hub.md) | [简体中文](agent-hub.cn.md)

## MVP contract

- **User:** A person operating multiple agents and multiple external accounts.
- **Job:** Let an agent discover available APIs and accounts, request exactly the access it needs, and wait for the owner to approve in the web or Android app.
- **Riskiest assumption:** Account and operation permissions are understandable enough for the owner to approve without granting unrelated access.
- **Loop:** Capability discovery → agent pairing request → owner checks CLI code → account discovery → operation request → owner approval → bounded provider read → owner revocation.
- **Proof:** Real CLI and browser flow against an isolated local service; owner/agent/account isolation, expired grants and revocation tests; connector contract tests; Android build and device acceptance recorded separately.
- **No-gos:** No agent self-approval, shared owner credential, credentials in command arguments, third-party writes, automatic model calls, or claims that unconfigured providers are connected.
- **Stage boundary:** Deliver the permission and read path first. Live account onboarding requires owner-controlled provider configuration; owner-selected exports use local snapshot imports. Android push requires Firebase configuration and device acceptance.

## Agent commands

Global flags precede the command. `--server` chooses the Hub, not a third-party endpoint. The public capability catalog contains operation descriptions and input JSON schemas; it does not expose account data.

```sh
edc --server https://hub-api.example.com capabilities
edc --server https://hub-api.example.com agent connect \
  --owner OWNER_USERNAME --name coding-agent --output ./agent-private.json

edc --config ./agent-private.json agent status
edc --config ./agent-private.json source integrations
edc --config ./agent-private.json source list
edc --config ./agent-private.json access request \
  --connection CONNECTION_ID --operation messages.list \
  --reason 'Find email relevant to the current project' --duration 1h
edc --config ./agent-private.json access list
edc --config ./agent-private.json api call \
  --connection CONNECTION_ID --operation messages.list \
  --args '{"query":"subject:proposal","limit":10}'
```

`agent connect` creates a new mode-0600 file; it refuses to overwrite an existing file and never prints its credential. Keep it out of Git. Send the owner the returned approval URL and ask them to compare the CLI verification code in their own setup session. Pending pairing expires in 15 minutes. Approval gives the agent a 7-day identity that can discover all connected account names/identities for this owner. It does **not** grant source content access. Discovery of account identities is itself disclosed on the approval screen.

An operation request grants one exact connection and one exact API operation for 1 minute to 7 days, bounded by the agent's expiry. The requested duration starts at request creation, not approval. Reasons are untrusted agent-supplied text. Except for the calendar constraints below, approval covers every resource reachable through that operation in the connected account; a query argument is **not** an authorization boundary. File, folder, message, and label restrictions are not yet supported. An agent can only see its own requests.

Missing authorization produces a nonzero CLI exit and structured `authorization_required` error. The agent must request access and ask the owner to review it; there is intentionally no agent approval command. A missing connection or expired provider credential requires owner setup, not a wider agent grant. Provider reads are bounded. Continue only when the response and advertised input schema provide a supported cursor; Graph and DAV may report incomplete results without a continuation API. Snapshot imports use numeric offsets.

## Deployment-aware discovery

```sh
# Approved Agent configuration
edc --config ./agent-private.json source integrations
# Owner configuration; Agent credentials cannot use this endpoint
edc --config ./owner-private.json source integrations --owner
```

The authenticated registry returns provider metadata, deployment readiness, account counts, `visible`, `hidden_reason`, `onboarding_method`, account-specific operations, and feature flags. The Agent view adds `authorized` and active grants with their expiry and constraints. An authorized calendar operation still needs a grant covering the exact calendar and requested date window; the boolean alone is insufficient.

The web source section shows only registry-visible, configured accounts with available operations. Missing adapters, encryption configuration, decryptable credentials, accounts, or APIs hide the corresponding source controls. A separate collapsed **Add account** entry offers only configured `browser_oauth` providers, including supported Google sources without an account yet. Registry errors hide source controls while existing authorization requests and revocation remain available. Refresh occurs on session/account changes and every 30 seconds while the page is visible.

Registry readiness checks local configuration and credential decryption; it never proves a successful provider call. `source list` remains the broader owner-account inventory after Agent pairing and can include `needs_auth` or disconnected records; use `source integrations` for usable-operation discovery. Public `capabilities` remains a catalog, including explicitly unavailable providers, without account identities or secrets.

## Calendar grants

Google Calendar `events.list` and `freebusy.query`, and Microsoft Calendar `events.list`, require a connection, exact calendar ID, and a positive date window of at most seven days. This data window is separate from the grant's lifetime. Supply RFC3339 timestamps with explicit offsets; execution may narrow the approved window but cannot change the calendar or extend the window. The server checks these boundaries before the provider read and before returning the result.

```sh
edc --config ./agent-private.json access request \
  --connection CALENDAR_CONNECTION_ID --operation events.list \
  --reason 'Plan the approved calendar week' --duration 1h \
  --constraints '{"calendar_id":"primary","time_min":"2026-09-21T00:00:00+08:00","time_max":"2026-09-28T00:00:00+08:00"}'

edc --config ./agent-private.json api call \
  --connection CALENDAR_CONNECTION_ID --operation events.list \
  --args '{"calendar_id":"primary","time_min":"2026-09-21T00:00:00+08:00","time_max":"2026-09-22T00:00:00+08:00","limit":20}'
```

Use the actual calendar ID from that account; `primary` is the Google example, not a Microsoft alias. Basic event fields or busy intervals are returned, not full event bodies or attachments. The approval page displays calendar and time bounds and rejects unsupported or malformed constraints. These rules do not apply to DAV `calendars.query`: a DAV connection selects one collection, its query is bounded, but it has no independent grant-level time-window constraint. ICS imports expose raw snapshot components through `records.list`, not live calendar queries.

## Owner web and Android

The workspace header links to `hub.html` (**My authorizations**) and shows the pending count while signed in. The page lists agents, API requests, and registry-visible individual connected accounts. It supports pairing-code verification, approve/deny/revoke, and account disconnection. It refreshes every 30 seconds while visible. Google Drive, Gmail, Calendar, Tasks, Contacts, Docs, Sheets and Chat have browser OAuth onboarding when their server configuration is available; repeat onboarding for additional accounts.

Android uses the existing owner session for a native My Authorizations screen. Notifications are opt-in per account and server. Default builds support periodic checks, subject to Android scheduling. Optional FCM sends only a generic wake-up; the app retrieves pending requests with authentication before showing them. See [Android setup and acceptance](android-hub.md). Web reminders are in-page reminders, not background Web Push.

## Provider and account separation

Google Docs/Sheets/Chat, Microsoft Contacts/OneNote/Teams, Asana, Airtable, Linear, GitLab, Box, Discord Bot, Feishu/Lark and WhatsApp Business have additional operations; see [Data source CLI](data-source-cli.md).

A connection has an independent `id`, `provider_id`, verified or owner-declared `account_id`, display name, owner and status. Multiple accounts of the same provider are supported. Google browser onboarding verifies the account with the provider. Manual credentials are for operator setup and do not establish live account verification. `configured` means a credential exists; it does not prove a successful read.

- Google: Drive, Gmail, Calendar, Tasks and Contacts, using the operations advertised by the catalog.
- Microsoft Graph: Calendar, To Do, OneDrive and Outlook Mail, using owner-supplied delegated access tokens.
- DAV: CalDAV/CardDAV and iCloud Calendar/Contacts, with one owner-selected public HTTPS collection per connection.
- SaaS: Todoist, Notion, Dropbox, Readwise Reader, GitHub and Slack, using owner-supplied tokens and explicit read operations.
- Telegram Bot: bot identity and non-acknowledging update peek, limited by the Bot API and existing webhook use.
- Imported snapshots: WhatsApp exported chats, ICS calendar files and Markdown notes.
- Personal Telegram sessions, Android Health Connect and Google Photos Picker remain explicitly unavailable until implemented. They are hidden from ordinary source controls.

See [connector details and official references](hub-connectors.md). A capability being implemented, a provider being configured, and an agent being authorized are separate states.

## Self-host configuration

Use an existing deployment's private configuration mechanism. Never commit keys or paste them into a conversation.

| Variable | Purpose |
| --- | --- |
| `EDC_HUB_CREDENTIAL_KEY` | Base64-encoded 32-byte encryption key for provider credentials, pending OAuth verifiers and push-device tokens. |
| `EDC_HUB_GOOGLE_CLIENT_ID` | Owner-controlled Google OAuth web application client ID. |
| `EDC_HUB_GOOGLE_CLIENT_SECRET` | Google OAuth client secret. |
| `EDC_HUB_GOOGLE_REDIRECT_URL` | Exact registered callback, `https://YOUR_API/v1/hub/google/callback`. |
| `EDC_HUB_FCM_PROJECT_ID` | Firebase project used by the Android app. |
| `EDC_HUB_FCM_SERVICE_ACCOUNT_FILE` | Private file containing an FCM-authorized service account. |

Configure the trusted frontend origin in `-allowed-origins` and the frontend URL in the existing web-base configuration. Cross-origin OAuth start calls must include cookies. Google flow uses one-use state, encrypted PKCE, callback browser binding and expiry. Google requests source-specific read scopes with explicit owner disclosure: `drive.readonly`, `gmail.readonly`, Calendar list/events/free-busy read scopes, `tasks.readonly`, or `contacts.readonly`. Calendar, Tasks and Contacts also use `openid` for identity. Provider consent may cover the account broadly; Hub calendar grants enforce the narrower calendar/date boundaries separately. Selected-file Picker onboarding is not implemented. OAuth refresh happens on demand; it cannot revive a disconnected account.

For controlled operator setup, existing owner credentials can add a connection using `edc source add --provider ID --account ACCOUNT --name NAME --credential-stdin`. Supply the credential privately through stdin, never as an argument. Omitting credentials registers a `needs_auth` account. Repeating the same provider/account with fresh credentials reconfigures that connection and revokes earlier grants. `edc source disconnect CONNECTION_ID` clears the active stored credential and revokes pending/approved grants. It does not revoke the provider's own OAuth consent or erase filesystem backups.

Graph, DAV and SaaS adapters currently use manual owner setup rather than browser onboarding or automatic refresh. Use the provider IDs, required scopes and credential shape in [connector details](hub-connectors.md). DAV takes a JSON credential containing `url`, `username` and `password` for one collection; iCloud requires an app-specific password, never the primary Apple password. Local configuration, saved credentials and passing tests do not establish live account verification.

Authorization metadata and encrypted secrets live in the identity SQLite database; provider read responses are returned on demand and are not appended to immutable project events. WhatsApp Business additionally retains encrypted webhook receipts and records in this database. Imports additionally persist immutable original files and snapshot metadata in the deployer's data directory. Keep the encryption key in private deployment configuration and back it up separately from the database. Losing it makes stored secrets unreadable; replacing it without migration is not key rotation. No automatic key migration is implemented.

## Owner-selected imports

Use the owner's normal configuration to import a selected UTF-8 file of at most 1 MiB. The CLI reads only the given file; it does not scan folders or retrieve attachments.

```sh
edc --config ./owner-private.json source import \
  --provider whatsapp-import --account personal-chat --name 'Personal chat export' \
  --format whatsapp-text --file ./chat.txt
edc --config ./owner-private.json source import \
  --provider calendar-import --account calendar-export --name 'Calendar export' \
  --format ics --file ./calendar.ics
edc --config ./owner-private.json source import \
  --provider markdown-import --account project-notes --name 'Project notes' \
  --format markdown --file ./notes.md

edc --config ./agent-private.json access request \
  --connection IMPORT_CONNECTION_ID --operation records.list \
  --reason 'Read the selected snapshot' --duration 1h
edc --config ./agent-private.json api call \
  --connection IMPORT_CONNECTION_ID --operation records.list \
  --args '{"offset":0,"limit":20}'
```

The owner approves `records.list` before the final Agent call. Follow `next_offset` until it is null. Results identify an immutable snapshot with original filename, hash, import time and record count; they are never described as a live connection. WhatsApp parsing preserves original message text, multiline content and uncertain source timestamps without inventing a timezone. ICS returns raw VEVENT and calendar-metadata blocks with selected properties as untrusted text; recurrences are not expanded. Markdown returns the original document.

Every import preserves a new original with private permissions. Reimporting the same provider/account updates that connection's snapshot reference and revokes earlier grants; it does not overwrite older files. Imported account identity is owner-declared. Disconnection removes active access and credentials but does not erase immutable originals or backups. Agents cannot import files using their own credentials.

## Verification and outstanding acceptance

The implementation includes tests for agent/user credential separation, cross-owner access, distinct accounts, exact operation grants, code verification, expiry, revocation, reconnect invalidation, OAuth state/PKCE, provider request bounds, redirects, registry visibility and malformed-state handling, scoped calendar windows, snapshot provenance and pagination, push token ownership, stale web/mobile sessions, and untrusted-text rendering. Run `make check` and `make build`; see Android instructions for its independent checks.

Current verification is local. Actual Google consent/refresh, live Graph/DAV/SaaS/Telegram reads, public deployment, installed Android UI and FCM delivery require external configuration or devices; no live-provider or production success is implied by local tests.

## Connector continuation queue

The 2026-10-07 source adapters, CLI discovery/onboarding/operation reads and durable WhatsApp ingestion are described in [Data source CLI](data-source-cli.md) and [messaging readiness](hub-messaging-readiness.md). Implementation and live-account acceptance remain separate. Publication is owner-authorized; account registration still requires an owner-selected identity and provider verification.

Continue one independently verifiable step at a time, recording exact evidence and committing/pushing task-owned changes:

1. Configure owner-controlled Google OAuth and verify two accounts, provider reads, refresh and disconnection using non-sensitive test data.
2. Verify Telegram Bot identity/peek with an explicitly provided test bot; do not consume another application's update queue.
3. Verify owner-selected WhatsApp/ICS/Markdown exports with representative user-provided fixtures, including ambiguous timestamps; preserve snapshot-only claims.
4. Accept the implemented durable WhatsApp webhook ingestion after the owner provides Meta application/phone configuration and a callback host; signature validation alone does not provide replay protection or account ownership verification.
5. Confirm the proposed personal Telegram session/cache model and optional TDLib runtime with the owner before implementing interactive login and chat-scoped reads; feasibility is documented separately from Bot API support.
6. Verify owner-configured Graph, DAV and SaaS read operations with non-sensitive test accounts; verify per-account isolation and honest incomplete-result handling.
7. Configure Firebase, install the Android build on a user-selected device, and prove background notification → approval → CLI grant → revocation.

Work that needs owner login, app credentials or a device stays explicitly blocked; do not fabricate accounts, broaden permissions, send third-party messages, or redeploy unrelated services. Notify the owner only about a completed verified stage, meaningful failure or required input.
