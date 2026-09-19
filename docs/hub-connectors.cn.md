# Hub 数据源连接器

[English](hub-connectors.md)

## 当前实现边界

`backend/internal/hubconnectors` 包实现有限制的第三方 HTTP 读取和 Google OAuth 辅助函数。它属于服务端适配层：调用方必须先认证 Agent，确定连接属于批准授权的用户，检查有效的操作授权，并从受保护的服务端存储中取得凭证。适配器测试通过不代表生产账号已经连接。

目标用户拥有多个账号，希望 Agent 能发现可用操作、申请权限并读取批准的数据。当前连接器阶段验证最重要的技术假设：明确的操作能够访问不同平台，同时不暴露凭证，也不接受任意上游地址。成功证据包括模拟真实平台协议的集成测试，以及部署凭证配置完成后真实用户授权的账号检查。本包不会调用模型，也不会发送消息。

## 数据源能力目录

`Catalog()` 返回公开能力、输入 JSON schema、认证方式、平台 OAuth 权限选项、前置条件和限制。`Lookup(providerID)` 返回一个数据源。目录中的可用性与账号配置、Agent 权限分开：

- `adapter_available`：存在可执行的适配器代码，不代表部署凭证已配置或用户已批准。
- `not_implemented`：不公布任何可执行操作。
- 应用当前保存的连接状态是 `needs_auth`、`configured` 或 `disconnected`。`configured` 仅表示已保存凭证，手工填写 token 不代表已经验证连接。Google OAuth 在保存前验证身份。连接器元数据契约还预留未配置、已连接、需要重新授权和受阻状态，供后续连接健康检查使用。
- 宿主必须按连接和操作检查 Agent 授权，不能从平台支持情况或账号连接状态推断权限。

| Provider ID | 已实现操作 | 当前边界 |
| --- | --- | --- |
| `google-drive` | `files.list`、`files.get`、`files.export` | 单页元数据；Workspace 文档可导出为 `text/plain`、`text/csv` 或 `application/pdf`。没有二进制文件下载或持久同步。 |
| `gmail` | `messages.list`、`messages.get` | 单页邮件 ID、会话 ID；完整邮件 MIME 内容。没有附件下载或持久同步。 |
| `telegram-bot` | `identity.get`、`updates.peek` | Bot 身份和排队中的更新；不读取个人账号历史，也不确认消费。 |
| `telegram-user` | 无 | 需要独立的用户授权 MTProto 会话，尚未实现。 |
| `whatsapp-business` | 无 | 商业 webhook 接收和验证尚未实现。 |
| `whatsapp-import` | 无 | 用户选择的个人聊天导出文件解析器尚未实现。 |

Google 列表操作接受 `query`、`page_token`、`limit`（1–100，默认 20）。`files.get` 需要 `file_id`；`files.export` 需要 `file_id` 和 `mime_type`；`messages.get` 需要 `message_id`。`updates.peek` 只接受 `limit`。列表结果保留平台分页字段，包括 Drive 的 `incompleteSearch`。调用方必须区分空页、未完整搜索和平台失败。

## 多账号和授权

`Connection` 分别保存 `id`、`owner_id`、`provider_id`、`account_id`、`display_name` 和 `status`。即使属于同一用户，两个 Gmail 账号也必须具有不同的连接 ID。账号身份应来自经过验证的平台响应；用户填写的标签不是已验证身份。名称不赋予权限。凭证不能只按 provider 存储，也不能在发现接口中返回。

适配器使用 `Client.Execute(context, providerID, operationID, credential, args)`。这个底层 API 不接受 Agent 身份，也不检查授权。宿主必须将平台和凭证绑定到已经授权的连接，并在每次执行前重新检查撤销状态。账号级操作授权允许该操作访问整个账号；如果没有在每个操作上强制检查限制，就不能承诺按文件、标签或聊天隔离。Gmail 查询过滤不会缩小平台 OAuth 权限范围。

## Google 授权

`GoogleOAuth` 生成 PKCE S256 授权链接，并执行授权码交换和 token 刷新。宿主提供已配置的 client ID、secret、准确注册的回调地址，以及可选 HTTP client。宿主必须生成随机 state 和 verifier，将短期、一次性的 state 记录绑定到已登录用户、连接及请求的平台权限，并在交换回调授权码前验证 state。回调页面必须避免通过日志或 referrer 泄露授权码。

