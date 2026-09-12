"""Check exported examples against source text and inspected V2 shape limits.
This does not call the product API or claim an integration test.
"""
import json,re,uuid
from pathlib import Path
R=Path(__file__).resolve().parents[1]
events=json.loads((R/'examples/events.json').read_text());byid={e['id']:e for e in events}
states=json.loads((R/'examples/states.json').read_text())
for v,s in enumerate(states,1):
    assert s['version']==v and s['expected_version']==v-1
    assert re.fullmatch(r'[a-z0-9-]+/[A-Za-z0-9_.{}-]+',s['key'])
    assert len(s['refs'])<=32 and len(s['refs'])==len(set(s['refs']))
    assert len(s['content']['text'].encode())<=256*1024
    assert len(json.dumps(s['data']).encode())<=256*1024
    for item in s['data']['items']:
        assert item['kind']!='audio'
        for source in item['sources']:
            eid=source['event_id'];uuid.UUID(eid)
            assert item['asserted_by']['speaker_label']==byid[eid]['speaker']
            assert eid in s['refs'] and byid[eid]['sequence']<=s['based_on_sequence']
            span=source['span'];assert span['unit']=='utf8-byte'
            assert byid[eid]['text'].encode()[span['start']:span['end']].decode()==item['text']
converted=json.loads((R/'examples/edc-events.json').read_text())['events']
assert all(e['metadata']['speaker_label']==byid[e['id']]['speaker'] for e in converted)
result={'versions':len(states),'exact_spans_checked':sum(len(s['data']['items']) for s in states),'max_refs':max(len(s['refs']) for s in states),'speaker_labels_preserved':True,'status':'passed','scope':'Offline shape/span validation; no product API/authentication/runtime test'}
(R/'results/example_validation.json').write_text(json.dumps(result,indent=2));print(result)
