#!/usr/bin/env python3
"""Real expert acceptance requires successful component records, not reply text."""
import argparse,json,pathlib,urllib.request,base64,uuid,time
p=argparse.ArgumentParser();p.add_argument('--cli',required=True);p.add_argument('--mapping',required=True);p.add_argument('--report',required=True);a=p.parse_args();out=pathlib.Path(a.report)
c=json.loads(pathlib.Path(a.cli).read_text());m=json.loads(pathlib.Path(a.mapping).read_text());project=next(x for x in m if x['capability']=='关键用户推荐');auth='Basic '+base64.b64encode(('owner:'+c['password']).encode()).decode()
def api(path,body=None):
 q=urllib.request.Request(c['url'].rstrip('/')+'/api/v1'+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':auth,'Content-Type':'application/json'})
 with urllib.request.urlopen(q,timeout=45) as r:return json.load(r)
report=json.loads(out.read_text()) if out.exists() else {'operationKey':'expert-acceptance:'+str(uuid.uuid4())}
out.parent.mkdir(parents=True,exist_ok=True);out.write_text(json.dumps(report));out.chmod(0o600)
v=api('/sessions/'+project['sessionId']+'/component-config/run',{'operationKey':report['operationKey']});rid=v['runId'];report['runId']=rid;out.write_text(json.dumps(report));
for _ in range(120):
 r=api('/runs/'+rid)
 if r['state'] in ['completed','failed','cancelled','interrupted']:break
 time.sleep(2)
assert r['state']=='completed',r.get('error')
calls=[x for x in api('/sessions/'+project['sessionId']+'/component-calls') if x.get('operationKey','').startswith(rid+':')]
assert any(x['action']=='recommend_users' and x['state']=='succeeded' for x in calls),'expert finished without actual component execution'
report.update(passed=True,seconds=round((r['finishedAt']-r['startedAt'])/1000,2),actualComponents=[{'action':x['action'],'state':x['state']} for x in calls],fixedExpertVersion=r['expertDigest'],mode=r['componentConfig']['mode'],noCustomerSending=True)
out.write_text(json.dumps(report,ensure_ascii=False,indent=2));print(json.dumps(report,ensure_ascii=False))
