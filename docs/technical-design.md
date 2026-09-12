# Event-driven Context V2 技术设计

版本：2.0 · 日期：2026-09-12 · 状态：目标设计，按步骤实现与验收

本文把 [产品设计 V2](product-V2.md) 的功能映射到统一的数据和公开接口。公开字段和行为以产品 V2 第 7 节为准；内部存储布局、运行参数和框架选择由开发 Agent 根据现有代码确定，并用同一组契约测试验证。当前进度与证据记录在 [V2 实施与审查](v2-implementation.md)；本文不把设计目标描述成已完成能力。

旧版设计完整保留在 [technical-design-v1.md](archive/technical-design-v1.md)。V2 不以旧版 `query_context`、后端任务协调器或 inbox 改名代替 Event、File、State 与插件模型。

## 1. 设计目标

系统让同一项目的对话、随手记和录音形成可追溯的追加记录，并让不同客户端看到一致的项目状态。核心只提供事实存储、权限和并发边界；转录、概况、证据检索和每日回顾由可替换插件提供。

P1 必须形成一条完整路径：

1. App、CLI、hook、MCP 或 HTTP 向同一项目追加 Event；文件先作为 File 上传。
2. 插件处理器按 sequence 增量读取，在自己的权限内追加 derived Event 或发布 State。
3. 新会话读取项目概况，需要时查询原始证据，并把新决定作为 Event 写回。
4. Web 先完成共享项目、可恢复路由和中心登录主链；Android 随后展示同步状态、转录和按日期发布的回顾 State。

核心语义检索、向量库、图片分析执行、外部发布、提醒推荐、跨项目和插件市场不进入 P1。

## 2. 系统职责

```mermaid
flowchart LR
    Clients[Android / Web / CLI / hooks] --> API[HTTP API]
    Agents[Conversation agents] --> MCP[MCP]
    API --> Core[Core service]
    MCP --> Core
    Core --> Events[Event log]
    Core --> Files[File store]
    Core --> States[Versioned State]
    Host[Processor host] --> API
    Events --> Host
    Files --> Host
    Host --> Events
    Host --> States
```

- **Core service**：认证项目身份，追加与查询 Event，保存 File 和 State，管理插件安装与最小权限。它不运行模型或插件业务逻辑。
- **HTTP 与 MCP**：是同一服务能力的传输适配器，必须返回相同的 UUID、sequence、版本、权限结果和错误含义。
- **CLI、网页与 Android**：只通过公开接口工作，不读取服务端数据目录；客户端可声明 source，actor、producer 和插件权限由服务端认证确定。
- **Hook**：捕获客户端会话事件并调用 CLI；共享项目启用前必须明确确认。
- **Processor host**：持插件令牌运行处理器。失败时不推进游标，不把请求受理当作处理成功。

## 3. 统一数据模型

### Project

Project 是 Event、File、State、插件和成员权限的边界，并保存用于日期与计划运行解释的 IANA 时区。创建时可省略 `timezone`，服务端默认使用 `UTC`；owner 可用 `PATCH /v1/projects/{project_id}` 更新，成功直接返回 Project。插件自省返回当前 `project_timezone`，让每次处理使用与项目一致的时间语义；本次不增加 MCP 修改工具。

Project 同时是团队 context 的共享边界。项目可以有多位 owner；任一 owner 可按注册用户名添加成员，也可把其他成员提升为 owner 或降为普通 member。服务端在事务中保证项目至少保留一位 owner。项目列表按当前登录用户的成员关系返回，并通过 `owner_user_ids` 返回完整 owner 集合；`owner_user_id` 暂时保留为旧客户端的原始 owner 字段。加入后，成员通过 HTTP、MCP、CLI、网页或 App 读取同一份 Event、File 和公开 State，并可向同一项目追加 Event。

Web 用 `?project=<project_id>#<view>` 表达当前项目与分区，并可把该 URL 作为成员间的共享链接。服务端成员权限仍是访问边界；未知或无权 project 不能回退到列表中的其他项目。

### Event

Event 是不可覆盖的项目记录，分为 `log`、`note`、`derived`。写入方提供 UUID；服务端补充 sequence、recorded_at 和经过认证的 actor。

