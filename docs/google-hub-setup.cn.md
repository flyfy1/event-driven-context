# Google Hub 应用配置

[English](google-hub-setup.md) | [简体中文](google-hub-setup.cn.md)

各账号的 OAuth 时序与 Integ.Auth 登录的区别，见[架构与多账号授权](hub-architecture.cn.md)。已有 Google Web client 可以新增 Hub 回调，同时保留身份登录回调。

一个部署方控制的 Google OAuth 应用可接入多个 Gmail 和 Calendar 账号，每个连接分别加密保存，并独立授予 Agent 权限。创建应用与授予邮箱访问分开。通过部署方控制的 Google 账号登录 [Google Cloud Console](https://console.cloud.google.com/)，不要共享账号密码或验证码。

1. 选择/创建项目，配置 OAuth 同意屏幕（应用名、支持邮箱、受众及联系人）。外部应用处于测试模式时，把计划接入的每个 Google 账号加入测试用户。受限 Gmail scope 面向更广用户分发时可能需要验证；测试/刷新行为取决于 Google 策略。
2. 启用 Gmail API、Google Calendar API 和其他选定服务。按服务分别请求 scope；授权 Gmail 不会静默接入 Calendar 或其他账号。
3. 创建 OAuth **Web application** client，注册准确回调 `https://YOUR_API/v1/hub/google/callback`。维护者部署为 `https://context-api.integ.life/v1/hub/google/callback`，自托管不必使用此公共部署。
4. 私密下载 client 配置，通过部署方已有私密环境机制设置 `EDC_HUB_GOOGLE_CLIENT_ID`、`EDC_HUB_GOOGLE_CLIENT_SECRET`、`EDC_HUB_GOOGLE_REDIRECT_URL`。保留原 `EDC_HUB_CREDENTIAL_KEY`，更换会导致既有凭据不可读。密钥不能贴进聊天或提交仓库。
5. 重启服务并检查 `GET /v1/hub/google/status`。`configured: true` 只证明本地应用配置有效。然后以用户身份登录 Context，运行 `edc source connect --provider gmail --name 'Personal mail' --open` 并完成浏览器流程。再用不同账号/名称重复，`google-calendar` 也单独接入。Google 在浏览器选择账号并要求同意。
6. 配对独立 Agent，仅批准所需操作。日历正文还需准确日历 ID 和正向、最多七天的 RFC3339 时间窗口。完成真实有边界读取、第二账号隔离、刷新和撤销验收后，才称账号已接通。

官方参考：[Web server OAuth](https://developers.google.com/identity/protocols/oauth2/web-server)、[Gmail scopes](https://developers.google.com/workspace/gmail/api/auth/scopes)、[Calendar scopes](https://developers.google.com/workspace/calendar/api/auth)。另见 [CLI 命令](data-source-cli.cn.md)和[连接器实现](hub-connectors.cn.md)。
