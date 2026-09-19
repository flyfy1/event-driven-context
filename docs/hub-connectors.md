# Hub data source connectors

[简体中文](hub-connectors.cn.md)

## Current implementation boundary

The `backend/internal/hubconnectors` package implements bounded provider HTTP reads and Google OAuth helpers. It is a server-side adapter layer: callers must authenticate an agent, resolve a connection owned by the approving user, check a current operation grant, and obtain credentials from protected server storage before invoking it. A passing adapter test does not establish a connected production account.

The target user owns several accounts and wants an agent to discover available operations, request access, and read approved data. The current connector slice tests the riskiest technical assumption: explicit operations can reach different providers without exposing credentials or accepting arbitrary upstream endpoints. Success is a provider-shaped integration test plus a real owner-authorized account check once deployment credentials are configured. This package neither invokes a model nor sends messages.

## Provider capability catalog

`Catalog()` returns public capabilities, input JSON schemas, authentication modes, provider OAuth scope alternatives, requirements, and limitations. `Lookup(providerID)` returns one provider. Catalog availability is distinct from account configuration and agent permission:

- `adapter_available`: executable adapter code exists; this says nothing about deployment credentials or owner approval.
- `not_implemented`: no executable operation is advertised.
- The application currently stores `needs_auth`, `configured`, or `disconnected` for connections. `configured` means stored credentials exist; manual token entry alone is not a verified connection. Google OAuth verifies identity before saving. The connector metadata contract additionally reserves explicit unconfigured, connected, reauthorization, and blocked states for later connection health tracking.
- Agent authorization is enforced by the host for each connection and operation. It must not be inferred from provider support or an account's connection status.

| Provider ID | Implemented operations | Current boundary |
| --- | --- | --- |
| `google-drive` | `files.list`, `files.get`, `files.export` | One page of metadata; Workspace export as `text/plain`, `text/csv`, or `application/pdf`. No binary-file download or persistent sync. |
| `gmail` | `messages.list`, `messages.get` | One page of IDs and thread IDs; full message MIME payload. No attachment download or persistent sync. |
| `telegram-bot` | `identity.get`, `updates.peek` | Bot identity and queued updates; no personal-account history or acknowledgment. |
| `telegram-user` | None | Requires a separate user-authorized MTProto session; not implemented. |
| `whatsapp-business` | None | Business webhook ingestion and verification are not implemented. |
| `whatsapp-import` | None | Owner-selected personal chat export parser is not implemented. |

Google list operations accept `query`, `page_token`, and `limit` (1–100, default 20). `files.get` requires `file_id`; `files.export` requires `file_id` and `mime_type`; `messages.get` requires `message_id`. `updates.peek` accepts only `limit`. List responses retain provider pagination fields, including Drive `incompleteSearch`. The caller must distinguish an empty page, incomplete search, and provider failure.

## Multiple accounts and authorization

`Connection` separates `id`, `owner_id`, `provider_id`, `account_id`, `display_name`, and `status`. Two Gmail accounts have different connection IDs even when owned by one user. Account identities should come from a verified provider response; a human label is not verified identity. Names do not confer authority. Credentials must never be keyed by provider alone or exposed through discovery responses.

The adapter uses `Client.Execute(context, providerID, operationID, credential, args)`. This low-level API does not accept an agent identity and does not enforce grants. The host must bind the provider and credential to the authorized connection and recheck revocation before each execution. Account-wide operation grants permit that operation across the account; do not promise file-, label-, or chat-level isolation without enforcing those constraints on every operation. Gmail query filters do not narrow the provider's OAuth scope.

## Google authorization

`GoogleOAuth` creates a PKCE S256 authorization URL and performs code exchange and refresh. The host supplies a configured client ID, secret, exact registered redirect URL, and optionally an HTTP client. It must generate random state and verifier values, bind a short-lived one-use state record to the signed-in owner, connection, and requested provider scopes, then verify state before exchanging the callback code. Callback pages must avoid leaking codes through logs or referrers.

