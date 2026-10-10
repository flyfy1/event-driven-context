# Agent 数据 Hub

[English](agent-hub.md) | [简体中文](agent-hub.cn.md)

架构图、各数据源接入方式，以及数据源同意与 Agent 授权的区别，见[架构与多账号授权](hub-architecture.cn.md)。

## MVP 约定

- **用户：** 同时使用多个 Agent 和多个外部账号的人。
- **任务：** Agent 发现可用 API 和账号，申请所需权限，等待用户在网页或 Android 中批准。
- **最大假设：** 用户能理解账号和操作级权限，并在不授权无关访问的情况下完成审批。
- **闭环：** 发现能力 → Agent 申请绑定 → 用户核对 CLI 验证码 → 发现账号 → 申请操作 → 用户批准 → 有限读取数据源 → 用户撤销。
- **证据：** 隔离本地服务中的真实 CLI 与浏览器流程；用户、Agent、账号隔离以及过期和撤销测试；连接器契约测试；Android 构建和真机验收分别记录。
- **不做：** Agent 自行批准、共享用户凭证、命令参数携带凭证、第三方写操作、自动模型调用，或将未配置的数据源称为已连接。
- **阶段边界：** 先交付授权与读取路径。真实账号接入需要用户控制的数据源配置；用户主动选择的导出文件使用本地快照导入；Android 推送需要 Firebase 配置和真机验收。

## Agent 命令

全局参数位于子命令前。`--server` 指向 Hub，不是第三方地址。公开能力目录包含操作说明和输入 JSON schema，不暴露账号数据。

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

`agent connect` 创建权限为 0600 的新文件，不覆盖已有文件，也不打印凭证。不要将其提交到 Git。将返回的审批网址交给用户，请用户核对自己发起的配置会话中的 CLI 验证码。待绑定申请 15 分钟后过期。批准后，Agent 获得有效期 7 天的身份，可以发现该用户所有已连接账号的名称和身份信息，**不自动获得内容读取权限**。审批页面明确说明账号目录本身的可见范围。

未认证的 `POST /v1/hub/agents` 对有效请求返回 `201`，包含 `id`、`name`、`verification_code`、`status: "pending"`、`created_at`、`expires_at`、`token` 和 `approval_url`，用户不存在或队列已满时也相同。每位用户最多同时有 10 个未过期的待批准 Agent（以及 1000 个未过期的待批准或已批准 Agent）；过期记录不计入。用户不存在或队列已满时，返回的注册不会持久化，其令牌无法获批或使用。此不透明响应避免暴露用户是否存在或队列容量，不代表申请已送达用户。注册耗时至少为 100 ms（取消请求除外），推送异步发送，以掩盖常规时间差异，但不保证高负载下的恒定耗时。独立的 IP/并发限制仍可返回通用的 `429`，与用户是否存在无关。

操作申请限定为一个连接和一个准确的 API 操作，有效期 1 分钟至 7 天，且不超过 Agent 身份的到期时间。有效期从申请时开始计算。申请原因是 Agent 提供的不可信文本。除下述日历限制外，批准后，该操作可以访问连接账号内所有可达资源；查询参数**不是**授权边界。目前不支持文件、文件夹、邮件或标签级权限。Agent 只能查看自己的申请。

缺少权限时，CLI 以非零状态退出并返回结构化 `authorization_required` 错误。Agent 应提交申请并请用户审阅，故意不提供 Agent 自行审批的命令。缺少连接或第三方凭证过期需要用户配置，并非扩大 Agent 授权。第三方读取均有大小与数量限制。仅当响应和输入 schema 提供可用游标时继续分页；Graph 和 DAV 可能报告结果不完整而不提供继续分页接口。导入快照使用数字偏移量。

## 感知部署状态的发现接口

```sh
# Approved Agent configuration
edc --config ./agent-private.json source integrations
# Owner configuration; Agent credentials cannot use this endpoint
edc --config ./owner-private.json source integrations --owner
```

