"""Isolated real-Harness acceptance. Never run against production."""
import json,urllib.request,urllib.error,pathlib,base64,time,uuid
cfg=json.loads((pathlib.Path.home()/".config/project-workbench/cli.json").read_text())
auth="Basic "+base64.b64encode(("owner:"+cfg["password"]).encode()).decode()
base="http://127.0.0.1:3095/api/v1"
def api(path,obj=None,method=None):
 req=urllib.request.Request(base+path,data=json.dumps(obj,ensure_ascii=False).encode() if obj is not None else None,method=method or ("POST" if obj is not None else "GET"),headers={"Authorization":auth,"Content-Type":"application/json"})
 with urllib.request.urlopen(req,timeout=20) as r:return json.load(r)
def wait(fn,seconds=240):
 end=time.time()+seconds
 while time.time()<end:
  v=fn()
  if v:return v
  time.sleep(1)
 raise RuntimeError("acceptance timed out")
report={"runs":[],"checks":[]}
def ask(text):
 sid=api("/today")["sessionId"];ev=str(uuid.uuid4())
 api("/messages",{"source":"web","eventId":ev,"sessionId":sid,"text":text})
 r=wait(lambda:next((r for r in api("/today")["runs"] if r.get("eventId")==ev and r["state"] in ["completed","failed","cancelled","interrupted"]),None))
 assert r["state"]=="completed",r.get("error")
 report["runs"].append({k:r.get(k) for k in ["id","sessionId","personalRulesVersion","result","state"]})
 print(json.dumps(report["runs"][-1],ensure_ascii=False),flush=True)
 return r
original=api("/personal/rules")
try:
 marker="规则验收"+uuid.uuid4().hex[:6]
 rules=api("/personal/rules",{**original,"agent":original["agent"]+"\n隔离测试期间，在每次回答以及非空的每日建议末尾加上「"+marker+"」。"},"PUT")
 r=ask("今天的演示做完了，对方听懂了，也问了几个具体问题。我只是回来分享一下，没有别的问题。请按我当前已保存的 AGENT.md 回答格式回应。另外只在本对话记住连续性标识 溪谷572，不调用记忆工具。")
 assert marker in r["result"] and r["personalRulesVersion"]==rules["version"]
 marker2="规则更新"+uuid.uuid4().hex[:6]
 rules=api("/personal/rules",{**rules,"agent":original["agent"]+"\n隔离测试期间，每次回答以及非空的每日建议末尾加上「"+marker2+"」。"},"PUT")
 r2=ask("你还记得本次连续性标识吗？请从对话历史回答，不调用工具，并在结尾带上当前 AGENT.md 规定的测试标记。")
 assert r2["sessionId"]==r["sessionId"] and "溪谷572" in r2["result"] and marker2 in r2["result"] and marker not in r2["result"]
 assert r2["personalRulesVersion"]==rules["version"]
 report["checks"].append("edited system rules applied to resumed native session without replacing history")
 api("/personal/memories",{"kind":"temporary","text":"隔离验收情境：今天下午14:00有一次真实客户演示，材料已齐，希望练习把产品价值讲清楚。这是今天新确定的安排，此前尚未提出过相应建议。","expiresAt":int(time.time()*1000)+86400000})
 prior=api("/today")["daily"]
 api("/today/refresh",{})
 d=wait(lambda:(lambda v:v if v.get("version",0)>prior.get("version",0) and v.get("state")=="ready" else None)(api("/today")["daily"]),360)
 assert d["personalRulesVersion"]==rules["version"]
 assert not d.get("text") or marker2 in d["text"],d
 report["daily"]=d;report["checks"].append("daily suggestion uses same saved rules revision")
 report["passed"]=True
finally:
 latest=api("/personal/rules");api("/personal/rules",{**latest,"soul":original["soul"],"agent":original["agent"]},"PUT")
 pathlib.Path("artifacts/today").mkdir(parents=True,exist_ok=True)
 pathlib.Path("artifacts/today/personal-rules-model.json").write_text(json.dumps(report,ensure_ascii=False,indent=2))
 print(json.dumps({"passed":report.get("passed",False),"checks":report["checks"]},ensure_ascii=False),flush=True)
