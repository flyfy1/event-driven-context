# V2 实施与审查

日期：2026-09-12。唯一当前范围来源：[product-V2.md](product-V2.md)，特别是第 7、9、10 节。Android 独立位于 `app/android/`；用户要求保留已经完成的 iOS 文件。此文记录实现决定与证据，不缩减产品要求。

## 当前执行优先级

用户最新要求以跑通 MVP 为重点，安全性工作暂不考虑。接下来集中实现和实际演示“写入记录 → 自动整理 → 查看结果 → 下一次对话使用”。暂停额外安全加固、极端输入测试和不影响主流程的审核扩展；现有基础能力继续使用。审核与集成代理优先发现和修复正常用户流程的阻断问题。以下历史检查记录保留为已有证据，不作为继续堆积验收门槛的理由。

团队共享和“谁写了什么”是 MVP 必须能力。Project 成员必须共同读取和追加同一 context；Event actor 必须由服务端登录身份产生，和客户端声明的 source、metadata 分开。Agent 生成概况、冲突与回顾时保留原 Event 引用，并在需要归属时使用 actor。

## 当前切片：团队共享与作者归属

生产 dogfood 项目已有 owner 与 `songyy` 两个真实成员。`songyy` 从 Chrome 写入 Event `01a09371-27f0-7008-8fe8-9f3a79987ae1`（sequence 148），网页显示作者 `songyy`；另一个已登录 owner 用 CLI 读取同一 Event，仍得到 `actor={type:user,id:usr_zhf5geavmwa4gv6xv6gh3b2pmj,username:songyy}`、`source={channel:api,client:web}` 和服务端 `recorded_at=2026-09-12T02:27:41.83258778Z`。这证明成员共享读写与作者绑定的 API/CLI/网页记录主链。

网页 Event 列表已显示 actor、记录时间和 source channel；成员列表和 owner 按注册用户名添加的控件已在 `da19592` 实现并由 Pages 提交 `ff3ab80` 发布，完成真实双身份浏览器验收前不标为完成。Android 能选择成员可见项目并读写 app Event，但当前记录模型未保留或展示 actor，因此 Android 的团队作者展示仍是明确缺口。

真实 `project-brief/current` 已更新到 v2、`based_on_sequence=148`、lag 0；摘要把 sequence 148 的团队共享和作者归属要求列为约束，并保留该 Event ref。这是 Agent 整理输出保留新要求与来源的生产证据。

## 当前切片：定时回顾

上一轮完成媒体 → 转录 → 概况 → 网页来源 → SessionStart 注入的真实主链。本轮只补 P1 中的自动定时与每日回顾：项目时区 → 按固定计划窗口执行 → 日期 State → Android/网页查看来源。日期、窗口和成功状态已持久化；实际 watch 到点发布、重复 tick 不改写，以及网页和 Android 模拟器来源导航均已通过。全新 Codex 会话也已验证 evidence 检索回答与 recorder 追加/完成待办；这些是合成验收，新的 dogfood 任务不据此宣称完成。

本轮不扩插件市场、通知、推荐、多 host 或通用任务平台。manual run 消费、重转录 generation 和错误显示仍是已识别的 P1 缺口，不能把已有“请求已接受”当作完成。

## 范围变化

V2 要求客户端 UUID、log/note/derived、独立 File、插件令牌、版本化 State、hook 自动写入、project-brief 和独立 processor host。旧版服务端生成 event ID、media-events 合并提交、query_context、后端任务协调器和 inbox 不能通过改名称来充当 V2。旧测试保留为旧实现证据；V2 须重新验证。

基础公开接口已通过生产验收，完整 MVP 闭环仍在集成。根据用户最新并行实现要求，已经定义公开契约的功能可同时编码；验收仍按产品依赖顺序推进。Android 不得绑定旧上传或收件箱协议，必须在真实 V2 接口通过验证后再验收。

## 并行职责

具体代码由 Sol / high 子代理承担，GPT-6 / high 子代理持续审核，主代理协调类型、文件边界、依赖和集成验收。接口契约已明确的功能可并行实现，依赖步骤之间保留验收门槛。