需要身份验证的注册表返回数据源元数据、部署就绪状态、账号数量、`visible`、`hidden_reason`、`onboarding_method`、每个账号的操作和功能开关。Agent 视图额外包含 `authorized` 以及有效授权的到期时间和限制。已授权的日历操作仍需匹配准确日历和日期范围；不能只判断这个布尔值。

网页的数据源区域只显示注册表中可见、已配置且存在可用操作的账号。缺少适配器、加密配置、可解密凭证、账号或 API 时，相应数据源控件隐藏。单独折叠的**添加账号**入口仅提供已配置的 `browser_oauth` 数据源，也包括尚未连接账号但支持接入的 Google 数据源。注册表出错时隐藏数据源控件，已有授权申请和撤销操作仍可使用。登录账号或会话变化时刷新，页面可见时每 30 秒刷新。

注册表仅检查本地配置和凭证解密，不能证明第三方调用成功。Agent 完成绑定后，`source list` 仍提供更广泛的用户账号清单，可能包含 `needs_auth` 或已断开的记录；发现可用操作应使用 `source integrations`。公开的 `capabilities` 仍是包含明确不可用数据源的能力目录，不包含账号身份或凭证。

## 日历授权

Google Calendar 的 `events.list`、`freebusy.query` 和 Microsoft Calendar 的 `events.list` 必须指定连接、准确日历 ID，以及正向且不超过七天的日期范围。这个数据时间范围与授权有效期是两回事。时间戳使用带明确时区偏移的 RFC3339；执行时可以缩小已批准范围，但不能更换日历或扩大范围。服务端在读取第三方之前和返回结果之前检查这些边界。

```sh
edc --config ./agent-private.json access request \
  --connection CALENDAR_CONNECTION_ID --operation events.list \
  --reason 'Plan the approved calendar week' --duration 1h \
  --constraints '{"calendar_id":"primary","time_min":"2026-09-21T00:00:00+08:00","time_max":"2026-09-28T00:00:00+08:00"}'

edc --config ./agent-private.json api call \
  --connection CALENDAR_CONNECTION_ID --operation events.list \
  --args '{"calendar_id":"primary","time_min":"2026-09-21T00:00:00+08:00","time_max":"2026-09-22T00:00:00+08:00","limit":20}'
```

请使用该账号真实的日历 ID；`primary` 是 Google 示例，不是 Microsoft 的别名。接口返回基本日程字段或忙闲区间，不返回完整日程正文或附件。审批页面显示日历和时间边界，不支持或无效的限制无法批准。这些规则不适用于 DAV 的 `calendars.query`：DAV 连接选择一个集合，查询范围有限，但没有独立的授权级时间窗口限制。ICS 导入通过 `records.list` 返回原始快照组件，不是实时日历查询。

## 用户网页与 Android

工作区顶部提供 `hub.html`（**我的授权**）入口，登录后显示待处理数量。页面按 Agent、API 申请和注册表中可见的独立账号展示，支持验证码核对、批准、拒绝、撤销及断开账号。页面可见时每 30 秒刷新。对应服务端配置可用时，Google Drive、Gmail、Calendar、Tasks、Contacts、Docs、Sheets 和 Chat 支持浏览器 OAuth 接入；重复操作即可连接多个账号。

Android 复用用户登录态，提供原生“我的授权”页面。通知按账号和服务地址单独开启。默认构建使用受 Android 调度影响的定时检查；可选 FCM 仅发送通用唤醒信号，App 登录获取待办后才展示通知。参见 [Android 配置与验收](android-hub.cn.md)。网页提醒是页面内提醒，不是后台 Web Push。

## 数据源与账号分离

新增 Google Docs/Sheets/Chat、Microsoft Contacts/OneNote/Teams、Asana、Airtable、Linear、GitLab、Box、Discord Bot、飞书/Lark 和 WhatsApp Business；配置与操作见[数据源 CLI](data-source-cli.cn.md)。

每个连接分别具有 `id`、`provider_id`、已验证或由用户声明的 `account_id`、显示名、所有者和状态。同一种数据源支持多个账号。Google 浏览器接入会向数据源核实账号；手动凭证用于运维配置，不能证明真实账号访问已验证。`configured` 仅表示已保存凭证，不代表读取成功。

