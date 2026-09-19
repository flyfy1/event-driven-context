# 消息连接器接入状态

[English](hub-messaging-readiness.md) | [简体中文](hub-messaging-readiness.cn.md)

检查日期：2026-09-19。本文记录 [Agent Hub](agent-hub.cn.md) 不需要真实凭据的一项有限续作。模拟数据测试通过或测试载荷签名有效，都不代表真实账号已接通。

## WhatsApp 导出导入

用户主动选择的 `whatsapp-import` 路径已经实现。它保留不可变原始文件与账号来源，支持所有者范围内的多账号连接，并要求 Agent 单独获得 `records.list` 授权。完整 HTTP 模拟测试覆盖审批、读取和再次导入时旧授权失效。本阶段没有使用私人对话。真实地区时间戳与导出格式变体仍需用户提供代表性导出进行验收；解析器不会把不确定的本地日期猜测为 UTC。

## WhatsApp Business 校验准备

`backend/internal/hubwebhooks` 提供独立校验函数。它们没有注册 HTTP 端点，也未接入账号配置、持久化或 Agent 执行。`whatsapp-business` 仍为 `not_implemented`，没有可调用操作并保持隐藏。

校验器由私密应用配置和一份可信绑定构建：所有者 ID、Hub 连接 ID、WABA ID、商业号码 ID。每个账号分别绑定，不从 webhook 数据推断所有者。订阅挑战检查已配置的验证 token 和 `subscribe` 模式。POST 使用应用 secret 对准确的原始请求体验证 `X-Hub-Signature-256`，通过后才解释 JSON。验证 token 与应用 secret 用途不同。

已签名载荷必须包含正确的 WhatsApp 对象、支持的消息变更，且每个变更都匹配配置的 WABA 与号码。混合账号批次整体拒绝。函数限制载荷最多 1 MiB，拒绝有歧义的 JSON，返回经过认证的原始字节副本、账号绑定来源和内容哈希。消息/状态内容仍是不可信数据。签名与内容哈希**不能**证明新鲜度、实现投递去重或证明用户拥有商业账号。

启用真实接收前，宿主仍需完成：

1. 用户授权的 Meta 应用及私密 secret、验证 token、WABA/号码绑定和选定 HTTPS 回调主机。本阶段没有创建或访问这些资源。
2. HTTP 边界：限制原始请求体，拒绝重复签名请求头/查询参数，以纯文本响应订阅挑战，并限制请求及安全记录日志。
3. 仅用户可操作的配置、经过上游验证的身份、原子性的仅追加持久化、持久且按账号隔离的去重、可安全重试的确认，以及断开/重连行为。不能只凭全局消息 ID 或正文哈希跨所有者路由数据。
4. 单独的有界 Agent 读取 API 与操作授权，以及真实多账号 webhook、重放、撤销和故障验收。审批通知仍须与上游事件接收分开。

不能因为校验函数测试通过就启用接收器。完整接收仍等待用户选定 Meta 配置和回调环境。协议参考：[Meta webhook 校验说明](https://whatsapp.github.io/WhatsApp-Nodejs-SDK/api-reference/webhooks/start/)（已归档 SDK 文档，仅用于校验协议参考）及 [WhatsApp Business 平台](https://developers.facebook.com/documentation/business-messaging/whatsapp/overview)。

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

## 继续工作的前提

验证仅使用合成消息和密钥。Webhook 包测试覆盖原始请求体篡改、错误/格式不正确的签名、先认证后解析 JSON、跨账号及混合账号批次、有歧义/过大/过深的 JSON、订阅挑战、返回字节不可变和重复投递。运行 `go -C backend test -race ./internal/hubwebhooks`、`make check` 和 `make build`。这一准备阶段没有真实 Meta 验收，也没有新增网页/Android 流程需要验证。

| 队列项目 | 当前证据 | 缺少的外部输入 |
| --- | --- | --- |
| WhatsApp/ICS/Markdown 导入 | 解析、存储与完整 HTTP 模拟测试 | 用户选定的代表性导出，用于真实数据验收 |
| WhatsApp Business | 独立签名/账号校验测试；无接收路由 | 已授权的 Meta 配置和回调主机 |
| 个人 Telegram | 官方协议和依赖可行性评估 | 会话/缓存/运行时决策、应用凭据和已授权测试登录 |
| Google 及其他实时适配器 | 既有有界适配器/授权测试 | 用户配置的测试账号、OAuth/API 启用和真实同意 |
| Android 通知 | 既有单元测试/构建/lint 证据 | Firebase 配置及用户选定设备验收 |
| 公开发布 | 仅本地提交 | 明确允许将这些提交发布到公开仓库 |

后续定时检查应等待前提发生变化，不重复实现模拟流程、不要求用户在聊天中粘贴真实凭据，也不把受阻数据源描述为已连接。本阶段未部署、发布、订阅、访问私人账号或向第三方发送消息。
