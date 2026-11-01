import json,urllib.request,urllib.error,pathlib,base64,time,uuid
cfg=json.load(open(pathlib.Path.home()/'.config/project-workbench/cli.json'))
auth='Basic '+base64.b64encode(('owner:'+cfg['password']).encode()).decode()
url='http://127.0.0.1:3095/api/v1'
def api(path,body=None,method=None):
 q=urllib.request.Request(url+path,data=json.dumps(body,ensure_ascii=False).encode() if body is not None else None,headers={'Authorization':auth,'Content-Type':'application/json'},method=method or ('POST' if body is not None else 'GET'))
 try:
  with urllib.request.urlopen(q,timeout=30) as r:return json.load(r)
 except urllib.error.HTTPError as e:raise RuntimeError(str(e.code)+' '+e.read().decode())
def wait(f,seconds=300):
 end=time.time()+seconds
 while time.time()<end:
  v=f()
  if v:return v
  time.sleep(1)
 raise RuntimeError('acceptance timed out')
report={'runs':[],'checks':[]}
def run(text):
 today=api('/today');sid=today['sessionId'];event=str(uuid.uuid4());api('/messages',{'source':'web','eventId':event,'sessionId':sid,'text':text})
 def done():
  runs=api('/today')['runs'];r=next((r for r in runs if r.get('eventId')==event),None)
  return r if r and r['state'] in ['completed','failed','cancelled','interrupted'] else None
 r=wait(done);report['runs'].append({'id':r['id'],'state':r['state'],'result':r.get('result'),'error':r.get('error'),'queueMs':r.get('startedAt',0)-r['createdAt'],'executionMs':r.get('finishedAt',0)-r.get('startedAt',0)})
 assert r['state']=='completed',r.get('error');return r
try:
 r=run('隔离验收：请记住本次连续性标识 藤蔓628，不使用工具，只回复已记住。')
 r=run('只根据这个对话上文，原样回复刚才的连续性标识，不调用工具。');assert '藤蔓628' in r['result'];report['checks'].append('real native Harness history continuity')
 r=run('请只读查询青山表达实验的原始资料.md，原样引用最后一条关键反对意见并列出来源。');assert '不要为了快速上线而跳过需求验证' in r['result'] and '原始资料.md' in r['result'];report['checks'].append('personal reads scoped project source')
 api('/today/refresh',{})
 daily=wait(lambda:(lambda d:d if d.get('updatedAt',0)>int(time.time()*1000)-180000 and d.get('state')=='ready' and d.get('version',0)>0 else None)(api('/today')['daily']),420)
 report['daily']=daily;report['contexts']=api('/today')['contexts'];assert all(c['state']=='ready' for c in report['contexts']);report['checks'].append('real project summary and daily generation')
 report['passed']=True
finally:
 pathlib.Path('artifacts/today/model-report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))
 print(json.dumps({'passed':report.get('passed',False),'checks':report['checks'],'runs':[{k:v for k,v in r.items() if k not in ['result']} for r in report['runs']]},ensure_ascii=False))