The helper requests `drive.readonly` for Drive and `gmail.readonly` for Gmail, with offline access and explicit consent. Both are broad account read scopes: the owner must see this scope before consent. The adapter also accepts externally obtained `drive.file` credentials, but no Google Picker workflow is implemented here; `drive.file` itself includes provider write access despite this adapter exposing reads only. See [Drive scope documentation](https://developers.google.com/workspace/drive/api/guides/api-specific-auth) and [Gmail scope documentation](https://developers.google.com/workspace/gmail/api/auth/scopes) for current verification requirements.

`GoogleToken` is sensitive server data. Persist it encrypted, compute expiry from `expires_in`, inspect returned scopes rather than assuming all were granted, and preserve an existing refresh token when a refresh response omits one. Do not return tokens to an agent. The helper does not manage callback routes, credential storage, revocation, account identity, or scheduled refresh. Google's [web-server OAuth guide](https://developers.google.com/identity/protocols/oauth2/web-server) documents the upstream flow.

The application integration in `backend/internal/api/hub_google.go` and `backend/internal/core/hub_google.go` supplies the callback, state persistence, account verification, encrypted storage, and refresh-on-use around this helper:

- `GET /v1/hub/google/status` returns deployment configuration availability.
- Owner-authenticated `POST /v1/hub/google/start` accepts `provider_id` and `display_name`, returning `authorization_url`. Configure `EDC_HUB_GOOGLE_CLIENT_ID`, `EDC_HUB_GOOGLE_CLIENT_SECRET`, `EDC_HUB_GOOGLE_REDIRECT_URL`, and a base64-encoded 32-byte `EDC_HUB_CREDENTIAL_KEY` on the server.
- Register the exact `GET /v1/hub/google/callback` URL with Google. The callback requires the initiating browser's HttpOnly, SameSite=Lax cookie and a valid one-use state. The database stores a state hash and encrypted PKCE verifier with a ten-minute expiry; at most ten pending attempts per owner are allowed. The frontend must send `credentials: "include"`, and deployment CORS must allow credentials only for trusted UI origins.
- The callback checks the returned read scope and verifies account identity using Drive `about.get` or Gmail `users.getProfile`. Different verified accounts create separate connections. Reconnecting the same account preserves the connection ID but revokes existing Agent grants; the owner approves fresh grants explicitly.
- Access tokens refresh when within one minute of expiry. Encrypted credential updates use compare-and-swap so an in-flight refresh cannot overwrite a newer reconnection or revive a disconnected account. Missing refresh credentials require owner reconnection. There is no background sync worker.

OAuth integration tests cover owner binding, browser binding, one-use state, expiry, verified identity, credential refresh, multi-account isolation, and disconnect/reconnect races. They do not establish live Google consent or deployment configuration.

## Telegram and WhatsApp limitations

Telegram Bot API `getUpdates` acknowledges earlier updates when called with a higher offset; a negative offset can discard queued updates. `updates.peek` therefore rejects `offset` and `allowed_updates`, sends `timeout=0`, and does not advance a cursor. It cannot coexist with an active webhook and must be coordinated with other consumers of the same bot. It is a bounded inspection operation, not a durable ingestion worker. Telegram retains pending updates for at most 24 hours. See [Bot API](https://core.telegram.org/bots/api#getupdates). Personal Telegram accounts use [user authorization](https://core.telegram.org/api/auth), which needs a separate session implementation.

WhatsApp Business Platform is a business integration surface. No personal WhatsApp chat-history API is assumed. Business webhook ingestion needs a configured business account, phone number, signature verification, replay handling, and account provenance. Personal exported chats need a distinct local import flow. Both remain explicitly unavailable. The [Meta-owned Cloud API collection](https://www.postman.com/meta/whatsapp-business-platform/documentation/wlk6lh4/whatsapp-cloud-api) documents the business account and webhook boundary; Meta's [platform documentation](https://developers.facebook.com/documentation/business-messaging/whatsapp/overview) may require login.

## Safety and evidence

- Fixed HTTPS hosts and operation paths; resource IDs cannot contain path traversal or arbitrary URLs. Unknown arguments are rejected.
- Redirects and automatic application retries are disabled. Timeouts are capped at 30 seconds; each provider response is capped at 10 MiB, OAuth responses at 64 KiB. Oversized responses fail rather than silently truncate.
- Provider errors contain stable error codes and HTTP status only. They omit raw bodies and URLs, particularly Telegram URLs containing bot tokens. A custom transport must not log credentials or request URLs.
- Returned provider content remains untrusted data. The host chooses safe download disposition and must not render arbitrary payloads as executable HTML.
- Local persistence is deployer-controlled. Content handed to an external model by an authorized agent may leave the device.

Run `go test ./internal/hubconnectors` from `backend/`. Tests use local HTTP servers with provider-shaped responses to check endpoint routing, Google pagination and PKCE, read-only Telegram behavior, rejected arguments, redirects, size limits, and error redaction. No real user credentials or private source data are used. Live OAuth consent and provider access remain deployment checks.

The next external prerequisites are deployment-owned Google OAuth credentials and API enablement, an owner-authorized bot token for Telegram, and a deliberate choice of WhatsApp Business ingestion versus personal export. MTProto sessions, WhatsApp ingestion/import, attachment download, and background incremental sync are later work; none is reported as connected by this catalog.
