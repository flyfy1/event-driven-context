# Event-driven Context memory 实验

独立实验目录，仅公开/合成数据。实验期间主产品仓库只读，无生产、客户端配置或私人对话操作。用户随后追加授权，将全部材料归档到 `/Users/songyy/Documents/event-driven-context/docs/research/2026-09-12-memory-lab`；该新增研究目录是唯一向产品仓库写入的范围。推荐与结论见 [REPORT.md](REPORT.md)，结构建议见 [docs/structure.md](docs/structure.md)，公开研究见 [docs/sources.md](docs/sources.md)。

## 复现

在此目录运行。模型使用已有本地 `qwen3.5:35b-a3b-nvfp4`，量化权重 digest 和 Ollama 版本固定在 `sources/model.json`。**不自动下载权重、不购买 API**；没有同一模型时先只运行确定性检查，并在报告注明模型不可用，不能沿用本分数。

```sh
uv venv --python 3.12.13 .venv
uv pip install --python .venv/bin/python -r requirements-lock.txt
.venv/bin/python src/download.py
.venv/bin/python src/make_product.py
.venv/bin/python src/prepare_public.py
.venv/bin/python src/verify.py
.venv/bin/python src/export_examples.py
.venv/bin/python src/verify_examples.py
.venv/bin/python src/stress.py
```

在单独终端启动临时推理服务，完成后 Ctrl-C 停止。只使用本地地址和已有模型。

```sh
OLLAMA_HOST=127.0.0.1:11439 OLLAMA_CONTEXT_LENGTH=8192 OLLAMA_NUM_PARALLEL=1 OLLAMA_MAX_LOADED_MODELS=1 OLLAMA_NO_CLOUD=1 ollama serve
```

先冒烟，再按固定主配置运行。选择新的 run 名，避免覆盖已有结果。

```sh
.venv/bin/python src/run.py --run repeat-smoke --smoke
.venv/bin/python src/run.py --run repeat-main
.venv/bin/python src/analyze.py --run repeat-main
.venv/bin/python src/stress.py --answer
.venv/bin/python src/scale_probe.py
.venv/bin/python src/compact_summary.py --run repeat-compact --histories product-0 --methods B
.venv/bin/python src/run.py --run repeat-empty --benchmarks locomo,mab --methods E
.venv/bin/python src/action_probe.py
.venv/bin/python src/lineage_probe.py
```

历史原始 smoke 用 380 token B 摘要，只验证流程；当前脚本重跑 smoke 使用正式 1536 配置，不会复现旧 smoke 的回答。正式配置与每个历史阶段的原始请求均已保存。当前 `stress.py --answer` 和 `scale_probe.py` 追加输出；已有结果时应将整个复制的实验目录作为新复现目录，或先备份这两份结果文件，避免重复计数。

模型请求按完整参数和消息 SHA-256 缓存到 `results/model_calls/`。缓存命中能重现已保存输出，但不是重新验证模型确定性；需要重新实测时在复制的实验目录使用空 model_calls 目录。输出不会被自动改写或纠错。种子为 912，温度 0、think=false，量化/GPU/运行库仍可能使新跑结果不同。

## 实验契约

- 构建只接收原始历史和数字 cutoff。问题与 gold 放在独立文件；仅在构建快照后进入检索和评分。
- A 按最近原始片段取；B 逐批递归更新摘要；C BM25 检索原文；D 保留证据单元、来源分类、明确 refs、topic 冲突候选和查询时闭包。E 无背景，仅作污染/过度拒答诊断。
- 四组看到相同原文、来源和输入 metadata。产品集是**给定记录元数据与 refs 的条件实验**；不能证明对任意对话的自动语义抽取已解决。公共数据不提供产品 topic/refs 标签。
- 回答证据预算统一用 cl100k_base 限制 1536 token；这是跨组预算代理，不是 Qwen tokenizer。真实原生输入/输出 token 另计，model num_ctx=8192。系统指令与问题在预算外、各方法一致，证据和其来源标签均在预算内。
- 原始历史远大于回答背景；不得把索引保存整段历史与“整段历史放进 reader”混为一谈。每次 reader 看到的实际 context 已保存并可逐条验证。
- B 构建窗口上限 4200 proxy tokens；最大生成 2048 原生 tokens，结果再限 1536 proxy tokens。截断、模型忽略紧凑要求和来源丢失不隐藏。紧凑提示词敏感性另报，不能替换 B 成绩。
- 成功原始调用保留耗时/原生用量，失败重试写 errors.jsonl。无该文件表示没有已捕获网络异常，不表示被中断请求消耗为零。所有阶段已知用量去重汇总，电力成本未测。

## 交付物索引

| 路径 | 内容 |
|---|---|
| `data/product_histories.json` | 按时间排列的合成事件；含 3 个参数变体和相似名称的其他项目 |
| `data/product_questions.json` | 63 问、答案、所需引用和禁止结论 |
| `data/product_expected.json` | 33 个 State 检查点；是选定断言而非穷尽标注 |
| `data/product_scenarios.md` | 可读场景说明与主变体完整事件 |
| `data/public_selection.json` | 完整历史、问题索引、种子选择后的原始 token 规模 |
| `sources/downloads.json` | 主源 URL、revision、SHA-256、日期与大小 |
| `sources/product-snapshot.json` | 只读产品代码 HEAD 与文件内容摘要；工作树有并行开发，不可仅依赖 HEAD |
| `results/main-v2/` | 正式四组原始回答、快照、费用和分组结果 |
| `results/stress/` | 真正 State 滞后、启动约束、元数据依赖、确定性反例 |
| `results/compact-product/` | 单个产品历史的紧凑摘要提示词敏感性；原始 compact 公共构建已停止，未评分 |
| `results/empty/` | 空背景对照 |
| `results/scale/` | 两条完整 LongMemEval-S 历史，A/C/D/E；B 因构建成本未运行 |
| `results/model_calls/` | 完整请求、模型原始响应与用量；仅公开或合成文本 |
| `examples/` | 当前产品 Event/State 模型的演变实例与精确来源 span |
| `docs/review.md` | 方法审查、改正和仍存在的限制 |

`src/audit_integrity.py`、`src/manual_audit.py` 与 `src/latency_probe.py` 默认读取保存的 `results/main-v2`。前两者分别检查流程和重建已记录的复核注释；后者在所有其他推理任务结束后才运行，绕过应用缓存，发起 16 次相同请求的独占延迟校准。它们不是新答案的自动人类审查。`action_probe.py` 和 `lineage_probe.py` 写入固定输出文件，应在复制的目录复现并保留原结果。

`examples/edc-events.json` 包含不同来源身份的 EventInput 示例，不能把整批内容当成一个普通用户/插件凭据都可提交的请求；实际接入应按已认证的作者身份分别写入。本轮没有调用产品 API。示例 State 的来源身份分类是插件数据，不是现有核心已经提供的可信认证语义。

历史 smoke、被停止的 main 与 main-v1 不参与正式质量统计，但保留执行失败与用量证据。这里的得分不等同任何官方排行榜。LoCoMo 内容受 CC BY-NC 4.0 限制；这些本地研究材料不自动获得商业使用或公开再分发许可。此目录没有推送到远端。
