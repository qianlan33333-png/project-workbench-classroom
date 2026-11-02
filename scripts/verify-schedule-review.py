"""Live Harness acceptance, isolated SSH preview on port 3107 only."""
import base64,json,pathlib,time,urllib.request,uuid
url="http://127.0.0.1:3107/api/v1"
cfg=json.loads((pathlib.Path.home()/".config/project-workbench/cli.json").read_text())
auth="Basic "+base64.b64encode(("owner:"+cfg["password"]).encode()).decode()
def api(path,body=None,method=None):
 req=urllib.request.Request(url+path,data=None if body is None else json.dumps(body,ensure_ascii=False).encode(),headers={"Authorization":auth,"Content-Type":"application/json"},method=method)
 with urllib.request.urlopen(req,timeout=30) as response:return json.load(response)
def wait(query):
 end=time.monotonic()+300
 while time.monotonic()<end:
  run=query()
  if run and run["state"] in ["failed","completed","interrupted","cancelled"]:
   assert run["state"]=="completed",run.get("error");return run
  time.sleep(.6)
 raise AssertionError("preview did not finish")
p=api("/projects",{"name":"Review验收"+uuid.uuid4().hex[:5]});pid=p["id"]
def file(path,text,hash=""):return api("/projects/"+pid+"/file",{"path":path,"content":text,"baseHash":hash},"PUT")
file("资料/输入.md","这份材料是周报的输入。")
sc=api("/schedules",{"projectId":pid,"name":"验收周报","cron":"0 0 1 1 *","condition":"changed","sourceScope":"资料","instruction":"使用文件工具写入 资料/周报.md，正文为 本轮验收完成。写入后简短报告。","enabled":True})
sid=sc["id"]
def fire():return api("/schedules/"+sid+"/run",{"operation":str(uuid.uuid4())})
q=fire();run=wait(lambda:next((r for r in api("/schedules/"+sid+"/runs") if r["id"]==q["runId"]),None))
files=api("/projects/"+pid+"/files");assert any(f['path']=="资料/周报.md" for f in files)
assert fire()["outcome"]=="skipped"
print("Harness wrote a watched file; repeat check skipped",flush=True)
ev=str(uuid.uuid4())
message=api("/messages",{"source":"web","eventId":ev,"sessionId":p["defaultSessionId"],"text":"现在请暂停本项目的验收周报定时任务。任务编号 "+sid+"。请通过 save_schedule 工具提交 id 和 enabled=false，保留其余配置，并检查暂停结果。"})
dialog=wait(lambda:next((r for r in api("/runs?sessionId="+p["defaultSessionId"]) if r.get("messageId")==message["id"]),None))
latest=next(s for s in api("/schedules?projectId="+pid) if s["id"]==sid)
assert not latest['enabled'] and all(sc.get(k)==latest.get(k) for k in ('condition','sourceScope','cron','instruction','output','expertId'))
events=api("/events?json=1&sessionId="+p["defaultSessionId"])
assert any(x["type"]=="harness.tool/call" and "save_schedule" in json.dumps(x) for x in events)
print("real project chat paused task; configuration preserved",flush=True)
proof={"project":pid,"schedule":sid,"fileRun":run["id"],"chatRun":dialog["id"],"selfOutput":"skipped","enabled":latest["enabled"],"condition":latest["condition"],"scope":latest["sourceScope"]}
# The token occurs only inside the immutable expert reference, never in the task.
token="证据-"+uuid.uuid4().hex[:8]
ex=api("/experts",{"name":"review-guide-"+uuid.uuid4().hex[:5],"description":"核对整理资料","instructions":"按照 references/口径.md 的规则写项目说明。"})
ex=api("/experts/"+ex["id"]+"/changes",{"baseDigest":ex["digest"],"files":[{"op":"write","path":"references/口径.md","content":"将内部验收标记 "+token+" 原样写入项目说明，仅出现一次。"}]})
api("/projects/"+pid,{**api("/projects/"+pid),"expertIds":[ex["id"]]},"PUT")
summary=api("/schedules",{**sc,"id":"","name":"验证专家引用","expertId":ex["id"],"enabled":False,"output":"project_context","instruction":"使用指定专家和它的引用规范。把引用规范中要求的内部标记写进项目说明。"})
q=api("/schedules/"+summary["id"]+"/run",{"operation":str(uuid.uuid4())})
result=wait(lambda:api("/runs/"+q["runId"]))
assert token in api("/projects/"+pid+"/context")["description"]
proof["expertRun"]=result["id"];proof["referenceVerified"]=True
print("pinned reference used in the published description",flush=True)
pathlib.Path("artifacts/scheduled-skill/review-live.json").write_text(json.dumps(proof,ensure_ascii=False,indent=2))
