"""Run AFTER all model jobs finish. Fresh API requests, no application response cache.
Same exact reader request, two repeated measurements on two fixed questions.
"""
import json,time,urllib.request
from pathlib import Path
R=Path(__file__).resolve().parents[1]
rs=[json.loads(l) for l in (R/'results/main-v2/answers.jsonl').read_text().splitlines()]
selected=[r for r in rs if r['id'] in ('product-0/budget_updated','locomo-0/q24')]
out=[]
for rep in range(2):
    # Alternate order to reduce a simple order effect; model KV cache remains enabled.
    for r in selected[::1 if rep==0 else -1]:
        call=json.loads((R/'results/model_calls'/f"{r['call_key']}.json").read_text());req=call['request'];t=time.monotonic()
        raw=json.load(urllib.request.urlopen(urllib.request.Request('http://127.0.0.1:11439/api/chat',json.dumps(req).encode(),{'Content-Type':'application/json'}),timeout=240))
        out.append({'id':r['id'],'method':r['method'],'repetition':rep,'elapsed_s':time.monotonic()-t,'request':req,'response':raw,'note':'Fresh API request, application cache bypassed, no other lab jobs running. Server prefix cache enabled; small latency calibration only.'})
        (R/'results/latency_probe.json').write_text(json.dumps(out,ensure_ascii=False,indent=2))
        print(r['id'],r['method'],round(out[-1]['elapsed_s'],2),flush=True)
