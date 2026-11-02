"""Planner journeys in the isolated localhost preview, with real Harness and channel replay."""
import atexit,base64,json,os,pathlib,time,urllib.request,urllib.error,uuid
url="http://127.0.0.1:3107/api/v1"
creds=json.loads((pathlib.Path.home()/".config/project-workbench/cli.json").read_text())
owner="Basic "+base64.b64encode(("owner:"+creds["password"]).encode()).decode()
channel="Bearer "+pathlib.Path(os.environ["PW_PREVIEW_TOKEN_FILE"]).read_text().strip()
def api(path,obj=None,ch=False,method=None):
    req=urllib.request.Request(url+path,data=None if obj is None else json.dumps(obj,ensure_ascii=False).encode(),method=method,headers={"Content-Type":"application/json","Authorization":channel if ch else owner})
    try:
        with urllib.request.urlopen(req,timeout=35) as r:return json.load(r)
    except urllib.error.HTTPError as e:raise RuntimeError(e.read().decode()) from e
def wait(check):
    end=time.monotonic()+230
    while time.monotonic()<end:
        value=check()
        if value:return value
        time.sleep(.7)
    raise RuntimeError("等待隔离执行超时")
key=lambda:uuid.uuid4().hex
suffix=key()[:6];name="规划验收"+suffix
p=api("/projects",{"name":name});sid=api("/planner/personal-space")["defaultSessionId"]
def cleanup():
    for rule in api("/schedules"):
        if rule["projectId"] == p["id"]:
            api("/schedules/"+rule["id"],method="DELETE")
atexit.register(cleanup)
t=api("/projects/"+p["id"]+"/tasks",{"title":"核对交付清单","operation":key()})
pt=api("/projects/personal-assistant/tasks",{"title":"梳理本周安排","operation":key()})
d=api("/tasks/"+pt["id"]+"/prepare",{});assert d["session"]["id"]==sid and d["task"]["state"]=="pending"
start=int(time.time()*1000)//86400000*86400000+6*3600000
api("/calendar/create",{"title":"产品评审","operation":key(),"destination":"cloud","start":start,"end":start+3600000})
def ask(kind,text):
    ev=key();body={"source":kind,"eventId":ev,"text":text}
    if kind=="web":
        body["sessionId"]=p["defaultSessionId"];api("/messages",body);session=body["sessionId"]
    else:
        c=api("/connections",{"connection":{"kind":kind,"label":"验收"+kind,"enabled":True,"ownerId":"preview-"+ev}})
        body.update(connectionId=c["id"],actorId=c["ownerId"],conversationId="private-"+ev);api("/channel/messages",body,True);session=sid
    r=wait(lambda:next((r for r in api("/runs?sessionId="+session) if r.get("eventId")==ev and r["state"] in ["failed","completed","interrupted"]),None));assert r["state"]=="completed",r.get("error")
    if kind!="web":
        delivery=wait(lambda:api("/channel/connections/"+c["id"]+"/claim",{},True)["delivery"]);assert delivery["replyTo"]==ev
        api("/channel/deliveries/"+delivery["id"]+"/finish",{"state":"sent","receipt":"isolated-"+ev},True)
    return r
tomorrow=start+86400000
title="时间核对"+suffix
wx=ask("wechat",f"请用 change_calendar 创建一个仅云端的个人日程，标题{title}，start={tomorrow}，end={tomorrow+1800000}，北京时间。请实际保存。")
events=api(f"/calendar?start={tomorrow-1}&end={tomorrow+1800001}")["items"]
assert len([v for v in events if v["title"]==title])==1,wx["result"]
fs=ask("feishu",f"用 query_calendar 查 {tomorrow-1} 到 {tomorrow+1800001} 之间的个人日程，只报实际查到的标题。");assert title in fs["result"],fs["result"]
web=ask("web","实际调用 save_schedule，创建本项目定时规则，名称验收早报，指令为只回复完成，五段 cron 为 0 9 * * 1，enabled=false。")
sc=next(v for v in api("/schedules?projectId="+p["id"]) if v["name"]=="验收早报")
ss=api("/projects/"+p["id"]+"/sessions",{"title":"本次验收专题"})
sc.update(sessionId=ss["id"]);sc=api("/schedules/"+sc["id"],sc,method="PUT")
api("/sessions/"+ss["id"],method="DELETE")
pre=api("/schedules/"+sc["id"]+"/run",{"operation":key()});assert pre["outcome"]=="failed" and not pre.get("runId")
n=next(x for x in api("/notices") if x["title"].startswith("验收早报"))
f=api("/failures/"+n["id"]);assert f["scheduleId"]==sc["id"]
sc=next(v for v in api("/schedules") if v["id"]==sc["id"]);sc["sessionId"]=p["defaultSessionId"];api("/schedules/"+sc["id"],sc,method="PUT")
r=api("/failures/"+n["id"]+"/retry",{});r2=api("/failures/"+n["id"]+"/retry",{});assert r["runId"]==r2["runId"]
done=wait(lambda:(lambda x:x if x["state"] in ["failed","completed"] else None)(api("/runs/"+r["runId"])))
assert done["state"]=="completed" and done["sessionId"]==p["defaultSessionId"]
api("/schedules",{"name":"整理项目进展","instruction":"根据资料更新","cron":"0 17 * * 2","projectId":p["id"],"enabled":True})
print(json.dumps({"wechat":wx["id"],"feishu":fs["id"],"web":web["id"],"retry":r["runId"],"failure":f["id"],"scope":p["id"],"personalTask":pt["id"]},ensure_ascii=False))

