# 实验过程记录索引

执行过程通过以下原始工件重建，不依赖最终报告的叙述：

1. `sources/product-snapshot.json` 与 `product-followup.json`：只读实现观察时刻、版本与文件哈希。
2. `sources/downloads.json`、`docs/sources.md`：论文/官方仓库/数据集证据、固定 revision、获取日期与许可边界。
3. 每个 `results/<run>/run.json`：模型参数、样本问题 ID、数据摘要、启动代码 commit 和起始时间。
4. 每个 `snapshots/`：每个处理上界之后实际保存的摘要、原始事件集合与结构化证据。
5. `results/model_calls/`：成功请求的完整 system/user prompt、原始模型响应、用量、耗时、请求哈希与时间。缓存按请求去重，具体 run 的 build_usage/answers 记录缓存命中。
6. `results/*progress.log`、`final-model-phases.log`：完整 runner 标准输出。原始失败/中断阶段用 INVALIDATED、SUPERSEDED、SCOPE_REDUCED 标记，不混入正式成绩。
7. `code-history.txt` 与归档中的 `experiment-history.bundle`：代码演变与完整提交历史。最终交付 commit 在归档元数据中；这个文本日志在最后提交前导出。
8. `manual_audit.json`、`failures/`：评分后的定向复核与源事件；未回流给构建器，未覆盖原始分数。
9. `runtime/ollama.log` 与 `runtime-cleanup.json`：本任务临时推理服务的输出和关闭记录。服务只接收本实验公开/合成内容。
10. `execution.md`、`run_inventory.json`、`total_usage.json`：最终汇总、阶段边界、成本与未测量项。

本索引定义“完整实验日志”为 runner 和构建/回答过程的可追溯输入输出、版本与执行记录；没有复制其他任务的对话、全局客户端日志或私人会话。协调 agent 的每一条 UI/搜索工具消息未另存成原始工具事务日志，文献阅读由来源表和下载哈希追踪。

用户在执行途中报告网络中断；恢复后检查到本地推理进程仍运行，直接继续，没有重跑已有结果。之后用户追加授权，把全部实验材料放进 Event-driven Context 仓库的独立研究目录。归档不包括可重建虚拟环境与已有模型权重；包括下载的公开输入文件和重现它们的脚本。
