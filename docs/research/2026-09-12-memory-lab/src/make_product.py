"""Synthetic only. Write histories separately from gold to enforce blind construction."""
import json,random,uuid
from pathlib import Path
from datetime import datetime,timedelta,timezone

ROOT=Path(__file__).resolve().parents[1]
def main():
    histories=[];questions=[];expect=[]
    for variant in range(3):
        rng=random.Random(912+variant);project=f'product-{variant}';events=[];refs={};day=datetime(2026,1,1,tzinfo=timezone.utc)
        a,b=[('林','陈'),('周','赵'),('江','孙')][variant];old,new=[(50000,30000),(82000,47000),(64000,29000)][variant]
        def add(key,text,kind='fact',topic='',rel=None,channel='app',etype='note',speaker=None,ephemeral=False,occurred=None):
            idx=len(events)+1;eid=str(uuid.uuid5(uuid.NAMESPACE_URL,project+'/'+key));refs[key]=eid
            e={'id':eid,'sequence':idx,'project':project,'time':(day+timedelta(hours=idx)).isoformat(),'text':text,'type':etype,'speaker':speaker or a,'metadata':{'kind':kind,'topic':topic},'source':{'channel':channel},'refs':[{'rel':r,'id':refs[k]} for r,k in (rel or [])]}
            if ephemeral:e['metadata']['ephemeral']=True
            if occurred:e['occurred_at']=occurred
            events.append(e);return eid
        def noise(n):
            for _ in range(n):add(f'noise-{len(events)}',rng.choice(['今天咖啡很香，午后去散步。','测试日志：临时缓存被清理，天气晴朗。','闲聊：最近在看一部科幻电影。','午餐点了面条，周末准备读小说。']),kind='chat',etype='log',channel='hook',ephemeral=True)
        def q(key,text,answers,ev,status='known',forbidden=None,category='',active=None):
            questions.append({'id':f'{project}/{key}','history':project,'cutoff':len(events),'question':text,'answers':answers,'evidence':[refs[k] for k in ev],'status':status,'forbidden':forbidden or [],'category':category or key,'benchmark':'product','group':project})
            if active is not None:expect.append({'history':project,'cutoff':len(events),'active':[refs[k] for k in active],'must_not_active':[r['id'] for e in events for r in e['refs'] if r['rel'] in ('supersedes','retracts','resolves') and e['source']['channel']!='skill'],'must_not_promote':[e['id'] for e in events if e['source']['channel']=='skill'],'scope':'selected assertions, not exhaustive semantic recall'})
        add('goal','海桥项目目标：在三月底前验证离线录音转文字闭环。','goal','goal')
        add('budget0',f'海桥项目预算确定为 {old} 元。','decision','budget')
        add('constraint','海桥项目约束：所有原始录音只保存在本地设备，不上传第三方。','constraint','privacy')
        add('preference','海桥项目偏好：评审材料用中文，术语可保留英文。','preference','language')
        q('budget_initial','海桥项目当前预算多少元？',[str(old)],['budget0'],active=['goal','budget0','constraint','preference'],category='budget')
        noise(14)
        add('budget1',f'海桥项目预算改为 {new} 元，取代此前预算。','decision','budget',[('supersedes','budget0')])
        q('budget_updated','海桥项目当前预算多少元？只报当前数额。',[str(new)],['budget1'],forbidden=[str(old)],active=['goal','budget1','constraint','preference'],category='budget')
        add('todo0','待办：由我在周五前提交海桥录音原型。','todo','task-prototype')
        q('todo_open','海桥录音原型待办是什么状态？',['待办','未完成','待提交','未提交'],['todo0'],category='todo')
        add('todo1','海桥录音原型已提交，这项待办完成。','progress','task-prototype',[('resolves','todo0')])
        q('todo_done','海桥录音原型待办是什么状态？',['完成','已提交'],['todo1'],category='todo',active=['todo1'])
        add('todo2','录音原型验收失败，重新打开提交录音原型待办。','todo','task-prototype',[('supersedes','todo1')])
        q('todo_reopen','海桥录音原型待办是什么状态？',['重新打开','未完成','重新提交'],['todo2'],forbidden=['已经完成'],category='todo',active=['todo2'])
        add('taskcancel0','待办：安排海桥印刷宣传册。','todo','task-brochure')
        add('taskcancel1','取消海桥印刷宣传册待办，不再需要宣传册。','todo','task-brochure',[('retracts','taskcancel0')])
        q('todo_cancel','还需要印刷海桥宣传册吗？',['取消','不再需要','不需要'],['taskcancel1'],category='todo',active=['taskcancel1'])
        add('datea','海桥上线日期定为 4 月 10 日。','decision','release',speaker=a)
        add('dateb','海桥上线日期定为 4 月 20 日。','decision','release',speaker=b)
        q('member_conflict','海桥上线日期到底是哪天？',['4 月 10','4月10','4 月 20','4月20'],['datea','dateb'],status='conflict',category='conflict',active=['datea','dateb'])
        add('inference','我推测用户愿意为海桥购买付费云端转录服务，但尚未询问用户。','decision','cloud',channel='skill',speaker='agent')
        q('agent_unconfirmed','用户已经同意为海桥购买付费云转录服务了吗？',['未','没有','不知道','尚未'],['inference'],status='unknown',forbidden=['已经同意'],category='inference',active=['inference'])
        add('duplicate','海桥目标设备数量为 12 台。','fact','devices')
        # Duplicate transport arrivals are separately represented, not future events.
        q('duplicate_once','海桥目标设备数量是多少台？',['12'],['duplicate'],category='duplicate',active=['duplicate'])
        add('audio','[合成音频占位：海桥交付负责人，原始字节不在此文本实验中。]','audio','owner',etype='note')
        add('transcript','海桥交付负责人是 [小林/小宁，听不清]。','transcript','owner',[('derived_from','audio')],channel='plugin',etype='derived',speaker='audio-transcribe')
        q('audio_unclear','海桥交付负责人是谁？',['听不清','不确定','不知道','无法确定'],['transcript'],status='unknown',category='audio')
        add('audio_fix','我澄清录音：海桥交付负责人是小宁。','fact','owner',[('supersedes','transcript')])
        q('audio_corrected','海桥交付负责人是谁？',['小宁'],['audio_fix'],forbidden=['小林'],category='audio',active=['audio_fix'])
        noise(20)
        add('gap','海桥尚未选择具体转录引擎，准确率也没有测试数据。','question','engine')
        q('unknown','海桥当前选定的转录引擎实测准确率是多少？',['不知道','未','没有','无法'],['gap'],status='unknown',category='unknown')
        q('old_goal','海桥项目最初要完成的核心目标是什么？',['离线录音','录音转文字'],['goal'],category='retention',active=['goal','constraint','preference','budget1','todo2','audio_fix','gap'])
        q('irrelevant_constraint','请安排下一步技术验证；关于原始录音存放，必须遵守什么约束？',['本地','不上传'],['constraint'],category='constraint')
        q('preference','海桥评审材料应该用什么语言？',['中文'],['preference'],category='preference')
        add('budget_stale',f'再次修改海桥预算为 {new-5000} 元。','decision','budget',[('supersedes','budget1')])
        q('lag','海桥最新预算是多少元？',[str(new-5000)],['budget_stale'],forbidden=[str(new)],category='lag',active=['budget_stale'])
        q('historical','海桥最初确定的预算是多少元？',[str(old)],['budget0'],category='historical')
        add('late','补记：上周海桥在办公室讨论过离线测试，发生时间早于今天。','progress','test',occurred='2025-12-26T09:00:00Z')
        q('late_arrival','今天新增的补记描述了什么时候的讨论？',['上周','2025-12-26'],['late'],category='time')
        add('counterproposal','作为 agent，我建议将预算升到 99000 元；用户尚未确认。','decision','budget',[('supersedes','budget_stale')],channel='skill',speaker='agent')
        q('untrusted_update','用户确认的海桥最新预算是多少元？',[str(new-5000)],['budget_stale'],forbidden=['99000'],category='inference')
        add('openquestion','海桥开放问题：离线词表纠错是否需要先标注十段录音？尚未决定。','question','vocabulary')
        q('openquestion','海桥关于词表纠错有哪些尚未解决的问题？',['十段','标注'],['openquestion'],category='question')
        histories.append({'id':project,'benchmark':'product','events':events,'duplicate_ids':[refs['duplicate']],'description':'合成项目时间线；模板变体不是独立真实项目样本。'})
        # Near-name project, same people; must never enter project-scoped memory.
        histories.append({'id':project+'-other','benchmark':'product','events':[{'id':str(uuid.uuid5(uuid.NAMESPACE_URL,project+'/other')),'sequence':1,'project':project+'-other','time':day.isoformat(),'text':f'海桥二期项目预算为 99000 元，负责人是小林。','type':'note','speaker':a,'metadata':{'kind':'decision','topic':'budget'},'source':{'channel':'app'},'refs':[]}],'duplicate_ids':[]})
        questions.append({'id':project+'/isolation','history':project,'cutoff':len(events),'question':'海桥（不是海桥二期）交付负责人是谁？','answers':['小宁'],'evidence':[refs['audio_fix']],'status':'known','forbidden':['小林'],'category':'isolation','benchmark':'product','group':project})
    for name,data in [('product_histories.json',histories),('product_questions.json',questions),('product_expected.json',expect)]:
        (ROOT/'data'/name).write_text(json.dumps(data,ensure_ascii=False,indent=2))
    print(f'{len(histories)} histories, {len(questions)} questions, {len(expect)} state checkpoints')
if __name__=='__main__':main()
