# Hub data source connectors

[English](hub-connectors.md) | [简体中文](hub-connectors.cn.md)

## Implementation and availability

The Hub provides read-only adapters, encrypted multi-account credentials, per-agent operation grants, and owner-selected file imports. These are implemented paths, not evidence of live provider access. No real provider account is required for the fixture tests. The server does not invoke a model; an agent that forwards retrieved data to an external model may send that data off the deployment.

The public `GET /v1/hub/capabilities` catalog describes code and schemas. Authenticated `GET /v1/hub/integrations` describes the current owner's deployment and accounts. Approved agents use `GET /v1/hub/integrations-agent`; their response additionally includes `authorized` and their active `grants` with expiry and constraints on each connection operation. Discovery never returns provider credentials.

| Registry field | Meaning |
| --- | --- |
| `provider.implementation_status` | `adapter_available` has executable operations; `not_implemented` has none. |
| `deployment_configured` | Adapter and a valid local encryption key exist. |
| `connectable` / `onboarding_method` | Setup can be initiated through `browser_oauth` or `owner_cli`; this is separate from existing account availability. |
| `connected_account_count` | Configured accounts whose stored credentials can be decrypted locally. |
| `visible` | Adapter, local credential storage, and at least one configured account are available. |
| `hidden_reason` | `adapter_unavailable`, `credential_storage_unconfigured`, `credential_unavailable`, or `no_configured_accounts`; empty when visible. |
| `connections[].operations` | Account-specific API schemas; agent responses include authorization metadata. |
| `feature_flags` | Local availability of Hub approvals, Google OAuth and push configuration. |

`configured` does not mean the token remains valid upstream. Registry reads do not probe providers or renew consent. Execution returns provider errors when a token has expired or scope is missing. Frontend source cards use `visible` and operations from this registry, fail closed on registry errors, and show browser onboarding only when available. Owner CLI setup remains available for providers without browser onboarding. A missing adapter never becomes visible simply because someone saved a token.

```sh
edc --config ./owner-private.json source integrations --owner
edc --config ./agent-private.json source integrations
```

## Implemented providers

Every provider supports separate connections for multiple accounts. Operations are bounded reads, not background synchronization. Read the exact input schema and limitations from `capabilities` before calling an operation.

| Provider ID | Operations | Setup and boundary |
| --- | --- | --- |
| `google-drive` | `files.list`, `files.get`, `files.export` | Google OAuth; metadata and Workspace text/CSV/PDF export, no binary download. |
| `gmail` | `messages.list`, `messages.get`, `attachments.get` | Google OAuth; message IDs, MIME payload and selected attachment base64url bytes. |
| `google-calendar` | `calendars.list`, `events.list`, `freebusy.query` | Google OAuth; event/basic busy data with mandatory calendar and time-window grants. |
| `google-tasks` | `tasklists.list`, `tasks.list` | Google OAuth; one page of task lists or tasks. |
| `google-contacts` | `contacts.list` | Google OAuth; names, email addresses and phone numbers. |
| `microsoft-calendar` | `calendars.list`, `events.list` | Owner-supplied delegated Graph token; basic calendarView data, mandatory calendar/window grants. |
| `microsoft-todo` | `tasklists.list`, `tasks.list` | Delegated Graph token with `Tasks.Read`. |
| `onedrive` | `files.list`, `files.get` | Delegated Graph token with `Files.Read`; metadata only, no download URL or file body. |
| `outlook-mail` | `messages.list`, `messages.get` | `Mail.ReadBasic` for listing or `Mail.Read` for message text; no attachments. |
| `todoist` | `tasks.list`, `tasks.get`, `projects.list` | Owner token; API v1, active tasks/projects, OAuth `data:read` when applicable. |
| `notion` | `search`, `pages.get`, `blocks.children` | Integration token with shared pages and Read content; title search, direct blocks, API version `2026-03-11`. |
| `dropbox` | `files.list`, `files.continue`, `files.get`, `files.download` | Owner token with metadata/content read scopes; file download capped at 10 MiB. |
| `readwise-reader` | `documents.list`, `documents.get` | Readwise token; Reader v3 metadata and selected document HTML. HTML remains untrusted. |
| `github` | `repos.list`, `issues.list`, `contents.get` | Owner token with selected repository permissions; GitHub.com, API version `2026-03-10`. |
| `slack` | `conversations.list`, `conversations.history` | Owner bot/user token and matching conversation scopes; history capped at 15 messages. |
| `telegram-bot` | `identity.get`, `updates.peek` | Owner bot token; queued updates without advancing the offset. |
| `rss-feed` | `entries.list` | Owner JSON credential `{ "url": "https://example.com/feed.xml" }`; RSS/Atom, no crawling. |
| `caldav`, `icloud-calendar` | `calendars.query` | Owner collection credentials; original ICS from an explicit window of at most seven days, without recurrence expansion. |
| `carddav`, `icloud-contacts` | `contacts.list` | Owner collection credentials; bounded contact fields, optional name/email query. |
| `whatsapp-import` | `records.list` | Owner-selected chat text or ZIP export; retained originals and message provenance, not live access. |
| `telegram-import` | `records.list` | Telegram Desktop JSON snapshot; original messages/chat IDs, not a live session. |
| `wechat-import` | `records.list` | Owner-prepared UTF-8 CSV; not official export or encrypted backup access. |
| `calendar-import` | `records.list` | Owner-selected ICS snapshot; original events/recurrence fields, no recurrence expansion. |
| `markdown-import` | `records.list` | Owner-selected Markdown snapshot; original document content. |

