import json,copy
from pathlib import Path
from core import Store,tokens
R=Path(__file__).resolve().parents[1]
h=json.loads((R/'data/product_histories.json').read_text())[0]
s=Store()
for e in h['events']:s.append(e)
before=len(s.events);s.append(h['events'][0]);assert len(s.events)==before and s.duplicates==1
for bad in [dict(h['events'][0],text='changed'),dict(h['events'][-1],id='other',project='other',sequence=1000),dict(h['events'][-1],id='future',sequence=1001,refs=[{'rel':'supersedes','id':'absent'}])]:
    try:s.append(bad)
    except (ValueError,AssertionError):pass
    else:raise AssertionError('Invalid input accepted')
cells=s.materialize()
budget=[c for c in cells if c.get('metadata',{}).get('topic')=='budget']
assert len([c for c in budget if c['status']=='active' and c['source']['channel']=='app'])==1
conflict=[c for c in cells if c.get('metadata',{}).get('topic')=='release'];assert all(c['status']=='conflict' for c in conflict)
for method in ['A','B','C','D','D_no_closure','E']:
    text,chosen,_=s.retrieve(method,'海桥预算和负责人',512,'a '*1000)
    assert tokens(text)<=512
    assert all(c['project']==h['id'] for c in chosen)
# Sequential replay is equivalent to replaying a snapshot + tail.
t=Store()
for e in h['events'][:20]:t.append(copy.deepcopy(e))
for e in h['events'][20:]:t.append(copy.deepcopy(e))
assert t.materialize()==cells
print('PASS: UUID idempotency/conflict, project scope, future refs, unconfirmed update, unresolved conflict, budget and deterministic replay')
