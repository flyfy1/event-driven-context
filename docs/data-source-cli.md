# Data source CLI

[English](data-source-cli.md) | [简体中文](data-source-cli.cn.md)

The `edc` CLI can discover provider schemas, connect owner-controlled accounts, inspect an agent's effective grants, and read selected source data. It runs against a local/self-hosted server or an HTTPS deployment. A catalog entry is implementation support, not proof of account access. Install the CLI from [GitHub Releases](https://github.com/flyfy1/event-driven-context/releases/latest), or run `make build` and use `./bin/edc`. CLI v0.1.2 includes the commands below.

## Discover, connect and read

```sh
# Public schemas and setup requirements; no account credentials required.
edc --server https://YOUR_API source catalog --available-only
edc --server https://YOUR_API source operations --provider gmail

# Use the owner's existing edc login/config. Google connection is completed in
# the browser, so callback cookies stay with the initiating browser session.
edc --config ./owner-private.json source connect --provider gmail --name 'Work mail' --open
edc --config ./owner-private.json source list --owner

# Pair an agent using a separate private configuration (never share owner auth).
edc agent connect --owner OWNER_USERNAME --name 'My agent' --output ./agent-private.json
# The owner reviews the pairing code at hub.html before the following commands.
edc --config ./agent-private.json source integrations
edc --config ./agent-private.json source operations --connection CONNECTION_ID
edc --config ./agent-private.json access request \
  --connection CONNECTION_ID --operation messages.list \
  --reason 'Find the requested mail' --duration 1h
# The owner approves this operation separately in hub.html.
edc --config ./agent-private.json source read \
  --connection CONNECTION_ID --operation messages.list \
  --args '{"query":"newer_than:7d","limit":10}'
```

JSON output makes the same flow usable by scripts and agents. `source read` uses the same authorization as `api call`. `source operations --provider` describes code capabilities; `--connection` returns available account operations and current agent grants. Owner configurations may use `--connection ... --owner`. An `authorized` boolean does not replace calendar/window constraints. A nonzero exit and structured authorization error indicate that the agent must request owner approval.

`source connect` reports deployment prerequisites when unavailable. For Google it prints the browser URL, optionally opens it, and explicitly returns `connected: false` until browser OAuth succeeds. For manually configured providers it returns requirements, official documentation and the `source add --credential-stdin` command. It does not register third-party accounts or fabricate a successful connection. Token renewal for non-Google sources remains operator-controlled.

## Expanded read operations

Existing Gmail, Drive, calendars, tasks, contacts, Outlook, OneDrive, Notion, Slack, GitHub, Dropbox, Todoist, Readwise, Telegram Bot, RSS/Atom, DAV/iCloud and file imports remain supported. The following additional adapters use fixed official endpoints:

| Provider | Data | Setup |
| --- | --- | --- |
| `google-docs` | Document and all tabs | Google browser OAuth / token, `documents.readonly` |
| `google-sheets` | Workbook metadata and explicit A1 range | Google browser OAuth / token, `spreadsheets.readonly` |
| `google-chat` | Spaces and selected space messages | Google browser OAuth / token, Chat read scopes |
| `microsoft-contacts` | Names, email addresses and phone fields | Delegated Graph token, `Contacts.Read` |
| `microsoft-onenote` | Notebooks, pages and page HTML | Delegated Graph token, `Notes.Read` |
| `microsoft-teams` | Joined teams, channels and root messages | Work/school delegated Graph token and tenant-approved read scopes; replies excluded |
| `asana` | Workspaces, projects and tasks | Owner PAT / OAuth token |
| `airtable` | Bases and table records | PAT with selected bases and schema/record read scopes |
| `linear` | Current user, teams and issues | Owner API key with read permissions |
| `gitlab` | Membership projects and issues | GitLab.com PAT with `read_api` |
| `box` | Folder contents and file metadata | Owner OAuth access token; no file download |
| `discord-bot` | Bot identity, guild channels and messages | Installed bot token, channel permissions and message-content intent where needed |
| `feishu`, `lark` | Accessible chats and chat messages | User/tenant token and corresponding IM read permissions |
| `whatsapp-business` | Retained messages/statuses and authenticated original receipts | Private Meta app secret, verification token, WABA and phone binding; webhook subscription |

Gmail additionally supports `attachments.get` with `message_id` and `attachment_id`. The Gmail API returns base64url-encoded bytes and size; the response remains bounded to 10 MiB. Read and approve that operation separately. Spreadsheet ranges are explicit arguments. OneNote HTML and all provider text are untrusted source data; they are never instructions to the Hub.

The catalog is extensible through provider schemas, fixed-endpoint adapters and encrypted per-owner connections. No API argument can choose an arbitrary request host or provider method. Lists read one bounded page; consult the provider cursor and incompleteness fields. Teams and Feishu/Lark requests are capped at 50 items. Graph next links are validated and reduced to a supported cursor, never followed as an arbitrary URL. Token scope, channel membership, provider rate limits and tenant policies can still prevent a read.

## WhatsApp Business setup

