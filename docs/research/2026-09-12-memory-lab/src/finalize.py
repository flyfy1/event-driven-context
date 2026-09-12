"""Build the final report from completed artifacts; never calls a model."""
import collections,csv,datetime,hashlib,json,statistics,time
from pathlib import Path
R=Path(__file__).resolve().parents[1]
def load(p):return json.loads((R/p).read_text())
def lines(p):return [json.loads(x) for x in (R/p).read_text().splitlines()]
def iso(t):return datetime.datetime.fromtimestamp(t,datetime.timezone.utc).isoformat()
def avg(xs):return sum(xs)/len(xs)
def main():
    assert load('results/main-v2/completed.json')['records']==412
    assert load('results/empty/completed.json')['records']==40
    assert (R/'results/final-model-phases-completed.json').exists()
    rs=lines('results/main-v2/answers.jsonl');empty=lines('results/empty/answers.jsonl');lat=load('results/latency_probe.json');assert len(lat)==16
    costs=load('results/main-v2/build_costs.json');state=load('results/main-v2/state_audit.json')
    report=(R/'REPORT.md').read_text()
    table=['| 数据 | A 最近原文 | B 递归摘要 | C 原文检索 | D 结构化证据 |','|---|---:|---:|---:|---:|']
    for b,title in [('product','专项（每组63）'),('locomo','LoCoMo（每组16）'),('mab','MemoryAgentBench（每组24）')]:
        vals=[]
        for m in 'ABCD':
            rows=[r for r in rs if r['benchmark']==b and r['method']==m];n=len(rows);k=sum(r['scores']['strict_answer'] for r in rows);vals.append(f'{k}/{n}（{k/n:.1%}）')
        table.append('|'+title+'|'+'|'.join(vals)+'|')
    table+=['','以上是**规则通过**，包含格式误判。专项 D 较好，公共题未显示足以宣称通用优越性的证据；尤其主 D 在没有显式 refs 的公开事实中，仍依赖 reader 理解更新。没有把各基准混成一个总分。']
    report=report.replace('<!-- MAIN_RESULTS -->','\n'.join(table))
    want=sum(x['expected_n'] for x in state);got=sum(x['D_active_retained'] for x in state);stale=sum(x['D_stale_active'] for x in state);bm=sum(x['B_expected_source_marker_retained'] for x in state)
    report=report.replace('<!-- STATE_RESULTS -->',f'33 个检查点共有 {want} 次必需活动条目检查，D 保留 {got}/{want}，已标注旧条目仍错误活跃 {stale} 次；B 保留对应来源标记 {bm}/{want}。后者不检查句子保真，已被“最初预算”案例证明不能单独作为质量指标。完整 State 体积与构建耗时在 `state_audit.json`；不把存储体积当成 reader 背景体积。')
    es=[]
    for b in ['locomo','mab']:
        rr=[r for r in empty if r['benchmark']==b];es.append(f"{b} {sum(r['scores']['strict_answer'] for r in rr)}/{len(rr)}，unknown {sum(r['answer'].get('status')=='unknown' for r in rr)}/{len(rr)}")
    pub='空背景 E 的规则结果：'+'；'.join(es)+'。LoCoMo 本就含不可回答题，拒答能获得部分分数，因此 E 通过不自动等于泄漏。'
    pub+='\n\n复核发现三类重要偏差：日期顺序/正确释义造成假阴性；给出无依据的具体日期仍包含 gold 月份而假阳性；原文包含问题未证实的日期前提。另有 D 取入过多邻居而挤掉关键书名、最新事实已取到却仍答旧值，以及答案匹配但中间证据链缺失的真实问题。53 个定向复核输出含专项、公共题与长历史探针，不能将其有偏比例当总体准确率。'
    report=report.replace('<!-- PUBLIC_AUDIT -->',pub)
    b_in=sum(x['native_input_tokens'] for x in costs);b_out=sum(x['native_output_tokens'] for x in costs);b_calls=sum(x['logical_build_calls'] for x in costs);b_trunc=sum(x['truncated_generation'] for x in costs)
    a_in=sum(x['answer_prompt_tokens'] for x in rs);a_out=sum(x['answer_completion_tokens'] for x in rs)
    calls=[json.loads(f.read_text()) for f in (R/'results/model_calls').glob('*.json')]
    usage={'cached_unique_successful_requests':len(calls),'fresh_latency_requests':len(lat),'known_successful_requests':len(calls)+len(lat),'native_input_tokens':sum(x['response'].get('prompt_eval_count',0) for x in calls+lat),'native_output_tokens':sum(x['response'].get('eval_count',0) for x in calls+lat),'request_elapsed_sum_s':sum(x['elapsed_s'] for x in calls+lat),'extra_runner_paid_api_cost_usd':0,'coordinator_and_research_agent_tokens':'not included; this ledger covers experiment runner model requests only','interrupted_in_flight_tokens':'unknown; see stopped-stage markers','electricity_and_opportunity_cost':'not measured','first_recorded_request_at':iso(min(x['timestamp']-x['elapsed_s'] for x in calls)),'last_phase_completed_at':iso(load('results/final-model-phases-completed.json')['completed_at'])}
    builds=[load('results/model_calls/'+x['key']+'.json') for x in load('results/main-v2/build_usage.json')]
    usage['main_builder_native_window_observation']={'configured_num_ctx':8192,'max_reported_prompt_plus_output':max(x['response'].get('prompt_eval_count',0)+x['response'].get('eval_count',0) for x in builds),'calls_reported_prompt_plus_output_above_num_ctx':sum(x['response'].get('prompt_eval_count',0)+x['response'].get('eval_count',0)>8192 for x in builds),'interpretation':'Reported counts only; backend window shifting/truncation semantics were not independently instrumented.'}
    (R/'results/total_usage.json').write_text(json.dumps(usage,indent=2))
    latency=[]
    for m in 'ABCD':
        xx=[x for x in lat if x['method']==m];latency.append({'method':m,'n':len(xx),'median_s':statistics.median(x['elapsed_s'] for x in xx),'min_s':min(x['elapsed_s'] for x in xx),'max_s':max(x['elapsed_s'] for x in xx),'native_input_mean':avg([x['response'].get('prompt_eval_count',0) for x in xx]),'native_output_mean':avg([x['response'].get('eval_count',0) for x in xx])})
    (R/'results/latency_summary.json').write_text(json.dumps(latency,indent=2))
    costtext=f'主实验 B 构建共 {b_calls} 次逻辑调用，输入 {b_in:,}、输出 {b_out:,} 原生 tokens；其中 {b_trunc} 次返回 length，另有摘要体积裁剪。主实验 412 个 reader 回答共输入 {a_in:,}、输出 {a_out:,} 原生 tokens。各阶段实际成功请求去重后再加 16 次新发延迟校准，共 {usage["known_successful_requests"]} 次，已知输入 {usage["native_input_tokens"]:,}、输出 {usage["native_output_tokens"]:,} 原生 tokens。详见 `build_costs.json`、`aggregate.csv` 与 `total_usage.json`。'
    costtext+='\n\n独占延迟校准每组只有两个问题×两次，保留服务端前缀缓存；p50：'+ '、'.join(f'{x["method"]} {x["median_s"]:.2f} 秒' for x in latency)+'。这是量级校准，不能据此给生产吞吐承诺。'
    win=usage['main_builder_native_window_observation']
    if win['calls_reported_prompt_plus_output_above_num_ctx']:
        costtext+=f'\n\n构建阶段有 {win["calls_reported_prompt_plus_output_above_num_ctx"]} 次报告的原生输入加输出超过配置 num_ctx=8192；runner未独立观测后端的窗口滚动/截断语义。这是 B 的额外实现限制，不能将其结果外推为最强摘要系统的上限。reader 的证据预算、实际输入和响应仍逐条保存。'
    report=report.replace('<!-- COST_RESULTS -->',costtext).replace('此文件的结果表在正式运行结束后填充。','正式四组实验、补充探针与复核已完成。')
    assert '<!--' not in report
    (R/'REPORT.md').write_text(report)
    # Source/hashes for final delivered supplemental code, explicitly retrospective.
    files=['core.py','run.py','stress.py','action_probe.py','lineage_probe.py','scale_probe.py','compact_summary.py','latency_probe.py','manual_audit.py']
    provenance={'recorded_at':iso(time.time()),'note':'Retrospective hashes of delivered supplemental sources, not runtime commit attestations. Main/smoke/empty/compact run.json record their launch commit; exact executed model requests remain in call records.','files':{f:hashlib.sha256((R/'src'/f).read_bytes()).hexdigest() for f in files}}
    (R/'sources/supplementary-provenance.json').write_text(json.dumps(provenance,indent=2))
    inventory=[]
    for p in sorted((R/'results').glob('*/run.json')):
        u=json.loads(p.read_text());end=p.parent/'completed.json';done=json.loads(end.read_text()) if end.exists() else {};markers={k:json.loads((p.parent/k).read_text()) for k in ['INVALIDATED.json','SUPERSEDED.json','SCOPE_REDUCED.json'] if (p.parent/k).exists()}
        inventory.append({'run':p.parent.name,'started_at':iso(u['started_at']),'completed_at':iso(done['finished_at']) if done else None,'elapsed_s':done['finished_at']-u['started_at'] if done else None,'records':done.get('records'),'code_commit':u['code_commit'],'args':u['args'],'config':u['config'],'markers':markers})
    (R/'results/run_inventory.json').write_text(json.dumps(inventory,ensure_ascii=False,indent=2))
    ex=['# 执行记录','', '仅对独立实验目录写入。主产品源码只读；首个读取快照与哈希保存在 sources/product-snapshot.json。用户报告中途断网，后续确认本地服务和主跑仍存活，未重复已完成阶段。','', '| 阶段 | UTC 开始 | UTC 完成 | 原始回答数 | launch commit |','|---|---|---|---:|---|']
    for x in inventory:ex.append(f'|{x["run"]}|{x["started_at"]}|{x["completed_at"] or "中止/作废，见标记"}|{x["records"] if x["records"] is not None else "不计分"}|{x["code_commit"][:12]}|')
    ex+=['','用户后续追加授权完整归档到产品仓库 `docs/research/2026-09-12-memory-lab`；实验仍在独立目录执行，归档是唯一新增写入产品仓库的范围。归档 SHA-256 清单与完整实验 Git bundle 随目录交付。']
    ex+=['','正式主跑源版本为 `6bfb08601dc5790e5c84c9222a6e75ee65fb2761`；后续补充脚本和审计工具随最终提交交付。主跑启动后对 Model 的调用上限增加入口检查，不改变已经载入进程的实验语义。补充脚本没有全部在请求时记录单独 Git commit，故另给最终文件 hash 并明确为回顾性记录；完整模型请求参数不依赖这些说明即可检查。','', '## 失败、中止与范围','', '- smoke 完整跑通后发现 B 摘要预算过小；正式使用统一1536代理tokens，旧 smoke 不参与质量对照。','- main 在任何回答前作废：B 尚未同等看到 metadata；修正后重跑。main-v1 在公共阶段前停止：澄清 MAB 按原始事实编号更新；相同产品构建/回答从请求缓存复用。标记文件保留。','- compact 原计划含公开历史；产品21问完成后限制为单个产品敏感性样本，公开构建部分中止，不评分。compact-product 用相同缓存请求完整导出。','- LongMemEval-S 只跑 A/C/D/E，省去两条十万token历史的重复摘要构建；没有用 oracle 缩短历史冒充长程效果。','- 捕获的网络错误/重试仅以 model_calls/errors.jsonl 为准；未捕获的中断请求tokens未知。JSON格式失败和length结果原样保留并计入评分，未静默重答。','', '## 命令与用量','', '精确脚本参数见 run_inventory.json 的 args/config，复现命令见 README。最终接续在主 completed.json 出现后运行 `src/run.py --run empty --benchmarks locomo,mab --methods E`，再独占运行 `src/latency_probe.py`，顺序日志见 final-model-phases.log。','', f'runner已知成功请求 {usage["known_successful_requests"]} 次；输入 {usage["native_input_tokens"]:,}、输出 {usage["native_output_tokens"]:,} 原生tokens。最早已记录模型请求 {usage["first_recorded_request_at"]}；最终模型阶段完成 {usage["last_phase_completed_at"]}。各请求elapsed相加不等于实验墙钟时间。','', '账本不包含本会话协调/文献研究agent的tokens；runner新增付费API支付为0，电力、机器占用与人类复核成本未测。临时服务只绑定127.0.0.1:11439，使用已有模型，完成后停止；未改用户配置。服务停止结果在 runtime-cleanup.json 单独记录。']
    (R/'results/execution.md').write_text('\n'.join(ex))
    print('Final report and usage written',usage['known_successful_requests'])
if __name__=='__main__':main()
