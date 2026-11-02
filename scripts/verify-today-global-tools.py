"""Real-model and channel replay acceptance; isolated loopback preview only."""
import base64, json, os, pathlib, time, urllib.request, uuid
url="http://127.0.0.1:3107/api/v1"
owner="Basic "+base64.b64encode(("owner:"+json.loads((pathlib.Path.home()/".config/project-workbench/cli.json").read_text())["password"]).encode()).decode()
channel="Bearer "+pathlib.Path(os.environ["PW_PREVIEW_TOKEN_FILE"]).read_text().strip()
def api(path,body=None,ch=False,method=None):
    req=urllib.request.Request(url+path,data=None if body is None else json.dumps(body,ensure_ascii=False).encode(),headers={"Authorization":channel if ch else owner,"Content-Type":"application/json"},method=method)
    with urllib.request.urlopen(req,timeout=30) as r: return json.load(r)
sid=api("/today")["sessionId"]
def wait(check,seconds=200):
    limit=time.monotonic()+seconds
    while time.monotonic()<limit:
        result=check()
        if result:return result
        time.sleep(.6)
    raise AssertionError("timeout")
def ask(kind,text,needle,expected,refs=None):
    ev=str(uuid.uuid4());msg={"eventId":ev,"text":text,"source":kind}
    if kind=="web":
        msg.update(sessionId=sid,capabilities=refs or [])
        api("/messages",msg)
    else:
        c=api("/connections",{"connection":{"kind":kind,"label":"隔离工具验收","enabled":True,"ownerId":"preview-"+ev}})
        msg.update(connectionId=c['id'],actorId=c['ownerId'],conversationId='private-'+ev);api("/channel/messages",msg,True)
    r=wait(lambda:next((r for r in api("/runs?sessionId="+sid) if r.get('eventId')==ev and r['state'] in ('completed','failed','cancelled','interrupted')),None))
    assert r['state']=='completed' and expected in r['result'],(r.get('error'),r.get('result'))
    events=api('/events?json=1&sessionId='+sid)
    assert any(x.get('runId')==r['id'] and x['type']=='harness.tool/call' and needle in json.dumps(x) for x in events)
    if kind!='web':
        d=wait(lambda:api('/channel/connections/'+c['id']+'/claim',{},True)['delivery']);assert d['runId']==r['id'] and d['replyTo']==ev
        api('/channel/deliveries/'+d['id']+'/finish',{'state':'sent','receipt':'isolated-'+ev},True)
    print(kind,r['id'],expected,flush=True)
nonce=uuid.uuid4().hex[:8]
mcp=api('/plugins',{'name':'隔离回显'+nonce,'transport':'stdio','command':'node','args':['/opt/workbench-runtime/test-mcp.mjs'],'enabled':True})
try:
    ask('web','请实际调用选中的回显 MCP，text 使用 '+nonce+'，原样回复结果。','echo','GLOBAL_MCP:'+nonce,[{'kind':'mcp','id':mcp['id']}])
    ask('wechat','请用 CRM CLI 的 --version 查询实际版本，只回版本号。','run_global_cli','0.6.1')
    ask('feishu','请用飞书 CLI 的 --version 查询实际版本，只回版本号。','run_global_cli','1.0.98')
finally:
    api('/plugins/'+mcp['id'],method="DELETE")