- 用户 Event 的 actor 固定为当前登录用户的 `type=user`、用户 ID 和 username；插件 Event 固定为 `type=plugin`、插件 ID 和 `on_behalf_of`。EventInput 不提供可由调用者指定的 actor 字段。
- 同项目同 UUID、同规范内容返回 `duplicate`；内容不同返回 `conflict`。
- metadata 与 source 是写入方声明，不能改变 actor、权限或可信级别。
- refs 只能指向同项目已有 Event，用于表达取代、撤回、完成、派生和回复。
- `derived` 只允许插件身份写入，并受安装清单约束。
- 批量写入逐条返回结果；局部失败不能回滚已成功项，也不能被客户端报告为整批成功。

### File

File 保存 Event 引用的原始字节。上传按“项目 + SHA-256”去重，读取始终重新检查项目权限，不生成长期公开链接。

- 客户端先上传 File，再用返回的 `file_id` 追加 Event。
- 服务端和客户端都核对大小、摘要和允许的媒体类型。
- 未引用文件可按保留策略清理；已被 Event 引用的文件不能因并发清理丢失。
- HTTP 支持较大文件流式传输；MCP 只承载限定大小的 base64 小文件。

### State

State 是插件发布的可重建项目视图，不是原始记录。key 使用 `<plugin_id>/<name>`，每次发布产生新版本并保留历史。

- `expected_version` 提供乐观并发控制；不匹配返回版本冲突且不覆盖旧版本。
- `based_on_sequence` 与项目最新 sequence 形成 lag，所有客户端用同一含义展示“已是最新”或“仍有记录未处理”。
- refs 指向形成该状态的 Event，界面可由 State 回到原文。
- 名称以 `_` 开头的 State 仅所属插件可读，用于游标和处理器内部状态。

### Plugin

插件清单固定版本、skill、State、处理器入口、配置与权限。安装生成项目和插件限定的令牌；修改配置产生修订，暂停或卸载立即阻止后续读写，既有 Event 与 State 保留。

插件只扩展三种能力：追加 derived Event、发布自己的 State、向 agent 提供 skill。P1 插件不能注册额外 HTTP 路由或 MCP 工具。

## 4. 功能与接口映射

| 功能 | HTTP | MCP | CLI / App 使用方式 |
|---|---|---|---|
| 身份 | Integ.Life start/callback、logout、me；CLI 兼容 register/login | 不暴露 | Web 使用 Context HttpOnly Session；CLI、hook 与 MCP 保存私有 Bearer token |
| 项目与成员 | projects、project timezone、project members | list/create projects，list/add members | owner 添加注册成员；成员在各入口看到并读写同一项目；Project 响应始终带 timezone |
| 追加记录 | project events 批量写入 | `record_events` | `edc push`、hook、App 共享 UUID 与逐项结果规则 |
| 查询与原文 | events query、event get、metadata | `query_events`、`get_event`、`list_metadata` | 网页、skill、`edc query/get/pull` 使用同一筛选和游标 |
| 文件 | multipart upload、认证原始下载 | `upload_file`、`get_file` | Android 与 `edc push --file` 先传 File 再写 Event |
| State | list、按 key/version get、put | `list_state`、`get_state`、`put_state` | App 回顾、会话背景、网页状态与 CLI 使用同一版本和 lag |
| 插件管理 | install、list、patch、run、delete | 不暴露 | 网页、App 与 CLI 管理安装；处理器只持插件令牌 |
| 对话接入 | `/mcp` | 标准工具集 | `edc mcp` 提供同一工具实现的 stdio 入口 |

项目、事件、文件、State 和插件的标识必须同时出现在路径或认证边界内。适配器不得仅相信请求体中的项目、actor、producer 或 plugin ID；响应也要防止把另一个项目的数据当成成功结果。

查询条件全部按 AND 组合，结果按 sequence 升序。分页 cursor 固定第一页快照，`after_sequence` 用于 pull 与处理器增量读取。HTTP、MCP 和 CLI 对相同输入必须观察到相同的事件内容与顺序。

## 5. 关键端到端流程

### 文本、会话与批量写入

