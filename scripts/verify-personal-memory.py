"""Isolated personal-memory acceptance (3095 only); no production inputs."""
import json,urllib.request,urllib.error,pathlib,base64,time,uuid
cfg=json.loads((pathlib.Path.home()/'.config/project-workbench/cli.json').read_text())
auth='Basic '+base64.b64encode(('owner:'+cfg['password']).encode()).decode()
base='http://127.0.0.1:3095/api/v1'
report={'runs':[],'checks':[],'timings':[]}
def api(path,obj=None,method=None):
 req=urllib.request.Request(base+path,data=json.dumps(obj,ensure_ascii=False).encode() if obj is not None else None,method=method or ('POST' if obj is not None else 'GET'),headers={'Authorization':auth,'Content-Type':'application/json'})
 with urllib.request.urlopen(req,timeout=25) as r:return json.load(r)
def wait(fn,seconds=300):
 end=time.time()+seconds
 while time.time()<end:
  v=fn()
  if v:return v
  time.sleep(1)
 raise RuntimeError('acceptance timed out')
def ask(text):
 sid=api('/today')['sessionId'];ev=str(uuid.uuid4());start=time.monotonic()
 api('/messages',{'source':'web','eventId':ev,'sessionId':sid,'text':text})
 r=wait(lambda:next((r for r in api('/today')['runs'] if r.get('eventId')==ev and r['state'] in ['completed','failed','cancelled','interrupted']),None))
 assert r['state']=='completed',r.get('error')
 report['runs'].append({k:r.get(k) for k in ['id','sessionId','result','state']});report['timings'].append({'kind':'chat','seconds':round(time.monotonic()-start,2)})
 print(json.dumps(report['runs'][-1],ensure_ascii=False),flush=True);return r

def consolidate():
 start=time.monotonic();api('/personal/understanding/refresh',{})
 def done():
  p=api('/personal/understanding')
  if p['state']=='failed':raise RuntimeError(p.get('error'))
  return p if p['state']=='ready' else None
 p=wait(done,360);assert len(p['text'])<=2400
 report['timings'].append({'kind':'consolidation','seconds':round(time.monotonic()-start,2),'version':p['version']})
 print(json.dumps({'profile':p['text'],'version':p['version']},ensure_ascii=False),flush=True)
 return p
try:
 api('/today')
 token='MEM-'+uuid.uuid4().hex[:6]
 m=api('/personal/memories',{'kind':'fact','text':f'隔离验收的明确约定：11月18日16:20在松林会议室核对交付，核对码{token}。这是精确事实，不代表长期偏好。'})
 r=ask('这是隔离验收。请只在当前原生对话历史记住连续性标识「银杏629」，不要使用记忆工具；回复收到即可。')
 p=consolidate()
 assert '银杏629' not in p['text'], 'conversation-only opt-out leaked into profile'
 assert '11月18日' in json.dumps(api('/personal/understanding/evidence?query='+urllib.parse.quote('核对码')),ensure_ascii=False)
 r2=ask('请使用 personal_memories 按“核对码”查准确原话，告诉我约定时间、地点与核对码。然后仅根据原生历史回答连续性标识。')
 assert token in r2['result'] and '银杏629' in r2['result']
 assert r2['sessionId']==r['sessionId'];report['checks'].append('bounded profile, exact tool recall and native session continuity')
 m=api('/personal/memories',{**m,'text':f'本人明确纠正：验收约定改为11月19日09:40，地点仍是松林会议室，核对码仍是{token}。'})
 r3=ask('不要调用任何工具。只按当前有效个人理解告诉我验收约定的最新日期和时间；再从原生历史回答连续性标识。')
 assert '19' in r3['result'] and ('09:40' in r3['result'] or '9:40' in r3['result']) and '银杏629' in r3['result']
 assert r3['sessionId']==r['sessionId'];report['checks'].append('named current context changed without native reset')
 ask('这次宣传方案我选B。仅记录这次决定，不要推导长期偏好。简单回应即可，不调用记忆工具。')
 ask('上个月青禾访谈需求不清楚时，我先问了实际使用者，才决定要不要开发。这是我的一次真实经历。只回应收到，不调用记忆工具。')
 ask('本周另一件松林试点，需求同样不清楚，我也先找实际使用者核对。这是另一次经历。只回应收到，不调用记忆工具。')
 p=consolidate();report['beforeCounterexample']=p
 for v in p['paragraphs']:
  assert not ('偏好B' in v['text'] or '偏爱B' in v['text'])
  if '倾向' in v['text']:assert v['status'] in ['observation','unverified']
 report['checks'].append('single choice remains local; generalized tendency stays tentative')
 ask('补充一个反例：最近的白石原型需求也不清楚，但没办法约到用户，我直接做了小原型试验。因此前两次先访谈不能代表我所有情况都先访谈，重要的是按约束验证。只回应收到，不调用记忆工具。')
 p=consolidate();report['afterCounterexample']=p
 report['checks'].append('counterexample profile captured for semantic review')
 api('/personal/memories/'+m['id']+'?version='+str(m['version']),method='DELETE')
 assert token not in api('/personal/understanding')['text']
 assert not api('/personal/understanding/evidence?query='+urllib.parse.quote(token))['items']
 p=consolidate();assert token not in p['text'];report['checks'].append('explicit forget excluded evidence and derived profile during later consolidation')
 report['passed']=True
finally:
 pathlib.Path('artifacts/memory').mkdir(parents=True,exist_ok=True)
 pathlib.Path('artifacts/memory/real-model.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))
 print(json.dumps({'passed':report.get('passed',False),'checks':report['checks']},ensure_ascii=False),flush=True)
