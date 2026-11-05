#!/usr/bin/env python3
"""Credentialed read-only/sync acceptance against an explicitly selected workbench.
Writes customer-free evidence; never approves or sends an original-system action.
"""
import argparse,base64,json,pathlib,time,urllib.request,uuid
p=argparse.ArgumentParser();p.add_argument('--cli',required=True);p.add_argument('--mapping',required=True);p.add_argument('--report',required=True);p.add_argument('--sync',action='store_true');args=p.parse_args()
cfg=json.loads(pathlib.Path(args.cli).read_text());mapping=json.loads(pathlib.Path(args.mapping).read_text());auth='Basic '+base64.b64encode(('owner:'+cfg['password']).encode()).decode();base=cfg['url'].rstrip('/')+'/api/v1'
def api(path,body=None):
 req=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),headers={'Authorization':auth,'Content-Type':'application/json'})
 with urllib.request.urlopen(req,timeout=90) as r:return json.load(r)
def call(family,action,parameters,wait=180):
 item=next(x for x in mapping if x['capability']==family)
 v=api('/sessions/'+item['sessionId']+'/component-calls',{'pluginId':item['pluginId'],'action':action,'arguments':parameters,'operationKey':'acceptance:'+str(uuid.uuid4())})
 end=time.time()+wait
 while v['state'] not in ['succeeded','failed','cancelled'] and time.time()<end:
  time.sleep(2);v=api('/component-calls/'+v['id']+'/refresh',{})
 if v['state']!='succeeded':raise RuntimeError(action+' did not succeed: '+v.get('summary',''))
 return v
report={'checks':[],'sourceSendingEnabled':False}
try:
 for entry in mapping:
  c=api('/sessions/'+entry['sessionId']+'/component-config');assert c['expertId']==entry['expertId'];assert c['pluginId']==entry['pluginId']
 report['checks'].append('seven project/expert/session fixed bindings')
 v=call('用户上下文维护','configuration',{});m=v['result']['migration'];assert m['configurationCount']==85 and len(m['sourceTimers'])==13;assert m['agentMode']=='disabled'
 report['configurations']=m['configurationCount'];report['timersRecorded']=len(m['sourceTimers']);report['originalAgentMode']=m['agentMode'];report['checks'].append('configuration manifest and original disabled state')
 e=next(x for x in m['entries'] if x['path'].endswith('manifest.json'))
 v=call('用户上下文维护','read_configuration',{'name':e['path']});assert v['result']['sha256']==e['sha256'];report['checks'].append('original configuration bytes hash verified through actual HTTPS MCP')
 if args.sync:
  v=call('用户上下文维护','sync_contexts',{},300);report['publication']=v.get('result');report['checks'].append('real source stream cleaned and atomically published in MySQL')
 v=call('用户上下文维护','list_user_refs',{'limit':3});refs=v['result']['user_refs'];assert refs
 v=call('用户上下文维护','query_contexts',{'user_ref':refs[0]});assert [x['user_ref'] for x in v['result']['items']]==[refs[0]];report['checks'].append('published warehouse read through session plugin')
 for family,operation in [('经营分析与历史核查','v3_operation_evaluation_get'),('报告与内容素材','v3_customer_report_archive_get')]:
  call(family,'legacy_read',{'operation':operation,**({'parameters':{'safe_user_ref':refs[0]}} if family=='报告与内容素材' else {})});report['checks'].append(family+' source pure-read compatibility')
 # This verifies transport and original eligibility source, never customer delivery.
 call('关键用户推荐','recommend_users',{});report['checks'].append('original recommendation candidates and evidence readable')
 report['passed']=True
finally:
 path=pathlib.Path(args.report);path.parent.mkdir(parents=True,exist_ok=True);path.write_text(json.dumps(report,ensure_ascii=False,indent=2));path.chmod(0o600)
 print(json.dumps({k:v for k,v in report.items() if k!='publication'},ensure_ascii=False))
