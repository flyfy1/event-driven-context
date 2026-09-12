# 延迟口径

正式主跑与补充 stress/compact/scale 请求曾在同一临时本地服务上交错排队，服务并行数为 1。原始 elapsed_s 是实际请求墙钟时间，包括排队；因此主跑表中的 p50/p95 不能作为四种方法的公平独占延迟排名。token 数、实际上下文、原始回答和成功调用数仍逐条保留。

全部作业完成后另跑 latency_probe：从主跑固定选取 product-0/budget_updated 与 locomo-0/q24 的四种方法原请求，每个重复两次，第二轮反向排序；直接发新 API 请求绕过应用回答缓存。服务内部前缀缓存保留。此小样本只能校准量级，不是生产并发性能结论。

构建逻辑耗时同样可能包含排队；原始 response 的 total_duration/prompt_eval_duration/eval_duration 一并保存在请求记录中。运行总耗时按起止时刻统计，不能把并行排队的各请求 elapsed_s 相加称为整场实验墙钟时间。
