"""User assertion vs agent inference vs repeated plugin summaries; synthetic only."""
import uuid,json
from pathlib import Path
from core import Store,Model,origin,digest,jdump,ANSWER_PROMPT,ANSWER_SCHEMA
R=Path(__file__).resolve().parents[1]
ids=[str(uuid.uuid5(uuid.NAMESPACE_URL,'lineage/'+str(i))) for i in range(4)]
texts=['用户明确陈述：预算为 30000 元。','我推测预算应该降到 10000 元，但没有用户确认。','插件摘要：预算为 10000 元。','再次插件摘要：预算为 10000 元。']
events=[]
for i in range(4):
    events.append({'id':ids[i],'project':'lineage-project','sequence':i+1,'time':f'2026-02-01T0{i}:00:00Z','text':texts[i],'type':'derived' if i>=2 else 'note','speaker':['user','agent','summary-plugin','summary-plugin'][i],'source':{'channel':['app','skill','plugin','plugin'][i]},'metadata':{'kind':'summary' if i>=2 else 'decision','topic':'budget'},'refs':[] if i==0 else [{'rel':'derived_from' if i>=2 else 'replies_to','id':ids[i-1]}]})
# A plugin claims supersession; this remains a summary, not a user confirmation.
events[2]['refs'].append({'rel':'supersedes','id':ids[0]})
byid={e['id']:e for e in events}
def roots(eid):
    parents=[r['id'] for r in byid[eid]['refs'] if r['rel']=='derived_from']
    return set().union(*(roots(p) for p in parents)) if parents else {eid}
s=Store()
for e in events:s.append(e)
cells=s.materialize();assert next(c for c in cells if c['id']==ids[0])['status']=='active';assert roots(ids[2])==roots(ids[3])=={ids[1]}
qs=[{'id':'current-budget','question':'用户明确确认的预算是多少？','gold':'30000 元','evidence':[ids[0]]},{'id':'independent-evidence','question':'两次插件摘要能算作两条独立的用户确认吗？','gold':'不能；它们都源于同一次未确认的 agent 推断。','evidence':[ids[1],ids[2],ids[3]]}]
fixture={'events':events,'questions':qs,'expected_memory':{'human_budget_source':ids[0],'plugin_roots':[ids[1]],'unconfirmed_sources':ids[1:]},'cell_basis':{e['id']:origin(e) for e in events}}
(R/'data/plugin_lineage.json').write_text(json.dumps(fixture,ensure_ascii=False,indent=2));out=[];m=Model(R/'results/model_calls')
for q in qs:
    c,_,_=s.retrieve('D',q['question'],1536)
    text,u=m.call('lineage/'+q['id'],[{'role':'system','content':ANSWER_PROMPT},{'role':'user','content':'EVIDENCE:\n'+c+'\nQUESTION:\n'+q['question']}],256,ANSWER_SCHEMA)
    out.append({'question':q,'context':c,'answer':json.loads(text),'call_key':digest(u['request'])})
(R/'results/lineage_probe.json').write_text(json.dumps(out,ensure_ascii=False,indent=2));print('lineage probes complete',len(out))
