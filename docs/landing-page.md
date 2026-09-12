# Landing page · V2 integration

## 本轮范围

目标用户是在多个 AI 工具中推进同一项目的人。本轮完成的路径是：未登录访问首页 → 了解产品 → 进入中心登录与工作区；已登录访问根地址进入工作区，并可往返产品介绍。主要风险是导航丢失项目、分区或语言，或旧令牌覆盖中心登录身份。

保留现有视觉设计、工作区、接入说明和四种语言；不扩建产品能力，不修改中心认证服务，不执行生产部署。

## 与 product-V2.md 的对照

| V1 首页内容 | V2 对齐结果 |
|---|---|
| 以手动文本记录为主 | 跨工具项目上下文层；hook 自动日志、skill 主动 note、CLI/API 推送与网页文件记录 |
| 按问题提取背景 | 插件发布有版本、有来源和覆盖范围的项目概况 State，agent 按需读原始事件 |
| 安装 skill 获得后台服务 | 区分 skill、插件和 processor host；核心只保存、查询与授权，不调用模型 |
| 回顾进入收件箱 | 每日回顾发布为按日期命名的 State；Web 优先，移动端继续验证 |
| record_event、query_context、idempotency-key | V2 record_events、list_state/get_state、UUID、push、outbox 和明确 refs |
| 手写旧版 event-context skill | 使用 backend/skills/edc-recorder/SKILL.md 的当前内容，展示与下载保持一致 |
| 仅独立静态展示 | 接入已有 Session、中心登录回跳、退出与项目分区路由 |

概念示例仍明确标注为示例，不执行后台推理、不写记录。页面避免把完整 V2 方向宣称为全部上线能力。

## 入口与登录规则

- `/` 与 `/index.html` 是公开入口。验证 `/v1/me` 后，未登录保留介绍页；已登录跳转到 `workspace.html`。
- `/?page=about` 是明确查看介绍的入口，已登录也不强制跳走，按钮显示“返回工作区”。
- 工作区的品牌和“产品介绍”链接保留 project、当前分区、locale 与本地 API 参数；首页返回按钮恢复同一项目和分区。
- 原有根地址上的 `?project=...#state` 等深链，以及 `auth=complete` 中心登录回跳，转交工作区处理，保留目的地与权限检查。
- 成功退出后回到没有项目参数的首页。退出请求失败则保留会话并显示错误，不假装已经退出。
- 先验证中心 Cookie Session，再在 401 时兼容旧 Bearer；中心身份有效时清除旧缓存令牌。网络故障不清除登录信息、不循环跳转。
- API 地址和本地会话键复用 workspace-utils，避免将生产令牌发送给任意查询参数指定的服务器。

## 本轮验证

`node --test frontend/i18n.test.js frontend/workspace-utils.test.js frontend/navigation.test.js`：19 项通过。

覆盖四语资源一致性、静态资源、V2 Skill 同步、访客首页、已登录自动跳转、已登录查看介绍、中心回跳、原项目与分区恢复、过期会话、Cookie 与旧 Bearer 优先级、网络失败，以及实际 landing.js 控制器在隔离环境中的启动行为。JavaScript 语法和 diff 检查通过。

这些是本地自动化验证；本轮未进行真实浏览器中心登录验收或生产部署，不替代 V2 完整链路验收。上一版的桌面与窄屏视觉记录不作为本轮登录集成的新证据。

## 发布约束

保留 `frontend/index.html` 为产品首页并一起发布 landing、navigation 和 workspace 资源。不得再用工作区覆盖首页；语言测试已移除允许这种覆盖的旁路。现有 `scripts/deploy-frontend.sh` 会同步完整目录并为本地 JS/CSS 引用添加版本号。
