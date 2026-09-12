"""Audit saved prompts without using gold to construct memory."""
import json,re,hashlib,datetime
from pathlib import Path
from core import tokens,digest
R=Path(__file__).resolve().parents[1]
def main():
    root=R/'results/main-v2';run=json.loads((root/'run.json').read_text());budget=run['config']['context_budget']
    rs=[json.loads(l) for l in (root/'answers.jsonl').read_text().splitlines()];assert len({(r['id'],r['method']) for r in rs})==len(rs)
    checks={'answers':len(rs),'budget_violations':0,'future_or_cross_project':0,'missing_call_records':0,'build_future_input':0,'benchmark_annotations_exposed':0}
    hs=json.loads((R/'data/product_histories.json').read_text())+json.loads((R/'data/public_histories.json').read_text());byh={h['id']:h for h in hs}
    for h in hs:
        if h.get('benchmark')=='locomo':
            dates=[datetime.datetime.strptime(e['time'],'%I:%M %p on %d %B, %Y') for e in h['events']]
            assert dates==sorted(dates), 'LoCoMo sessions out of chronological order'
    for r in rs:
        checks['budget_violations']+=tokens(r['context'])>budget
        ids={e['id'] for e in byh[r['history']]['events'] if e['sequence']<=r['cutoff']}
        checks['future_or_cross_project']+=not set(r['selected_ids'])<=ids
        checks['missing_call_records']+=not (R/'results/model_calls'/f"{r['call_key']}.json").exists()
    us=json.loads((root/'build_usage.json').read_text())
    for u in us:
        request=json.loads((R/'results/model_calls'/f"{u['key']}.json").read_text())['request']
        current=request['messages'][-1]['content'].split('NEW EVENTS:\n',1)[1]
        eventids=set(re.findall(r'^\[([^\]]+)\] time=',current,re.M))
        allowed={e['id'] for e in byh[u['history']]['events'] if e['sequence']<=u['cutoff']}
        checks['build_future_input']+=not eventids<=allowed
        checks['benchmark_annotations_exposed']+=any(x in current for x in ['answer_session_ids','qa_pair_ids','session_summary','event_summary','is_decision_point'])
    snaps=list((root/'snapshots').glob('*.json'))
    for f in snaps:
        s=json.loads(f.read_text());assert digest(s['events'])==s['raw_hash']
    checks['snapshot_hashes_verified']=len(snaps)
    (root/'integrity.json').write_text(json.dumps(checks,indent=2));print(json.dumps(checks,indent=2))
    assert not any(checks[k] for k in ['budget_violations','future_or_cross_project','missing_call_records','build_future_input','benchmark_annotations_exposed'])
if __name__=='__main__':main()