| 子任务 | 写入范围 | 当前交付 |
|---|---|---|
| A：核心数据与权限 | `backend/internal/v2/`；必要的旧 identity 窄接口 | UUID 事件、文件、查询、State、插件授权、持久化与重启测试 |
| B：服务适配 | `backend/internal/api/v2*.go`、MCP V2、服务入口 wiring | HTTP / MCP 真实路由、认证 scope、大小约束、核心集成测试 |
| C：客户端工具与处理器 | `backend/internal/v2client/`、`backend/cmd/edc/`、`backend/internal/processorhost/` | CLI、stdio MCP、hook 接线、once/watch host 与日期回顾；真实 Codex、ASR 和定时写回 |
| D：自动写入 | `backend/internal/capture/`、`backend/skills/edc-recorder/` | hook、稳定 UUID、outbox、目录绑定与安装预览；CLI 入口与 C 协作 |
| E：Android | `app/android/` | 录音、持久队列、File → Event、记录与 State 回顾；保留现有 iOS |
| F：网站 | `frontend/` 中 workspace 相关文件 | 记录、State、接入和插件流程迁移到 V2；保留无关 landing 修改 |
| G：跨接口测试 | 独立契约测试文件 | 同一数据在 HTTP、MCP 和客户端间的可观察结果 |
| H：持续审核 | 只读代码与临时复现 | GPT-6 审查真实行为，向实现者派发问题并复核修复 |
| 主代理：协调与 review | `docs/`、隔离测试产物 | 契约一致性、权限与数据完整性 review、独立验收、记录未完成项 |

多个代理不会同时无约定地编辑同一文件。processor host 和四个插件继续按依赖拆分；并行编码不替代真实会话、真实设备和定时执行的验收证据。

## 当前核验记录（2026-09-12）

- 已实际启动 8 个并发子代理；持续审核使用 GPT-6，其余实现与集成验证使用 Sol / high。
- 主代理基线执行 `go test ./internal/v2 ./internal/api ./internal/mcpserver ./internal/v2client ./cmd/edc`：V2 核心、MCP 和客户端包通过，CLI 当时没有测试；API 全包失败。
- API 初始失败定位为 `TestRealCLIAndStdioMCP` 仍用旧服务夹具运行 V2 CLI，成员接口返回 404。现已迁移真实 V2 集成夹具并通过，没有退回旧协议。
- 主代理发现的单写者锁缺失、公开返回值深层别名和文件完整性问题均已修复。GPT-6 用原始复现独立回归确认，核心 race 检查通过。
- 独立发布 worktree 的 `make check` 已通过：Go vet、全部 Go race 测试、真实 HTTP/MCP/CLI 集成和现有前端检查。旧 runner 的提交超时测试改为只让真实 submit 请求超时，仍验证超时后保留候选和成功重试。
- 已知音频边界：短 MP3 header 仍可能通过基础结构检查；不能据此宣称所有损坏音频都会在上传时被拒绝。后续音频结构与处理器解码验收仍待完善。
- 上述是实现过程证据，不是第 ① 步或完整 P1 的完成证明。

## 生产集成与实际验收

用户已明确授权直接在现有生产环境验收，并创建专用测试账号。集成代理负责可重复运行的测试和证据，主代理统一发布，避免并发修改线上版本；测试只写专用账号的项目。

- 本轮实查 `integ-prod`：`event-context` 与 `event-context-proxy` 均运行，初始基线发布为 `20260911T171910Z-df67175`；源站 `127.0.0.1:8401/healthz` 返回 200。
- 真实浏览器可打开生产网页。浏览器现有用户登录不用于自动测试；集成代理使用专用身份。
- 新发布从独立干净 worktree 固定源码、验证和发布，保留原工作区并行修改与生产回滚版本；不得把旧版本健康检查当作 V2 验收。
- 专用账号和每轮结果记录在 `v2-production-acceptance.md`；密码、token 不写入文档、日志或版本库。
- 基础 V2 已发布为 `20260912T000040Z-809d20b`，源分支 `codex/v2-integration-20260912` 已推送。公网返回 `api: v2`，同一请求 ID 已与生产进程日志对应；当前发布软链接和运行进程均已核验。
- 公网连续两轮验收通过：`production-20260912T000216Z-78707b`、`production-20260912T000217Z-58960f`。第二轮复用账号、项目和插件，验证 HTTP/MCP 写入与逐项结果、数字保真、State CAS/lag、插件暂停恢复、文件原始字节与跨项目隔离。
- 这两轮不覆盖 CLI 的生产会话、OAuth 全流程、服务重启、网站 V2 交互或 Android 设备验收；当时网站尚待适配，后续网页验证见下条。

- 网页与 self-plugin GET 已发布为 `20260912T002507Z-55e8c6f`；发布前 Go vet、全包 race 和前端检查通过。网页实际写入 sequence 21 并立即显示；390px 中文界面通过历史版本切换、来源展开和插件暂停/恢复，测试插件最终为 active。
- 实际 Codex RunOnce 已处理 21 条生产事件，将 `project-brief/current` 从合成种子 v1 推进为真实生成 v2，`based_on_sequence=21`。网页显示新摘要并能展开刚写入的中文决策来源。
- 第二客户端真实 Claude Code SessionStart 已自动注入上述 v2，包含 Android、保留 iOS、MVP 优先的决定。新增 hook 日志 sequence 22–24，outbox 为 0；隔离 Claude 模型仍未登录，因此没有把 hook 注入成功描述为完整模型回答成功。
- Android 代码已固定于 `e6d21b1`，生产模拟器通过录音、断网队列自动恢复和选文件上传；已有 iOS 原文件保留。