- Google：Drive、Gmail、Calendar、Tasks 和 Contacts，提供目录列出的操作。
- Microsoft Graph：Calendar、To Do、OneDrive 和 Outlook Mail，使用用户提供的委托访问 token。
- DAV：CalDAV/CardDAV 和 iCloud Calendar/Contacts，每个连接对应用户选择的一个公网 HTTPS 集合。
- SaaS：Todoist、Notion、Dropbox、Readwise Reader、GitHub 和 Slack，使用用户提供的 token 和明确的只读操作。
- Telegram Bot：机器人身份查询和不确认消费的更新预览，受 Bot API 和已有 webhook 限制。
- 导入快照：WhatsApp 聊天导出、ICS 日历文件和 Markdown 笔记。
- 个人 Telegram 会话、Android Health Connect 和 Google Photos Picker 在实现前仍明确不可用，并从普通数据源控件中隐藏。

参见[连接器细节及官方参考](hub-connectors.cn.md)。能力已实现、数据源已配置和 Agent 已获授权是三个不同状态。

## 自托管配置

使用部署环境现有的私密配置机制。不要提交密钥，也不要在对话中粘贴密钥。

| 变量 | 用途 |
| --- | --- |
| `EDC_HUB_CREDENTIAL_KEY` | Base64 编码的 32 字节加密密钥，用于第三方凭证、待完成 OAuth 的 PKCE verifier 和推送设备 token。 |
| `EDC_HUB_GOOGLE_CLIENT_ID` | 用户控制的 Google OAuth Web 应用 client ID。 |
| `EDC_HUB_GOOGLE_CLIENT_SECRET` | Google OAuth client secret。 |
| `EDC_HUB_GOOGLE_REDIRECT_URL` | 已登记的准确回调地址：`https://YOUR_API/v1/hub/google/callback`。 |
| `EDC_HUB_FCM_PROJECT_ID` | 与 Android App 配套的 Firebase 项目。 |
| `EDC_HUB_FCM_SERVICE_ACCOUNT_FILE` | 拥有 FCM 权限的服务账号私密文件。 |

通过 `-allowed-origins` 配置信任的前端 origin，并在现有 Web base 配置中指定前端地址。跨 origin 的 OAuth 发起请求必须携带 cookie。Google 流程使用一次性 state、加密 PKCE、回调浏览器绑定和过期检查，在明确告知用户后按数据源申请只读权限：`drive.readonly`、`gmail.readonly`、Calendar 的列表/日程/忙闲读取权限、`tasks.readonly` 或 `contacts.readonly`。Calendar、Tasks 和 Contacts 还使用 `openid` 核实身份。第三方同意范围可能覆盖整个账号；Hub 的日历授权会另行执行更窄的日历和日期边界。尚未实现选择文件的 Picker 接入。OAuth token 按需刷新，不能复活已断开的连接。

运维人员可以用已有用户凭证执行 `edc source add --provider ID --account ACCOUNT --name NAME --credential-stdin`。第三方凭证通过私密 stdin 输入，不能放在参数里。省略凭证时登记为 `needs_auth`；再次为同一数据源和账号提交新凭证会重新配置连接并撤销旧授权。`edc source disconnect CONNECTION_ID` 清除当前保存的凭证并撤销待审批和已批准授权，但不撤销第三方自己的 OAuth 同意，也不擦除文件系统备份。

Graph、DAV 和 SaaS 适配器目前采用用户手动配置，不提供浏览器接入或自动刷新。数据源 ID、所需权限和凭证格式参见[连接器细节](hub-connectors.cn.md)。DAV 使用包含 `url`、`username`、`password` 的 JSON 凭证指定一个集合；iCloud 必须使用 App 专用密码，不能使用 Apple 主密码。本地配置、已保存凭证和测试通过都不证明真实账号访问已验证。

授权元数据和加密凭证存储在身份 SQLite 数据库中；第三方读取结果按需返回，不追加到不可变项目事件中。WhatsApp Business webhook 额外保留加密原始回执和事件。导入功能还会在部署者的数据目录保存不可变原始文件和快照元数据。加密密钥保存在私密部署配置中，并与数据库分开备份。丢失密钥会导致旧凭证无法读取；不迁移数据而更换密钥不等于密钥轮换，目前没有自动迁移机制。

