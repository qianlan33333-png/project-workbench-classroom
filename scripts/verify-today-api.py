import json,urllib.request,urllib.error,base64,time,pathlib,uuid,os
cfg=json.load(open(pathlib.Path.home()/'.config/project-workbench/cli.json'))
url=os.environ.get('PW_TODAY_URL','http://127.0.0.1:3095')
auth='Basic '+base64.b64encode(('owner:'+cfg['password']).encode()).decode()
def api(method,path,body=None):
 q=urllib.request.Request(url+'/api/v1'+path,data=json.dumps(body,ensure_ascii=False).encode() if body is not None else None,method=method,headers={'Authorization':auth,'Content-Type':'application/json'})
 try:
  with urllib.request.urlopen(q,timeout=30) as r:return json.load(r)
 except urllib.error.HTTPError as e:raise RuntimeError(str(e.code)+' '+e.read().decode())
report={'checks':[],'projects':[]}
for name,desc in [('青山表达实验','这是一个练习表达的项目。目标是每周完成一次真实演讲，不追求流量。已确认时间约束：每次30分钟。'),('青海用户验证','这是一个验证CRM使用价值的项目。目标是访谈真实用户，不扩展新功能。')]:
 p=api('POST','/projects',{'name':name,'description':desc});report['projects'].append(p)
 api('PUT',f'/projects/{p["id"]}/file',{'path':'原始资料.md','content':desc+'\n关键反对意见：不要为了快速上线而跳过需求验证。','baseHash':''})
 t=api('POST',f'/projects/{p["id"]}/tasks',{'title':'完成一次小范围验证','description':'## 为什么做\n先获得真实反馈，再决定是否扩大投入。\n## 预期结果\n记录一个可验证的结论。','operation':str(uuid.uuid4())})
 d=api('POST',f'/tasks/{t["id"]}/prepare',{});assert d['session']['draft'];assert d['task']['state']=='pending'
 api('PUT',f'/tasks/{t["id"]}/draft',{'text':'保留我的手动草稿','version':d['task']['draftVersion']})
 assert api('POST',f'/tasks/{t["id"]}/prepare',{})['text']=='保留我的手动草稿'
 report['checks'].append('task prepare preserves edited draft without running')
 sc=api('POST','/schedules',{'projectId':p['id'],'name':'验收整理','instruction':'整理说明与实质动态','cron':'0 7 * * *','enabled':False,'condition':'changed','output':'project_context'})
 check=api('POST',f'/schedules/{sc["id"]}/run',{'operation':str(uuid.uuid4())})
 assert check['outcome']=='queued' and check['runId']
 deadline=time.monotonic()+360
 while time.monotonic()<deadline:
  run=next(r for r in api('GET',f'/schedules/{sc["id"]}/runs') if r['id']==check['runId'])
  if run['state'] in ('completed','failed','cancelled','interrupted'):break
  time.sleep(.5)
 assert run['state']=='completed',run.get('error')
report['today']=api('GET','/today')
pathlib.Path('.var/today-fixtures.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))
print(json.dumps({'projects':[p['name'] for p in report['projects']],'sessionId':report['today']['sessionId'],'checks':report['checks']},ensure_ascii=False))