Google Calendar event reads omit descriptions, attendees, attachments and locations. Free/busy output is limited to the requested calendar and clips busy intervals to the approved request. Unknown upstream calendar availability is an error, not an empty free interval. Graph reads project selected fields and strip continuation/download URLs. Calendar and Graph pages report incompleteness without accepting a provider continuation URL. Other providers support only the cursor fields advertised by their schemas; an empty page is not evidence that an account is empty.

Google Docs/Sheets/Chat, Microsoft Contacts/OneNote/Teams, Asana, Airtable, Linear, GitLab, Box, Discord Bot, Feishu/Lark and durable `whatsapp-business` webhook ingestion are additionally implemented. See [Data source CLI](data-source-cli.md) for complete operations, account setup, paging boundaries and CLI commands. New live providers still require owner-authorized account acceptance.

`telegram-user`, `health-connect`, and `google-photos-picker` remain hidden `not_implemented` entries, requiring a user-session implementation, device bridge and Picker session/media retrieval respectively. See [messaging readiness](hub-messaging-readiness.md).

## Owner setup

Set `EDC_HUB_CREDENTIAL_KEY` to a privately stored base64-encoded 32-byte key. Losing the key makes existing secrets unreadable; changing it is not a key migration. Do not place secrets in command arguments, logs, the repository or a conversation.

For manual token providers, pipe a private token into the owner CLI:

```sh
edc --config ./owner-private.json source add \
  --provider notion --account ACCOUNT_ID --name 'Personal notes' --credential-stdin
```

The command reads the credential from stdin. Repeat for each account and provider. Manual account IDs are owner-declared, not provider-verified. Replacing credentials for the same provider/account revokes earlier Agent grants. Microsoft/SaaS tokens do not have automated browser onboarding or refresh; the operator must renew them. Fine-grained upstream credentials are preferable because Hub read-only operations do not reduce a broader provider token's authority outside the Hub.

DAV setup uses JSON credentials with exactly `url`, `username`, and `password`. The URL selects one calendar/address-book collection and must end in `/`. Discovery of collection URLs is not implemented. iCloud requires an `icloud.com` host and an Apple app-specific password. DAV/feed endpoints must use public HTTPS on port 443; private-network deployments are currently rejected. Redirects, private DNS answers, resource-link following and proxies are disabled. A CalDAV operation grant covers the selected collection; its time query is a provider filter, **not** the independently enforced per-event boundary of Google/Microsoft Calendar grants. Original recurrence masters may describe dates outside that query.

## Google browser OAuth

Configure `EDC_HUB_GOOGLE_CLIENT_ID`, `EDC_HUB_GOOGLE_CLIENT_SECRET`, `EDC_HUB_GOOGLE_REDIRECT_URL`, the encryption key, and the relevant APIs. Register the exact `/v1/hub/google/callback` URL with Google. Allow credentials only from trusted frontend origins; frontend requests include cookies. `GET /v1/hub/google/status` reports validated local configuration, not live Google availability.

Owner `POST /v1/hub/google/start` accepts `provider_id` and `display_name`. The flow binds one-use state and encrypted PKCE to the owner, initiating browser and selected provider, with ten-minute expiry. It checks all returned scopes. Drive/Gmail verify account identity with their provider APIs; Calendar/Tasks/Contacts/Docs/Sheets/Chat use OpenID Connect userinfo with `openid`. Tokens are encrypted and refresh on use near expiry, with compare-and-swap protection against reconnect/disconnect races.

