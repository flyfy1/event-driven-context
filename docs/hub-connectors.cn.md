# Hub 数据源连接器

[English](hub-connectors.md) | [简体中文](hub-connectors.cn.md)

## 实现与可用性

Hub 提供只读适配器、加密的多账号凭据、逐 Agent 的操作授权和用户主动选择的文件导入。这些是已实现的执行路径，不代表真实账号已经接通。模拟响应测试不需要真实账号。服务端不会调用模型；Agent 如果把读取结果交给外部模型，数据可能离开部署环境。

公开的 `GET /v1/hub/capabilities` 描述代码能力和参数 schema。需要登录的 `GET /v1/hub/integrations` 描述当前用户的部署和账号状态。已获批准的 Agent 使用 `GET /v1/hub/integrations-agent`，其中还会在每个账号操作上返回 `authorized` 以及自己的有效 `grants`（包括过期时间和约束）。发现接口不返回上游凭据。

| Registry 字段 | 含义 |
| --- | --- |
| `provider.implementation_status` | `adapter_available` 有可执行操作；`not_implemented` 没有。 |
| `deployment_configured` | 存在适配器和有效的本地加密密钥。 |
| `connectable` / `onboarding_method` | 可通过 `browser_oauth` 或 `owner_cli` 发起配置；与已有账号是否可用分开。 |
| `connected_account_count` | 已配置且保存的凭据能够在本地解密的账号数。 |
| `visible` | 适配器、本地凭据存储和至少一个已配置账号都可用。 |
| `hidden_reason` | `adapter_unavailable`、`credential_storage_unconfigured`、`credential_unavailable` 或 `no_configured_accounts`；可见时为空。 |
| `connections[].operations` | 账号对应的 API schema；Agent 响应还包括授权元数据。 |
| `feature_flags` | Hub 审批、Google OAuth 和推送配置的本地可用性。 |

`configured` 不代表上游 token 仍然有效。Registry 不探测上游，也不重新获取用户同意。Token 过期或缺少 scope 时，执行接口会返回上游错误。前端数据源卡片使用 Registry 的 `visible` 和操作列表；读取失败时隐藏操作，仅在浏览器授权可用时显示该接入入口。没有浏览器接入流程的服务仍可由用户通过 CLI 配置。仅保存 token 不会让未实现的适配器变为可见。

```sh
edc --config ./owner-private.json source integrations --owner
edc --config ./agent-private.json source integrations
```

## 已实现的数据源

每个数据源都支持多个账号的独立连接。所有操作都是有边界的读取，不是后台同步。调用前通过 `capabilities` 查看准确的输入 schema 和限制。

| Provider ID | 操作 | 配置方式与边界 |
| --- | --- | --- |
| `google-drive` | `files.list`, `files.get`, `files.export` | Google OAuth；元数据和 Workspace 文本/CSV/PDF 导出，不下载二进制文件。 |
| `gmail` | `messages.list`, `messages.get` | Google OAuth；邮件 ID 与 MIME 正文，不下载附件。 |
| `google-calendar` | `calendars.list`, `events.list`, `freebusy.query` | Google OAuth；基础事件/忙闲数据，必须授权指定日历和时间范围。 |
| `google-tasks` | `tasklists.list`, `tasks.list` | Google OAuth；单页任务列表或任务。 |
| `google-contacts` | `contacts.list` | Google OAuth；姓名、邮箱和电话。 |
| `microsoft-calendar` | `calendars.list`, `events.list` | 用户提供委托访问 Graph token；基础 calendarView 数据，必须授权指定日历和时间范围。 |
| `microsoft-todo` | `tasklists.list`, `tasks.list` | 带 `Tasks.Read` 的委托 Graph token。 |
| `onedrive` | `files.list`, `files.get` | 带 `Files.Read` 的委托 Graph token；仅元数据，不返回下载链接或正文。 |
| `outlook-mail` | `messages.list`, `messages.get` | 列表使用 `Mail.ReadBasic`，邮件正文使用 `Mail.Read`；不含附件。 |
| `todoist` | `tasks.list`, `tasks.get`, `projects.list` | 用户 token；API v1、活动任务/项目；OAuth 场景使用 `data:read`。 |
| `notion` | `search`, `pages.get`, `blocks.children` | 具有已共享页面和 Read content 权限的集成 token；标题搜索、直接子块，API 版本 `2026-03-11`。 |
| `dropbox` | `files.list`, `files.continue`, `files.get`, `files.download` | 用户 token 和元数据/内容读取 scope；下载上限 10 MiB。 |
| `readwise-reader` | `documents.list`, `documents.get` | Readwise token；Reader v3 元数据和指定文档 HTML。HTML 仍是不可信数据。 |
| `github` | `repos.list`, `issues.list`, `contents.get` | 用户 token 和选定仓库权限；GitHub.com，API 版本 `2026-03-10`。 |
| `slack` | `conversations.list`, `conversations.history` | 用户提供 bot/user token 和对应会话 scope；历史每页最多 15 条。 |
| `telegram-bot` | `identity.get`, `updates.peek` | 用户 bot token；查看排队更新但不推进 offset。 |
| `rss-feed` | `entries.list` | 用户 JSON 凭据 `{ "url": "https://example.com/feed.xml" }`；RSS/Atom，不抓取全文。 |
| `caldav`, `icloud-calendar` | `calendars.query` | 用户提供集合凭据；查询最多七天时间范围内的原始 ICS，不展开重复事件。 |
| `carddav`, `icloud-contacts` | `contacts.list` | 用户提供集合凭据；有限联系人字段，可按姓名/邮箱查询。 |
| `whatsapp-import` | `records.list` | 用户选择聊天文本导出；消息和原始来源信息，不是 WhatsApp 实时接入。 |
| `calendar-import` | `records.list` | 用户选择 ICS 快照；保留原始事件/重复规则，不展开重复事件。 |
| `markdown-import` | `records.list` | 用户选择 Markdown 快照；原始文档内容。 |

