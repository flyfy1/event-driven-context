"""Export selected cases after scoring, with supporting raw source text.
The cases are deliberately chosen diagnostic examples, not an unbiased estimate.
"""
import json
from pathlib import Path
R=Path(__file__).resolve().parents[1]
rows=[json.loads(x) for x in (R/'results/main-v2/answers.jsonl').read_text().splitlines()]
hs={h['id']:h for h in json.loads((R/'data/product_histories.json').read_text())+json.loads((R/'data/public_histories.json').read_text())}
selected={('product-0/historical','B'),('product-0/audio_unclear','B'),('product-0/audio_corrected','B'),('product-0/member_conflict','A'),('product-0/unknown','D'),('product-0/untrusted_update','C'),('product-0/late_arrival','B')}
selected.update({('locomo-0/q103','D'),('locomo-1/q28','D'),('mab-0/4/q30','D'),('mab-0/0/q41','D'),('mab-0/0/q30','D')})
manual_evidence={'mab-0/4/q30':['e00384'],'mab-0/0/q41':['e00453','e00297'],'mab-0/0/q30':['e00183','e00401','e00103']}
dest=R/'results/failures';dest.mkdir(exist_ok=True)
for r in rows:
    if (r['id'],r['method']) not in selected:continue
    ids=set(r['gold'].get('evidence',[]))|{x.strip('[]〔〕') for x in r['answer'].get('citations',[])}|set(manual_evidence.get(r['id'],[]))
    out=dict(r,source_events=[e for e in hs[r['history']]['events'] if e['id'] in ids],selection='Diagnostic case chosen after results; not a random accuracy sample')
    if r['id'] in manual_evidence:out['posthoc_manual_evidence']=manual_evidence[r['id']]
    if r['method']=='B':out['summary_snapshot']=json.loads((R/'results/main-v2/snapshots'/f"{r['history']}-{r['cutoff']}.json").read_text())['summary']
    (dest/(r['id'].replace('/','-')+'-'+r['method']+'.json')).write_text(json.dumps(out,ensure_ascii=False,indent=2))
print('exported',len(list(dest.glob('*.json'))),'cases')
