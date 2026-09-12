# 推荐的最小 memory 结构

本建议将实测能力、代码检查和未验证部分分开。最终量化结果见 `REPORT.md` 与 `results/summary.md`。独立原型不是可直接部署的插件。

## 职责与组织

**Event 是证据与历史。State 是可重建的当前视图。索引是找到证据的加速器。** 不增加核心 Memory 实体；沿用当前产品的 `note` / `derived` / `State`。短期先用项目内词法检索和 refs 的双向查找。引用闭包是现有显式更新关系的遍历，不是新建一个通用知识图谱。

| 信息 | 长期 State 中保留什么 | 留在 Event 或按需查什么 |
|---|---|---|
| 目标 | 当前目标、成功条件、范围与来源 | 早期探索、放弃目标及其原因 |
| 决定 | 生效的决定及取代关系；未裁决的冲突并列 | 旧版本、完整讨论、备选方案 |
| 约束 | 当前行动必须遵守的限制，优先进入会话背景 | 形成约束的长讨论；过期约束历史 |
| 偏好 | 明确表达、有适用范围的稳定偏好 | 一次性措辞、从单次行为猜出的偏好 |
| 待办 | 未完成/重开项目事项、负责人、期限和来源 | 已完成/已取消事项默认退出启动背景；仍可查历史 |
| 进展 | 最近里程碑和对当前工作的影响 | 重复“已开始”、完整工具日志，进入日期回顾 |
| 问题/冲突 | 阻塞当前行动的问题；双方来源和缺失确认 | 已解决讨论留历史，必要时解释决定 |
| 推断/建议 | 有用时作为待确认候选，和当前决定分开 | 低价值推测、重复插件摘要，不提升为事实 |
| 闲聊/工具噪声 | 通常不进入项目概况 | 原文照常保留，可按问题检索；不是删除策略 |

优先提升的依据是明确陈述、持久适用性和当前行动影响，不是“被多个摘要重复说过”。同一录音的转录、摘要、回顾共享一个根来源，不构成三份独立证据。保存原文只解决可核查性，不能保证原话本身真实。

## 一份 State，两个投影视图

先只维护 `project-brief/current`。其 `content.text` 是短的启动概况，`data` 保留同一批条目的机器可读信息。不要分别让多个模型独立生成内容和结构，否则容易互相不一致；由同一已校验条目集渲染两者。

```json
{
  "key": "project-brief/current",
  "expected_version": 4,
  "based_on_sequence": 128,
  "content": {"format": "markdown", "text": "当前预算 30000 元〔预算更正事件 UUID〕；原始录音仅在本地。交付日期仍有冲突。"},
  "data": {
    "schema_version": 1,
    "items": [{
      "id": "budget/main",
      "kind": "decision",
      "subject_id": "project:haibridge",
      "text": "预算改为 30000 元",
      "lifecycle": "active",
      "epistemic_basis": "user_assertion",
      "confirmation": "asserted",
      "valid_from": null,
      "valid_to": null,
      "sources": [{"event_id": "预算更正事件 UUID", "span": {"unit": "utf8-byte", "start": 0, "end": 29}}],
      "supersedes_sources": ["旧预算事件 UUID"]
    }],
    "coverage": {"pending_audio": [], "unresolved_refs": [], "truncated": false},
    "extractor": {"version": "plugin-version", "policy_hash": "sha256"}
  },
  "refs": ["预算更正事件 UUID", "约束事件 UUID", "日期双方事件 UUID"]
}
```

这是字段形状说明，中文 UUID 占位符和 span 数字不是可提交的真实值。实际可加载的例子由 `src/export_examples.py` 生成到 `examples/`，使用数据集 UUID 和精确字节范围。`data` 和 Event.metadata 是现有可扩展 JSON，无需新增核心表。

不要把 `epistemic_basis=user_assertion` 解释为服务端验证了用户亲自说过。当前 `actor` 表示经过认证的写入身份；agent 可能用用户令牌写 note，`source.channel` 与说话人信息大多是声明。`user_assertion`、`agent_unconfirmed` 和 `plugin_summary` 是证据来源分类，不是新增权限级别。需要有可信独立确认通道时，才另行设计认证确认，当前实验没有验证这个安全能力。