Personal WhatsApp exports use `whatsapp-import`; they are snapshots. The Business path receives new webhook deliveries and does not fetch personal chat history, send messages or download media.

1. Use an owner-authorized Meta developer app and business phone. Configure `whatsapp_business_account` message subscriptions at Meta. A test phone can be used for acceptance.
2. Privately prepare a UTF-8 JSON credential containing exactly `app_secret`, `verify_token`, `waba_id`, `phone_number_id`. The secret is the Meta **app secret**, not a Graph bearer token. Generate a separate unpredictable verification token of at least 16 printable characters.
3. Add a connection through owner CLI using stdin:

```sh
edc --config ./owner-private.json source add \
  --provider whatsapp-business --account BUSINESS_PHONE_ID \
  --name 'Business messages' --credential-stdin < /PRIVATE/whatsapp-credential.json
```

4. Configure `https://YOUR_API/v1/hub/webhooks/whatsapp/CONNECTION_ID` as the HTTPS callback and the same verification token. GET answers the subscription challenge; POST validates the raw-body `X-Hub-Signature-256` HMAC and the configured WABA/phone on every change.
5. Pair an agent and separately approve `events.list` or `receipts.get`. Read with `source read --connection ... --operation events.list --args '{"after_sequence":0,"limit":20}'`. Continue using `next_sequence` when `has_more` is true.

The receiver limits bodies to 1 MiB and rejects duplicate signature headers/query parameters, ambiguous JSON, wrong-account and mixed-account batches. It atomically stores encrypted original receipts and account-scoped message/status records. Exact envelopes are deduplicated by SHA-256; repackaged deliveries of the same message/status identity do not create repeated records. Each event links to its first immutable receipt. `receipts.get` includes the exact original bytes as `original_base64`, its hash and recorded time. Deduplication does not establish timestamp freshness or independent ownership of a business phone. Reconfiguration revokes grants and stale verifier state; disconnect blocks future delivery/read access while retained originals remain stored.

## Account prerequisites and exploration

The code and synthetic contract/integration tests do not replace live acceptance. Google needs a deployer-owned OAuth application and enabled APIs; Meta needs an app, business phone and webhook subscription. Other sources require the owner's token or explicit consent. Account creation requires a selected email/name, completion of provider verification, and any organization permissions. Do not transmit passwords, verification codes or private tokens in chat.

Personal Telegram remains a separate MTProto/TDLib session project; a bot token cannot retrieve personal history. Health Connect needs an Android device bridge; Google Photos Picker needs a picker session. Those three catalog entries remain unavailable. No unofficial WhatsApp Web session scraping, outgoing messaging or account-password automation is introduced.

Official endpoint and permission references are linked in each provider's `documentation_url`. Start with [connector setup](hub-connectors.md), [Google OAuth](https://developers.google.com/identity/protocols/oauth2/web-server), [Discord message permissions](https://docs.discord.com/developers/resources/message#get-channel-messages), and [Meta's webhook payload reference](https://www.postman.com/meta/whatsapp-business-platform/folder/tduohwq/webhook-payload-reference). All newly added live adapters still need owner-authorized account acceptance; fixtures use synthetic data only.

## Personal chat history first

Personal WhatsApp supports official chat `.txt` or `.zip` exports directly. ZIPs are capped at 10 MiB and must contain exactly one UTF-8 chat text file of at most 1 MiB. The entire original ZIP, including any media, is retained; media is not indexed or read. Archives are never extracted to the filesystem; traversal, symlinks, ambiguous multiple texts and excessive expanded size are rejected. One export is not a promise of complete history; accept the range actually exported by WhatsApp.

Personal Telegram supports Desktop JSON exports: one chat's `messages` or a full account's `chats.list`, preserving chat IDs, senders, raw messages and rich text. WeChat currently accepts owner-prepared readable UTF-8 CSV with `timestamp,sender,text` and optional `chat_id`. This is an interchange format, not direct access to encrypted native backups or a live personal session. Original files use mode 0600 and the deployment's existing plaintext import storage; they are not encrypted backups.

```sh
edc --config ./owner-private.json source import \
  --provider whatsapp-import --account PERSONAL_CHAT --name 'WhatsApp personal' \
  --format whatsapp-zip --file ./chat.zip
edc --config ./owner-private.json source import \
  --provider telegram-import --account PERSONAL_ACCOUNT --name 'Telegram personal' \
  --format telegram-json --file ./result.json
edc --config ./owner-private.json source import \
  --provider wechat-import --account PERSONAL_CHAT --name 'WeChat prepared history' \
  --format wechat-csv --file ./wechat.csv
# Pair an agent and approve records.list for this connection first.
edc --config ./agent-private.json source read \
  --connection CONNECTION_ID --operation records.list --args '{"offset":0,"limit":20}'
```

See [WhatsApp official exports](https://faq.whatsapp.com/1180414079177245/?locale=en_US) and [Telegram official export instructions](https://telegram.org/blog/export-and-more).

[Google OAuth application setup and multiple mailbox connections](google-hub-setup.md).
