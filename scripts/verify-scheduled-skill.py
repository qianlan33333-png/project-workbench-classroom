"""Exercise the cloud preview only, with a synthetic project and no external delivery."""
import base64, json, pathlib, time, urllib.request, uuid

root = "http://127.0.0.1:3095/api/v1"
cfg = json.loads((pathlib.Path.home()/".config/project-workbench/cli.json").read_text())
authorization = "Basic " + base64.b64encode(("owner:"+cfg["password"]).encode()).decode()
def api(path, body=None, method=None):
    data = None if body is None else json.dumps(body,ensure_ascii=False).encode()
    req = urllib.request.Request(root+path,data=data,method=method,headers={"Authorization":authorization,"Content-Type":"application/json"})
    with urllib.request.urlopen(req,timeout=30) as response:
        return json.load(response)
def wait_run(schedule,run_id,states):
    deadline=time.monotonic()+360
    while time.monotonic()<deadline:
        r=next(r for r in api("/schedules/"+schedule+"/runs") if r["id"]==run_id)
        if r["state"] in states:return r
        time.sleep(.35)
    raise RuntimeError("test run did not reach expected state")
name="整理验收"+uuid.uuid4().hex[:5]
project=api("/projects",{"name":name});pid=project["id"]
def save(path,content):
    return api("/projects/"+pid+"/file",{"path":path,"content":content,"baseHash":""},"PUT")
save("资料/README.md","这次项目只验证定时整理。当前决定使用青竹方案，尚未开始真实客户交付。")
save("资料/catalog.json",json.dumps({"modules":["sales","support"],"endpoints":[{"id":i,"path":"/test/"+str(i)} for i in range(30000)]}))
sc=api("/schedules",{"projectId":pid,"name":"整理项目说明","condition":"changed","sourceScope":"资料","output":"project_context","instruction":"修订说明，保留“青竹方案”的决定及尚未交付的状态，只按资料作答。","cron":"0 7 * * *","enabled":False})
sid=sc["id"]
def fire(key=None):return api("/schedules/"+sid+"/run",{"operation":key or str(uuid.uuid4())})
def get_sc():return next(s for s in api("/schedules?projectId="+pid) if s["id"]==sid)
q=fire("initial-proof");assert fire("initial-proof")["runId"]==q["runId"]
r=wait_run(sid,q["runId"],["completed","failed"]);assert r["state"]=="completed",r.get("error")
c=api("/projects/"+pid+"/context");assert "青竹" in c["description"] and any(x["status"]=="partial" for x in c["sources"]),c
assert fire()["outcome"]=="skipped";assert len(api("/schedules/"+sid+"/runs"))==1
print("changed / exact replay / unchanged verified",flush=True)
api("/schedules/"+sid+"/enabled",{"enabled":True})
save("资料/验收.md","本次验证通过后再申请交付。")
q=fire();wait_run(sid,q["runId"],["running"])
# Wait for a runtime event so the draft has already captured its source version.
time.sleep(3)
c=api("/projects/"+pid+"/context");api("/projects/"+pid+"/context",{"description":"人工修订保留：青竹方案。","version":c["version"]},"PUT")
failed=wait_run(sid,q["runId"],["completed","failed"]);assert failed["state"]=="failed",failed["state"]
assert not get_sc()["enabled"] and get_sc()["pausedReason"]
assert api("/projects/"+pid+"/context")["description"]=="人工修订保留：青竹方案。"
sc=api("/schedules/"+sid,{**get_sc(),"instruction":"按人工说明修订，保留青竹方案。"},"PUT");assert not sc["enabled"]
q=fire();manual=wait_run(sid,q["runId"],["completed","failed"]);assert manual["state"]=="completed",manual.get("error");assert not get_sc()["enabled"]
assert len(api("/schedules/"+sid+"/runs"))==3
report={"project":pid,"name":name,"schedule":sid,"first":r["id"],"failed":failed["id"],"failure":failed["error"],"manual":manual["id"],"afterManual":"paused","unchanged":"skipped; no run created","largeJSON":"30000 endpoints, partial structural evidence"}
out=pathlib.Path("artifacts/scheduled-skill");out.mkdir(parents=True,exist_ok=True);(out/"real-harness.json").write_text(json.dumps(report,indent=2,ensure_ascii=False));print(json.dumps(report,ensure_ascii=False),flush=True)
