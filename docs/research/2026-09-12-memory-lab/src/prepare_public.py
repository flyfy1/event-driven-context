"""Adapters only ingest raw histories; gold lives in separate output files.
Selection uses seeded category-stratified IDs, never answers/evidence availability.
No oracle histories, author summaries or retrieval labels enter stores.
"""
import json, random, re
from pathlib import Path
import pyarrow.parquet as pq
from core import digest,tokens

ROOT=Path(__file__).resolve().parents[1]
def event(h,idx,text,time,speaker,session,role=None):
    return {'id':f'e{idx:05d}','sequence':idx,'project':h,'time':time,'text':text,'type':'log','speaker':speaker,'session':session,'source':{'channel':'hook','message_role':role or 'user'},'metadata':{},'refs':[]}
def main():
    rng=random.Random(912);hs=[];qs=[];manifest=[]
    rows=json.loads((ROOT/'data/upstream/locomo10.json').read_text())
    # Two complete conversations; multimodal/open-domain category 3 omitted explicitly.
    for ci in [0,1]:
        r=rows[ci];hid=f'locomo-{ci}';es=[];mapping={}
        c=r['conversation'];ns=sorted(int(k[8:]) for k in c if re.fullmatch(r'session_\d+',k))
        for n in ns:
            for turn in c[f'session_{n}']:
                e=event(hid,len(es)+1,turn['text'],c[f'session_{n}_date_time'],turn['speaker'],f's{n}')
                # Caption is supplied public text; no remote image read or downloaded.
                if turn.get('blip_caption'):e['text']+=' [Public image caption: '+turn['blip_caption']+']'
                es.append(e);mapping[turn['dia_id']]=e['id']
        hs.append({'id':hid,'benchmark':'locomo','events':es,'duplicate_ids':[]})
        selected=[]
        for cat in [1,2,4,5]:
            ids=[i for i,q in enumerate(r['qa']) if q['category']==cat];rng.shuffle(ids);selected+=ids[:2]
        for i in sorted(selected):
            q=r['qa'][i];ev=[mapping[x] for x in q.get('evidence',[]) if x in mapping]
            qs.append({'id':hid+f'/q{i}','history':hid,'cutoff':len(es),'question':q['question'],'answers':[str(q.get('answer','I don\'t know'))],'evidence':ev,'status':'unknown' if q['category']==5 else 'known','forbidden':[],'benchmark':'locomo','category':str(q['category']),'group':hid,'upstream_question_index':i})
        manifest.append({'history':hid,'upstream_sample_id':r['sample_id'],'questions':sorted(selected),'events':len(es),'raw_tokens':sum(tokens(e['text']) for e in es)})
    rows=pq.read_table(ROOT/'data/upstream/mab-conflict.parquet').to_pylist()
    # 6k and 32k contexts shared by single-hop and multi-hop; build each only once.
    for ci in [0,1]:
        r=rows[ci];hid=f'mab-{ci}';es=[]
        for line in r['context'].splitlines():
            if line.strip():es.append(event(hid,len(es)+1,line,'ordered fact '+str(len(es)+1),'benchmark',None))
        assert r['context']==rows[ci+4]['context'], 'shared-context assumption changed upstream'
        policy='This is a fictional ordered-fact benchmark. For the same subject and relation, the statement with the HIGHEST ORIGINAL FACT NUMBER replaces lower-numbered statements. Evidence may be shown in retrieval relevance order, NOT original chronological order. Use the numbered fact in the original text to resolve updates. Do not use real-world knowledge. This update policy applies only to this benchmark.'
        hs.append({'id':hid,'benchmark':'mab','events':es,'duplicate_ids':[],'policy':policy})
        selected=rng.sample(range(100),6)
        for subidx in [ci,ci+4]:
            r=rows[subidx]
            for i in selected:
                qs.append({'id':hid+f'/{subidx}/q{i}','history':hid,'cutoff':len(es),'question':r['questions'][i],'answers':[str(x) for x in r['answers'][i]],'evidence':[],'status':'known','forbidden':[],'benchmark':'mab','category':'multi-hop' if subidx<4 else 'single-hop','group':hid,'upstream_question_index':i})
        manifest.append({'history':hid,'source_rows':[ci,ci+4],'questions':selected,'events':len(es),'raw_tokens':sum(tokens(e['text']) for e in es),'warning':'No official evidence IDs. Citation entailment requires manual audit.'})
    # Two scale probes: one knowledge update, one explicit abstention; full S histories.
    rows=json.loads((ROOT/'data/upstream/longmemeval_s_cleaned.json').read_text())
    selections=[]
    for typ in ['knowledge-update','abstention']:
        candidates=[i for i,r in enumerate(rows) if (r['question_id'].endswith('_abs') if typ=='abstention' else r['question_type']==typ and not r['question_id'].endswith('_abs'))]
        selections.append(rng.choice(candidates))
    for ri in selections:
        r=rows[ri];hid=f'longmemeval-{ri}';es=[];smap={}
        # Actual dates, NOT session IDs, define the ingestion order.
        sessions=sorted(zip(r['haystack_dates'],r['haystack_session_ids'],r['haystack_sessions']),key=lambda x:x[0])
        assert all(dt<=r['question_date'] for dt,_,_ in sessions), 'history contains events after question time'
        for sn,(dt,sid,turns) in enumerate(sessions):
            smap[sid]=[]
            for t in turns:
                e=event(hid,len(es)+1,t['content'],dt,t['role'],f's{sn}',t['role']);es.append(e);smap[sid].append(e['id'])
        # No 'answer_' / ShareGPT tags are exposed to any method.
        hs.append({'id':hid,'benchmark':'longmemeval','events':es,'duplicate_ids':[]})
        abstain=r['question_id'].endswith('_abs')
        qs.append({'id':hid+'/'+r['question_id'],'history':hid,'cutoff':len(es),'question':r['question'],'answers':[str(r['answer'])],'evidence':[],'evidence_groups':[smap[s] for s in r['answer_session_ids'] if s in smap] if not abstain else [],'status':'unknown' if abstain else 'known','forbidden':[],'benchmark':'longmemeval','category':'abstention' if abstain else r['question_type'],'group':hid,'question_time':r['question_date']})
        manifest.append({'history':hid,'upstream_row':ri,'question_id':r['question_id'],'events':len(es),'raw_tokens':sum(tokens(e['text']) for e in es),'question_date':r['question_date']})
    for name,data in [('public_histories.json',hs),('public_questions.json',qs),('public_selection.json',manifest)]:
        (ROOT/'data'/name).write_text(json.dumps(data,ensure_ascii=False,indent=2))
    print(json.dumps(manifest,indent=2))
if __name__=='__main__':main()
