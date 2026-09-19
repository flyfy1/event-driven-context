# Agent 数据 Hub

[English](agent-hub.md) | [简体中文](agent-hub.cn.md)

## MVP 约定

- **用户：** 同时使用多个 Agent 和多个外部账号的人。
- **任务：** Agent 发现可用 API 和账号，申请所需权限，等待用户在网页或 Android 中批准。
- **最大假设：** 用户能理解账号和操作级权限，并在不授权无关访问的情况下完成审批。
- **闭环：** 发现能力 → Agent 申请绑定 → 用户核对 CLI 验证码 → 发现账号 → 申请操作 → 用户批准 → 有限读取数据源 → 用户撤销。
- **证据：** 隔离本地服务中的真实 CLI 与浏览器流程；用户、Agent、账号隔离以及过期和撤销测试；连接器契约测试；Android 构建和真机验收分别记录。
- **不做：** Agent 自行批准、共享用户凭证、命令参数携带凭证、第三方写操作、自动模型调用，或将未配置的数据源称为已连接。
- **阶段边界：** 先交付授权与读取路径。真实账号接入需要用户控制的 Google/Telegram 配置；Android 推送需要 Firebase 配置和真机验收。

## Agent 命令

全局参数位于子命令前。`--server` 指向 Hub，不是第三方地址。公开能力目录包含操作说明和输入 JSON schema，不暴露账号数据。

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

`agent connect` 创建权限为 0600 的新文件，不覆盖已有文件，也不打印凭证。不要将其提交到 Git。将返回的审批网址交给用户，请用户核对自己发起的配置会话中的 CLI 验证码。待绑定申请 15 分钟后过期。批准后，Agent 获得有效期 7 天的身份，可以发现该用户所有已连接账号的名称和身份信息，**不自动获得内容读取权限**。审批页面明确说明账号目录本身的可见范围。

操作申请限定为一个连接和一个准确的 API 操作，有效期 1 分钟至 7 天，且不超过 Agent 身份的到期时间。有效期从申请时开始计算。申请原因是 Agent 提供的不可信文本。批准后，该操作可以访问连接账号内所有可达资源；查询参数**不是**授权边界。目前不支持文件、文件夹、邮件或标签级权限。Agent 只能查看自己的申请。

缺少权限时，CLI 以非零状态退出并返回结构化 `authorization_required` 错误。Agent 应提交申请并请用户审阅，故意不提供 Agent 自行审批的命令。缺少连接或第三方凭证过期需要用户配置，并非扩大 Agent 授权。第三方操作均为单页读取，应根据能力目录中的输入 schema 使用返回的第三方分页游标继续读取。

## 用户网页与 Android

工作区顶部提供 `hub.html`（**我的授权**）入口，登录后显示待处理数量。页面按 Agent、API 申请和独立账号展示，支持验证码核对、批准、拒绝、撤销及断开账号。页面可见时每 30 秒刷新。服务端配置完成后，Google Drive 和 Gmail 支持浏览器 OAuth 接入；重复操作即可连接多个账号。

Android 复用用户登录态，提供原生“我的授权”页面。通知按账号和服务地址单独开启。默认构建使用受 Android 调度影响的定时检查；可选 FCM 仅发送通用唤醒信号，App 登录获取待办后才展示通知。参见 [Android 配置与验收](android-hub.cn.md)。网页提醒是页面内提醒，不是后台 Web Push。

## 数据源与账号分离

每个连接分别具有 `id`、`provider_id`、已验证或由用户声明的 `account_id`、显示名、所有者和状态。同一种数据源支持多个账号。Google 浏览器接入会向数据源核实账号；手动凭证用于运维配置，不能证明真实账号访问已验证。`configured` 仅表示已保存凭证，不代表读取成功。

- Google Drive：文件元数据列表与读取，Google Workspace 文档的文本、CSV、PDF 导出。
- Gmail：邮件列表与读取。
- Telegram Bot：机器人身份查询和不确认消费的更新预览，受 Bot API 和已有 webhook 限制。
- 个人 Telegram、WhatsApp Business 消息接收和个人 WhatsApp 导出，在实现并验证之前会在目录中明确标为不可用。

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

通过 `-allowed-origins` 配置信任的前端 origin，并在现有 Web base 配置中指定前端地址。跨 origin 的 OAuth 发起请求必须携带 cookie。Google 流程使用一次性 state、加密 PKCE、回调浏览器绑定和过期检查，在明确告知用户后申请全账号 `drive.readonly` 和/或 `gmail.readonly`。尚未实现选择文件的 Picker 接入。OAuth token 按需刷新，不能复活已断开的连接。

运维人员可以用已有用户凭证执行 `edc source add --provider ID --account ACCOUNT --name NAME --credential-stdin`。第三方凭证通过私密 stdin 输入，不能放在参数里。省略凭证时登记为 `needs_auth`；再次为同一数据源和账号提交新凭证会重新配置连接并撤销旧授权。`edc source disconnect CONNECTION_ID` 清除当前保存的凭证并撤销待审批和已批准授权，但不撤销第三方自己的 OAuth 同意，也不擦除文件系统备份。

授权元数据和加密凭证存储在身份 SQLite 数据库中；第三方读取结果按需返回，不追加到不可变项目事件中。加密密钥保存在私密部署配置中，并与数据库分开备份。丢失密钥会导致旧凭证无法读取；不迁移数据而更换密钥不等于密钥轮换，目前没有自动迁移机制。

## 验证与待完成验收

实现包含 Agent 与用户凭证分离、跨用户隔离、多账号、准确操作授权、验证码、过期、撤销、重新连接后授权失效、OAuth state/PKCE、第三方请求大小与重定向限制、推送 token 归属、网页和手机旧会话响应、不可信文本渲染等测试。运行 `make check` 和 `make build`；Android 使用其独立检查命令。

当前验证属于本地范围。真实 Google 同意与刷新、真实 Telegram 读取、公开部署、Android 安装后界面与 FCM 到达，都需要外部配置或设备；本地测试不代表真实数据源或生产成功。

## 连接器持续推进队列

每次完成一个可独立验证的步骤，记录准确证据，并提交、推送本任务文件：

1. 配置用户控制的 Google OAuth，用非敏感测试资料验证两个账号、读取、刷新和断开。
2. 使用用户明确提供的测试机器人验证 Telegram Bot 身份与预览，不消费其他应用的更新队列。
3. 实现用户主动选择的个人 WhatsApp 聊天导出导入器，保留账号来源并进行样本测试，不宣称为实时同步。
4. 用户提供 Meta 应用、号码和回调主机后，实现验签的 WhatsApp Business webhook 接收。
5. 将个人 Telegram MTProto 授权、加密会话存储和依赖选择与 Bot API 分别评估。
6. 配置 Firebase，在用户指定的 Android 设备安装并验证后台通知 → 审批 → CLI 获权 → 撤销。

需要用户登录、应用凭证或设备的工作应明确标记阻塞。不得虚构账号、扩大权限、向第三方发消息或重新部署无关服务。仅在可验证阶段完成、有意义的失败或需要用户输入时通知用户。
