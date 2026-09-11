# 第一版范围

- 用户：通过 CLI 或 MCP 为团队项目积累原始信息的人。
- 任务：可靠追加文本/文本文件，并按时间、metadata 找回。
- 核心假设：自由 metadata 加时间查询足以支持初步人工/agent 检索。
- 闭环：注册、登录 → 创建项目 → 添加成员 → 两个用户追加事件 → 查询、读取文件、发现 metadata。
- 成功证据：真实 CLI、官方 MCP 客户端通过 HTTP 和 stdio 调用；跨项目拒绝访问；重启保留数据；event 和原始文件落在 Git 忽略的数据目录，SQLite 不保存事件内容。
- 范围：Go 单体 + SQLite 身份授权 + 文件化 event 数据；项目直接包含事件；创建者管理成员，成员平等读写；事件不可修改和删除；自由 JSON metadata。
- 输入：直接文本或显式声明 MIME 的 UTF-8 文本文件，单份最多 1 MiB。文件使用可扩展的 base64 字节信封，未来支持其他 MIME，无需改事件结构。
- 时间：recorded_at 服务端生成；occurred_at 可选。默认按 recorded_at 查询，时间段为 [from,to)。
- metadata：支持顶层 key 的 JSON 精确相等、存在，条件 AND；列出字段/类型/事件数，以及单字段的去重值与计数，均限制在项目权限内。
- 接入：CLI、Codex、OpenAI API、Claude Code/Desktop 继续通过静态 Bearer 或 stdio；生产 Streamable HTTP 另提供 OAuth 2.1 Authorization Code + PKCE（S256）、discovery、DCR 与 `context:read` / `context:write`，供 ChatGPT 网页自定义连接器使用。OAuth 页面支持登录或按现有规则注册；新注册用户不自动加入既有项目。所有路径复用同一用户与项目成员权限。
- 不做：事件编辑/删除、异步消费者、消息队列、自动摘要、向量库；OAuth 暂不提供 refresh token、第三方 IdP 或管理员客户端控制台。
- 时间边界：先交付和验证上述闭环，再讨论数据处理逻辑。
