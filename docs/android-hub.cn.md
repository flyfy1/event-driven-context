[English](android-hub.md) | [简体中文](android-hub.cn.md)

# Android 授权收件箱与通知

现有 Android 应用新增了“授权”导航入口，打开“我的授权”。使用现有所有者账号登录你自己的 HTTPS Hub。此页面不要求选择项目，也不保存第三方数据源凭证。

收件箱默认显示未过期的待审批申请。“已授权及历史”显示已有授权、过往决定和已连接账号，并可撤销仍有效的授权。打开通知后，会先获取最新状态并优先展示对应申请；已处理、过期或不可用的申请会明确提示。收件箱显示 Agent 配对、数据访问申请、状态和到期时间。同一数据源的多个连接分别作为不同账号展示。批准数据访问前，检查 Agent、数据源账号、操作、原因和到期时间。操作授权适用于所选账号，不仅限于 Agent 提供的申请原因中提及的示例。账号缺失或已断开时无法批准。批准 Agent 配对将授予为期 7 天的身份访问权，并允许读取已连接账号目录；配对的 15 分钟到期时间是审核截止时间。批准 Agent 配对时，必须输入 CLI 显示的验证码；应用不会从所有者接口的返回值中自动填充。允许、拒绝和撤销都需要明确确认。已过期或时间格式无效的待处理项不能在本地批准，服务器仍是最终判断依据。

## 通知

每个 HTTPS 服务和所有者账号分别选择是否开启通知。Android 13 及以上需要通知权限。通知被禁止时，收件箱仍可使用。通知仅显示通用的待处理数量，应用内部 Intent 包含账号范围和申请标识；通知中不包含数据源令牌、文档、邮件、发件人或批准操作。

有两种通知方式：

- **定期检查：** WorkManager 在网络可用时轮询，最快每 15 分钟一次。Android 电池策略可能延迟检查。这是定期提醒，不是实时远程推送。
- **可选 Firebase Cloud Messaging（FCM）：** 已配置的构建将设备令牌注册到 Hub。包含 `type=authorization_changed` 的数据消息唤醒后台任务。任务先读取经过身份认证的收件箱，再显示通知，不直接展示远程消息内容。定期检查仍作为备用方式。实际送达时间仍取决于设备和 Android 调度。

会话变更和通知发布使用同一进程级锁。界面响应必须匹配原始服务地址、所有者、会话令牌和页面版本。退出登录会清除当前会话、取消定期任务及授权通知，并在撤销服务器会话前尝试注销 FCM 设备。已在途的请求不能将结果发布到不同会话中。通知偏好保留在原服务和账号范围内，再次登录该账号会恢复偏好。过期推送只会触发当前已登录且开启通知账号的新读取。离线退出无法保证立即删除服务器上的设备注册，因此推送消息必须保持通用内容。

## 可选 FCM 构建配置

默认构建无需 Firebase 项目，并明确显示远程推送未配置。要开启客户端，在你自己的 Firebase 项目中注册 Android 包 `life.integ.context`，通过构建环境提供全部四个公开的 Firebase 项目标识：

```sh
export EDC_FIREBASE_APP_ID='<Firebase mobile SDK app ID>'
export EDC_FIREBASE_API_KEY='<Firebase Android API key>'
export EDC_FIREBASE_PROJECT_ID='<Firebase project ID>'
export EDC_FIREBASE_SENDER_ID='<Firebase project number>'

cd app/android
JAVA_HOME=/opt/homebrew/opt/openjdk@17 \
ANDROID_SDK_ROOT=/Users/songyy/Library/Android/sdk \
./gradlew testDebugUnitTest assembleDebug lintDebug
```

四项必须全部设置或全部不设置。构建直接生成 Firebase 资源，无需 `google-services.json` 或 Google Services Gradle 插件。Firebase Messaging 自动初始化已关闭，只有当前账号选择开启通知后才请求注册令牌。未引入 Firebase Analytics。不要将服务账号私钥或第三方访问令牌放入这些变量或 APK。

仅客户端配置不足以提供推送。Hub 必须配置匹配的 FCM 服务器参数和服务器凭证，设备必须有可用的 Google Play 服务并授予通知权限。尚未实现 UnifiedPush。平台设置和送达限制请参阅 [Firebase Android 设置](https://firebase.google.com/docs/android/setup) 和 [FCM Android 消息接收指南](https://firebase.google.com/docs/cloud-messaging/android/receive)。

## API 约定

应用使用现有所有者 bearer 会话：

| 接口 | 用途 |
| --- | --- |
| `GET /v1/hub/devices/status` | 检查服务器 `push_configured` 状态。 |
| `GET /v1/hub/owner` | 读取 `agents`、`requests` 和 `connections`。 |
| `POST /v1/hub/agents/{id}/decision` | 提交 `decision`；批准时必须包含 `verification_code`。 |
| `POST /v1/hub/requests/{id}/decision` | 提交 `decision`（`approve`、`deny` 或 `revoke`）。 |
| `POST /v1/hub/devices` | 为已登录所有者注册 `{token, platform: "android"}`。 |
| `DELETE /v1/hub/devices` | 从该所有者移除 `{token}`。 |

所有者申请包含 `agent_name`、`connection_id`、`connection_name`、`operation`、`reason`、`status` 和 RFC3339 格式的 `expires_at`。连接包含 `id`、`provider_id`、`account_id`、`display_name` 和 `status`。Agent 记录包含 `id`、`name`、`status` 和 `expires_at`。设备令牌接口必须校验所有权，并防止同一个物理令牌在同一 Hub 上同时关联不同所有者。

## 验证边界

`testDebugUnitTest assembleDebug lintDebug` 验证源码、资源、打包，以及申请过期和服务地址、账号、会话变更相关测试；它不证明手机已经收到通知。在宣称 Android 推送端到端完成前，需要配置真实 Firebase 项目和服务器，在选定 Android 设备安装并开启通知，通过 CLI 触发配对及限定范围的数据申请，在手机批准，验证 CLI 访问与撤销。同时验证通知权限拒绝、后台送达、退出登录、切换账号、申请过期、离线恢复，以及退出后收到过期推送。默认构建不构成真机或真实 FCM 送达证据。