Google Calendar 事件读取不含描述、参与者、附件和地点。忙闲输出仅含指定日历，并将忙碌区间裁剪到批准的请求范围。上游日历忙闲状态未知时返回错误，不会把它当作空闲。Graph 仅读取选定字段，去掉分页/预授权下载 URL。Calendar 和 Graph 在单页不完整时明确标记，不接受上游后续页面 URL。其他数据源只支持 schema 公布的游标字段；空页不代表整个账号没有数据。

`telegram-user`、`whatsapp-business`、`health-connect`、`google-photos-picker` 仍为显式 `not_implemented`，没有操作并保持隐藏。它们分别需要 MTProto 用户会话实现、经过验证的商业 webhook 接入、原生设备采集桥接和 Picker 会话/媒体读取。保存凭据不会启用这些路径。

## 用户配置

将 `EDC_HUB_CREDENTIAL_KEY` 设置为私密保存的 base64 编码 32 字节密钥。丢失密钥会导致既有凭据无法读取；修改密钥不是密钥迁移。不要把凭据放到命令参数、日志、仓库或对话中。

手动 token 数据源通过标准输入传给用户 CLI：

```sh
edc --config ./owner-private.json source add \
  --provider notion --account ACCOUNT_ID --name 'Personal notes' --credential-stdin
```

该命令从 stdin 读取凭据。每个账号和数据源分别配置。手动账号 ID 是用户声明，未经上游验证。替换同一数据源/账号的凭据会撤销旧 Agent 授权。Microsoft/SaaS token 没有自动浏览器接入或刷新，须由操作者更新。优先使用上游细粒度凭据，因为 Hub 的只读操作不会缩小同一个 token 在 Hub 之外的权限。

DAV 使用恰好包含 `url`、`username` 和 `password` 的 JSON 凭据。URL 指定一个日历/通讯录集合，必须以 `/` 结尾。尚未实现集合 URL 自动发现。iCloud 要求 `icloud.com` 主机和 Apple 应用专用密码。DAV/Feed 只允许公网 HTTPS 443，暂不允许内网服务。禁止重定向、私网 DNS 地址、跟随资源链接和代理。CalDAV 操作授权覆盖选定集合；时间查询是上游过滤条件，**不具有** Google/Microsoft Calendar 授权中独立强制执行的逐事件范围边界。原始重复事件主记录可能描述查询区间之外的日期。

## Google 浏览器 OAuth

配置 `EDC_HUB_GOOGLE_CLIENT_ID`、`EDC_HUB_GOOGLE_CLIENT_SECRET`、`EDC_HUB_GOOGLE_REDIRECT_URL`、加密密钥及对应 API。在 Google 中注册准确的 `/v1/hub/google/callback` URL。只对可信前端来源允许携带凭据，前端请求需包含 cookie。`GET /v1/hub/google/status` 返回经过校验的本地配置状态，不代表 Google 实际可用。

用户 `POST /v1/hub/google/start` 接受 `provider_id` 和 `display_name`。流程通过一次性 state 和加密 PKCE 绑定用户、发起浏览器和选定数据源，十分钟过期，并检查全部返回 scope。Drive/Gmail 使用自身 API 验证账号，Calendar/Tasks/Contacts 通过 `openid` 和 OpenID Connect userinfo 验证。Token 加密保存，在接近过期时按需刷新，采用比较并交换避免与重连/断开竞争。

