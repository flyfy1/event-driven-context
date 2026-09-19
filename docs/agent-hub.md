# Agent data hub

[English](agent-hub.md) | [简体中文](agent-hub.cn.md)

## MVP contract

- **User:** A person operating multiple agents and multiple external accounts.
- **Job:** Let an agent discover available APIs and accounts, request exactly the access it needs, and wait for the owner to approve in the web or Android app.
- **Riskiest assumption:** Account and operation permissions are understandable enough for the owner to approve without granting unrelated access.
- **Loop:** Capability discovery → agent pairing request → owner checks CLI code → account discovery → operation request → owner approval → bounded provider read → owner revocation.
- **Proof:** Real CLI and browser flow against an isolated local service; owner/agent/account isolation, expired grants and revocation tests; connector contract tests; Android build and device acceptance recorded separately.
- **No-gos:** No agent self-approval, shared owner credential, credentials in command arguments, third-party writes, automatic model calls, or claims that unconfigured providers are connected.
- **Stage boundary:** Deliver the permission and read path first. Actual account onboarding requires owner-controlled Google/Telegram configuration. Android push requires Firebase configuration and device acceptance.

## Agent commands

Global flags precede the command. `--server` chooses the Hub, not a third-party endpoint. The public capability catalog contains operation descriptions and input JSON schemas; it does not expose account data.

```sh
edc --server https://hub-api.example.com capabilities
edc --server https://hub-api.example.com agent connect \
  --owner OWNER_USERNAME --name coding-agent --output ./agent-private.json

edc --config ./agent-private.json agent status
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

An operation request grants one exact connection and one exact API operation for 1 minute to 7 days, bounded by the agent's expiry. The requested duration starts at request creation, not approval. Reasons are untrusted agent-supplied text. Approval covers every resource reachable through that operation in the connected account; a query argument is **not** an authorization boundary. File, folder, message, and label restrictions are not yet supported. An agent can only see its own requests.

Missing authorization produces a nonzero CLI exit and structured `authorization_required` error. The agent must request access and ask the owner to review it; there is intentionally no agent approval command. A missing connection or expired provider credential requires owner setup, not a wider agent grant. All provider operations are single-page reads; continue using returned provider pagination tokens and the advertised input schema.

## Owner web and Android

The workspace header links to `hub.html` (**My authorizations**) and shows the pending count while signed in. The page lists agents, API requests, and individual connected accounts. It supports pairing-code verification, approve/deny/revoke, and account disconnection. It refreshes every 30 seconds while visible. Google Drive and Gmail have browser OAuth onboarding when the server is configured; repeat onboarding for additional accounts.

Android uses the existing owner session for a native My Authorizations screen. Notifications are opt-in per account and server. Default builds support periodic checks, subject to Android scheduling. Optional FCM sends only a generic wake-up; the app retrieves pending requests with authentication before showing them. See [Android setup and acceptance](android-hub.md). Web reminders are in-page reminders, not background Web Push.

## Provider and account separation

A connection has an independent `id`, `provider_id`, verified or owner-declared `account_id`, display name, owner and status. Multiple accounts of the same provider are supported. Google browser onboarding verifies the account with the provider. Manual credentials are for operator setup and do not establish live account verification. `configured` means a credential exists; it does not prove a successful read.

- Google Drive: file metadata listing/reading and Google Workspace text/CSV/PDF export.
- Gmail: message listing and reading.
- Telegram Bot: bot identity and non-acknowledging update peek, limited by the Bot API and existing webhook use.
- Personal Telegram, WhatsApp Business ingestion, and personal WhatsApp exports remain explicitly unavailable in the catalog until implemented and verified.

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

Configure the trusted frontend origin in `-allowed-origins` and the frontend URL in the existing web-base configuration. Cross-origin OAuth start calls must include cookies. Google flow uses one-use state, encrypted PKCE, callback browser binding and expiry. Full-account `drive.readonly` and/or `gmail.readonly` are requested with explicit owner disclosure. Selected-file Picker onboarding is not implemented. OAuth refresh happens on demand; it cannot revive a disconnected account.

For controlled operator setup, existing owner credentials can add a connection using `edc source add --provider ID --account ACCOUNT --name NAME --credential-stdin`. Supply the credential privately through stdin, never as an argument. Omitting credentials registers a `needs_auth` account. Repeating the same provider/account with fresh credentials reconfigures that connection and revokes earlier grants. `edc source disconnect CONNECTION_ID` clears the active stored credential and revokes pending/approved grants. It does not revoke the provider's own OAuth consent or erase filesystem backups.

Authorization metadata and encrypted secrets live in the identity SQLite database; provider read responses are returned on demand and are not appended to immutable project events. Keep the encryption key in private deployment configuration and back it up separately from the database. Losing it makes stored secrets unreadable; replacing it without migration is not key rotation. No automatic key migration is implemented.

## Verification and outstanding acceptance

The implementation includes tests for agent/user credential separation, cross-owner access, distinct accounts, exact operation grants, code verification, expiry, revocation, reconnect invalidation, OAuth state/PKCE, provider request bounds, redirects, push token ownership, stale web/mobile sessions, and untrusted-text rendering. Run `make check` and `make build`; see Android instructions for its independent checks.

Current verification is local. Actual Google consent/refresh, real Telegram account reads, public deployment, installed Android UI and FCM delivery require external configuration or devices; no live-provider or production success is implied by local tests.

## Connector continuation queue

Continue one independently verifiable step at a time, recording exact evidence and committing/pushing task-owned changes:

1. Configure owner-controlled Google OAuth and verify two accounts, provider reads, refresh and disconnection using non-sensitive test data.
2. Verify Telegram Bot identity/peek with an explicitly provided test bot; do not consume another application's update queue.
3. Implement an owner-selected personal WhatsApp chat-export importer with account provenance and fixture tests; do not imply it is live sync.
4. Implement WhatsApp Business verified webhook ingestion after the owner provides Meta application/phone configuration and a callback host.
5. Evaluate personal Telegram MTProto authorization, encrypted session storage and library suitability separately from Bot API support.
6. Configure Firebase, install the Android build on a user-selected device, and prove background notification → approval → CLI grant → revocation.

Work that needs owner login, app credentials or a device stays explicitly blocked; do not fabricate accounts, broaden permissions, send third-party messages, or redeploy unrelated services. Notify the owner only about a completed verified stage, meaningful failure or required input.
