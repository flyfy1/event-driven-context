import argparse, json, time, subprocess, platform, collections
from pathlib import Path
from core import Model,Store,render,tokens,clip,digest,jdump,ANSWER_PROMPT,ANSWER_SCHEMA,SUMMARY_PROMPT,TOKENIZER

ROOT=Path(__file__).resolve().parents[1]
def load(name):return json.loads((ROOT/'data'/name).read_text())
def batches(events,budget):
    # Preserve whole event order; exceptionally long individual turns split by token range.
    from core import chunks
    b=[]
    for e in chunks(events,limit=900):
        if b and tokens('\n'.join(render(x) for x in b+[e]))>budget:yield b;b=[]
        b.append(e)
    if b:yield b

def build(h,cutoffs,model,config,dest):
    store=Store();summary='';pos=0;result={};usage=[]
    for cutoff in sorted(set(cutoffs)):
        new=h['events'][pos:cutoff];pos=cutoff;t=time.monotonic()
        for e in new:
            store.append(e)
            if e['id'] in h.get('duplicate_ids',[]):store.append(e)
        append_s=time.monotonic()-t
        for n,b in enumerate(batches(new,config['build_batch_budget'])):
            prompt=SUMMARY_PROMPT.replace('380 cl100k',str(config['summary_budget'])+' cl100k')
            text,u=model.call('build-summary/'+h['id'],[{'role':'system','content':prompt+'\n'+h.get('policy','')},{'role':'user','content':'PRIOR SUMMARY:\n'+summary+'\nNEW EVENTS:\n'+'\n\n'.join(render(e) for e in b)}],max_tokens=config.get('summary_max_tokens',520))
            summary=clip(text,config['summary_budget']);usage.append({'key':digest(u['request']),'cutoff':cutoff,'prompt_tokens':u['response'].get('prompt_eval_count',0),'completion_tokens':u['response'].get('eval_count',0),'elapsed_s':u['elapsed_s'],'cache_hit':u['cache_hit'],'done_reason':u['response'].get('done_reason')})
        t=time.monotonic();cells=store.materialize();deterministic_s=append_s+time.monotonic()-t
        snap={'history':h['id'],'cutoff':cutoff,'events':list(store.events),'summary':summary,'cells':cells,'duplicate_arrivals':store.duplicates,'raw_hash':digest(store.events),'D_build_s':deterministic_s,'shared_append_s':append_s,'D_build_model_tokens':0,'summary_tokens':tokens(summary),'state_tokens':tokens(jdump(cells))}
        (dest/f'{h["id"]}-{cutoff}.json').write_text(jdump(snap));result[cutoff]=snap
        print('built',h['id'],cutoff,'summary_tokens',tokens(summary),flush=True)
    return result,usage

def norm(s):return ''.join(c for c in str(s).lower() if c.isalnum())
def f1(a,b):
    import re
    aa=collections.Counter(re.findall(r'\w+|[\u4e00-\u9fff]',str(a).lower()));bb=collections.Counter(re.findall(r'\w+|[\u4e00-\u9fff]',str(b).lower()))
    hit=sum((aa&bb).values());return 2*hit/max(1,sum(aa.values())+sum(bb.values()))
def score(q,answer,chosen,context,events):
    text=answer.get('answer','');status=answer.get('status');cited=set(answer.get('citations',[]));available={e['id'] for e in events}
    expected=set(q.get('evidence',[]));selected={e['id'] for e in chosen}
    if not chosen:selected={eid for eid in available if eid in context}
    forbidden=any(norm(x) in norm(text) for x in q.get('forbidden',[]))
    match=any(norm(x) in norm(text) for x in q['answers'])
    if q['status']=='unknown':match=status=='unknown'
    if q['status']=='conflict':match=status=='conflict' and expected<=cited
    # Citation validity is not entailment. Public gold evidence at differing granularities stays separate.
    cited_in_context=cited&selected if chosen else set()
    return {'answer_match':int(match),'status_correct':int(status==q['status']),'forbidden_hit':int(forbidden),
            'strict_answer':int(match and status==q['status'] and not forbidden),
            'f1':max(f1(text,a) for a in q['answers']),
            'citation_id_exists':len(cited&available)/len(cited) if cited else None,
            'citation_source_visible':len(cited_in_context)/len(cited) if cited and chosen else None,
            'citation_precision_gold':len(cited&expected)/len(cited) if cited and expected else None,
            'citation_recall_gold':len(cited&expected)/len(expected) if expected else None,
            'retrieval_recall_gold':len(selected&expected)/len(expected) if expected else None,
            'retrieval_session_recall':sum(bool(set(g)&selected) for g in q.get('evidence_groups',[]))/len(q['evidence_groups']) if q.get('evidence_groups') else None,
            'citation_complete':int(expected<=cited) if expected else None,
            'unanswerable':q['status']=='unknown','conflict_question':q['status']=='conflict'}

