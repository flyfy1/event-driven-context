# 数据源 CLI

[English](data-source-cli.md) | [简体中文](data-source-cli.cn.md)

`edc` CLI 可以发现数据源 schema、接入用户控制的账号、查看 Agent 实际授权，并读取指定数据。可连接本地、自托管服务或 HTTPS 部署。目录中的条目表示代码支持，不证明真实账号已经接通。从 [GitHub Releases](https://github.com/flyfy1/event-driven-context/releases/latest) 安装，或运行 `make build` 后使用 `./bin/edc`。以下命令从 CLI v0.1.2 提供。

## 发现、接入与读取

```sh
# 公开 schema 和配置要求；不需要账号凭据。
edc --server https://YOUR_API source catalog --available-only
edc --server https://YOUR_API source operations --provider gmail

# 使用用户已有的 edc 登录/配置。Google 接入在浏览器内完成，
# 回调 cookie 与发起流程的浏览器会话保持一致。
edc --config ./owner-private.json source connect --provider gmail --name 'Work mail' --open
edc --config ./owner-private.json source list --owner

# 用独立私密配置配对 Agent，不能共享用户登录凭据。
edc agent connect --owner OWNER_USERNAME --name 'My agent' --output ./agent-private.json
# 用户在 hub.html 核对配对码后，执行以下命令。
edc --config ./agent-private.json source integrations
edc --config ./agent-private.json source operations --connection CONNECTION_ID
edc --config ./agent-private.json access request \
  --connection CONNECTION_ID --operation messages.list \
  --reason 'Find the requested mail' --duration 1h
# 用户在 hub.html 单独批准这个操作。
edc --config ./agent-private.json source read \
  --connection CONNECTION_ID --operation messages.list \
  --args '{"query":"newer_than:7d","limit":10}'
```

JSON 输出便于脚本和 Agent 使用。`source read` 与 `api call` 使用同一授权机制。`source operations --provider` 查看代码能力，`--connection` 返回账号操作及当前 Agent 授权。用户配置可使用 `--connection ... --owner`。`authorized` 布尔值不代替日历/时间窗口约束。非零退出和结构化授权错误表示 Agent 需要申请用户批准。

部署未配置时，`source connect` 返回所缺前提。Google 返回浏览器 URL，可选择打开，并在浏览器 OAuth 成功前明确返回 `connected: false`。手动配置的数据源会返回要求、官方文档和 `source add --credential-stdin` 命令。不会自动注册第三方账号或虚构接通结果。非 Google token 仍由操作者续期。

## 新增读取操作

既有 Gmail、Drive、日历、任务、联系人、Outlook、OneDrive、Notion、Slack、GitHub、Dropbox、Todoist、Readwise、Telegram Bot、RSS/Atom、DAV/iCloud 和文件导入继续支持。以下新增适配器使用固定官方端点：

| 数据源 | 数据 | 配置 |
| --- | --- | --- |
| `google-docs` | 文档及全部标签页 | Google 浏览器 OAuth / token，`documents.readonly` |
| `google-sheets` | 工作簿元数据和指定 A1 范围 | Google 浏览器 OAuth / token，`spreadsheets.readonly` |
| `google-chat` | 空间及指定空间消息 | Google 浏览器 OAuth / token，Chat 读取 scope |
| `microsoft-contacts` | 姓名、邮箱和电话字段 | 委托 Graph token，`Contacts.Read` |
| `microsoft-onenote` | 笔记本、页面和页面 HTML | 委托 Graph token，`Notes.Read` |
| `microsoft-teams` | 已加入团队、频道和根消息 | 工作/学校委托 Graph token 和租户批准的读取 scope；不含回复 |
| `asana` | 工作区、项目和任务 | 用户 PAT / OAuth token |
| `airtable` | Base 与表记录 | 限定 Base 的 PAT 和 schema/记录读取 scope |
| `linear` | 当前用户、团队和 Issue | 用户具有读取权限的 API key |
| `gitlab` | 成员项目和 Issue | GitLab.com PAT，`read_api` |
| `box` | 文件夹内容和文件元数据 | 用户 OAuth access token；不下载文件 |
| `discord-bot` | Bot 身份、服务器频道和消息 | 已安装 Bot token、频道权限，必要时启用消息内容 intent |
| `feishu`, `lark` | 有权访问的聊天和消息 | User/tenant token 及对应 IM 读取权限 |
| `whatsapp-business` | 留存的消息/状态和已认证原始回执 | 私密 Meta app secret、verify token、WABA/号码绑定及 webhook 订阅 |

Gmail 新增 `attachments.get`，参数为 `message_id` 和 `attachment_id`。Gmail API 返回 base64url 编码字节和大小，响应上限仍为 10 MiB；此操作需要独立批准。表格范围由显式参数指定。OneNote HTML 和全部来源正文都属于不可信数据，不能成为 Hub 的执行指令。

通过数据源 schema、固定端点适配器和按用户加密的账号连接扩展目录。API 参数不能指定任意主机或任意上游方法。列表每次只读取有上限的一页，需查看游标和不完整标记。Teams 和飞书/Lark 每页最多请求 50 条。Graph 后续链接经校验后仅提取支持的游标，不跟随任意 URL。Token scope、频道成员资格、上游限流和租户策略仍可能阻止读取。

## WhatsApp Business 配置

个人 WhatsApp 导出使用 `whatsapp-import`，属于快照。Business 接收新的 webhook 投递，不获取个人聊天历史、不发送消息、不下载媒体。

1. 使用用户授权的 Meta 开发者应用及商业号码，在 Meta 配置 `whatsapp_business_account` 消息订阅，可用测试号码验收。
2. 私密准备 UTF-8 JSON 凭据，恰好含 `app_secret`、`verify_token`、`waba_id`、`phone_number_id`。Secret 是 Meta **应用密钥**，不是 Graph bearer token。另生成至少 16 个可打印字符的不可预测 verify token。
3. 用用户 CLI 从标准输入添加连接：

```sh
edc --config ./owner-private.json source add \
  --provider whatsapp-business --account BUSINESS_PHONE_ID \
  --name 'Business messages' --credential-stdin < /PRIVATE/whatsapp-credential.json
```

4. 将 `https://YOUR_API/v1/hub/webhooks/whatsapp/CONNECTION_ID` 配为 HTTPS callback，并填同一 verify token。GET 回答订阅 challenge；POST 用原始字节校验 `X-Hub-Signature-256` HMAC，并检查每个 change 的 WABA/号码。
5. 配对 Agent 后单独批准 `events.list` 或 `receipts.get`。用 `source read --connection ... --operation events.list --args '{"after_sequence":0,"limit":20}'` 读取。`has_more` 为 true 时按 `next_sequence` 继续。

接收器限制正文 1 MiB，拒绝重复签名头/查询参数、歧义 JSON、错误账号和混合账号批次。原始回执与账号内消息/状态记录原子写入并加密。相同信封按 SHA-256 去重；重新打包投递的相同消息/状态身份不产生重复记录。每条事件关联首次不可变回执。`receipts.get` 用 `original_base64` 返回准确原始字节，并返回哈希和记录时间。去重不证明时间戳新鲜度或商业号码所有权。重新配置撤销授权并拒绝旧校验器状态；断开阻止后续投递/读取，保留已存原文。

## 账号前提与探索结论

代码和模拟契约/集成测试不能代替真实验收。Google 需要部署方 OAuth 应用并启用 API；Meta 需要应用、商业号码和 webhook 订阅；其他数据源需要用户 token 或明确同意。创建账号需要指定邮箱/名称、完成服务验证及必要组织权限。密码、验证码、私密 token 不应在聊天中传输。

个人 Telegram 是独立的 MTProto/TDLib 会话项目，Bot token 不能获取个人历史。Health Connect 需要 Android 设备桥接，Google Photos Picker 需要选择器会话。这三个目录条目仍不可用。没有引入非官方 WhatsApp Web 会话抓取、发送消息或账号密码自动化。

各数据源的 `documentation_url` 提供官方端点及权限文档。参见[连接器配置](hub-connectors.cn.md)、[Google OAuth](https://developers.google.com/identity/protocols/oauth2/web-server)、[Discord 消息权限](https://docs.discord.com/developers/resources/message#get-channel-messages)和 [Meta webhook payload 参考](https://www.postman.com/meta/whatsapp-business-platform/folder/tduohwq/webhook-payload-reference)。所有新增实时适配器仍需用户授权的账号验收；模拟测试只使用合成数据。

## 个人聊天记录优先

个人 WhatsApp 可直接导入官方聊天导出的 `.txt` 或 `.zip`。ZIP 最多 10 MiB，必须恰好包含一个最多 1 MiB 的 UTF-8 聊天文本；原始 ZIP（包括其中的媒体）完整保留，媒体暂不索引或读取。ZIP 不向文件系统解压，拒绝路径穿越、符号链接、多文本歧义及过大展开内容。单次导出不保证包含全部历史，按 WhatsApp 实际导出范围验收。

Telegram 个人账号使用 Desktop 的 JSON 导出（单聊天 `messages` 或全账号 `chats.list`），保留聊天 ID、发信人、原始消息及富文本。微信暂只接受用户已有的可读 UTF-8 CSV，列为 `timestamp,sender,text`，可加 `chat_id`；这是整理格式，不能直接读取微信加密备份，也不声称连接了实时个人账号。原始文件权限为 0600，按部署者已有文件导入机制存储为本地明文；不是加密备份。

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

参见 [WhatsApp 官方导出](https://faq.whatsapp.com/1180414079177245/?locale=en_US)和 [Telegram 官方导出说明](https://telegram.org/blog/export-and-more)。

[Google OAuth 应用配置与多邮箱接入](google-hub-setup.cn.md)。