对应 scope 为 `drive.readonly`、`gmail.readonly`、Calendar 的 `calendar.calendarlist.readonly` + `calendar.events.readonly` + `calendar.events.freebusy`、`tasks.readonly` 或 `contacts.readonly`。新增个人数据源还请求 `openid`。Calendar 当前同时请求事件和忙闲 scope，即使 Agent 随后只申请忙闲。Google 同意范围是账号级，Hub 单独强制执行更窄的 Agent 授权。尚未实现选定文件 Picker 和仅忙闲的 OAuth 接入模式。

## 有范围的 Calendar 授权

`google-calendar`、`microsoft-calendar` 的 `events.list`，以及 Google 的 `freebusy.query`，必须带 `constraints`，且恰好含 `calendar_id`、`time_min`、`time_max`。时间戳必须含 RFC3339 时区偏移，区间为正且不超过七天。请求绑定连接、操作、日历和时间范围。每次执行在上游读取前后都检查日历完全匹配、调用范围被授权范围包含，并保留小数秒精度。网页和 Android 展示约束，缺少或格式错误时不能批准。

这些约束不适用于 `calendars.list`、导入的 ICS 或 CalDAV `calendars.query`。其他操作仍是账号/连接级授权；查询字符串、标签和文件 ID 不是单独的权限边界。

## 不可变导入

```sh
edc --config ./owner-private.json source import \
  --provider whatsapp-import --account PERSONAL_ACCOUNT --name 'Chat export' \
  --format whatsapp-text --file ./chat.txt
```

`calendar-import` 使用 `--format ics`，`markdown-import` 使用 `--format markdown`。用户 API 是 `POST /v1/hub/imports`，字段为 `provider_id`、`account_id`、`display_name`、`filename`、`format`、`content`。输入必须为 UTF-8、最多 1 MiB、不含 NUL。服务端在数据目录的 `hub-imports` 中保存不可变原始字节和清单，含 SHA-256、数据源/账号/所有者来源和导入时间。文件权限 0600；导入内容为本地明文，并非加密备份。连接中保存的是加密引用，而非原始正文。

Agent 获得正常授权后，用 `records.list` 的 `offset` 和 `limit` 分页读取。导入保留原文，不把不确定的 WhatsApp 本地时间猜测成 UTC，也不展开 ICS 重复规则。同一账号再次导入会创建新快照、保留旧原始内容并撤销旧快照授权。断开连接移除当前访问，不删除原始内容或备份。

## 验证与待验收事项

测试覆盖模拟上游响应、固定端点、schema、读取边界、范围校验、凭据/账号隔离、错误密钥隐藏、错误配置、分页、SSRF 防护、不可变导入与来源记录、撤销及安全前端渲染。运行 `make check` 和 `make build`；Android 的独立单元测试/构建/lint 见 [Android 配置](android-hub.cn.md)。这些检查不能证明真实同意流程、token 有效性、上游 API 启用、设备通知送达或生产部署。

Telegram peek 必须与其他 bot 消费者协调，不能与 bot webhook 并存。不开放任意上游方法、消息发送、上游写入、持久增量同步或自动创建账号。

## 官方接入参考

- Google：[Calendar API](https://developers.google.com/workspace/calendar/api/guides/overview)、[Tasks API](https://developers.google.com/tasks/reference/rest)、[People API](https://developers.google.com/people/api/rest)、[Drive scopes](https://developers.google.com/workspace/drive/api/guides/api-specific-auth)、[Gmail scopes](https://developers.google.com/workspace/gmail/api/auth/scopes)、[OAuth](https://developers.google.com/identity/protocols/oauth2/web-server)。
- Microsoft：[Graph calendarView](https://learn.microsoft.com/en-us/graph/api/calendar-list-calendarview?view=graph-rest-1.0)、[To Do](https://learn.microsoft.com/en-us/graph/api/resources/todo-overview?view=graph-rest-1.0)、[driveItem](https://learn.microsoft.com/en-us/graph/api/resources/driveitem?view=graph-rest-1.0)、[messages](https://learn.microsoft.com/en-us/graph/api/resources/message?view=graph-rest-1.0)。
- SaaS：[Todoist](https://developer.todoist.com/api/v1/)、[Notion](https://developers.notion.com/reference/post-search)、[Dropbox](https://www.dropbox.com/developers/documentation/http/documentation)、[Reader](https://readwise.io/reader_api)、[GitHub](https://docs.github.com/en/rest/repos/repos)、[Slack](https://docs.slack.dev/reference/methods/conversations.history/)。
- 消息与协议：[Telegram Bot API](https://core.telegram.org/bots/api#getupdates)、[Telegram 用户授权](https://core.telegram.org/api/auth)、[WhatsApp Business](https://developers.facebook.com/documentation/business-messaging/whatsapp/overview)、[CalDAV](https://www.rfc-editor.org/rfc/rfc4791.html)、[CardDAV](https://www.rfc-editor.org/rfc/rfc6352.html)、[Apple 应用专用密码](https://support.apple.com/en-us/102654)。
