# Messaging connector readiness

[English](hub-messaging-readiness.md) | [简体中文](hub-messaging-readiness.cn.md)

Status checked on 2026-09-19. This records the bounded, credential-free continuation of [Agent Hub](agent-hub.md). Neither a successful fixture nor a signed test payload establishes a live account connection.

## WhatsApp export imports

The owner-selected `whatsapp-import` path is implemented. It preserves immutable originals and account provenance, supports multiple owner-scoped connections, and requires a separate Agent grant for `records.list`. Full HTTP fixture tests cover approval, reading and grant invalidation on reimport. No private conversation was used. Representative owner-provided exports remain necessary to validate real regional timestamp and export variants; the parser does not guess ambiguous local dates into UTC.

## WhatsApp Business verification preparation

`backend/internal/hubwebhooks` contains standalone verification helpers. They are deliberately not registered as an HTTP endpoint or wired into connection setup, persistence or Agent execution. `whatsapp-business` remains `not_implemented`, with no callable operations, and stays hidden.

The verifier is constructed from private application configuration and one trusted binding: owner ID, Hub connection ID, WABA ID, and business phone-number ID. Create a separate binding for each account; never infer the owner from webhook data. The subscription challenge checks the configured verification token and `subscribe` mode. POST verification uses the application's secret and the exact raw body for `X-Hub-Signature-256`, before interpreting JSON. Verification tokens and application secrets serve different purposes.

Signed envelopes must contain the expected WhatsApp object, supported message changes, and the configured WABA and phone on every change. Mixed-account batches fail as a whole. The helper bounds payloads at 1 MiB, rejects ambiguous JSON, and returns a defensive copy of authenticated bytes with binding provenance and a content hash. Message/status content remains untrusted data. A valid signature and content hash do **not** establish freshness, deduplicate delivery, or prove that an owner controls a business account.

Before live ingestion can be enabled, the host still needs:

1. An owner-authorized Meta application and private secret, verification token, WABA/phone binding and selected HTTPS callback host. None was created or accessed during this stage.
2. An HTTP boundary that bounds the raw body, rejects duplicate signature headers/query parameters, handles the verification response as plain text, and applies safe logging and request limits.
3. Owner-only setup and verified provider identity, atomic append-only persistence, durable account-scoped duplicate detection, retry-safe acknowledgments, and disconnect/reconnect behavior. A global message ID or body hash alone must not route data between owners.
4. A separate, bounded Agent read API and operation approval, with actual multi-account webhook, replay, revocation and failure acceptance. Approval notification delivery must remain separate from provider event receipt.

The receiver must not be enabled merely because the helper passes tests. Full ingestion remains blocked on the owner-selected Meta setup and callback environment. Protocol references: [Meta's webhook verification description](https://whatsapp.github.io/WhatsApp-Nodejs-SDK/api-reference/webhooks/start/) (archived SDK documentation, used only for the verification contract) and [WhatsApp Business platform](https://developers.facebook.com/documentation/business-messaging/whatsapp/overview).

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

## Continuation gates

Verification uses synthetic messages and keys only. The webhook package tests cover exact-body tampering, wrong/malformed signatures, authentication before JSON parsing, cross-account and mixed-account batches, ambiguous/oversized/deep JSON, subscription challenges, immutable returned bytes and repeated deliveries. Run `go -C backend test -race ./internal/hubwebhooks`, `make check`, and `make build`. There is no live Meta acceptance or new browser/Android flow to verify in this preparation stage.

| Queue item | Current evidence | Missing external input |
| --- | --- | --- |
| WhatsApp/ICS/Markdown imports | Parser, storage and full HTTP fixture tests | Owner-selected representative exports for real-data acceptance |
| WhatsApp Business | Standalone signature/account verification tests; no ingestion route | Authorized Meta configuration and callback host |
| Personal Telegram | Official-protocol and dependency feasibility review | Session/cache/runtime decision, application credentials and authorized test login |
| Google and other live adapters | Existing bounded adapter/authorization tests | Owner-configured test accounts, OAuth/API enablement and live consent |
| Android notifications | Existing unit/build/lint evidence | Firebase configuration and user-selected device acceptance |
| Public publication | Local commits only | Explicit permission to publish these commits to the public repository |

Subsequent scheduled checks should wait for a changed prerequisite, not repeat fixture implementations, request real credentials in chat, or claim a blocked source is connected. No deployment, publication, subscription, private-account access or third-party message was performed here.
