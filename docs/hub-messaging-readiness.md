# Messaging connector readiness

[English](hub-messaging-readiness.md) | [简体中文](hub-messaging-readiness.cn.md)

Status updated on 2026-10-07. This records the credential-free implementation and acceptance prerequisites of [Agent Hub](agent-hub.md). Neither a successful fixture nor a signed test payload establishes a live account connection.

## WhatsApp export imports

The owner-selected `whatsapp-import` path is implemented. It preserves immutable originals and account provenance, supports multiple owner-scoped connections, and requires a separate Agent grant for `records.list`. Full HTTP fixture tests cover approval, reading and grant invalidation on reimport. No private conversation was used. Representative owner-provided exports remain necessary to validate real regional timestamp and export variants; the parser does not guess ambiguous local dates into UTC.

## WhatsApp Business ingestion

The signed webhook receiver and Agent reads are implemented. Owner setup validates private app secret, verification token and WABA/phone binding. GET/POST `/v1/hub/webhooks/whatsapp/{id}` answers the challenge and authenticates exact raw payload bytes before parsing. Each change must match the configured account; duplicate headers/query fields, ambiguous JSON and bodies above 1 MiB are rejected.

Core atomically stores encrypted immutable receipts and message/status records. Account-scoped hashes deduplicate identical envelopes; message IDs and structured status identities deduplicate repackaged deliveries. Reconfiguration is checked within the ingestion transaction and revokes earlier grants; disconnect blocks further receipt and reads. `events.list` is sequence-paginated; `receipts.get` preserves original bytes as base64 and hash. Both require independent Agent approval. Synthetic API/core tests cover challenge, tampering, wrong phone, replay, pagination, encryption, atomic batch rejection, rotation, account/grant isolation and revocation. See [CLI and complete setup](data-source-cli.md).

Live acceptance still needs the owner's Meta app, verified business phone, HTTPS callback subscription and provider-side test delivery. No real Meta account was created or accessed. Signature verification and deduplication do not prove freshness or independent account ownership. Personal WhatsApp history and media downloads are excluded. Official reference: [Meta-owned webhook payload collection](https://www.postman.com/meta/whatsapp-business-platform/folder/tduohwq/webhook-payload-reference).

## Personal Telegram feasibility

A Bot API token cannot provide a personal Telegram session. Telegram documents application `api_id`/`api_hash` setup and an interactive user authorization state machine, which may require phone/email codes and a two-step password. Bind a successful session to the provider's returned user ID, not a user-entered label. See [application setup](https://core.telegram.org/api/obtaining_api_id), [authorization](https://core.telegram.org/api/auth), and [TDLib authorization states](https://core.telegram.org/tdlib/getting-started).

**Proposed dependency:** Telegram's official TDLib, behind an optional local process with a small allowlisted interface. This is a design recommendation, not an installed or benchmarked integration. TDLib supplies client networking, update handling and local encrypted storage; its native/JSON interfaces imply additional packaging work compared with this repository's Go HTTP adapters. See [TDLib](https://core.telegram.org/tdlib/) and its [source/build instructions](https://github.com/tdlib/td).

Proposed boundaries before implementation:

- One owner/connection-specific session directory and independently generated encryption key; protect keys through Hub storage. Do not share a session database across owners or accounts.
- Owner-only interactive login, with short-lived state bound to the initiating session. Agents never receive login codes, passwords, application secrets or session files. Do not automate login or use an existing personal session without explicit authorization.
- Start with selected ordinary cloud chats and bounded text reads. Exclude secret chats, automatic media downloads, outgoing messages, read acknowledgments and general-purpose MTProto invocation. These are proposed Hub restrictions, not Telegram-issued read-only credentials.
- Restrict provider requests and responses to approved chat IDs and absolute ranges. Existing generic account/operation grants alone cannot promise selected-chat access. Define and test chat constraints before presenting that approval text.
- Decide whether to retain a local message cache; do not silently create a permanent mirror. Disconnect must stop the worker and invalidate future Hub grants; provider logout and local retained-data policy need explicit behavior.

The next step requires the owner's decision to accept this session/cache model and optional native runtime, then a deployment-owned API application and a user-authorized test login. No library dependency, login flow, worker or personal-history API was added. `telegram-user` remains unavailable and hidden.

## Remaining acceptance

Run `make check` and `make build`. Fixtures use synthetic messages/keys only and do not establish live account consent. CLI, server and frontend publication are authorized by the owner; verify the actual deployed routes separately.

| Queue item | Implemented evidence | Missing external input |
| --- | --- | --- |
| WhatsApp/ICS/Markdown/Telegram/prepared WeChat CSV imports | Parser, storage and full HTTP fixture tests | Representative owner-selected exports; WeChat requires readable CSV, not a native backup |
| WhatsApp Business | Signed receiver, atomic encrypted retention, replay detection and authorized reads | Owner Meta application/phone setup and live subscription |
| Personal Telegram | Official-protocol and TDLib feasibility review | Session/runtime decision, application credentials and authorized login |
| Google and other live sources | Fixed-endpoint reads, schemas, CLI discovery and authorization tests | OAuth application, API enablement and owner consent/tokens |
| Android notifications | Existing unit/build/lint evidence | Firebase setup and selected device acceptance |

Account registration requires the owner's selected email/name and provider verification. Do not ask for secrets or codes in chat. Complete independent implementation work while awaiting those inputs; never claim a fixture is a connected private account.

## 2026-10-07 publication verification

CLI v0.1.2 and backend source commit `b020c1991bfaca742b6b21daaa43fe2d6d32a22f` were published. GitHub release artifacts for Linux/macOS, ARM64/AMD64 passed CI; the downloaded Linux ARM64 checksum and real CLI catalog call were verified. The public Pi API returned v0.1.2 and 41 available adapters; a health request ID matched the Pi journal, and database integrity was `ok`. The public Hub JavaScript matched this source commit. `make check`, `make build`, focused race tests and desktop/mobile browser fixtures passed.

The deployed Google application is not enabled yet. Existing deployment-owned Google application credentials were located in Integ.Auth configuration, but an unauthenticated upstream probe returned `redirect_uri_mismatch` for the Context callback. Registration of `https://context-api.integ.life/v1/hub/google/callback` in that application's allowed redirects remains required. No Gmail/Calendar consent or personal chat data was obtained. Fixture and deployment evidence must remain distinct from provider-account acceptance.