同时保留“谁被记录为发言者”与“谁通过认证写入”。例子的 `metadata.speaker_label` 与 State `asserted_by.speaker_label` 保留合成人物标签，并注明 `identity_basis=synthetic_source_declaration`；它不是一个已认证的成员 ID。实际有认证身份时从核心 Event.actor 读取，转录引用的发言者仍需独立标明身份来源。不要仅按相同名字跨项目合并人物。

“确认状态”与“生命周期”独立：未确认建议可以仍在待确认区；用户陈述被撤回后不能因曾确认就继续使用。`valid_from/valid_to` 与录入时间也独立，未知即 null，不能用高置信分数填补未知。未来生效规则是本轮原型的已知失败，应在上线前实现或明确保留为待生效。

## State key 与索引目录

```text
服务端 Event/File：继续由现有核心保存，插件不直接读写底层目录。

State keys（每个 Project 独立）
  project-brief/current       启动概况 + 同一内容的结构化条目
  project-brief/_cursor       已处理 sequence、版本/策略、失败与覆盖情况
  daily-review/2026-09-12     当日进展和历史回顾；不作为新的独立事实来源

插件本地可删除缓存（示意，不是当前服务端物理布局）
  cache/<project-id>/
    index.jsonl              event_id、文本 span、词项、来源角色
    refs.json                正向/反向引用；按 Event 重建
    manifest.json            source watermark、schema/policy version、内容摘要
```

插件可以用 SQLite FTS 或本轮内存 BM25 实现索引，选择取决于实际历史规模和延迟。这里没有证据要求向量数据库；这也不等于向量检索没有价值。对于释义、多跳和中英文词汇不匹配，当前词法方案仍可能失败，应该用相同 reader、预算和新问题集比较混合检索后再决定。

注意 P1 插件不能注册新 MCP 工具/HTTP 接口，当前 query_events 也没有全文 query 参数。本轮 BM25 可先放在调用方的 evidence skill 配套本地 helper：通过已有公开接口分页取得授权 Event，在客户端索引/排序，或先用已有 metadata/topic/time/ref 过滤再取原文。不把实验脚本误描述成当前服务端已有检索接口；也不让插件绕过 API 直接读取数据目录。只需原文查证时，已有 get_event 与 refs_to 足够。

现有 V2 `splitStateKey` 只接受一个 `/`，不能提出 `project-brief/projects/foo/budget` 这类 key。State refs 上限为 32，content 和 data 各为 256 KiB；一份概况无法无限扩张。若当前独立来源超过 32，先压缩启动视图并提供按需查证；若确有必要维护更多可直接读取条目，再增加明确授权的 `project-brief/decisions`、`project-brief/tasks`、`project-brief/questions`，每份仍遵守来源和大小上限。不是为八种信息立即建八份 State。

拆分 State 的触发条件是来源数、稳定更新边界或测得的上下文拥挤，不是目录整齐。跨多个 State 发布时当前核心没有一次提交多个 State 的原子契约：先发布分项，再在 current.data 指定分项版本并最后发布 current；读取方按这些固定版本取，不能混用任意 latest。这个方案为设计推断，尚未在生产验证。

## 增量更新、重建和读取

1. 固定项目和读取快照上界，用 sequence 拉取新 Event。`occurred_at` 仅表述事情发生时间，迟到事件依然按写入 sequence 纳入处理。
2. 单条或小批抽取候选，保留原文 span、source 和 refs。用户明确 note 可直接成条目；log/转录抽取尚未由本轮验证为可靠的自动语义抽取。
3. 先校验来源存在、同项目、未超上界，再解释 refs。`supersedes` 更换当前主张；`retracts` 撤回；`resolves` 完成。重开用新 todo `supersedes` 完成事件，无需添加核心 ref 类型。
4. 同一槽位无明确取代关系的不同成员主张并列为待核实。不同措辞未必矛盾；不能用“文本不同”自动断定冲突，也不能用“更新”自动断定更可信。
5. 从条目集合渲染 State，通过 expected_version 发布；成功后推进处理游标。失败保持游标，重试使用确定性输出 ID。是否完成以 State/游标写入为准。
6. 插件升级、规则改变或抽查发现摘要错误时，从原始 Event 重建。旧 State 作为历史审计，不作为重建事实。原型的全量重放与增量尾部处理结果一致性已测；生产崩溃恢复和并发发布尚未实跑。

