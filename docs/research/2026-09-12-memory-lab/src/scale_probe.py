"""LongMemEval-S full-history scale probe; A/C/D/E only, no summary baseline.
Separate from main four-condition comparison. No oracle truncation of input history.
"""
import json,time
from pathlib import Path
from core import Store,Model,tokens,digest,jdump,ANSWER_PROMPT,ANSWER_SCHEMA
from run import score
R=Path(__file__).resolve().parents[1]
def main():
    hs=json.loads((R/'data/public_histories.json').read_text());qs=json.loads((R/'data/public_questions.json').read_text());m=Model(R/'results/model_calls')
    dest=R/'results/scale';dest.mkdir(exist_ok=True)
    for h in hs:
        if h['benchmark']!='longmemeval':continue
        t=time.monotonic();s=Store()
        for e in h['events']:s.append(e)
        build_s=time.monotonic()-t;q=next(q for q in qs if q['history']==h['id'])
        for method in ['A','C','D','E']:
            c,chosen,lat=s.retrieve(method,q['question'],1536)
            text,u=m.call('scale/'+q['id']+'/'+method,[{'role':'system','content':ANSWER_PROMPT},{'role':'user','content':f"PROJECT={h['id']} CUTOFF={q['cutoff']} QUESTION_TIME={q['question_time']}\nEVIDENCE:\n{c}\nQUESTION:\n{q['question']}"}],256,ANSWER_SCHEMA)
            answer=json.loads(text);r={'id':q['id'],'method':method,'history':h['id'],'gold':q,'answer':answer,'context':c,'context_tokens':tokens(c),'scores':score(q,answer,chosen,c,h['events']),'input_history_tokens':sum(tokens(e['text']) for e in h['events']),'build_s':build_s,'retrieval_s':lat,'answer_s':u['elapsed_s'],'prompt_tokens':u['response'].get('prompt_eval_count'),'completion_tokens':u['response'].get('eval_count'),'call_key':digest(u['request']),'done_reason':u['response'].get('done_reason')}
            with (dest/'answers.jsonl').open('a') as f:f.write(jdump(r)+'\n')
            print(q['id'],method,text,flush=True)
    (dest/'README.md').write_text('Full S histories, 2 questions, A/C/D/E only. B omitted to bound repeated-model construction cost; not part of the 4-method main comparison. Not official judge scores.\n')
if __name__=='__main__':main()
