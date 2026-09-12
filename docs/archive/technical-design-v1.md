# Event-driven Context 技术设计

> 历史设计：当前实施依据已切换为 [product-V2.md](product-V2.md)。本文保留旧方案与既有实现说明；V2 的任务拆分和验收见 [V2 实施与审查](v2-implementation.md)。旧版 query_context、后端任务协调器和 inbox 不作为 V2 输出架构。

版本：0.2 · 日期：2026-09-12 · 状态：P1 实现中。第 1 节与第 8 节标明已完成的本地能力，其余仍包含设计提案；实际验收见 [P1 审查记录](p1-review.md)。本文不代表生产部署已更新。

产品约束与验收范围以 [产品设计](product.md) 为准；既有基线见 [第一版范围](mvp.md)。本文描述完整链路，但只为产品文档第 10 节安排下一轮实现切片。

## 1. 已有实现与新增边界

以下为本次读取仓库代码确认的能力，不代表已验证生产运行状态。

| 领域 | 已有实现 | 设计所需增量 |
|---|---|---|
| 身份与共享 | SQLite 保存身份和项目；文件化 runner 身份、安装授权、任务约束 | 第三方插件授权后续评估 |
| 事件 | 不可覆盖 JSON 与原始文件；平台 provenance、显式纠正/确认、自动化幂等衍生追加 | 更多来源类型 |
| 文件 | text/* 保留 1 MiB 上限；新增 20 MiB 有界音频 multipart 上传、认证原始下载与幂等 | 后续图片上传 |
| 查询 | 项目、时间、顶层 metadata 精确匹配；追加序号快照分页；关键词 context 与关系扩展 | 更广泛真实题集验证，语义召回后续评估 |
| MCP | HTTP 和 stdio；原有工具与 query_context | 后台执行使用独立受限接口 |
| 前端 | 项目页、媒体与 context；runner 注册、固定 skill 设置、运行状态、收件箱与来源 | 真实使用反馈 |
| 执行 | 文件化协调器、runner CLI、事件与每日触发；本机 ASR 与 Codex 真实联调 | 长期调度需用户配置唤醒器 |
| 手机采集 | 既有 iOS 原型保留，已有 Core / 构建证据；后端媒体与回顾契约可复用 | 当前只开发 Android，`app/android/` 独立实现并验收录音、持久同步与结果查看 |

主要代码位置：

- [types.go](../backend/internal/core/types.go)：数据结构、Backend、大小上限。
- [filestore.go](../backend/internal/core/filestore.go)：事件发布、幂等、追加序号、文件读取和快照查询。
- [store.go](../backend/internal/core/store.go)：身份与项目授权、输入校验。
- [schema.sql](../backend/internal/core/schema.sql)：当前 SQLite 表。
- [HTTP server](../backend/internal/api/server.go)、[MCP server](../backend/internal/mcpserver/server.go)：传输与 scope。
- [frontend/app.js](../frontend/app.js)：现有用户流程。

现有幂等键按“项目 + 作者”隔离。现有进程内互斥不提供多进程写入协调。现有 OAuth context:write 同时涵盖创建项目、添加成员和追加，静态用户令牌同样不具备插件最小权限，不能直接给任意 skill 当作受限执行凭据。

## 2. 架构与职责

~~~mermaid
flowchart TD
    Phone[手机 App 录音 / 媒体采集] --> Local[本机原始文件与上传队列]
    Local --> Input[认证媒体上传 / 网页 / CLI / MCP]
    Input --> API[Go API 与权限边界]
    API --> Events[原始事件与文件]
    Chat[对话 Agent] --> Context[query_context]
    Context --> API
    Events --> Select[候选选择与关系解析]
    Select --> Context
    Cron[用户侧 cron 或其他定时器] --> Runner[用户侧 Runner]
    Runner --> Coordinator[后端规则与任务协调器]
    Coordinator --> Events
    Coordinator --> Config[固定版本 Skill 安装与规则]
    Coordinator --> Runs[文件化 Run 记录]
    Runner --> Agent[已验证的 Agent 适配器执行 Skill]
    Agent --> Restricted[任务级受限 MCP / 文件读取]
    Restricted --> API
    Agent --> Candidate[结构化候选结果]
    Candidate --> Commit[后端校验与幂等提交]
    Commit --> Events
    Commit --> Inbox[产品内收件箱]
    Commit --> Delivery[独立授权的外部投递]
~~~

- Core 负责身份、原始记录、关系验证、候选提取和任务授权，不在原始写入请求中调用模型。
- Coordinator 放在同一个 Go 服务中，负责生成和领取任务、状态转换、结果提交及投递状态。首版不引入消息队列或第二个业务数据库。
- Runner 是用户环境中的进程，由 cron 等调度器周期启动。它领取任务、加载固定版本 skill、调用 agent、管理超时并提交结果。
- Agent 只获得本次任务的必要输入和工具，不获得 runner 注册凭据、用户全量令牌或外部发布凭据。
- 主动对话的 query_context 首版同步返回证据，由调用方 agent 理解和组织语言；不会为了检索再启动一个后台 agent。
- Skill 可以执行多步推理或调用已授权工具，但不能自己扩大项目范围、创建任意 shell 任务或绕过投递授权。

首版部署模型是单个服务进程拥有数据目录的写权。runner 不挂载服务器数据目录，只走认证接口；数据库仍限于身份、项目和执行授权，不保存事件内容。

## 3. 持久化模型

### 3.1 存储布局

保留现有路径，新增内容置于同一 Git 忽略的数据根目录：

~~~text
data/
  projects/<project_id>/
    events/<event_id>.json
    files/<file_id>
    automation/
      installations/<installation_id>/revisions/<revision_id>.json
      rules/<rule_id>/revisions/<revision_id>.json
      control/<object_id>/<sequence>.json
      runs/<run_id>/request.json
      runs/<run_id>/transitions/<sequence>.json
      runs/<run_id>/attempts/<attempt_id>/candidate.json
      runs/<run_id>/commit.json
      deliveries/<delivery_id>/transitions/<sequence>.json
    cache/
      automation-state.json
      retrieval-index.json
  users/<user_id>/inbox/<entry_id>/transitions/<sequence>.json
  skills/<skill_digest>/...
~~~

- event、文件、配置版本、候选结果、运行转换和提交凭据不可覆盖。
- control 是启用、暂停、选定版本等追加记录；当前状态由投影得到。
- cache 是可替换、可删除并可从原始数据重建的加速文件。缓存丢失不能导致逻辑重复执行。
- inbox 保存结果引用、接收者和已读等状态，不复制项目原文；每次打开仍重新校验项目权限。
- runner 注册、安装执行授权和令牌哈希属于授权数据，放在现有身份 SQLite 中；skill 包与用户提示词留在文件存储。

所有修改通过单后端串行协调。写入沿用临时文件、内容同步、不可覆盖发布；实现时补齐目录同步及故障测试。以 manifest 为可见性边界，不能把文件已存在等同于完整事件已提交。

备份需在协调器和新写入停止后同时备份身份库与全部 data；恢复时重新验证授权、run 转换和缓存水位。普通缓存可丢弃，提交和投递凭据不能丢弃重建为“从未执行”。

### 3.2 Event 扩展

保留现有 content.kind=text/file、自由 metadata、服务端作者与 recorded_at。新增可选的受控扩展，旧事件未包含时仍可读取：

~~~json
{
  "id": "evt_transcript_1",
  "project_id": "prj_example",
  "actor_user_id": "usr_installer",
  "content": {"kind": "text", "text": "转录内容……"},
  "metadata": {"language": "zh", "tags": ["meeting"]},
  "provenance": {
    "origin": "derived",
    "source_id": "src_upload",
    "source_assurance": "authenticated",
    "run_id": "run_example",
    "installation_id": "ins_transcribe",
    "skill_digest": "sha256:<digest>",
    "config_revision": "rev_1",
    "output_slot": "transcript",
    "generation": 1
  },
  "relations": [
    {"type": "derived_from", "event_id": "evt_audio_1"}
  ],
  "interpretation": {
    "kind": "transcript",
    "assertion_status": "machine_generated"
  }
}
~~~

示例省略已有时间等字段以突出增量。provenance 由服务端根据认证输入路径或已领取 run 写入，客户端不能直接声明自己是可信 runner。source_assurance 仅证明入口绑定，不证明内容真实。

- 原始记录的自由 metadata 可继续使用 source 等键，但只能作为用户声明；规则界面明确区分它与绑定的 source_id。
- 旧事件标记为 legacy/unknown 来源；旧 metadata 中看起来像系统字段的键不升级为可信字段。
- runner 代表授权安装者执行，actor_user_id 保留委托用户，另记录 runner 与 run 来源。用户确认必须来自交互用户授权路径，不能由同一个生成任务自我确认。
- interpretation 表示 transcript、summary、claim、suggestion、confirmation 等输出性质；不是准确性保证。
- relations 的 derived_from、supersedes、retracts、confirms、contradicts 必须指向可访问且同项目的已有事件。禁止环和跨项目引用；P1 支持整条事件关系，不做局部文本的隐式覆盖。contradicts 只声明冲突，不撤销任意一方。
- 自动结果只有在同一安装、同一处理阶段、同一输入谱系内才可替代自己的旧版本；不能自动撤回用户原始记录。
- 用户纠正默认只能明确替代自己写入或以自己名义生成的记录。对其他作者的不同意见保留为冲突；共享项目额外裁决权不在首版隐含赋予。

交互用户的 RecordInput 可新增受校验的 relations 输入，用于追加纠正、撤回、确认或冲突声明；服务端依据授权路径赋予原始/确认性质。普通 record_event 不能接收 run_id 或可信 provenance。自动提交只允许 task grant 指定的关系和结果类型。

### 3.3 生成内容与有效状态

“最近写入”不是通用真值规则。确定有效版本使用显式合法关系和同一处理谱系的 generation；occurred_at 仍是用户声明时间，recorded_at 仅表示写入顺序。

同一输入、同一安装、同一 output_slot 的重跑生成单调增加的 generation。只有成功提交且 generation 更高的输出成为默认版本；失败的新版本不使旧版消失。不同安装的分析彼此独立，不能自动互相覆盖。

检索按原始证据谱系去重。摘要与其来源转录不能当作两个独立来源“投票”。建议默认进入 suggestions 分组，用户确认后才可产生 confirmed 状态。无法确定的冲突保留双方出处；不承诺首版能够自动发现所有自然语言矛盾。

## 4. 媒体输入与来源

### 4.1 原始写入契约

现有 JSON/base64 文本接口保持兼容，不直接放大所有请求的全局上限。新增专门的有界 multipart 媒体入口，一次提交项目、metadata、类型、幂等键和一个文件；校验通过后发布一个 file event。

P1 支持手机录制端与处理端实际验证通过的共同格式；候选包括 audio/mp4（M4A/AAC）、audio/wav 和 audio/mpeg，启用格式通过能力信息返回。不能让 App 默认产生服务端不接受的文件，转码如有需要属于明确的处理步骤，保留原始媒体。媒体入口已实施文件 20 MiB、multipart 请求 21 MiB 的固定上限；媒体端点单独设置 180 秒读取与 190 秒响应期限。录音入口提前展示短录音限制，触限时结束并可靠保留已录内容，超限文件不得静默裁剪。

上传时校验声明类型、受支持的容器特征和字节限制；先保存有效原始文件。解码、时长和转录预算在隔离的处理阶段检查，初始转录时长预算为 10 分钟；超出时保留原文件，处理返回 limit_exceeded。

后续图片允许列表拟为 JPEG/PNG，独立限制字节和解码像素数，暂不支持活动内容格式。解析器需要有界内存和超时，不能把扩展名当实际类型。

### 4.2 MCP 文件路径

现有小文件 base64 行为保留。大媒体通过认证上传端点和 CLI/网页提交，避免将大段 base64 放入对话工具上下文。需要从 agent 上传时，由支持附件或本地文件的适配器使用同一上传协议，并返回事件 ID；未验证的客户端不承诺大媒体 MCP 上传。

runner 按任务 grant 下载授权文件，流式读取到任务临时目录；不接受日志中给出的服务器路径或任意 URL。媒体下载端点每次授权，不发长期公开链接。任务结束或超时清理自己的临时媒体和进程，重试所需的候选结果保存在服务端。

### 4.3 来源绑定

来源集成的授权绑定 project_id、source_id 及允许的写入类型；服务端生成可信 source_id。普通用户可以声明 metadata.source，但这不会命中要求“认证来源”的自动外部动作。

上传成功意味着原始事件已提交；是否发现匹配规则、是否支持当前分析能力作为独立状态返回。原始记录不能因处理配置错误而被回滚。

### 4.4 手机采集与自动上传

手机 App 是媒体采集主入口，网页文件上传是补充。客户端只负责采集、可靠同步和结果浏览；媒体分析仍走后端规则与用户配置的 runner，不要求手机运行后台 agent。

- 首次设置默认私人项目“我的记录”。每次录制前显示归属；共享项目必须显式选择或预先绑定，不根据模型分类自动改变权限范围。初次没有网络且尚未获得项目 ID 时，本机暂存为未绑定草稿，登录并绑定私人项目后再上传。
- 开始录音时生成稳定 capture_id；媒体数据持续写入本机文件，结束后关闭文件并确认可读取，再持久化队列项。queue 包含所属账户、项目、capture_id、文件位置、实际 MIME、大小、摘要、发生时间及已固定的 metadata。
- 输入来源由客户端注册身份在服务端绑定，如 phone.recorder；metadata 中的来源标签不等于可信来源。发生时间来自设备并标为声明时间，服务端 recorded_at 仍表示接收时间。
- 上传幂等键使用稳定 capture_id，沿用服务端“项目 + 作者”的隔离。重试必须复用相同文件和输入；若服务器已提交但回执丢失，再次提交返回原事件。响应中记录 event_id 和摘要后才将本机状态置为 synced。
- 本机状态为 recording → local_saved → queued/uploading → synced → processing/ready。waiting_network、waiting_auth、interrupted、failed 是可见分支；处理状态从服务端查询，不能因 HTTP 请求发出就标记完成。
- 网络恢复、进入前台及平台允许的后台时机触发补传。首版允许短录音整文件重试，并用退避及并发上限控制消耗；不为首版承诺分片断点续传或无限后台执行。
- App 被结束、来电或音频会话中断时，重新打开应能发现已持久化片段并提示“录音中断，已保留可恢复部分”；不能把不完整媒体当成完整会议。锁屏录制与后台上传分别进行设备验证。
- 登录过期只暂停同步，不丢弃文件。队列绑定原账户，切换账号不得把待传媒体发到另一个账户；目标项目无权访问时保留本机文件并提示处理。凭据由客户端安全凭据设施保管，不写入媒体或普通日志。
- 服务端提交确认前不得自动清理原始文件。确认后本机仍保留可播放副本，缓存清理策略后续单独配置；“已在手机保存”与“已在服务端保存”的提示不可互换。
- 用户结束录音后不再经过上传确认表单；标题和标签由处理结果提供建议，原始 event 仍保持 append-only。图片拍摄、相册和文件导入后续复用同一队列契约。

当前交付平台已明确为 Android，独立工程位于 `app/android/`。既有 `app/EventDrivenContext/`、`app/Core/` 与 Xcode 工程保留；其 iOS 构建、测试及失败记录不构成 Android 验收证据。首次离线未登录采集、可信来源注册以及操作系统长期后台上传仍属于设计目标。

Android 采用用户在前台明确开始的麦克风前台服务，录音时提供持续通知和结束入口；不在设备重启或后台自动打开麦克风。权限与服务类型遵循 [Android foreground service 要求](https://developer.android.com/about/versions/14/changes/fgs-types-required)。持久队列与网络约束、退避重试通过 [WorkManager](https://developer.android.com/develop/background-work/background-tasks/persistent/getting-started/define-work) 接续；调度受系统条件限制，不能把提交任务描述为已上传。测试服务器的 CA 只能进入调试构建的 [debug trust 配置](https://developer.android.com/privacy-and-security/security-config)，发行构建不得放宽证书或主机名校验。

## 5. Skill 包、安装与规则

### 5.1 Skill 包

采用便携的 SKILL.md 加可选资源文件；产品附加一个小型 manifest 表达运行契约。此 manifest 是本产品提案，不声称任意 agent 原生识别同一 schema。

~~~yaml
id: audio-transcribe
version: 1.0.0
entrypoint: SKILL.md
capabilities:
  - audio_transcription
input_schema: schemas/input.json
output_schema: schemas/output.json
output_slots:
  - transcript
allowed_tools:
  - get_event
  - get_file
~~~

安装时固定包内容摘要，包含所有被引用资源。拒绝指向包外的路径或链接；脚本和网络访问必须显式声明并被适配器支持。P1 只接受经过审查的随产品提供或用户本地安装的两个 skill，不运行远程包自动更新脚本。

Skill 描述语义流程，不包含用户账户密码、动态项目令牌或外部发送凭据。用户提示词覆盖允许的分析配置，不覆盖权限策略、输出 schema 或系统数据边界。

执行器按自身接口把用户配置作为任务的 system prompt 或等价分析指令加载，保持执行约束、skill 说明、用户配置与原始资料分段。用户可以自由改变分析要求；授权仍由服务端和工具边界执行，不能寄希望于模型始终遵守某段提示词。

输出采用统一信封。下面是回顾的候选结果；转录沿用信封并增加可选的毫秒级片段定位与听不清标记。没有经过能力验证的转录器不能生成伪造时间戳或说话人身份。

~~~json
{
  "schema_version": 1,
  "outcome": "output",
  "output_slot": "daily_review",
  "kind": "summary",
  "text": "今天确定了预算，仍需确认交付时间。",
  "source_event_ids": ["evt_budget_change", "evt_schedule_question"],
  "items": [
    {
      "kind": "decision",
      "text": "预算调整为 3 万。",
      "source_event_ids": ["evt_budget_change"]
    },
    {
      "kind": "open_question",
      "text": "交付时间尚未确认。",
      "source_event_ids": ["evt_schedule_question"]
    }
  ]
}
~~~

每个证据引用必须出现在本任务已授权且实际提供的输入中；后端验证引用合法性，但不声称通过 JSON 校验就能证明结论正确。候选中不接受发布地址、可执行命令或新的工具授权。no_output 信封必须带结构化 reason，不产生内容事件；它仍保留 run 记录。

### 5.2 安装实例

安装实例包括 installation_id、owner_user_id、project_id、固定 skill_digest、runner_id、config_revision、read_scope、output_policy 与 enabled 状态。scope=personal 的实例由安装者管理；scope=project 的共享实例由项目创建者管理。

默认事件处理只读取匹配输入及其关联文件；每日回顾安装可授予同项目历史读取。权限在安装、领取、读取、提交和投递各阶段检查。配置和 prompt 发布新版本，既有 run 固定其快照。

### 5.3 规则示例

~~~yaml
id: rule_audio_notes
installation_id: ins_transcribe
revision: rev_1
stage: transcript
trigger:
  type: event.appended
  origin: raw
match:
  source_id: src_voice_notes
  media_types: [audio/wav, audio/mpeg]
  actor: installation_owner
  metadata_equals:
    purpose: project_note
priority: 100
activation:
  mode: future_only
configuration:
  user_prompt: 保留原话，标记听不清的段落，不推测人名。
outputs:
  append_to: same_project
  external_delivery: disabled
~~~

条件 AND；缺失字段不匹配，空数组等非法配置在保存时拒绝。MIME 使用显式允许列表，metadata 比较沿用现有类型敏感的顶层精确相等。可信来源与声明来源分别配置，不隐式替换。

同一输入可触发不同安装或不同 stage。对同一 installation + stage 命中的多个规则，按 priority 降序、rule_id 字典序打破平局，只选一个；显示冲突预览，不靠遍历顺序决定。

新规则激活时记录项目序号水位，只处理之后的事件。配置变更记录新的生效水位区间，调度扫描历史时按事件所属区间选择规则版本，不能误用最新提示词处理停机前的旧输入。用户选择的历史重跑是独立请求。

默认只匹配 raw；derived 链路必须显式指定生产 installation 与 output_slot，并验证无环、最大深度及任务数。P1 无通用 DAG 执行器；音频转录只有一个处理阶段。

## 6. Runner 与定时执行

### 6.1 Runner 协议

已实现命令 `edc-runner tick`，每次最多执行一个任务；可由用户已有 cron 或其他调度器定期启动。`edc-runner watch --interval 30s --max-runtime 10m` 提供有界的连续检查，默认十分钟后退出。本文与本轮实现均不安装永久 cron 条目。

每次 tick：

1. 获取本地进程锁，防止同一 runner 重叠运行。
2. 使用自己的注册凭据请求后端 dispatch；后端仅评估其授权安装的事件水位和到期时间。
3. 领取一个任务，获得 lease、attempt_id、fencing_token、固定配置与输入白名单；当前没有另发独立 grant bearer。
4. 校验本地 skill 包摘要及能力，启动对应 agent 适配器。
5. 心跳续约，收集结构化结果，保存候选并请求提交。
6. 输出本次状态并退出；下次 tick 继续。watch 负责间隔和总运行期限。

事件处理初版同样通过 tick 扫描发现，写入延迟与轮询间隔相关。可将一分钟作为配置起点，但不承诺实时响应。服务重启或 runner 长期离线时，扫描从持久化水位继续。仅在全部匹配任务或明确跳过决定持久化后推进水位，避免丢任务。

后端唯一协调器决定是否到期和是否可领取，cron 只负责唤醒。手动运行或多个唤醒源都经过相同幂等路径，不能各自生成独立任务。

### 6.2 适配器边界

适配器输入为固定任务 JSON、skill 目录和受限工具连接；输出为声明的结构化 JSON 及退出状态。它还负责超时、取消、子进程清理和能力声明。调用命令使用固定可执行文件和参数数组，绝不把 metadata 或 prompt 拼进 shell 命令。

适配器能力至少区分非交互运行、结构化输出、受限工具连接、图片读取及音频转录。文本 agent 可以调用单独转录工具；不能因它能理解文本就推断它能读取音频。

Codex 或其他 agent 可以是适配器候选，但本设计不依赖具体命令行参数、登录实现或订阅额度。选定适配器后按该提供商当前官方契约验证；不复制桌面登录状态、不抓取订阅凭据、不自动替换成收费 API。

P1 的能力探针必须用真实账号环境完成：无人交互启动、读取一个获授权事件、处理小音频、输出有效结果、失败退出、超时清理。不能通过探针时不显示“可用”。

### 6.3 时间语义

P1 每个定时规则保存 IANA 时区与用户选择的 HH:MM 本地时间，不接受任意 cron 表达式；起始时间来自安装版本，补跑策略和运行预算为固定值。UTC 用于服务端比较，UI 展示用户时区。更改时区或时间产生新规则版本和未来生效边界。

每日回顾默认覆盖前一个计划执行时刻到本次计划执行时刻的半开区间，例如前一天 21:00 至当天 21:00；范围不随实际延迟启动时间变化。按 recorded_at 选择该窗口中的记录；当前没有额外的跨窗口历史背景召回。首个未来槽位也使用完整窗口，可能包含安装前的当日记录；这与事件转录仅处理版本启用后的新输入不同。默认 UI 可建议 21:00，实际时间由用户设置，不用浏览器当前时区替代已保存时区。

DST 重复的本地时刻只执行一次，选择第一次出现的时刻；不存在的本地时刻顺延至当天第一个有效时刻。调度实现必须有测试证明该语义，不能依赖未确认的 cron 库默认行为。

停机补跑默认只执行最近一个错过的回顾槽位，明确记录其他槽位为 skipped_misfire；手动请求仅接受明确来源事件，不提供历史槽位选择界面。未来提醒插件若发现提醒已过期，默认跳过并记录原因，而非恢复后集中发送。

回顾固定项目快照，注明仍待处理的音频。之后才完成的转录可在下次回顾的“迟到处理结果”中补充，不改写或重新投递旧回顾。

## 7. 任务、重试与结果提交

### 7.1 逻辑任务身份

- 事件任务键：project + installation + stage + input_event + rule_revision + generation。
- 时间任务键：installation + rule_revision + 本地日历槽位标识；同时保存解析后的 UTC due_at。
- 手动请求键：owner + installation + 用户提交的 request_id。
- Run 的 request 固定输入 ID、项目快照水位、skill 摘要、配置版本、授权安装和输出预算。

首次自动处理 generation=1。失败重试仍属于同一个 run 的新 attempt；用户主动重新分析才分配新的 generation。配置变化不自动生成历史任务。

### 7.2 状态机

~~~text
queued -> leased -> running -> candidate_saved -> committing -> succeeded
                    |                 |
                    +-> retry_wait ---+-> queued（在允许的失败类型下）
                    +-> failed
                    +-> blocked_auth / blocked_capability
任意未终结状态 -> cancelled（暂停、卸载或显式取消时）
queued -> skipped（misfire、规则禁用或明确无须运行）
~~~

所有转换由后端验证前置状态并追加，当前状态是投影。attempt 使用单调 fencing token；过期 attempt 的心跳、候选和提交请求全部拒绝。lease 过期后可重新领取，不能接受旧进程稍后提交的结果。

running 也可在校验 no_output 信封后直接进入 succeeded。candidate_saved 之后的网络或磁盘重试优先恢复固定候选提交，不回到模型执行；只有尚无候选的可重试计算失败才重新调用 agent。

当前值：lease 120 秒，CLI 默认 10 秒心跳，单次服务端执行期限 10 分钟，最多 3 次尝试；暂时失败使用一分钟、两分钟退避，尚未加入抖动。候选上限 64 KiB。每日输入最多 128 条、合计 1 MiB 文本，超出时记录遗漏数量与覆盖范围，不能仅依赖 prompt。

可重试：临时网络错误、限流、执行器暂时不可用。认证失效进入 blocked_auth；不支持的媒体或能力进入 blocked_capability 或明确失败；无效输出最多允许一次受控修复尝试并计入总预算。

权限撤销终止未完成任务；修复登录可恢复仍有效的阻塞任务。恢复安装默认只生成未来任务，已取消的历史任务需用户主动重跑。

### 7.3 提交与崩溃恢复

转录 run 发布一个 transcript event。每日回顾将 progress、decision、open_question 项组成 summary，并将每个 suggestion 项发布为独立 suggestion event；完整候选保留在个人收件箱中。这样默认 context 不会把未接受的建议混入事实摘要。各输出槽身份固定，全部完成后才标记 run 成功并建立收件箱记录；不声称跨文件事务原子性。无内容时可返回 no_output 并成功结束。

1. 后端在有效 attempt 下验证输出 schema、大小、引用、允许的输出类型和安装状态。
2. 先不可覆盖保存 candidate.json，记录内容摘要。提交重试复用该候选，不再调用模型生成一个不同结果。
3. 后端使用固定的 run_id + output_slot 写入幂等键，通过核心记录逻辑发布衍生 event。保留现有“同键不同输入冲突”的语义，不对不一致结果静默覆盖。
4. 保存 commit.json，记录 event_id 和输出摘要，再追加 succeeded。
5. 以确定性的 run + output_slot + recipient 生成 inbox 或投递任务。

如果在 event 发布后、commit 保存前崩溃，恢复时用相同键和候选查回原 event，再补齐 commit。若候选和已发布内容不同，进入冲突待核对，不重新生成或覆盖。磁盘满时不推进状态水位。

这提供至少一次调度、幂等逻辑结果提交；不承诺外部模型调用或任意第三方发送端到端 exactly-once。结果已被模型计算但尚未保存就断电时，重试可能消耗第二次计算费用。

### 7.4 暂停与授权竞态

暂停和提交在同一协调器序列化：提交已成功则保留历史结果；暂停先发生则拒绝新提交。暂停撤销任务 grant，runner 下一次心跳应中断执行。agent 已读取的数据无法被“收回”，因此应在领取前限制最小输入。

运行中的本地进程终止存在延迟，但后端必须立即拒绝后续读取、提交和投递授权。P1 无外部发送；后续外部发送采用第 10 节的授权与不确定结果处理。

## 8. 按需 Context 提取

### 8.1 接口契约

已实现只读 HTTP `POST /v1/context/query` 与 MCP `query_context`。P1 每次只接受一个明确 project_id，不默认搜索全部项目，也不自动生成项目总摘要。

~~~json
{
  "project_id": "prj_example",
  "query": "这周应该先推进什么？",
  "max_output_bytes": 24000,
  "include_suggestions": true
}
~~~

当前返回结构示例：

~~~json
{
  "project_id": "prj_example",
  "snapshot": {"sequence": 128},
  "evidence": [
    {
      "event_id": "evt_budget_change",
      "excerpt": "预算调整为 3 万，取代此前的 5 万。",
      "excerpt_truncated": false,
      "source_event_ids": ["evt_budget_change"],
      "interpretation": "user_correction",
      "state": "active_evidence",
      "retrieval_reason": "relation_context",
      "recorded_at": "2026-09-12T00:00:00Z"
    }
  ],
  "suggestions": [],
  "conflicts": [],
  "coverage": {
    "snapshot_sequence": 128,
    "indexed_through_sequence": 128,
    "pending_processing_count": 1,
    "truncated": false,
    "conflict_detection": "explicit_update_forks_only"
  },
  "warnings": ["keyword_retrieval", "explicit_relations_only", "pending_audio"]
}
~~~

字节预算针对完整序列化响应（包括 HTTP 末尾换行），默认和最大 24,000、最小 512 字节；不是精确 token 承诺。query 为有效 UTF-8、1–4,096 字节。当前 context 请求只支持 project_id、query、max_output_bytes 和 include_suggestions；intent 与 time_range 暂未实现。warnings 使用稳定代码，由界面按语言展示。

context 是只读返回值，不自动追加保存查询或结果。调用方需要留档时显式记录，避免检索行为自身制造事件噪音和隐私副本。

### 8.2 P1 提取算法

1. 检查当前成员身份及任务 grant，固定项目最大追加序号。
2. 枚举该快照内可访问事件及受支持文本文件；使用已提交转录作为音频的可检索文本，未处理媒体只返回说明。
3. 在整个项目快照内用文本词项匹配选候选；中文使用简单字符片段及规范化匹配，拉丁文本使用规范化词项。当前 context 不接受时间/metadata 过滤；需要这些过滤时使用 query_events。近期程度用于排序，不作为有效性判定。
4. 对候选补全其原始来源、合法取代链和撤回/确认关系；关系解析覆盖整个快照，避免旧记录命中但更新记录因关键词不同而被漏掉。
5. 按谱系去重，保留最新成功的合法处理版本；建议单独分组，显式冲突保留双方。
6. 在输出预算内截取关键词附近的 UTF-8 片段，附 event_id、时间、来源及 excerpt_truncated；全文通过 event ID 读取。预算不够时整条剔除或明确标注片段截断，不切断 JSON 或伪造完整引文。
7. 返回检索覆盖范围、待处理数量、截断和已知能力边界。候选为空时明确说明，调用方可换问题或读取更多原文。

P1 不依赖向量库或后端第二次模型调用，也不声称拥有完整语义召回。先用验收题集比较正确引用、过时结论与遗漏情况；若关键词召回不足，下一步再评估重写查询、重排或可重建语义索引。

conflicts 当前只编码查询关系闭包内同一目标上的多个未被取代的合法更新分支，kind 为 explicit_update_fork，包含 target_event_id 和 update_event_ids；未实现 contradicts 关系或语义冲突分类。普通文本中的矛盾由调用方 agent 根据返回证据判断。空 conflicts 不能表述为“已经证明没有冲突”。验收既覆盖显式关系，也覆盖调用方对未结构化矛盾的真实表现。

### 8.3 缓存与降级

最初可扫描文件并构建进程内文本索引；存在磁盘缓存时记录项目序号、索引版本和内容摘要。索引只用于候选加速，最终引用与权限以原始 manifest 为准。

缓存落后时补扫描新增事件或回退完整快照扫描；在配置的时间预算内无法完成则返回 partial 和实际水位，不能把陈旧结果标记为完整。默认时间预算初拟 5 秒，耗尽后明确降级；任何值都要在真实数据上测量。

即使已经返回某个 context 包，之后追加的新事件也只会出现在新调用里。调用方可据 snapshot 判断新旧。预计算摘要是可选材料，不是替代原始记录的唯一 memory。

### 8.4 回顾复用

每日回顾 skill 使用同一 query_context 和原始读取工具，以日期窗口及有限历史背景组装输入。它通过明确的任务预算分页补读，不递归触发同一个回顾安装。

skill 输出需区分 progress、decisions、open_questions 和 suggestions，并附来源。固定输入快照后到来的新事件或转录不混入同一次 run。P1 不能仅靠“生成了一段摘要”验收，必须验证原始引用和建议类型。

## 9. API、身份与权限

### 9.1 当前 P1 接口

以下接口已在本地代码中接线。服务启动必须显式传入 `-skill-root <backend/skills 的绝对路径>` 才启用自动化；完整验证边界见 P1 审查记录。公开交互 MCP 只增加 query_context，不暴露 runner 控制接口。

| 接口或工具 | 用途 | 权限 |
|---|---|---|
| POST /v1/media-events | 有界媒体原子上传及记录 | 项目追加 |
| GET /v1/files/{id}/content | 用户认证原始下载 | 项目读取 |
| POST /v1/context/query；query_context | 同步返回 context 证据 | 项目读取 |
| GET/POST /v1/automation/runners | 列出或注册 runner；新令牌只返回一次 | 当前用户 |
| POST /v1/automation/runners/{id}/actions | revoke 撤销 runner | runner 所有人 |
| GET/POST /v1/automation/installations | 列出或安装固定 skill、绑定 runner | 安装管理 |
| POST /v1/automation/installations/{id}/revisions | 发布提示词或配置版本 | 安装管理 |
| POST /v1/automation/installations/{id}/actions | pause 暂停、resume 恢复 | 安装管理 |
| POST /v1/automation/runs | 请求试跑或显式重跑 | 安装管理 |
| GET /v1/automation/runs；GET /v1/automation/runs/{id} | 运行状态与引用 | 安装可见性及项目读取 |
| POST /v1/runner/dispatch | 生成到期任务并领取 | runner 注册授权 |
| POST /v1/runner/runs/{id}/heartbeat | 续 lease | 当前 runner 与 attempt |
| GET /v1/runner/runs/{id}/input-files/{file_id}/content | 下载固定任务白名单内的音频 | 当前 attempt grant |
| POST /v1/runner/runs/{id}/submit | 校验、保存并幂等提交候选，或 no_output | 当前 attempt grant |
| POST /v1/runner/runs/{id}/fail | 失败或阻塞 | 当前 attempt grant |
| GET /v1/inbox；POST /v1/inbox/{id}/read | 查看回顾与标记已读 | 接收者及当前项目读取 |

P1 的 trigger 直接属于安装修订，不另建 rules 接口。列表响应分别使用 runners、installations、runs、entries 数组字段。dispatch 无任务时返回 HTTP 204，否则直接返回 Claim。其余 runner 请求使用独立 bearer token，并同时提供 X-EDC-Attempt-ID 与 X-EDC-Fencing-Token；普通用户 token 不能当 runner token 使用。

客户端管理面以网页和 HTTP API 为主。P1 的模型子进程只接收已授权的固定输入，不获得 MCP、后端或 runner 令牌；候选由 runner 通过专用接口提交。交互对话的 MCP 仍使用用户自己的读取身份。

错误采用现有 error.code/message 结构，增加 capability_unavailable、blocked_auth、lease_expired、budget_exceeded、invalid_output 等稳定 code。大输出不写入错误消息。

### 9.2 三种身份

1. 用户身份：保留现有交互登录和项目权限，管理自己的安装或创建者的共享安装。
2. Runner 注册身份：授权领取特定安装的任务，不可添加项目成员，不自动获得所有项目历史。
3. Task 范围：runner 注册令牌与 `run_id`、`X-EDC-Attempt-ID`、`X-EDC-Fencing-Token` 组合校验，约束 lease、当前安装、项目成员身份及输入白名单。它不是另一个独立 bearer；模型子进程不持有这些凭据。

有效权限取用户当前成员权限、安装授权和任务范围的交集。注册令牌只存哈希；用户令牌不能代替 runner 身份。暂停、撤销、lease 过期或项目授权失效后，任务下载、心跳与提交均被拒绝。已成功提交的结果保留；同一合法候选的响应丢失重试返回原结果。

P1 模型只读取固定输入。runner 的媒体接口只提供当前任务列出的文件；没有向模型暴露能扩展项目历史的通用 MCP 工具。交互用户则按正常项目读取契约访问，MCP readOnlyHint 等注解不替代授权。

安装管理授权和项目内容读取分开：其他成员可按既有项目契约读取已发布结果，但不能查看别人的私人安装配置、原始 agent 日志或控制其任务。收件箱只是个人入口，不改变结果所在项目的可见性。

## 10. 输出、通知与对外动作

P1 output_policy 只允许 append_event、inbox 或 no_output。由后端提交输出，agent 无直接外部发送能力。

完整产品增加 draft_share 与 publish。投递记录固定 output_event_id、内容摘要、目的地、授权版本和投递键，与计算 run 分开。凭据在专用投递器或用户指定的受控端保存，不进入 skill prompt。

- 默认草稿，自动发布必须匹配启用的内容类型、目的地和范围。
- 发送前重新验证授权并记录开始状态；暂停先于发送开始时必须拦截。
- 发送已经开始后不能保证撤回；暂停页面明确显示可能正在外部完成的请求。
- 支持幂等的提供商使用固定 delivery_id。遇到超时、提供商不支持幂等且结果未知，转为 unknown，需要查询回执或用户核对，不能盲目重发。
- 产品内 inbox 使用固定条目 ID，重复提交不会重复出现。
- 提醒指纹按安装、根证据/待办、提醒类型和时间窗口计算；已读不等于完成。稍后提醒改变未来投递时间，用户确认完成则追加新事实。
- 把结果发布到外部是独立效果，不因生成成功就记作发送成功。

## 11. 运行约束与可观测性

日志只保留 event/run/installation ID、耗时、状态码、大小和可用的用量统计；正文、媒体、prompt 与凭据不进入普通日志。受授权的运行详情展示必要的输入输出引用，输出 schema 不请求或保存模型内部思维过程。

runner 执行于专用任务工作目录，按适配器能力限制文件和网络工具；无法实施所需隔离的适配器不能用于不受信任的第三方 skill。P1 仅验证一个受控 skill 环境，不声称自动 sandbox 任意代理。

队列视图至少包含积压数量、最早等待时间、最后成功心跳、blocked_auth、失败原因及暂停状态。资源预算必须有实际约束：超时、任务数、文件大小和输出大小均由代码执行；提供商成本无法准确计量时不显示虚假剩余额度。

磁盘空间不足时拒绝无法可靠保存的输入或任务状态，不提前确认成功。数据校验发现损坏应停止受影响任务并报告，不悄悄跳过导致回顾遗漏。首版不支持多个后端进程直接共同写同一数据目录，启动时需独占写入锁。

## 12. 兼容、落地与验证

### 12.1 兼容策略

- 旧事件与 metadata 原样保留，新字段可选，不重写历史 manifest。
- 旧 HTTP/MCP 文本输入及原有查询顺序保持兼容；媒体新增独立大小限制和传输入口。
- 对受控 provenance 和关系使用专用写入路径，普通 record_event 不接受伪造运行来源。
- 缓存可重建，配置和运行历史不能靠 Git 跟踪；身份 SQLite 不增加事件、转录或向量内容表。
- 启动恢复先完成未决提交检查再调度，回滚软件前确认它能读取新字段。运行数据先备份，不用删除新事件让旧版本“兼容”。

### 12.2 P1 实现顺序

| 顺序 | 最小变更 | 对应证明 |
|---|---|---|
| 0 | 选择一个手机平台，实现录音、本机队列和账户/项目绑定的自动上传探针 | 真机开始/结束录音、断网保存、重启补传、回执丢失重试不重复 |
| 1 | 选择一个真实执行器，完成非交互转录和受限工具探针 | 能真实处理媒体并在超时后退出 |
| 2 | 扩展核心事件关系、媒体入口、认证下载，保留旧文本接口 | 原始媒体持久化、哈希一致、失败不丢原文 |
| 3 | 增加安装/规则文件、授权凭据、任务协调器和 runner tick | 规则版本、事件触发、幂等重试、启停生效 |
| 4 | 提供转录 skill、query_context 和 MCP 注册 | 新对话引用转录及最新合法更新 |
| 5 | 提供回顾 skill、时区调度和产品内 inbox | 实际定时器触发，结果有来源且不重复 |
| 6 | 手机端提供记录与回顾入口，现有网页加入必要配置、状态、结果和暂停入口 | 手机采集到回顾的完整链路，网页桌面/窄屏配置可用 |

建议模块：在 backend/internal/core 中复用记录/授权逻辑；提取可置于 backend/internal/contextquery，规则与状态可置于 backend/internal/automation；runner 入口位于 backend/cmd/edc-runner；两个包放在仓库 skills/ 下。按实际代码体量决定是否拆分，不先搭插件框架或通用引擎。

### 12.3 必须验证的风险

| 风险 | 有意义的验证 |
|---|---|
| 持久化半途失败 | 分别在原始文件发布、候选保存、事件提交、commit 保存处故障注入；重启后验证不丢逻辑结果且不重复 |
| 重叠或过期执行 | 同 run 多次领取、lease 过期后旧 attempt 提交被拒绝，只有合法候选可提交 |
| 暂停与权限绕过 | 暂停前后并发读取和提交；验证 HTTP、MCP、媒体和 inbox 都受范围限制 |
| 配置变化和停机 | 旧事件使用正确生效区间，改 prompt 不自动重跑历史，手动重跑生成新版本 |
| 时间问题 | 时区变更、DST 缺失/重复时刻、停机补跑与多个 tick 去重 |
| 来源与反馈污染 | metadata 不能伪造系统来源；建议不能自行升级为用户确认；同源多个摘要不增加独立证据数 |
| 检索错误 | 明确预算变更、未关联冲突、未转录媒体、空结果、输出预算及缓存水位不足 |
| 循环触发 | 衍生输出默认不触发自身，非法显式循环配置被拒绝 |
| 媒体输入 | 大小上限、错误 MIME、损坏音频、解析超时、原始字节往返 |
| 手机采集与同步 | 麦克风拒绝、音频中断、离线录制、本机队列恢复、登录过期与账户切换、回执丢失后的幂等补传 |
| 产品效果 | 真实 agent 的新对话和实际调度器回顾，验证来源、当前结论与用户反馈 |

实现时运行现有 make check、make build 及与上述新增边界相关的测试。本文仅修改文档，不把这些未来测试描述为已通过。

### 12.4 后续能力

图片分析可复用媒体、来源规则与结果契约；提醒和推荐复用定时触发与反馈；外部分享接入独立投递器。更强检索、跨项目聚合、多 runner 并行、第三方市场和托管执行，均需在 P1 证据之后重新确定范围。

## 13. 决策记录

| 决策 | 选择 | 可调整条件 |
|---|---|---|
| Context 生成位置 | 首版确定性后端提取 + 调用方 agent 理解 | 真实召回与回答证据不足时评估额外模型步骤 |
| 持久化 | 文件为内容真源，SQLite 仅身份授权 | 需要另行确认存储方向才能扩大数据库职责 |
| 定时器 | 用户侧 cron 等唤醒 runner，后端负责去重与到期判断 | 托管执行需求得到确认后增加托管唤醒 |
| Skill 兼容 | 固定包 + 本产品 manifest + 单适配器探针 | 对第二个真实 agent 验证后扩展 |
| 分享 | 独立动作、默认草稿 | 用户配置具体自动发布授权后启用 |
| 项目边界 | 单 run 单项目，输出回同项目 | 私人衍生空间或跨项目能力单独设计 |
| 下一轮范围 | 手机录音与自动上传、音频转录、按需提取、每日回顾 | 验收完成并评估用户价值后再扩展 |

实现前需要确定的环境参数集中在执行器、支持的媒体格式、运行主机与资源预算；它们不会被伪装成已完成的集成能力。