def main():
    p=argparse.ArgumentParser();p.add_argument('--run',required=True);p.add_argument('--smoke',action='store_true');p.add_argument('--benchmarks',default='product,locomo,mab');p.add_argument('--budget',type=int);p.add_argument('--methods');p.add_argument('--histories');p.add_argument('--limit-questions',type=int);a=p.parse_args()
    conf=json.loads((ROOT/'configs/main.json').read_text())
    if a.budget:conf['context_budget']=a.budget
    if a.methods:conf['methods']=a.methods.split(',')
    dest=ROOT/'results'/a.run;dest.mkdir(exist_ok=True);(dest/'snapshots').mkdir(exist_ok=True)
    if (dest/'answers.jsonl').exists():raise RuntimeError('Use a new --run name. Model cache is automatically reused; results must not be appended twice.')
    model=Model(ROOT/'results/model_calls',conf['model'],conf['endpoint'],conf['seed'],max_calls=conf['max_model_calls'])
    hs=load('product_histories.json')+load('public_histories.json');qs=load('product_questions.json')+load('public_questions.json')
    qs=[q for q in qs if q['benchmark'] in a.benchmarks.split(',')]
    if a.histories:qs=[q for q in qs if q['history'] in a.histories.split(',')]
    if a.smoke:qs=[q for q in qs if q['id'] in ['product-0/budget_initial','product-0/budget_updated']]+[next(q for q in qs if q['benchmark']=='locomo'),next(q for q in qs if q['benchmark']=='mab')]
    if a.limit_questions:qs=qs[:a.limit_questions]
    (dest/'run.json').write_text(jdump({'config':conf,'args':vars(a),'question_ids':[q['id'] for q in qs],'history_hash':digest(hs),'question_hash':digest(qs),'tokenizer':TOKENIZER,'python':platform.python_version(),'platform':platform.platform(),'code_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),'started_at':time.time()}))
    records=[];build_usage=[]
    for h in hs:
        hqs=[q for q in qs if q['history']==h['id']]
        if not hqs:continue
        # Builder receives histories and numeric cutoff schedule ONLY, not hqs or gold.
        snaps,us=build(h,[q['cutoff'] for q in hqs],model,conf,dest/'snapshots');build_usage.extend(dict(u,history=h['id']) for u in us)
        (dest/'build_usage.json').write_text(jdump(build_usage))
        for q in hqs:
            snap=snaps[q['cutoff']];store=Store()
            for e in snap['events']:store.append(e)
            for method in conf['methods']:
                context,chosen,latency=store.retrieve(method,q['question'],conf['context_budget'],snap['summary'])
                assert tokens(context)<=conf['context_budget']
                assert all(e['sequence']<=q['cutoff'] and e['project']==q['history'] for e in chosen)
                if len(list((ROOT/'results/model_calls').glob('*.json')))>=conf['max_model_calls']:raise RuntimeError('local experiment call cap reached')
                system=ANSWER_PROMPT+'\n'+h.get('policy','')
                messages=[{'role':'system','content':system},{'role':'user','content':f"PROJECT={h['id']} CUTOFF={q['cutoff']} QUESTION_TIME={q.get('question_time','not specified')}\nEVIDENCE:\n{context}\nQUESTION:\n{q['question']}"}]
                text,u=model.call('answer/'+q['id']+'/'+method,messages,conf['answer_max_tokens'],ANSWER_SCHEMA)
                try:ans=json.loads(text)
                except ValueError:ans={'answer':text,'status':'invalid','citations':[]}
                rec={'id':q['id'],'history':q['history'],'benchmark':q['benchmark'],'category':q['category'],'method':method,'cutoff':q['cutoff'],'question':q['question'],'gold':q,'answer':ans,'context':context,'context_tokens':tokens(context),'selected_ids':list(dict.fromkeys(e['id'] for e in chosen)),'retrieval_s':latency,'answer_prompt_tokens':u['response'].get('prompt_eval_count',0),'answer_completion_tokens':u['response'].get('eval_count',0),'answer_s':u['elapsed_s'],'cache_hit':u['cache_hit'],'call_key':digest(u['request']),'done_reason':u['response'].get('done_reason'),'scores':score(q,ans,chosen,context,snap['events'])}
                records.append(rec)
                with (dest/'answers.jsonl').open('a') as f:f.write(jdump(rec)+'\n')
                print('answered',q['id'],method,rec['scores']['strict_answer'],flush=True)
    (dest/'completed.json').write_text(jdump({'finished_at':time.time(),'records':len(records),'build_calls':len(build_usage)}))
if __name__=='__main__':main()
