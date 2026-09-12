# 给主开发任务的最小实施建议

建议先沿用现有 Event / refs / State / plugin 契约。研究脚本只在独立实验目录中运行，不能直接复制后当作生产插件。代码观察以 `sources/product-snapshot.json` 的内容摘要为准；主仓库同时开发，当前 HEAD 可能已经变化。

## 第一条可验收的路径

1. 扩展现有 `project-brief` skill 的输出约定：`project-brief/current` 同时保存短 `content` 和有来源的 `data.items`。从同一条目集渲染二者；每条须有原始 event_id，最好附精确 span。先不另建 Memory API、数据库或八类 State。
2. 更新器固定项目与 sequence 上界，通过现有 API 取得事件；先检查显式 refs，再解释更正、撤回和完成。agent 推断、转录和插件摘要单独标记来源；确认状态不可由重复摘要升级。写 State 时使用已有 `expected_version`，成功后才推进游标。
3. evidence skill 的调用方 helper 先读 `based_on_sequence` 和项目最新水位，再检索。发现落后须读取尾部或明确返回“截至旧水位，当前不可确认”。仅在 prompt 中加“过期”提示在本实验中失败。
4. 查询先做项目范围过滤和词法检索，再补取命中的反向更新、来源和冲突另一侧。证据组必须完整装入预算，否则返回不完整状态。当前实验的 packer 只按条目装入，**尚未实现这个原子装包门槛**。
5. 第二客户端读短概况，用引用打开原文；从当前概况中固定保留目标和硬约束。任务完成/取消后退出待办清单，旧决定和已完成记录按需查询。

此路径可先完全在插件 data、现有 refs 与客户端 helper 中完成。当前 P1 插件不能注册新 MCP/HTTP 工具；query_events 也没有全文 query 参数，不能假设服务端已能执行 BM25。不要绕过公开 API 直接读写服务端底层文件。

## 接入前的验收

| 验收行为 | 可复用工件与边界 |
|---|---|
| 预算修改、完成/重开/取消、历史值可查 | `data/product_questions.json` 与 `product_expected.json`；本轮给定准确 metadata/refs，真实对话抽取还要另测 |
| 成员冲突并列、推断不能替代明确陈述 | 主产品集 + `data/plugin_lineage.json`；source.channel 只是声明，不能当认证身份安全证明 |
| 同 UUID 重试不重复、异体冲突拒绝 | `src/verify.py`；不同 UUID 的同义重复仍未解决，先保留原文再归并候选 |
| stale State 不作为当前值 | `results/stress/answers.jsonl`；必须验证 helper 控制流程，不能只检查提示词含警告 |
| 检索问题未提到约束时仍正确行动 | `results/action_probe.json`；一个模板的 3 个变体，不是大规模安全保证 |
| 来源可打开且确实支持原子主张 | 先校验 ID/项目/上界，再检查 span 蕴含；仅事件 ID 存在不够，摘要不能成为独立根证据 |
| 已知反例保持可见 | `results/stress/deterministic.json` 的互补事实误冲突、未来生效和不同 ID 重复，不能悄悄删掉失败测试 |

涉及多 State 的方案晚于这条路径。只有独立来源持续超过现有 32 refs 上限或确有不同更新节奏时，再拆 `project-brief/decisions` 等 key，并让 current 固定引用分项版本。现有 key 只允许一个斜杠；多 State 原子发布不是已提供的契约。

未在本轮实现/验证的要求包括：可信用户确认通道、任意语音的语义抽取、并发发布与崩溃恢复、跨客户端实操、长期运行维护成本。应把它们列为开发验收，不能因本地研究分数较高就视为完成。