辅助函数为 Drive 请求 `drive.readonly`，为 Gmail 请求 `gmail.readonly`，同时请求离线访问和明确同意。两者都是较宽的账号读取权限，用户必须在同意前看到范围。适配器也接受外部取得的 `drive.file` 凭证，但这里没有实现 Google Picker；即使适配器只提供读取，`drive.file` 本身仍包含平台写入能力。最新审核要求见 [Drive 权限文档](https://developers.google.com/workspace/drive/api/guides/api-specific-auth) 和 [Gmail 权限文档](https://developers.google.com/workspace/gmail/api/auth/scopes)。

`GoogleToken` 是敏感的服务端数据。应加密保存，按照 `expires_in` 计算过期时间，检查实际返回的权限而不是假设全部获批，并在刷新结果没有 refresh token 时保留已有值。不得向 Agent 返回 token。辅助函数不管理回调路由、凭证存储、撤销、账号身份或定时刷新。Google [服务端 OAuth 指南](https://developers.google.com/identity/protocols/oauth2/web-server) 描述了上游流程。

`backend/internal/api/hub_google.go` 和 `backend/internal/core/hub_google.go` 中的应用集成，在辅助函数之上实现了回调、state 持久化、账号验证、加密存储及使用时刷新：

- `GET /v1/hub/google/status` 返回部署配置是否就绪。
- 用户认证的 `POST /v1/hub/google/start` 接受 `provider_id` 和 `display_name`，返回 `authorization_url`。服务端需要配置 `EDC_HUB_GOOGLE_CLIENT_ID`、`EDC_HUB_GOOGLE_CLIENT_SECRET`、`EDC_HUB_GOOGLE_REDIRECT_URL`，以及经过 base64 编码的 32 字节 `EDC_HUB_CREDENTIAL_KEY`。
- 在 Google 注册准确的 `GET /v1/hub/google/callback` 地址。回调同时要求发起浏览器的 HttpOnly、SameSite=Lax cookie 和有效的一次性 state。数据库保存 state 哈希及加密的 PKCE verifier，十分钟后过期；每位用户最多十个待完成的授权。前端必须发送 `credentials: "include"`，部署 CORS 仅对受信任的 UI 来源允许凭证。
- 回调检查返回的读取权限，并通过 Drive `about.get` 或 Gmail `users.getProfile` 验证账号身份。不同的已验证账号建立不同连接。同一账号重新连接时保留连接 ID，但撤销已有 Agent 授权，需要用户明确批准新的授权。
- Access token 将在距离过期不足一分钟时刷新。加密凭证更新使用 compare-and-swap，避免正在进行的刷新覆盖较新的重新连接凭证，或恢复已断开的账号。没有刷新凭证时需要用户重新连接。没有后台同步 worker。

OAuth 集成测试覆盖用户绑定、浏览器绑定、state 一次性使用和过期、已验证身份、凭证刷新、多账号隔离及断开/重连竞争条件。测试不证明真实 Google 同意流程或部署配置已经完成。

## Telegram 和 WhatsApp 限制

Telegram Bot API 的 `getUpdates` 在传入更大的 offset 时会确认较早的更新；负 offset 可能丢弃排队中的更新。因此 `updates.peek` 拒绝 `offset` 和 `allowed_updates`，发送 `timeout=0`，不推进游标。它无法与已启用的 webhook 同时使用，且必须与使用同一 Bot 的其他消费者协调。这是有限的查看操作，不是持久接入 worker。Telegram 最多保留待处理更新 24 小时。参见 [Bot API](https://core.telegram.org/bots/api#getupdates)。个人 Telegram 账号使用[用户授权](https://core.telegram.org/api/auth)，需要独立的会话实现。

WhatsApp Business Platform 提供商业集成能力。这里不假定存在个人 WhatsApp 聊天历史 API。商业 webhook 接收需要配置商业账号、电话号码、签名验证、重放处理和账号来源记录。个人聊天导出需要独立的本地导入流程。两者目前都明确不可用。[Meta 官方 Cloud API 集合](https://www.postman.com/meta/whatsapp-business-platform/documentation/wlk6lh4/whatsapp-cloud-api) 描述商业账号和 webhook 边界；Meta 的[平台文档](https://developers.facebook.com/documentation/business-messaging/whatsapp/overview) 可能需要登录。

## 安全边界和验证证据

- HTTPS 域名和操作路径固定；资源 ID 不允许路径穿越或任意 URL。拒绝未知参数。
- 禁止重定向，不做应用层自动重试。超时最长 30 秒；每次平台响应最多 10 MiB，OAuth 响应最多 64 KiB。超大响应明确失败，不静默截断。
- 平台错误只包含稳定错误码和 HTTP 状态，不包含原始响应和 URL，特别是包含 Bot token 的 Telegram URL。自定义 transport 不得记录凭证或请求 URL。
- 平台返回内容仍然是不可信数据。宿主选择安全的下载方式，不能将任意内容渲染为可执行 HTML。
- 本地持久化由部署者控制。获得授权的 Agent 将内容交给外部模型时，数据仍可能离开设备。

在 `backend/` 中运行 `go test ./internal/hubconnectors`。测试通过本地 HTTP 服务模拟平台响应，检查端点路由、Google 分页和 PKCE、Telegram 只读行为、参数拒绝、重定向、大小上限和错误脱敏。不会使用真实用户凭证或私有数据源内容。真实 OAuth 同意和平台访问仍需部署验证。

下一步外部前置条件是部署者的 Google OAuth 凭证与 API 启用、用户授权的 Telegram Bot token，以及明确选择 WhatsApp 商业接入或个人导出。MTProto 会话、WhatsApp 接收/导入、附件下载和后台增量同步属于后续工作；目录不会将它们报告为已经连接。
