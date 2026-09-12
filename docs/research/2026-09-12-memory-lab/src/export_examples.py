import json
from pathlib import Path
from core import Store,origin,jdump
R=Path(__file__).resolve().parents[1]
hs=json.loads((R/'data/product_histories.json').read_text());qs=json.loads((R/'data/product_questions.json').read_text());ex=json.loads((R/'data/product_expected.json').read_text())
lines=['# 产品专项场景与标准答案','', '全部输入均为新生成的合成数据；所有 UUID、人物与项目上下文只用于本次测试。完整机器可读输入、标准答案、期望状态分别在三个 JSON 文件中，构建器不读取 gold。','']
for h in hs:
    if h['id']!='product-0':continue
    lines+=['## 按时间排列的输入','', '| sequence | 原文与类型 | 来源/关系 |','|---|---|---|']
    for e in h['events']:
        lines.append(f"| {e['sequence']} | {e['type']}: {e['text']} | {e['id']}; {jdump(e['refs'])} |")
    lines+=['','## 各问题与期望','']
    for q in [x for x in qs if x['history']==h['id']]:
        lines += [f"### {q['id']}，截止 sequence {q['cutoff']}",f"问题：{q['question']}",f"标准：{q['status']}；可接受关键表达：{' / '.join(q['answers'])}",f"必须引用：{', '.join(q['evidence']) or '无，须明确说不知道'}",f"不应出现：{', '.join(q['forbidden']) or '超出证据的确定结论'}",'']
    lines+=['## 保存状态检查点','', 'active 为本检查点必须保留的条目集合，不是穷尽所有应保留条目。must_not_active 表示历史可查，但不得继续作为当前事实。','']
    for x in [x for x in ex if x['history']==h['id']]:lines.append('```json\n'+json.dumps(x,ensure_ascii=False,indent=2)+'\n```')
(R/'data/product_scenarios.md').write_text('\n\n'.join(lines))
h=hs[0];events=h['events'];s=Store();versions=[];evolution=['# 同一项目的 State 演变','', '每个版本只处理该时刻已录入的 Event，历史源记录不变。例子将当前 note、转录和少量未解决候选渲染为一份 State；非当前证据仍在索引/Event 中。','']
cutoffs={4,19,21,22,26,27,28,30,53,54}
for e in events:
    s.append(e)
    if e['sequence'] not in cutoffs:continue
    cells=[c for c in s.materialize() if c['status'] in ('active','conflict') and c['type']!='log' and c['metadata'].get('kind')!='audio']
    items=[]
    for c in cells:
        items.append({'id':c['id']+'/0','kind':c['metadata'].get('kind','fact'),'subject_id':h['id'],'topic':c['metadata'].get('topic'),'text':c['text'],'asserted_by':{'speaker_label':c.get('speaker'),'identity_basis':'synthetic_source_declaration','authenticated_actor_id':None},'lifecycle':c['status'],'epistemic_basis':origin(c),'confirmation':'unconfirmed' if origin(c)!='user_assertion' else 'asserted','valid_from':None,'valid_to':None,'sources':[{'event_id':c['id'],'span':{'unit':'utf8-byte','start':0,'end':len(c['text'].encode())}}],'relations':c['refs']})
    source_ids=[c['id'] for c in cells];assert len(source_ids)<=32
    text='\n'.join(f"- ({x['kind']}; {x['lifecycle']}; {x['epistemic_basis']}; 发言者={x['asserted_by']['speaker_label']}) {x['text']}〔{x['sources'][0]['event_id']}〕" for x in items)
    st={'key':'project-brief/current','version':len(versions)+1,'expected_version':len(versions),'based_on_sequence':e['sequence'],'content':{'format':'markdown','text':text},'data':{'schema_version':1,'items':items,'coverage':{'pending_audio':[],'truncated':False},'extractor':{'version':'lab-extractive-v1'}},'refs':source_ids}
    versions.append(st);evolution += [f"## Version {st['version']} — sequence {e['sequence']}",text,'']
(R/'examples/states.json').write_text(json.dumps(versions,ensure_ascii=False,indent=2));(R/'examples/events.json').write_text(json.dumps(events,ensure_ascii=False,indent=2));(R/'examples/evolution.md').write_text('\n\n'.join(evolution))
edc_events=[{'id':e['id'],'type':e['type'],'content':{'kind':'text','text':e['text']},'metadata':dict(e['metadata'],speaker_label=e.get('speaker'),speaker_identity_basis='synthetic_source_declaration'),'source':e['source'],'refs':e['refs'],'occurred_at':e.get('occurred_at',e['time'])} for e in events]
(R/'examples/edc-events.json').write_text(json.dumps({'events':edc_events},ensure_ascii=False,indent=2))
put={k:v for k,v in versions[-1].items() if k!='version'};put['as_plugin_id']='project-brief'
(R/'examples/edc-state-put.json').write_text(json.dumps(put,ensure_ascii=False,indent=2))
print('exported scenarios and',len(versions),'State examples')
