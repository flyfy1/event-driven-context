# 公开证据与基准选择

检索截止：2026-09-12。下表是文献与官方实现的证据，不是本实验的测量结果。“最新”指此次主源检索所确认的近期工作，不声称穷尽当天所有论文。

| 类别与来源 | 版本、能力和适用性 | 缺口、成本、许可 |
|---|---|---|
| 基准：LoCoMo | [论文](https://arxiv.org/abs/2402.17753)，2024-02-27 v1、ACL 2024；[官方数据与代码](https://github.com/snap-research/locomo)。10 段长对话，单跳、多跳、时间、开放域和对抗问题，部分问题有对话级证据 ID。适合检测摘要损失和检索定位。 | 不充分覆盖明确撤回、协作冲突和任务状态。作者的 observation、session_summary、event_summary 不能作本轮输入。[CC BY-NC 4.0](https://github.com/snap-research/locomo/blob/main/LICENSE.txt)，本地非商业研究使用，不默认可用于产品训练或分发。 |
| 基准：LongMemEval | [论文](https://arxiv.org/abs/2410.10813)，2024-10-14 v1、2025-03-04 v2；[官方代码](https://github.com/xiaowu0162/LongMemEval)、[2025-09 cleaned 数据](https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned)。500 问；信息提取、多会话推理、知识更新、时间、拒答。 | 每题一份独立长历史，S 约十万 token，构建开销大。主要测最终 QA，不测每次更新后的 State。oracle 只有证据会话，不能替代检索实验。代码和数据卡 MIT；完整参考栈用 CUDA，直接读 JSON 无此依赖。 |
| 基准：MemoryAgentBench | [论文 v4](https://arxiv.org/abs/2507.05257v4)，2025-07-07 v1、2026-06-28 v4、ICLR 2026；[官方仓库](https://github.com/HUST-AI-HYZ/MemoryAgentBench)、[数据](https://huggingface.co/datasets/ai-hyz/MemoryAgentBench)。增量输入，检索、测试时学习、长程理解、冲突/事实更新。单跳与多跳 FactConsolidation 可共享一次构建。 | 有些事实刻意违背现实；必须遵从基准的按序更新规则。该规则不同于真实团队成员冲突。事实更新不是隐私删除的验证。代码/数据卡 MIT，保留上游归属。单个 Conflict_Resolution parquet 约 1.5 MB，本轮轻量实跑。 |
| 基准：BEAM | [论文 v2](https://arxiv.org/abs/2510.27246v2)，2025-10-31 v1、2026-02-21 v2、ICLR 2026；[官方代码](https://github.com/mohammadtavakoli78/BEAM)、[数据](https://huggingface.co/datasets/Mohammadta/BEAM)。100 对话、2,000 问，覆盖更新、冲突解决、排序、拒答、偏好等十类，长度至 10M。 | README 的最小档称 128K，HF split 名为 100K，需测实际长度。生成式对话与自动 judge 带偏差，长档很贵。代码 MIT，数据 CC BY-SA 4.0。本轮未运行。 |
| 基准：MemoryArena | [论文](https://arxiv.org/abs/2602.16313)，2026-02-18；[作者项目页](https://memoryarena.github.io/)、[官方仓库](https://github.com/ZexueHe/MemoryArena)。跨会话相互依赖的旅行、购物、搜索、形式推理执行任务。 | 执行表现同时受工具与环境影响，不能纯归因于 memory。所检 README 仍称 preview，未在根目录确认 LICENSE；环境/API 依赖较重。本轮不运行，也不默认有再发布许可。 |
| 基准：LongMemEval-V2 | [论文](https://arxiv.org/abs/2605.12493v1)，2026-05-12；[官方代码](https://github.com/xiaowu0162/LongMemEval-V2)、[数据](https://huggingface.co/datasets/xiaowu0162/longmemeval-v2)。451 人工问题，网页/企业 agent 轨迹，静态/动态状态、流程、环境陷阱、前提识别；同时考察查询延迟。 | 不等于 v1 升级版聊天 QA；含截图、最大 115M token。官方 reader 与 judge 的设定不同，本地小文本实验不能对标官方成绩。代码/数据 Apache-2.0。本轮只研究，因规模与多模态运行成本未执行。 |
| 基准：GroupMemBench | [论文 v2](https://arxiv.org/abs/2605.14498v2)，2026-05-14 v1、05-16 v2；[官方代码](https://github.com/UCSB-NLP-Chang/GroupMemBench)、[数据](https://huggingface.co/datasets/kimperyang/GroupMemBench)。多人、线程、项目阶段、身份绑定、更新、歧义、时间与拒答，更接近团队项目。 | 作者指出抽取/合并可能丢发言者和词汇，BM25 有竞争力；不等于所有结构化 memory 无效。题目筛选使用检索难度，存在选择偏差。所检卡未声明数据许可，本轮不再发布、不纳入实验。is_noise、is_decision_point 和 gold trace 应排除。 |
| 近期补充：PM-Bench | [论文](https://arxiv.org/abs/2607.12385v1)，2026-07-14 v1；测未来线索触发时执行意图，即 prospective memory。 | 与存取事实不同，适合后续提醒/待办执行插件。本轮未核验完整代码/数据许可，不称可复现实跑候选。 |
| 近期补充：InMind | [作者项目页](https://keep-it-inmind.github.io/)，2026-07，arXiv:2607.24368；区分直接回忆、已给事实时推理、检索取出和最终应用。 | 提示“检索到了”不等于“约束被应用”；适合补充注入评测。本轮未完成许可/代码审计，不引用排行榜作产品结论。 |

## 不混淆三个层次

检索还确认了以下 8—9 月工作。它们补充评测方向，并未替代已经锁定的问题集；不把新论文的作者分数写成本轮实测。

| 近期主源 | 能力、与本产品的关系和复现边界 |
|---|---|
| **StateMemBench / StateMem**：[论文 v1](https://arxiv.org/abs/2608.19652v1)，2026-08-20；[全文](https://arxiv.org/html/2608.19652v1) | 前者是 234 个多会话场景、322 个 graded probes 的**基准**，区分当前状态、被替代状态与其他错误，覆盖依赖重算、约束重要性、复合更新及过度失效陷阱；后者是显式单位、supersession、依赖与重查的**方法**。比纯回忆题更贴近项目 State。程序生成再自然语言渲染、封闭答案池仍有外推限制。截至此次检索，未定位到可独立下载并核验许可的作者数据/代码工件；这是本次未找到，不是断言作者没有发布。本轮不运行，以专项状态/反例测试替代；不引用作者倍数提升证明 D 有效。 |
| **LoCoMo-Conv**：[论文 v1](https://arxiv.org/abs/2609.03467v1)，2026-09-03；[作者代码](https://github.com/MiuLab/LoCoMo-Conv/) | 在 LoCoMo 上构造 dialog、implicit、counterfactual、composed 四种问法，分别评检索与回复；指出正确使用背景不一定显式说出 gold 字面词。其 supportive_memory 是评测标注，不能输入构建。网页首次读取失败后，经 GitHub API 确认代码和数据目录存在，commit `1925e924c36e632283ea91f2212157829f4e0e45`。README 给出本地 BM25/embedding 与多个模型 judge 的流程；根目录无 LICENSE，README 只有上游归属，衍生标注许可仍待明确。因已锁定实验范围、评测 API 成本与许可未明，本轮未运行；隐含硬约束动作探针覆盖一小部分相近能力。 |
| **The Memory Trust Gap**：[论文 v1](https://arxiv.org/abs/2609.01852v1)，2026-09-01 | 封闭动作评分，将必须依赖存储事实与权威工具已有现值的情形分开；研究旧记录、来源和模型规模如何改变信任。提示“展示 metadata”并非通用纠错方案。仍是 workshop 在审预印本；代码/许可未审计，不运行，也不将其模型间结论外推到本地 Qwen 量化模型。 |
| **RSM-full**：[论文 v1](https://arxiv.org/abs/2609.04915v1)，2026-09-04 | 属于组织方法：在线 max-member 聚类与成组证据打包，在紧预算下比较质量/体积。作者在 RealMem 上没有证明相对 BM25 的显著优势，不能据此要求本产品引入向量/图。代码/许可未核验，本轮未运行；证据成组打包属于后续明确候选。 |
| **Grounding Agent Memory**：[论文 v1](https://arxiv.org/abs/2609.11060v1)，2026-09-10 | 方法与执行实验：让整理器用最小权限的只读环境探测验证、刷新 memory，在 CLBench 和适配的 APEX 任务上测量。更适合会变化的外部环境事实；原始说法有引用也不代表环境仍成立。实现/数据许可未在本轮审计，未运行；依用户边界，本研究没有探测真实生产或私人系统。 |

这些近期结果使下一轮评测优先级更清楚：当前状态与历史状态分开评分、隐含约束是否用于动作、证据单位是否完整装入上下文。它们没有解决本轮自动抽取、长期团队运行或多模型外推尚未验证的问题。

LoCoMo、LongMemEval、MemoryAgentBench 等是**基准**。Mem0、A-Mem、HippoRAG、Hindsight，以及 BEAM 的 LIGHT、LongMemEval-V2 的 AgentRunbook 是**组织/检索方法或系统**。Codex、LangGraph 等是**执行框架**。本轮不将它们混在一个排行榜，也不因框架能运行就假设其 memory 更好。

本实验选择 A 近期日志、B 递归摘要、C 原文 BM25、D 可追溯证据条目与更新闭包，覆盖四种可解释机制；没有安装完整商业 memory 服务。向量检索、图谱、学习型语义抽取属于后续候选，不是当前结论的前提。

| 组织方法/系统 | 可借鉴机制与局限 |
|---|---|
| [A-MEM](https://arxiv.org/abs/2502.12110)，2025-02-17 v1；[系统代码](https://github.com/agiresearch/A-mem) | 根据笔记属性、语境和标签形成链接，并随新笔记调整旧笔记表示。适合组织发现，但语义链接不天然等于可审计的更正或用户确认；额外抽取/链接需要模型与存储。本轮不复现、不引用其自报分数证明收益。 |
| [Mem0](https://arxiv.org/abs/2504.19413)，2025-04-28 v1；[官方代码](https://github.com/mem0ai/mem0) | 动态抽取、整合、检索；另有图表示变体。论文报告性能/成本改善，但读者模型、上下文和评分设置与本轮不同。原文保全与完整更新历史需要单独核对，不能仅从压缩率推导。本轮四组实验覆盖其部分机制思想，不声称运行了 Mem0。 |
| [Hindsight](https://arxiv.org/abs/2512.12818)，2025-12-14；[ACL 2026 系统论文](https://aclanthology.org/2026.acl-demo.27/)，2026-07；[代码](https://github.com/vectorize-io/hindsight) | 分离 world、experience、observation、opinion；retain/recall/reflect 分开，结合词法、向量、时间和实体关系。证据与信念分离值得借鉴，但 PostgreSQL/pgvector、抽取与反思的复杂度尚未由本实验证明必要。 |
| [Hindsight Memory-PRM](https://arxiv.org/abs/2608.29605)，2026-08-30 | 近期的不同工作，利用检索、引用、版本链和受控删除后重答来归因记忆操作价值；不是上行同名系统的已验证替代。提示应测 memory 是否被有效应用，而非只存得多。本轮只核验论文，未完成代码/许可和训练成本复现，列为未验证方法。 |

## 评分限制与泄漏防线

[LoCoMo 官方 evaluator](https://github.com/snap-research/locomo/blob/3eb6f2c585f5e1699204e3c3bdf7adc5c28cb376/task_eval/evaluation.py) 主要用 token F1；对抗题用固定拒答字串；某些没有检索上下文/证据的路径会把检索 recall 计为 1。因此本轮独立算实际 evidence ID 的召回，没有 gold evidence 的题记 N/A，不补成满分。本文本简化 F1 也不是作者的 stemmed F1，不能对比官方榜单。

[LongMemEval 官方 judge](https://github.com/xiaowu0162/LongMemEval/blob/9e0b455f4ef0e2ab8f2e582289761153549043fc/src/evaluation/evaluate_qa.py) 对时间允许部分 off-by-one，对更新问可以接受同时提到旧新信息。故“judge 通过”不能证明无旧事实污染；本轮另外检查禁用结论、来源与状态。本轮没有调用付费官方 judge。

MemoryAgentBench FactConsolidation 的官方指标为 substring exact match；包含新答案仍不代表答案没有夹带旧值，且多跳题没给原文证据标注。本轮的该指标与引用有效性分开报告。

公开数据可能在模型训练中出现，本轮无法排除；空上下文 E 是诊断对照，不是消除污染的证明。专项数据为此次确定性生成，仍由同一研究流程设计与评分，有设计者偏差。多个模板变体、同一历史下的多个问题不能视为相互独立。

## 固定版本

| 项目 | Code commit | Data revision |
|---|---|---|
| LoCoMo | `3eb6f2c585f5e1699204e3c3bdf7adc5c28cb376` | 同仓库；本地下载内容另存 SHA-256 |
| LongMemEval | `9e0b455f4ef0e2ab8f2e582289761153549043fc` | `98d7416c24c778c2fee6e6f3006e7a073259d48f` |
| MemoryAgentBench | `fe1735de8cf8b9908e1e3d3b5612afc815698062` | `7ea066982b140a19337e17e60d45d4076e042faf` |
| BEAM | `b2da22eac88bb0874c64665f13457eb99835774a` | `3205395e897e7318c7b094ef4e6047b9b82dbb03` |
| LongMemEval-V2 | `2cc8c540bdb87fe6761629b585e727e1c4704520` | `f152293e235517d504809563c833d7190b8c713b` |
| GroupMemBench | `e2682e01ff490acfe4fac2940159dce60307dfc9` | `7d0b079b894c5915ce2191fccdfce26137525d68` |
| MemoryArena | `6cd9de14b71915e39ac742a20dc33785e14b6aab` | 本轮未获取 |

模型名称、Ollama 版本、量化权重 digest 在 `sources/model.json`；数据 URL、大小、抓取时间和 SHA-256 在 `sources/downloads.json`。代码日期由只读研究核对；正式复现以固定 revision 和下载摘要为准。
