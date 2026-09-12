"""Transparent offline metrics: rule proxies, source labels, costs and state checks.
No results are silently corrected; manual audit is a separate artifact.
"""
import argparse,collections,csv,json,statistics,random,math
from pathlib import Path
R=Path(__file__).resolve().parents[1]
def mean(xs):
    xs=[x for x in xs if x is not None];return sum(xs)/len(xs) if xs else None
def pct(x):return 'N/A' if x is None else f'{x*100:.1f}%'
def qtile(xs,p):
    xs=sorted(xs);return xs[max(0,math.ceil(len(xs)*p)-1)] if xs else 0
def main():
    p=argparse.ArgumentParser();p.add_argument('--run',default='main-v2');a=p.parse_args();root=R/'results'/a.run
    rs=[json.loads(l) for l in (root/'answers.jsonl').read_text().splitlines()]
    groups=collections.defaultdict(list)
    for r in rs:groups[(r['benchmark'],r['method'])].append(r)
    tables=[]
    for (bench,method),rows in sorted(groups.items()):
        row={'benchmark':bench,'method':method,'n':len(rows)}
        for key in ['strict_answer','answer_match','status_correct','forbidden_hit','f1','citation_id_exists','citation_source_visible','citation_precision_gold','citation_recall_gold','retrieval_recall_gold','retrieval_session_recall']:
            row[key]=mean([r['scores'].get(key) for r in rows])
        row.update(context_mean=mean([r['context_tokens'] for r in rows]),context_max=max(r['context_tokens'] for r in rows),prompt_native_mean=mean([r['answer_prompt_tokens'] for r in rows]),answer_output_native=sum(r['answer_completion_tokens'] for r in rows),answer_input_native=sum(r['answer_prompt_tokens'] for r in rows),answer_s_p50=qtile([r['answer_s'] for r in rows],.5),answer_s_p95=qtile([r['answer_s'] for r in rows],.95),retrieval_ms_p50=qtile([r['retrieval_s']*1000 for r in rows],.5),truncated=sum(r['done_reason']=='length' for r in rows),cache_hits=sum(r['cache_hit'] for r in rows))
        tables.append(row)
    with (root/'aggregate.csv').open('w') as f:
        w=csv.DictWriter(f,fieldnames=tables[0].keys());w.writeheader();w.writerows(tables)
    # Do not treat 3 template variants or many questions on one history as independent samples.
    detail=[]
    for groupkey in ['category','history']:
        for (bench,method),rows in sorted(groups.items()):
            for v in sorted({r[groupkey] for r in rows}):
                rr=[r for r in rows if r[groupkey]==v];detail.append({'benchmark':bench,'method':method,'group_by':groupkey,'group':v,'n':len(rr),'rule_pass':mean([r['scores']['strict_answer'] for r in rr]),'source_recall':mean([r['scores']['citation_recall_gold'] for r in rr])})
    (root/'by_category_history.json').write_text(json.dumps(detail,indent=2))
    expected=json.loads((R/'data/product_expected.json').read_text());state=[]
    for ex in expected:
        f=root/'snapshots'/f"{ex['history']}-{ex['cutoff']}.json"
        if not f.exists():continue
        snap=json.loads(f.read_text());cells={c['id']:c for c in snap['cells']}
        actual={k for k,c in cells.items() if c['status'] in ('active','conflict')}
        forbidden=set(ex['must_not_active']);want=set(ex['active'])
        row={'history':ex['history'],'cutoff':ex['cutoff'],'expected_n':len(want),'D_active_retained':len(want&actual),'D_stale_active':len(forbidden&actual),'D_unconfirmed_promoted':sum(cells[i].get('source',{}).get('channel')!='skill' for i in ex['must_not_promote'] if i in cells),'B_expected_source_marker_retained':sum(i in snap['summary'] for i in want),'B_source_marker_n':len(want),'B_summary_tokens':snap['summary_tokens'],'D_full_evidence_state_tokens':snap['state_tokens'],'D_build_s':snap['D_build_s']}
        state.append(row)
    (root/'state_audit.json').write_text(json.dumps(state,indent=2))
    usage=json.loads((root/'build_usage.json').read_text());costs=[]
    for hid in sorted({u['history'] for u in usage}):
        us=[u for u in usage if u['history']==hid];costs.append({'history':hid,'logical_build_calls':len(us),'native_input_tokens':sum(u['prompt_tokens'] for u in us),'native_output_tokens':sum(u['completion_tokens'] for u in us),'logical_s':sum(u['elapsed_s'] for u in us),'cache_hits':sum(u['cache_hit'] for u in us),'truncated_generation':sum(u['done_reason']=='length' for u in us)})
    (root/'build_costs.json').write_text(json.dumps(costs,indent=2))
    allcalls=[]
    for f in (R/'results/model_calls').glob('*.json'):
        u=json.loads(f.read_text());allcalls.append(u)
    # The final ledger also includes fresh, intentionally uncached latency requests.
    # Preserve its richer provenance when regenerating offline score tables.
    ledger=R/'results/total_usage.json'
    if not ledger.exists() or 'last_phase_completed_at' not in json.loads(ledger.read_text()):
        (ledger).write_text(json.dumps({'unique_successful_requests':len(allcalls),'native_input_tokens':sum(u['response'].get('prompt_eval_count',0) for u in allcalls),'native_output_tokens':sum(u['response'].get('eval_count',0) for u in allcalls),'inference_wall_seconds_sum':sum(u['elapsed_s'] for u in allcalls),'extra_paid_api_cost':0,'currency':'USD','electricity_and_opportunity_cost':'not measured','interrupted_requests':'see INVALIDATED and execution log; tokens of interrupted in-flight calls are unknown, not zero'},indent=2))
    lines=['# 实测结果','',f'主运行：{a.run}。规则通过率不是官方榜单分数，也不是人类完整正确率。完整输入/回答在 answers.jsonl。','', '| 基准 | 方法 | n | 规则通过 | 正确状态 | 禁用词命中 | 引用 gold 召回 | 检索 gold 召回 | 背景均值 | 回答 p50/p95 秒 |', '|---|---|---:|---:|---:|---:|---:|---:|---:|---:|']
    for t in tables:lines.append(f"|{t['benchmark']}|{t['method']}|{t['n']}|{pct(t['strict_answer'])}|{pct(t['status_correct'])}|{pct(t['forbidden_hit'])}|{pct(t['citation_recall_gold'])}|{pct(t['retrieval_recall_gold'])}|{t['context_mean']:.0f}|{t['answer_s_p50']:.2f}/{t['answer_s_p95']:.2f}|")
    lines+=['','B 的“检索召回”是摘要中仍出现的 gold 来源标记，不能等同取回原文。其他组也仅在事件 ID 粒度计召回，不代表片段蕴含。MAB 无原文证据 gold，记 N/A。主跑 elapsed 含其它实验排队，见 concurrency-note.md；独占校准单列。','', '## 按题型','', '| 基准/题型 | A | B | C | D |','|---|---:|---:|---:|---:|']
    for b,c in sorted({(r['benchmark'],r['category']) for r in rs}):
        vals=[pct(mean([r['scores']['strict_answer'] for r in rs if r['benchmark']==b and r['category']==c and r['method']==m])) for m in ['A','B','C','D']];lines.append('|'+b+'/'+c+'|'+'|'.join(vals)+'|')
    lines+=['','## 保存状态','',f"检查点 {len(state)}；D 必需活动条目保留 {sum(x['D_active_retained'] for x in state)}/{sum(x['expected_n'] for x in state)}；旧条目错误活跃计数 {sum(x['D_stale_active'] for x in state)}；B 必需来源标记保留 {sum(x['B_expected_source_marker_retained'] for x in state)}/{sum(x['B_source_marker_n'] for x in state)}。B 标记存在不代表内容未变；D 的规则通过不证明自动语义抽取可靠。"]
    hist={h['id']:h for h in json.loads((R/'data/product_histories.json').read_text())+json.loads((R/'data/public_histories.json').read_text())}
    normalized=[]
    for (bench,method),rows in sorted(groups.items()):
        recall=[];exists=[];fmt=[]
        for r in rows:
            raw=r['answer'].get('citations',[]);cited={c.strip('[]〔〕') for c in raw};valid={e['id'] for e in hist[r['history']]['events'] if e['sequence']<=r['cutoff']};gold=set(r['gold'].get('evidence',[]))
            if cited:exists.append(len(cited&valid)/len(cited));fmt.extend(c!=c.strip('[]〔〕') for c in raw)
            if gold:recall.append(len(cited&gold)/len(gold))
        normalized.append({'benchmark':bench,'method':method,'normalized_citation_exists':mean(exists),'normalized_gold_recall':mean(recall),'bracket_format_fraction':mean(fmt)})
    (root/'normalized_citations.json').write_text(json.dumps(normalized,indent=2))
    lines+=['','## 引用格式与可定位性','', '仅去掉完整显示括号后重新检查；不补齐短 ID，不修复伪造 ID，不读取 gold 来替换引用。该可定位性仍不是原文蕴含。','', '| 基准 | 方法 | 规范化后 ID 存在 | gold 召回 | 引用额外括号比例 |','|---|---|---:|---:|---:|']
    for x in normalized:lines.append(f"|{x['benchmark']}|{x['method']}|{pct(x['normalized_citation_exists'])}|{pct(x['normalized_gold_recall'])}|{pct(x['bracket_format_fraction'])}|")
    lines+=['','## B 构建成本（独立于回答）','', '| 历史 | 调用 | 原生输入 tokens | 原生输出 tokens | 逻辑推理秒 | 命中缓存 | 输出截断 |','|---|---:|---:|---:|---:|---:|---:|']
    for c in costs:lines.append(f"|{c['history']}|{c['logical_build_calls']}|{c['native_input_tokens']}|{c['native_output_tokens']}|{c['logical_s']:.1f}|{c['cache_hits']}|{c['truncated_generation']}|")
    lines+=['','A/C/D 构建模型 tokens 为 0；append/materialize CPU 耗时在 snapshot，BM25 索引在查询时构建并计入 retrieval_s。此原型没有持久索引优化，不能当作生产延迟承诺。逻辑成本按使用一次计；缓存命中不重复计实际运行成本，所有阶段实际成功调用去重合计见 total_usage.json。','', '## 不确定性','', '本轮只有 2 段 LoCoMo 对话、2 个嵌套 MAB 历史和一个产品模板的 3 个参数变体。各问不独立，不对这些相关样本给伪精确置信区间，也不声称统计显著。每历史/每题型结果已单列。公开基准预训练污染不能排除；空背景 E 另列。']
    (R/'results/summary.md').write_text('\n'.join(lines))
    # Audit queue: all product unknown/conflict plus deterministic mixed success/failure sample.
    rr=[r for r in rs if r['gold']['status'] in ('unknown','conflict') or not r['scores']['strict_answer']]
    (root/'audit_queue.json').write_text(json.dumps(rr,ensure_ascii=False,indent=2))
    print('rows',len(rs),'groups',len(tables),'state checkpoints',len(state))
if __name__=='__main__':main()
