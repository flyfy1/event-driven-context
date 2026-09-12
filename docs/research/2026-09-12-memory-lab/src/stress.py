"""Independent adversarial probes. Frozen before inspecting main answers.

No new model types. Offline mode verifies state/packing invariants; --answer
actually queries the same reader and logs outputs, including failed ones.
"""
import argparse,copy,json,time
from pathlib import Path
from core import Store,Model,origin,tokens,jdump,digest,ANSWER_PROMPT,ANSWER_SCHEMA,pack
R=Path(__file__).resolve().parents[1]
def main():
    p=argparse.ArgumentParser();p.add_argument('--answer',action='store_true');a=p.parse_args()
    hs=json.loads((R/'data/product_histories.json').read_text());conf=json.loads((R/'configs/main.json').read_text())
    dest=R/'results/stress';dest.mkdir(exist_ok=True);model=Model(R/'results/model_calls')
    probes=[];checks=[]
    def store(es):
        s=Store()
        for e in es:s.append(e)
        return s
    for h in [x for x in hs if not x['id'].endswith('other')]:
        es=h['events'];target=next(e for e in es if e['text'].startswith('再次修改'))
        before=[e for e in es if e['sequence']<target['sequence']];latest=before+[target]
        q='海桥当前最新预算是多少元？';old=store(before);fresh=store(latest)
        for mode,s in [('stale_undisclosed',old),('tail_catchup',fresh),('stale_disclosed',old)]:
            c,chosen,lat=s.retrieve('D',q,1536)
            if mode=='stale_disclosed':
                c='COVERAGE WARNING: State based_on_sequence='+str(before[-1]['sequence'])+'; latest_sequence='+str(target['sequence'])+'. New events are unavailable; do not claim the CURRENT value.\n'+c
                # Metadata consumes the same total budget.
                from core import clip
                c=clip(c,1536)
            probes.append({'id':h['id']+'/'+mode,'category':'real_lag','question':q,'mode':mode,'context':c,'gold_current':target['text'],'expected_status':'unknown' if mode=='stale_disclosed' else 'known','expected_source':target['id'] if mode=='tail_catchup' else None})
        replay=store(before)
        for e in latest[len(before):]:replay.append(e)
        checks.append({'probe':h['id']+'/tail_replay','pass':replay.materialize()==fresh.materialize(),'based_on':before[-1]['sequence'],'latest':target['sequence']})
        # Query carries no direct lexical hint for the long-standing privacy constraint.
        q='请为下周实验选择托管服务并说明可否直接开始。'
        s=store(es);c,chosen,_=s.retrieve('D',q,768)
        cells=s.materialize();pins=[e for e in cells if e['status'] in ('active','conflict') and e.get('metadata',{}).get('kind') in ('constraint','goal') and origin(e)=='user_assertion']
        pintext,pinned=pack(pins,384,True)
        # Reserve pin budget first; both alternatives total <=768.
        retrieved,chosen2,_=s.retrieve('D',q,max(0,768-tokens(pintext)-2))
        combo=pintext+'\n\n'+retrieved
        assert tokens(combo)<=768
        constraint=next(e for e in es if e.get('metadata',{}).get('kind')=='constraint')
        for mode,context in [('query_only',c),('pinned_constraints',combo)]:
            probes.append({'id':h['id']+'/'+mode,'category':'session_injection','question':q,'mode':mode,'context':context,'must_notice':'原始录音不能上传第三方','expected_source':constraint['id']})
        # Removing topic metadata simulates unstructured notes; refs are still explicit input.
        no_meta=copy.deepcopy(es)
        for e in no_meta:e['metadata']={}
        for mode,s in [('metadata_available',store(es)),('metadata_absent',store(no_meta))]:
            c,_,_=s.retrieve('D','海桥上线日期到底是哪天？',768)
            probes.append({'id':h['id']+'/'+mode,'category':'metadata_dependency','question':'海桥上线日期到底是哪天？','mode':mode,'context':c,'expected_status':'conflict'})
    base=copy.deepcopy(hs[0]['events'][0]);base['metadata']={'kind':'fact','topic':'capabilities'};base['text']='海桥支持离线录音。';base['refs']=[]
    other=copy.deepcopy(base);other.update(id='complementary',sequence=base['sequence']+1,text='海桥支持本地转录。')
    s=store([base,other]);checks.append({'probe':'complementary-facts-false-conflict','pass':not any(e['status']=='conflict' for e in s.materialize()),'observed':[e['status'] for e in s.materialize()],'expected':'Both compatible capabilities should be active without conflict.'})
    future=copy.deepcopy(other);future.update(id='future-effective',text='下个月才生效的新预算是 10000 元。',refs=[{'rel':'supersedes','id':base['id']}]);future['metadata']={'kind':'decision','topic':'budget','valid_from':'2026-12-01T00:00:00Z'}
    initial=copy.deepcopy(base);initial['text']='当前预算为 20000 元。';initial['metadata']={'kind':'decision','topic':'budget'}
    s=store([initial,future]);checks.append({'probe':'future-effective-update','pass':next(e for e in s.materialize() if e['id']==initial['id'])['status']=='active','observed':[(e['id'],e['status']) for e in s.materialize()],'expected':'Before valid_from, old value still applies. Prototype has no effective-time reducer.'})
    # Semantic duplicate with new ID: equal text avoids a false conflict but remains 2 cells.
    duplicate=copy.deepcopy(base);duplicate.update(id='same-content-new-id',sequence=2)
    s=store([base,duplicate]);checks.append({'probe':'same-content-new-id','pass':len(s.materialize())==1,'observed_cells':len(s.materialize()),'expected':'One assertion cluster, two raw events. Prototype lacks semantic alias clustering.'})
    # Same-ID transport replay is idempotent.
    s=store([base]);s.append(base);checks.append({'probe':'same-id-retry','pass':len(s.events)==1,'duplicates':s.duplicates})
    (dest/'deterministic.json').write_text(jdump(checks));(dest/'probes.json').write_text(jdump(probes))
    if a.answer:
        for probe in probes:
            text,u=model.call('stress/'+probe['id'],[{'role':'system','content':ANSWER_PROMPT},{'role':'user','content':'EVIDENCE:\n'+probe['context']+'\nQUESTION:\n'+probe['question']}],256,ANSWER_SCHEMA)
            r=dict(probe,answer=json.loads(text),call_key=digest(u['request']),context_tokens=tokens(probe['context']),prompt_tokens=u['response'].get('prompt_eval_count'),completion_tokens=u['response'].get('eval_count'),elapsed_s=u['elapsed_s'])
            with (dest/'answers.jsonl').open('a') as f:f.write(jdump(r)+'\n')
            print(probe['id'],text,flush=True)
    print('deterministic checks',len(checks),'passes',sum(x['pass'] for x in checks),'planned reader probes',len(probes))
if __name__=='__main__':main()