CLI、hook 和 skill 在写入前生成稳定 UUID。重试复用原 UUID；JSONL 的每一项保留自己的成功、重复或失败结果。目录绑定、hook 安装和持久 outbox 已接入这条路径；网络失败保留输入，后续恢复发送。

SessionStart 由 hook 读取插件声明的 `session_context` State。后续证据查询仍读取 Event；State 不能替代原文或掩盖 lag。

### Android 录音

Android 在录制开始时生成稳定 capture UUID，并把账户、项目、文件和待上传状态持久化。本机文件关闭且可读取后进入队列：

1. 上传 File 并校验服务端返回的大小和 SHA-256。
2. 使用 capture UUID 追加引用该 `file_id` 的 note Event。
3. 只有 Event 成功或 duplicate 后才标记服务端同步完成。
4. 登录过期、断网、进程中断或回执丢失时保留同一队列项并重试。

文件上传成功但 Event 未确认时仍是待同步状态。账号切换不能发送旧账号队列，转录失败也不能删除或阻止播放原录音。

### 插件处理

Processor host 用插件私有 State 保存游标，通过 `after_sequence` 拉取输入。当前转录输出 UUID 由项目、插件、输入 Event 和输出槽位稳定决定；发布 State 时带 expected_version。输出成功后更新游标，失败不跳过输入。历史重转录的新 generation 尚未实现。

Agent 或处理器整理团队记录时以 `actor` 判断写入者，以 refs 保留原 Event。`source.channel` 只说明进入渠道；导入旧会话时，执行导入的账号仍是 actor，原会话角色应留在 source 或 metadata，不能冒充为经过认证的成员发言。概况、冲突和回顾若需要区分成员说法，应显示成员 username 并允许打开原 Event。

手动 run 接口只表示请求已持久接受。实际执行、重试、错误状态和用量由 host 与插件状态呈现，不由 Core 假装同步完成。

## 6. P1 四个插件

| 插件 | 输入与输出 | 一致性要求 |
|---|---|---|
| `audio-transcribe` | 音频 File Event → 引用原录音的 derived 转录 | 保留原语言；失败不推进游标；历史重跑 generation 待实现 |
| `project-brief` | Event → `project-brief/current` State | 决定、约束、待办、问题均带来源；冲突不按时间自动选边 |
| `daily-review` | 计划时间范围内的 Event → 日期 State | 进展、决定、待办、问题、建议分开；待转录录音可见；错过运行按产品规则处理 |
| `evidence` | agent 读取概况并查询 Event | 返回来源、冲突、缺口与查询范围，不把 State 或摘要伪装成独立原始证据 |

网页、Android、CLI 和对话 agent 读取的是这些相同产物。用户纠正通过追加带 refs 的 note 生效，不直接编辑 State。

## 7. 身份、权限与错误

用户令牌按项目成员身份工作，并受 OAuth scope 限制。插件令牌固定到一个安装、项目、版本和权限集合；每次操作都重新检查暂停、卸载与修订状态。

Web 默认从 Context 后端进入 Integ.Life 中心 Google 登录。产品后端生成并校验 PKCE/state、读取中心确认的 email，再签发自己的 host-only HttpOnly Session；浏览器所有 API 和文件请求携带该 cookie，中心 token 不进入前端 URL 或存储。`return_to` 只接受产品同源相对路径，并保留 project query、当前 view hash 与 locale。CLI Bearer 登录继续兼容。

本地用户以 `(issuer, sub)` 唯一绑定。新用户缺少中心确认 email 时拒绝创建；旧用户首次绑定可按人工确认的 email 命中原 ID，随后固定 sub。`songyy` 与 `cwhy` 均绑定原 ID，其既有 Project、成员关系和 Event actor 不迁移、不重建；实际邮箱只保存在受控迁移证据和工作日志中。

- 普通用户不能通过请求字段冒充插件写 `derived` 或 producer。
- 插件只能读取清单允许的事件、引用可读事件，并写自己的 State 命名空间。
- 项目 owner 代表已安装插件发布 State 时仍要通过显式的 `as_plugin_id` 授权检查。
- 日志不得包含 token、原始音视频或完整模型输出。