- 音频生产主链完成：真实 M4A 为 sequence 25，Qwen 转录写为 derived sequence 26，内容为“预算调整为三万元，优先上线移动端，交付时间仍待确认。”。网页可从转录展开原音频；首次临时文件缺扩展失败没有推进游标，修复后成功。
- 后续真实 Codex 把音频知识合入 brief v3（based 26、8 条 refs），真实 Claude SessionStart 自动注入预算、移动端优先和交付待确认。新 hook sequence 27–29，outbox 为 0；覆盖从原媒体到下一次会话上下文的最小闭环。
- 固定 host 源码后的 `make check` 全部通过，GPT-6 最终正常路径审核无阻断；任务启动的转录/模型进程及临时处理目录已清理。该检查点尚无定时回顾证据；后续本轮补齐情况见下文，manual run 消费与物理手机仍未验收。

- 定时回顾依赖的项目时区已发布为 `20260912T005158Z-4c132b3`：旧项目迁移默认 UTC、创建/列表返回时区、owner PATCH 更新、插件自省即时读取项目时区。发布 `make check` 全过；真实网页已将主测试项目保存为 Asia/Singapore。
- 第二客户端 evidence 实测通过：全新 Codex ephemeral 会话只收到三问，实际执行 12 次 MCP 调用（list_projects 1、get_state 1、query_events 10），自行取得背景并正确回答预算、移动端优先和交付待确认，引用原始 Event。没有用手工背景替代检索，也未声称调用单条 get_event。

- recorder 真实会话验收通过：Codex 创建 sequence 30 的 UUIDv7 todo，新会话追加 sequence 31 的 completed note 与 `resolves` 引用，原 Event 不变；闲聊没有 MCP 调用或新增 note。首次因旧临时 CLI 不识别 timezone 而失败且未写入，更新当前 CLI 后完成。与上述 evidence 检索的摘要均在 `/tmp/edc-codex-evidence.k8xJ9c/`。
- 真实定时回顾通过：host 源码 `8b90ad0` 的 `--watch` 等待 Asia/Singapore 09:10 计划点，按前一日 09:10 至当日 09:10 固定窗口发布 `daily-review/2026-09-12` v1，based 32、20 条 refs。实际生成完成于 09:11:59；重复 tick 和 12 秒后回读仍为 v1。计划、watch 和 State 证据在 `/tmp/edc-daily-timed-20260912/`，该进程退出 0。
- 网页显示上述日期回顾并展开 sequence 32 的真实来源；Android 模拟器目标测试验证 Asia/Singapore、同一日期 State 和原 Event 对话框（1 test，3.081 秒）。本轮日期回顾切片完成，物理手机仍未验收。后续取消处理进程修复 `09bc135` 已测试推送，但不混入 `8b90ad0` 的实际定时证据。

## 第 ① 步的实现边界

- 复用现有身份、项目、成员与 OAuth；SQLite 继续承担这些信息。
- V2 事件、文件、State 和插件控制数据使用独立的 `data/v2/` 持久目录，避免把旧格式文件当作 V2 或改写既有记录。暂不迁移生产数据。
- HTTP 路径严格按 V2 第 7.7 节，MCP 名称按第 7.5 节。最终当前服务暴露 V2 能力；旧代码可作为保留实现，不用双协议兼容承诺延长范围。
- 核心只做数据、约束和权限，不执行转录、模型、语义检索或每日回顾逻辑。
- 手动处理请求必须可持久恢复；接收请求不表示已执行。processor host 后续通过公共接口处理，不引入旧版后端任务协调器。
- 插件管理与发布权限必须在服务端验证。客户端传来的 plugin ID、source、actor 或 producer 不能直接获得身份。
- Project 成员通过公开接口共享同一事件历史；用户 Event 的 actor 由当前登录身份绑定，写入方声明的 source 与 metadata 不得作为作者。Agent 整理团队结论时保留 refs 和必要的 actor 归属。
- 第 ① 步基础 CLI 用于验证公开接口；hook/setup、完整离线 outbox 与真实会话接入仍属于第 ② 步，未实现时不得返回成功。

## 第 ① 步执行前的验收预期

