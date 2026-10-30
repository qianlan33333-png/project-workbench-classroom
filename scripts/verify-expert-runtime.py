#!/usr/bin/env python3
"""Exercise pinned expert versions on a live service using disposable projects."""
import argparse, base64, json, time, uuid, urllib.request
from pathlib import Path
p=argparse.ArgumentParser()
p.add_argument('--config',default=str(Path.home()/'.config/project-workbench/cli.json'))
p.add_argument('--report',default='artifacts/expert-runtime-acceptance.json')
a=p.parse_args()
cfg=json.loads(Path(a.config).read_text())
base=cfg['url'].rstrip('/')+'/api/v1'
auth='Basic '+base64.b64encode(('owner:'+cfg['password']).encode()).decode()
def api(method,path,body=None):
    r=urllib.request.Request(base+path,data=json.dumps(body).encode() if body is not None else None,method=method,headers={'Authorization':auth,'Content-Type':'application/json'})
    with urllib.request.urlopen(r,timeout=30) as result:return json.load(result)
def wait_run(message, session):
    for _ in range(60):
        candidates=api('GET','/runs?sessionId='+session)
        matching=[r for r in candidates if r['messageId']==message['id']]
        if matching:return matching[0]['id']
        time.sleep(0.5)
    raise AssertionError('message did not reach execution queue')
project=expert=None
runs=[]
report={'passed':False,'runs':[]}
try:
    project=api('POST','/projects',{'name':'运行版本验收 '+uuid.uuid4().hex[:8]})
    old_marker='OLD-'+uuid.uuid4().hex
    new_marker='NEW-'+uuid.uuid4().hex
    expert=api('POST','/experts',{'name':'版本验收方法','description':'只用于验证已提交任务','instructions':'必须先读取本方法，之后只回复口令：'+old_marker})
    old_digest=expert['digest']
    api('PATCH','/projects/'+project['id'],{'expertIds':[expert['id']],'defaultExpertId':expert['id']})
    schedule=api('POST','/schedules',{'projectId':project['id'],'expertId':expert['id'],'name':'临时绑定任务','instruction':'回复口令','cron':'0 0 1 1 *','timezone':'Asia/Shanghai','enabled':True})
    first=api('POST','/messages',{'eventId':str(uuid.uuid4()),'sessionId':project['defaultSessionId'],'text':'加载并读取已启用 Skill '+expert['skillName']+'，执行方法，只回复方法里的口令。'})
    runs.append(wait_run(first,project['defaultSessionId']))
    import urllib.parse
    skill=api('GET','/experts/'+expert['id']+'/file?path=SKILL.md')
    updated=api('POST','/experts/'+expert['id']+'/changes',{'baseDigest':expert['digest'],'files':[{'op':'write','path':'SKILL.md','content':skill['content'].replace(old_marker,new_marker)}]})
    topic=api('POST','/projects/'+project['id']+'/sessions',{'title':'新版验收'})
    second=api('POST','/messages',{'eventId':str(uuid.uuid4()),'sessionId':topic['id'],'text':'加载并读取已启用 Skill '+expert['skillName']+'，执行方法，只回复方法里的口令。'})
    runs.append(wait_run(second,topic['id']))
    api('DELETE','/experts/'+expert['id'],{'baseDigest':updated['digest']})
    expert=None
    schedules=api('GET','/schedules?projectId='+project['id'])
    paused=next(s for s in schedules if s['id']==schedule['id'])
    assert not paused['enabled'] and paused['pausedReason']
    for run_id,marker,digest in [(runs[0],old_marker,old_digest),(runs[1],new_marker,updated['digest'])]:
        until=time.monotonic()+180
        while time.monotonic()<until:
            run=api('GET','/runs/'+run_id)
            if run['state'] in ['completed','failed','cancelled','interrupted']:break
            time.sleep(1)
        assert run['state']=='completed',run.get('error',run['state'])
        assert run['expertDigest']==digest,'run expert version changed'
        # API result contains the model's final text; preserve only disposable test evidence.
        assert marker in json.dumps(run,ensure_ascii=False),'fixed expert marker was not returned'
        report['runs'].append({'id':run_id,'state':run['state'],'expertDigest':run['expertDigest'],'markerMatched':True})
    assert report['runs'][0]['expertDigest'] != report['runs'][1]['expertDigest']
    report.update(passed=True,schedulePaused=True)
finally:
    if expert:
        current=api('GET','/experts/'+expert['id'])
        api('DELETE','/experts/'+expert['id'],{'baseDigest':current['expert']['digest']})
    if project:
        for session in api('GET','/projects/'+project['id']+'/sessions'):
            for run in api('GET','/runs?sessionId='+session['id']):
                if run['id'] not in runs:runs.append(run['id'])
        for run_id in runs:
            current=api('GET','/runs/'+run_id)
            if current['state'] in ['queued','running','waiting','stopping']:
                api('POST','/runs/'+run_id+'/stop',{})
                for _ in range(30):
                    if api('GET','/runs/'+run_id)['state'] not in ['queued','running','waiting','stopping']:break
                    time.sleep(1)
        api('DELETE','/projects/'+project['id'])
    out=Path(a.report);out.parent.mkdir(parents=True,exist_ok=True)
    out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(report,ensure_ascii=False))