查询时，State 是入口和指路信息。先检查 `latest_sequence - based_on_sequence`。若落后，拉取尾部并临时更新相关事实或请求插件重建；尾部不可获取时，必须说明范围，不能把旧 State 当作“当前”。词法召回后沿 `refs_to` 和来源链补取更新、撤回、完成和冲突的另一侧，并给这些证据预留预算。发现闭包超出预算时返回不完整标记并缩小查询范围，不可静默只留下旧锚点。

实测三组冻结 State 的问题中，单纯在正文附过期提示仍全部输出旧预算；补取尾部后全部得到新值。因此新鲜度判断应成为 helper/host 的控制逻辑；尾部缺失时不要仅把警告作为普通 evidence 文本交给模型。已知过期且不可更新的“当前值”应由程序返回不可确认。该程序门禁是建议，原型实测的是提示失败与尾部恢复，并未把“按规则必然拒绝”当成模型准确率。

会话启动优先注入目标、硬约束、当前关键决定、未完成工作、阻塞问题和覆盖水位；完整流水、已完成事项、长论证和历史值按问题取。常驻预算应是可配置的小上限，不应自动累加所有插件摘要。本轮 768 token 的 pinning 对照仅是机制探测，不足以确定最佳启动预算。

探索性供应商流程探测补充了应用层证据：预算相同且检索问题不含隐私词时，query-only 的 3 个模板变体都批准了违反既有约束的上传流程；预留目标/约束后 3 个都拒绝。原始回答见 `results/action_probe.json`。这是一个模板上的受控反例，不是对所有会话启动策略的最优性证明。

## 一个项目的演变

以下为同一合成项目的简化表示。完整逐事件版本见 `examples/evolution.md`。

| 时间/输入 | 当前 State 的变化 | 历史与来源 |
|---|---|---|
| E1：目标离线录音闭环；E2：预算 50000；E3：录音只留本地 | 目标、预算、约束进入概况 | 每项指向其原始 Event |
| E4：预算改 30000，supersedes E2 | 预算项换成 30000 | E2 不删除；问最初预算仍可查 E2 |
| E5：提交原型待办；E6：完成，resolves E5 | 从未完成清单退出，最近进展可保留完成状态 | 完成事件指向原待办 |
| E7：验收失败重开，supersedes E6 | 新待办重新出现 | 不把最初待办或完成记录覆盖 |
| E8：林说 4/10 上线；E9：陈说 4/20，无取代关系 | 一个冲突项并列两个主张 | 不按最后写入选择陈 |
| E10：agent 推测可以买云服务 | 待确认候选，不进入已确认决定 | agent 身份/通道与原话分开 |
| E11：转录负责人“小林/小宁听不清”；E12：用户澄清小宁 | 歧义先保留，澄清后当前负责人为小宁 | 转录与音频根来源仍可追溯 |

## 给主开发任务的最小实施建议

先完成一个 `project-brief` 垂直切片：固定项目 → 分页读取新 Event → 证据条目校验 → refs 更新 → current State（结构+渲染）→ 第二客户端读取 → 从引用打开原文。不要引入核心 Memory 表、专用目录树或通用图数据库。

优先补三个上线门槛：可信程度明确标识且 agent 推断不能提升为用户决定；State lag 的尾部检查与失败时保守回答；当前值检索必须检查引用它的更新事件。把本专项集导入独立契约测试，并增加互补事实、不明确更正、未来生效和跨项目误路由的未通过用例。

目前静态代码已经有 Event refs 同项目存在性校验、版本化 State、expected_version、lag、命名空间与只读原文接口。更多字段先放插件 data/metadata 和 skill 规则。只有实测来源容量持续超过上限、分页/查询成为瓶颈或可信确认需要新的认证语义时，才提出最小核心变更；这次没有部署或验证这些改变。