| 契约 | 预期与失败条件 |
|---|---|
| UUID | 同项目同 UUID 同规范化内容返回 duplicate；改变内容返回 conflict；重启后仍成立；原 actor/sequence/recorded_at 不变 |
| 批量 | 1–100 条顺序逐条处理，合法条目持久化，非法条目附错误；不是整批伪原子，也不把局部失败当作全部成功 |
| JSON | metadata 键顺序不影响去重；大整数不经 float64 损失；数据中的指令不改变权限 |
| 引用 | 仅允许规定 rel；禁止自身、缺失、跨项目目标；插件不能引用自己无权读取的事件 |
| File | 先上传再引用；同项目相同 SHA-256 只存一份；另一项目不能借摘要读取文件；50 MiB HTTP 与 1 MiB MCP 边界不同 |
| 原始字节 | 上传声明与实际媒体不符、损坏容器、超限或错误文件引用被拒绝；认证下载字节/摘要一致 |
| 文件回收 | 只清理达到保留期且仍未引用的文件；与追加事件并发时不能删除刚被引用的文件 |
| 查询 | types、metadata、source、refs_to、sequence、时间过滤全部 AND；升序、固定快照分页，无跨用户游标扩大权限 |
| State | 版本逐次追加并可读历史；expected_version 冲突不覆盖；lag 与项目最新序号一致；非法 based_on_sequence 拒绝 |
| 私有 State | `_` 开头的名称仅所属插件可读；普通成员、项目 owner、其他插件的 list/get/history 都不可见 |
| 插件身份 | 令牌限定项目/插件/清单权限；derived、namespace 与 refs 均校验；暂停/卸载后旧令牌和已取出的 principal 均不能继续操作 |
| 管理 | 共享项目安装与成员管理须项目创建者批准；普通用户不能凭请求体冒充插件发布者；配置版本保留 |
| 团队与作者 | owner 添加注册成员；两个真实身份从不同入口读写同一项目；跨成员读取仍保留写入者 actor ID/username、recorded_at 与独立 source channel；Agent 输出可回到原 Event |
| HTTP / MCP / CLI | 同一事实在三个入口的 UUID、sequence、内容、版本及权限一致；OAuth read-only 不可写；未知或未实现命令明确失败 |
| 持久性 | 关闭并重开进程后数据、去重、State 版本与撤销仍成立；写入失败不提前确认成功 |

## 完整目标的验收账本

| V2 步骤 | 完成所需证据 | 当前状态 |
|---|---|---|
| ① 数据与公开接口 | 上表的真实存储、HTTP、MCP 与 CLI 测试及独立复核 | 基础服务已发布；真实双成员网页写入与另一身份 CLI 读取保留 actor 已通过；网页成员管理 UI 待当前切片验收，Android actor 展示未完成 |
| ② 自动写入接入 | link/setup 预览确认，Claude Code 真实 hook 日志，脱敏、去重、outbox 恢复，recorder skill note 质量 | 真实 Claude hook 注入/日志与 outbox 通过；Codex recorder 创建、完成引用和闲聊不写入通过；Claude 完整 Stop 模型会话仍缺证据 |
| ③ 概况与跨工具 | project-brief State 正确及来源可读；第二客户端无手动交代取得背景；evidence skill 正确查询 | 真实 brief v2/v3、网页来源和 Claude 注入通过；全新 Codex evidence 检索与模型答复通过，持续真实使用待验证 |
| ④ 处理器与转录 | 独立 host 用插件令牌读写、游标恢复；真实无人值守转录与失败重试 | once/watch 已实现；生产 ASR、失败后恢复、derived 与 brief 写回通过；手动请求消费和重转录仍未实现 |
| ⑤ Android 采集 | 真机录音、先 File 后 Event、离线重启恢复、哈希一致与账户隔离；第 9.1 节拍照/选文件共用队列 | 生产模拟器录音、断网队列自动恢复、文件选择与下载哈希通过；物理设备仍待验收 |
| ⑥ 日期回顾 | 实际定时发布 daily-review State；Android 查看来源及待整理记录 | 实际 watch 定时、重复 tick 不改写、网页与 Android 模拟器来源通过；待整理/迟到转录为已实现分支，尚无独立生产场景证据 |
| ⑦ 网页与语言 | 记录/状态/接入/插件全流程；四语言真实桌面、窄屏、手机、OAuth 验证 | V2 中文窄屏记录、State 历史/来源、插件暂停恢复、项目时区与日期回顾通过；其余语言/设备/OAuth 场景待验收 |

真实跨工具项目需经历预算修改、待办完成、agent 推断、成员冲突、闲聊和语音记录，并记录漏记、误记及用户补充背景的次数。测试夹具不能代替若干天真实使用或用户反馈。没有这些证据时，完整目标保持未完成。