Requested scopes are `drive.readonly`, `gmail.readonly`, Calendar's `calendar.calendarlist.readonly` + `calendar.events.readonly` + `calendar.events.freebusy`, `tasks.readonly`, `contacts.readonly`, `documents.readonly`, `spreadsheets.readonly`, or Chat’s `chat.spaces.readonly` + `chat.messages.readonly`, as appropriate. The new personal-data providers also request `openid`. Calendar onboarding currently requests the event and free/busy scopes together, even if an Agent later requests only free/busy. Google consent is account-wide; the Hub enforces narrower Agent grants separately. Selected-file Picker and availability-only OAuth onboarding are not implemented.

## Scoped Calendar grants

`google-calendar` and `microsoft-calendar` `events.list`, plus Google `freebusy.query`, require `constraints` with exactly `calendar_id`, `time_min`, and `time_max`. Timestamps require RFC3339 offsets and a positive interval of at most seven days. Requests bind the connection, operation, calendar and interval. Every execution checks exact calendar equality and interval containment before and after the upstream read. Fractional timestamp precision is preserved. Web and Android show these constraints and refuse approval for missing or malformed scope.

These constraints do not apply to `calendars.list`, imported ICS, or CalDAV `calendars.query`. Other operations remain account/connection-wide; query strings, labels and file IDs are not separate permission boundaries.

## Immutable imports

```sh
edc --config ./owner-private.json source import \
  --provider whatsapp-import --account PERSONAL_ACCOUNT --name 'Chat export' \
  --format whatsapp-text --file ./chat.txt
```

Use `calendar-import` with `--format ics` or `markdown-import` with `--format markdown`. The owner API is `POST /v1/hub/imports` with `provider_id`, `account_id`, `display_name`, `filename`, `format`, and `content`. Inputs are UTF-8, at most 1 MiB, and cannot contain NUL. The server stores immutable original bytes and a manifest under its data directory's `hub-imports`, with SHA-256, source/account/owner provenance and import time. Files use mode 0600; imports are local plaintext content, not encrypted backups. The connection stores an encrypted reference rather than the raw document.

Agent `records.list` accepts `offset` and `limit` after normal approval. Imports preserve original text; uncertain WhatsApp local timestamps are not guessed into UTC, and ICS recurrence is not expanded. Reimporting an account creates a new snapshot, retains the old original, and revokes grants to the previous snapshot. Disconnecting removes active access but does not delete originals or backups.

## Verification and remaining acceptance

Tests cover provider-shaped responses, fixed endpoint routing, schemas, read bounds, scope validation, credential/account isolation, wrong-key hiding, malformed configuration, pagination, SSRF defenses, immutable imports and provenance, revocation, and safe frontend rendering. Run `make check` and `make build`; Android has independent unit/build/lint checks in [Android setup](android-hub.md). These checks do not establish live consent, token validity, provider API enablement, device notification delivery or production deployment.

Telegram peek must be coordinated with other bot consumers and cannot run alongside a bot webhook. No arbitrary provider method, message sending, provider write, durable incremental sync or automatic account creation is exposed.

## Official integration references

- Google: [Calendar API](https://developers.google.com/workspace/calendar/api/guides/overview), [Tasks API](https://developers.google.com/tasks/reference/rest), [People API](https://developers.google.com/people/api/rest), [Drive scopes](https://developers.google.com/workspace/drive/api/guides/api-specific-auth), [Gmail scopes](https://developers.google.com/workspace/gmail/api/auth/scopes), [OAuth](https://developers.google.com/identity/protocols/oauth2/web-server).
- Microsoft: [Graph calendarView](https://learn.microsoft.com/en-us/graph/api/calendar-list-calendarview?view=graph-rest-1.0), [To Do](https://learn.microsoft.com/en-us/graph/api/resources/todo-overview?view=graph-rest-1.0), [driveItem](https://learn.microsoft.com/en-us/graph/api/resources/driveitem?view=graph-rest-1.0), [messages](https://learn.microsoft.com/en-us/graph/api/resources/message?view=graph-rest-1.0).
- SaaS: [Todoist](https://developer.todoist.com/api/v1/), [Notion](https://developers.notion.com/reference/post-search), [Dropbox](https://www.dropbox.com/developers/documentation/http/documentation), [Reader](https://readwise.io/reader_api), [GitHub](https://docs.github.com/en/rest/repos/repos), [Slack](https://docs.slack.dev/reference/methods/conversations.history/).
- Messaging and protocols: [Telegram Bot API](https://core.telegram.org/bots/api#getupdates), [Telegram user authorization](https://core.telegram.org/api/auth), [WhatsApp Business](https://developers.facebook.com/documentation/business-messaging/whatsapp/overview), [CalDAV](https://www.rfc-editor.org/rfc/rfc4791.html), [CardDAV](https://www.rfc-editor.org/rfc/rfc6352.html), [Apple app-specific passwords](https://support.apple.com/en-us/102654).
