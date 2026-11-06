"""Real Harness and bound-channel replay on the isolated Today task preview."""
import base64
import json
from pathlib import Path
import time
import urllib.request
import uuid

base = "http://127.0.0.1:3197/api/v1"
auth = json.loads(Path("/var/lib/project-workbench-sidebar-preview/auth.json").read_text())
owner = "Basic " + base64.b64encode(("owner:" + auth["password"]).encode()).decode()


def api(path, data=None, *, channel=False):
    authorization = "Bearer " + auth["token"] if channel else owner
    req = urllib.request.Request(base + path,
        data=None if data is None else json.dumps(data, ensure_ascii=False).encode(),
        headers={"Authorization": authorization, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=20) as response:
        return json.load(response)


def wait(callback, seconds=180):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = callback()
        if value:
            return value
        time.sleep(.8)
    raise AssertionError("Timed out waiting for Today")


today = api("/today")
session = today["sessionId"]
nonce = uuid.uuid4().hex[:6]
a, b = [api("/projects", {"name": "隔离任务" + key + nonce}) for key in ("甲", "乙")]
terminal = {"completed", "failed", "interrupted", "cancelled"}


def ask(source, text):
    event = str(uuid.uuid4())
    message = {"source": source, "eventId": event, "text": text}
    if source == "web":
        message["sessionId"] = session
        api("/messages", message)
    else:
        c = api("/connections", {"connection": {
            "kind": source, "label": "隔离任务验收", "enabled": True, "ownerId": "owner-" + event}})
        message.update(connectionId=c["id"], actorId=c["ownerId"], conversationId="private-" + event)
        api("/channel/messages", message, channel=True)
    run = wait(lambda: next((r for r in api("/runs?sessionId=" + session)
        if r.get("eventId") == event and r["state"] in terminal), None))
    assert run["state"] == "completed", run.get("error")
    assert run["sessionId"] == session
    events = [e for e in api("/events?json=1&sessionId=" + session)
              if e.get("runId") == run["id"] and e["type"] == "harness.tool/call"]
    if source != "web":
        delivery = wait(lambda: api("/channel/connections/" + c["id"] + "/claim", {}, channel=True)["delivery"])
        assert delivery["runId"] == run["id"] and delivery["replyTo"] == event
        api("/channel/deliveries/" + delivery["id"] + "/finish",
            {"state": "sent", "receipt": "isolated-" + event}, channel=True)
    return run, events


initial_tasks = api("/tasks")
r, events = ask("web", "只是讨论：我在考虑写一份验收清单，请说一句你会怎么安排，不用创建任何任务。")
assert not any("create_task" in json.dumps(e) for e in events)
assert api("/tasks") == initial_tasks
print(json.dumps({"discussion": r["id"], "writes": 0}), flush=True)

for source, target, where in [
    ("web", "personal-assistant", "我的个人空间"),
    ("wechat", a["id"], "项目「" + a["name"] + "」"),
    ("feishu", b["id"], "项目「" + b["name"] + "」"),
]:
    title = "核对隔离资料" + source + nonce
    run, events = ask(source, f"请在{where}创建一条正式的待处理任务，标题为「{title}」，说明为「核对合成记录」，请直接保存并给我任务链接。")
    found = [task for task in api("/projects/" + target + "/tasks") if task["title"] == title]
    assert len(found) == 1 and found[0]["state"] == "pending", (source, len(found), run["result"])
    assert any("create_task" in json.dumps(e) for e in events), source
    assert found[0]["id"] in run["result"], (source, run["result"])
    print(json.dumps({"source": source, "task": found[0]["id"], "run": run["id"], "state": "pending"}), flush=True)