## 用户主动选择的导入

使用用户的普通配置，导入主动选择的 UTF-8 文件，最大 1 MiB；或导入最大 10 MiB 的 WhatsApp ZIP，其中须有一个最大 1 MiB 的 UTF-8 聊天文本。也支持 Telegram Desktop JSON 和整理好的微信 CSV。CLI 仅读取指定文件，不扫描文件夹，也不抓取附件。参见[个人聊天导入](data-source-cli.cn.md#个人聊天记录优先)。

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

最后一次 Agent 调用前，需要用户批准 `records.list`。按照 `next_offset` 继续读取，直到其值为 null。结果标识不可变快照，包含原始文件名、哈希、导入时间和记录数，不能称为实时连接。WhatsApp 解析保留原始消息、多行内容及不确定的源时间戳，不推断时区。ICS 返回原始 VEVENT 和日历元数据块，选定属性仅作为不可信文本，不展开重复日程。Markdown 返回原文档。

每次导入都以私密权限保存新的原始文件。再次导入同一数据源和账号会更新该连接的快照引用，并撤销先前授权，不覆盖旧文件。导入账号身份由用户声明。断开连接会移除当前访问权限和凭证，但不删除不可变原始文件或备份。Agent 不能用自己的凭证导入文件。

## 验证与待完成验收

实现包含 Agent 与用户凭证分离、跨用户隔离、多账号、准确操作授权、验证码、过期、撤销、重新连接后授权失效、OAuth state/PKCE、第三方请求大小与重定向限制、注册表可见性与无效状态处理、限定日历时间窗口、快照来源与分页、推送 token 归属、网页和手机旧会话响应、不可信文本渲染等测试。运行 `make check` 和 `make build`；Android 使用其独立检查命令。

[v0.1.2 发布](https://github.com/flyfy1/event-driven-context/releases/tag/v0.1.2)记录了 CLI 发布与部署检查。真实 Google 同意与刷新、真实 Graph/DAV/SaaS/Telegram 读取、Android 安装后界面与 FCM 到达，仍需对应账号或设备验收；本地测试及部署检查不代表这些真实数据源结果。

## 连接器持续推进队列

2026-10-07 新增的数据源读取、CLI 发现/接入/操作查询与 WhatsApp 持久化接收见[数据源 CLI](data-source-cli.cn.md)和[消息接入状态](hub-messaging-readiness.cn.md)。代码实现和真实账号验收分开；发布已获用户授权，账号注册仍需要用户选定的身份及验证。

每次完成一个可独立验证的步骤，记录准确证据，并提交、推送本任务文件：

1. 配置用户控制的 Google OAuth，用非敏感测试资料验证两个账号、读取、刷新和断开。
2. 使用用户明确提供的测试机器人验证 Telegram Bot 身份与预览，不消费其他应用的更新队列。
3. 使用用户提供的代表性样本验证 WhatsApp/ICS/Markdown 导出，包括不明确的时间戳，并保持仅为快照的说明。
4. 用户提供 Meta 应用、号码和回调主机后，验收已实现的 WhatsApp 持久化 webhook 接收；签名校验本身不提供重放保护或账号所有权验证。
5. 实现交互登录和聊天范围读取前，先由用户确认建议的个人 Telegram 会话/缓存模型及可选 TDLib 运行时；可行性说明与 Bot API 支持分开。
6. 使用非敏感测试账号验证用户配置的 Graph、DAV 和 SaaS 读取，检查账号隔离和结果不完整时的准确提示。
7. 配置 Firebase，在用户指定的 Android 设备安装并验证后台通知 → 审批 → CLI 获权 → 撤销。

需要用户登录、应用凭证或设备的工作应明确标记阻塞。不得虚构账号、扩大权限、向第三方发消息或重新部署无关服务。仅在可验证阶段完成、有意义的失败或需要用户输入时通知用户。
