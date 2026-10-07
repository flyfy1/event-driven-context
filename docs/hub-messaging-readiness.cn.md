# 消息连接器接入状态

[English](hub-messaging-readiness.md) | [简体中文](hub-messaging-readiness.cn.md)

检查日期：2026-10-07。本文记录 [Agent Hub](agent-hub.cn.md) 的实现和真实验收前提。模拟数据测试通过或测试载荷签名有效，都不代表真实账号已接通。

## WhatsApp 导出导入

用户主动选择的 `whatsapp-import` 路径已经实现。它保留不可变原始文件与账号来源，支持所有者范围内的多账号连接，并要求 Agent 单独获得 `records.list` 授权。完整 HTTP 模拟测试覆盖审批、读取和再次导入时旧授权失效。本阶段没有使用私人对话。真实地区时间戳与导出格式变体仍需用户提供代表性导出进行验收；解析器不会把不确定的本地日期猜测为 UTC。

## WhatsApp Business 持久化接收

签名 webhook 接收器和 Agent 读取已实现。用户配置校验私密 app secret、verify token 和 WABA/号码绑定。GET/POST `/v1/hub/webhooks/whatsapp/{id}` 回答 challenge，并在解析前认证准确原始字节。每个 change 必须匹配指定账号；拒绝重复头/查询字段、歧义 JSON 和超过 1 MiB 的正文。

Core 原子保存加密的不可变回执和消息/状态记录。账号内哈希去重相同信封，消息 ID 和结构化状态身份去重重新打包投递。事务内检查重新配置并撤销旧授权；断开阻止后续接收/读取。`events.list` 按序号分页，`receipts.get` 保留原始字节的 base64 和哈希，两者分别需要 Agent 授权。模拟 API/Core 测试覆盖 challenge、篡改、错误号码、重放、分页、加密、整批拒绝、配置轮换、账号/授权隔离及撤销。见[完整 CLI 与配置](data-source-cli.cn.md)。

真实验收仍需用户 Meta 应用、商业号码、HTTPS 回调订阅和上游测试投递，没有创建或访问真实 Meta 账号。签名和去重不证明时间新鲜度或独立账号所有权，不获取个人历史/媒体。[Meta webhook 参考](https://www.postman.com/meta/whatsapp-business-platform/folder/tduohwq/webhook-payload-reference)。

## 个人 Telegram 可行性

Bot API token 不能提供个人 Telegram 会话。Telegram 文档要求应用 `api_id`/`api_hash` 配置以及交互式用户授权状态机，可能需要手机/邮箱验证码和两步验证密码。成功登录后使用上游返回的用户 ID 绑定会话，不使用人工标签作为已验证身份。参见[应用配置](https://core.telegram.org/api/obtaining_api_id)、[授权](https://core.telegram.org/api/auth)和 [TDLib 授权状态](https://core.telegram.org/tdlib/getting-started)。

**建议依赖：**官方 TDLib，通过可选本地进程提供小范围白名单接口。这是设计建议，尚未安装或实测。TDLib 提供客户端网络、更新处理和本地加密存储；其原生/JSON 接口意味着相比仓库现有 Go HTTP 适配器，需要额外打包工作。参见 [TDLib](https://core.telegram.org/tdlib/) 和[源码/构建说明](https://github.com/tdlib/td)。

实现前建议确定以下边界：

- 每个所有者/连接有独立会话目录和独立生成的加密密钥，密钥由 Hub 存储保护。不跨所有者或账号共享会话数据库。
- 仅用户可交互登录，短期状态绑定发起会话。Agent 永远不接触验证码、密码、应用 secret 或会话文件。没有明确授权，不自动登录或使用已有个人会话。
- 从选定普通云端会话和有界文本读取开始。排除秘密聊天、自动媒体下载、发消息、已读确认和任意 MTProto 调用。这些是建议的 Hub 限制，不是 Telegram 颁发的只读凭据。
- 上游请求和返回值都限制在批准的聊天 ID 和绝对范围。现有通用账号/操作授权不足以承诺“仅指定聊天”。展示这类审批文案之前，必须先定义并测试聊天约束。
- 明确是否保留本地消息缓存，不默认建立永久镜像。断开必须停止进程并撤销后续 Hub 授权；上游退出登录和本地保留数据需明确行为。

下一步需要用户确认接受上述会话/缓存模型及可选原生运行时，再由部署方提供 API 应用并授权测试登录。本阶段未添加依赖、登录流程、工作进程或个人历史 API。`telegram-user` 继续不可用并隐藏。

## 待真实验收

运行 `make check` 和 `make build`。模拟测试只用合成消息/密钥，不证明真实账号同意。CLI、服务和前端发布已获用户授权，实际公开路由需单独验收。

| 项目 | 已实现证据 | 所缺外部输入 |
| --- | --- | --- |
| WhatsApp/ICS/Markdown/Telegram/微信整理 CSV 导入 | 解析、存储和授权读取测试 | 用户选择的代表性导出；微信需要可读 CSV，不能直接读取原生备份 |
| WhatsApp Business | 签名接收、原子加密保存、重放去重和授权读取 | 用户 Meta 应用/号码配置及真实订阅 |
| 个人 Telegram 实时会话 | 官方协议与 TDLib 可行性评估 | 会话/运行时决策、应用凭据和授权登录 |
| Google 及其他实时数据源 | 固定端点读取、schema、CLI 发现和授权测试 | OAuth 应用、API 启用及用户同意/token |
| Android 通知 | 既有单元测试/构建/lint | Firebase 配置和指定设备验收 |

注册账号仍需服务验证，不能在聊天中索取密钥或验证码。等待这些输入时继续独立实现，不能声称模拟测试接通了私密账号。