所有入口使用同一稳定错误含义：认证失败、禁止、找不到、冲突、过大、非法引用、不支持媒体、State 版本冲突、命名空间禁止和插件暂停。UI 将错误码映射成当前语言；未知错误保留可诊断信息，但不能显示 token 或把失败状态改写成成功。

## 8. 产品状态一致性

同一事实在网页、Android 与 CLI 上使用一致状态：

- 本机已保存、File 已上传、Event 已同步、插件处理中、State 已更新是不同阶段。
- duplicate 是成功确认；conflict 和批次局部失败需要保留原输入并提示处理。
- State lag、插件暂停、处理失败、输出截断和查询范围不足必须明确展示。
- source 与 actor 分开显示；agent note、用户原话、插件 derived 和 State 不能互相冒充。
- 团队 Event 视图显示 actor username（必要时 ID）、recorded_at 和 source channel；项目成员入口显示当前成员，并允许 owner 添加已注册用户名。
- 四种目标语言覆盖同一流程、校验、错误、空状态和跨页选择；生成内容语言由插件配置决定，转录保留原语言。

网页先提供项目记录、项目状态、接入、成员共享、可恢复 URL 路由和插件管理。Android 在 Web 主链之后继续提供记录、回顾、我的，并复用相同项目、插件和 State 接口。界面优先显示用户可理解的名称与结果，ID 用于来源和诊断。

## 9. 实施顺序与当前边界

验收按产品第 10.2 节的依赖推进；当前平台优先级调整为先完成 Web 共享、路由与中心登录，再继续 Android 设备验收。已完成的 Android 代码与模拟器证据保留，不作为 Web 尚未上线能力的完成证明。

根据用户要求，公开契约已定义的功能可由不同 Agent 并行开发，GPT-6 持续审核；后一步不能用未验收的前置能力作完成证明。当前每项状态以 `v2-implementation.md` 为准；旧 P1 代码与测试只能作为历史参考。

当前 processor host 支持 `edc host run --once` 单次处理和 `--watch` 持续检查。它持插件令牌读取当前 Installation、项目时区与配置，经公共接口发布转录 derived、项目概况或每日回顾 State；CLI、网页、Android 与 MCP 读取同一份内容、版本和来源。

`daily-review` 按项目时区和配置时间（默认 21:00）处理最近到期的计划窗口，以 `recorded_at` 筛选相邻计划点之间的记录，发布 `daily-review/YYYY-MM-DD`。启动延迟不移动窗口；已发布日期保持不变，重试可从 State 恢复完成状态。离线错过多期时仅补最近一期并记录跳过日期，空日也发布说明；迟到转录进入下一期。窗口信息随日期 State 保存，App 与网页按 key 的日期展示并可打开 refs。

生产已验证真实定时发布与重复 tick 不改写，以及网页和 Android 模拟器的日期回顾来源导航。插件私有 `_requests` 的消费、手动重转录 generation 和物理手机验收仍未完成；manual run 接口目前只持久接受请求。
## 10. 契约验收

当前按用户最新决定，MVP 验收优先正常闭环、实际产出和跨客户端可读。安全加固与极端输入测试暂不作为发布门槛。以下保留为后续完整契约检查范围，已有证据不必重复执行：

- Event UUID 去重与冲突、批量局部结果、refs 同项目校验、快照分页；
- File 先传后引用、摘要去重、跨项目隔离、下载字节与摘要一致；
- State 历史版本、expected_version 冲突、lag 与私有命名空间；
- 插件令牌最小权限、暂停和卸载即时失效、管理身份不可伪造；
- 同一数据经 HTTP、MCP 和 CLI 往返后 UUID、sequence、内容、版本和错误一致。
- 两个真实成员从不同入口读写同一项目，成员 B 的 Event 由成员 A 读取时仍保留 B 的 actor ID、username 和服务端 recorded_at；网页显示同一作者，Agent 输出保留来源和必要归属。

已有真实 Claude hook 注入、Codex 第二客户端检索与 recorder 写回、ASR/概况处理和实际定时回顾证据。完整 P1 仍需补齐手动处理闭环、物理手机与剩余四语言/OAuth 场景，以及持续真实使用；既有合成验收不能替代这些证据。
