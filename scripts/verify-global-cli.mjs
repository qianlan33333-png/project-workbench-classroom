import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
const live = process.env.PW_LIVE === "1";
const cfg = live
  ? JSON.parse(
      await fs.readFile(
        path.join(os.homedir(), ".config/project-workbench/cli.json"),
        "utf8",
      ),
    )
  : {
      url: process.env.PW_UI_URL || "http://127.0.0.1:3094",
      password: process.env.PWB_PASSWORD || "workbench-ux-test",
    };
const url = cfg.url.replace(/\/$/, "");
const auth = "Basic " + Buffer.from("owner:" + cfg.password).toString("base64");
const api = async (method, route, body) => {
  const r = await fetch(url + "/api/v1" + route, {
    method,
    headers: { Authorization: auth, "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  const v = await r.json();
  assert.equal(r.status, 200, JSON.stringify(v));
  return v;
};
const projects = [];
const report = { passed: false, url, runs: [] };
const dir = path.resolve("artifacts/lifecycle");
await fs.mkdir(dir, { recursive: true });
const prompt = `执行全局 CLI 验收，只读取，不写任何业务数据，不显示客户或日程明细。必须使用命令工具执行以下 Python 代码，不能用记忆代替。代码成功后简短报告结果。\n\npython3 - <<'PY'\nimport subprocess,json\ncommands=[['lark-cli','calendar','+agenda','--as','user','--format','json'],['aicrm-cli','invoke','admin.listCustomers','--query','limit=1']]\nfor args in commands:\n r=subprocess.run(args,capture_output=True,text=True,timeout=45)\n assert r.returncode==0,args[0]+': '+r.stderr[-250:]\n v=json.loads(r.stdout)\n assert v.get('ok') is True or (v.get('http_status')==200 and v.get('operation_id')=='admin.listCustomers'),'unexpected response status'\n data=v.get('data')\n print(args[0],'read_ok',len(data) if isinstance(data,list) else len(data.get('items',[])) if isinstance(data,dict) else 'ok')\nprint('CLOUD_GLOBAL_CLI_OK')\nPY`;
try {
  const tools = await api("GET", "/cli-tools");
  for (const id of ["lark", "aicrm"])
    assert(
      tools.some((t) => t.id === id && t.installed && t.enabled),
      "tool unavailable " + id,
    );
  const crm = tools.find((t) => t.id === "aicrm");
  await api("POST", "/cli-tools", {
    ...crm,
    origin: "https://www.youcangogogo.com",
  });
  for (const suffix of ["A", "B"]) {
    const p = await api("POST", "/projects", {
      name: "全局CLI验收 " + suffix + " " + Date.now(),
    });
    projects.push(p);
    const m = await api("POST", "/messages", {
      eventId: crypto.randomUUID(),
      sessionId: p.defaultSessionId,
      text: prompt,
    });
    let run;
    for (let i = 0; i < 150; i++) {
      const rows = await api("GET", "/runs?sessionId=" + p.defaultSessionId);
      run = rows.find((r) => r.messageId === m.id);
      if (run?.state === "waiting") {
        const interactions = await api("GET", "/interactions");
        for (const q of interactions.filter(
          (q) => q.runId === run.id && q.kind === "approval",
        ))
          await api("POST", `/interactions/${q.id}/answer`, {
            outcome: "allowed-once",
          });
      }
      if (
        run &&
        ["completed", "failed", "cancelled", "interrupted"].includes(run.state)
      )
        break;
      await new Promise((r) => setTimeout(r, 1000));
    }
    assert.equal(run?.state, "completed", run?.error);
    assert(run.result.includes("CLOUD_GLOBAL_CLI_OK"), run.result);
    const events = await api(
      "GET",
      "/events?json=1&sessionId=" + p.defaultSessionId,
    );
    assert(
      events.some((e) => e.type === "harness.tool/call"),
      "no actual tool call",
    );
    report.runs.push({
      project: suffix,
      sessionId: p.defaultSessionId,
      runId: run.id,
      state: run.state,
      result: run.result,
    });
  }
  assert.notEqual(report.runs[0].sessionId, report.runs[1].sessionId);
  report.passed = true;
} catch (e) {
  report.error = String(e);
  throw e;
} finally {
  for (const p of projects) {
    const runs = await api("GET", "/runs?sessionId=" + p.defaultSessionId);
    for (const r of runs)
      if (["queued", "running", "waiting"].includes(r.state)) {
        await api("POST", "/runs/" + r.id + "/stop", {});
        for (let i = 0; i < 30; i++) {
          const x = await api("GET", "/runs/" + r.id);
          if (!["queued", "running", "waiting", "stopping"].includes(x.state))
            break;
          await new Promise((r) => setTimeout(r, 500));
        }
      }
    await api("DELETE", "/projects/" + p.id);
  }
  await fs.writeFile(
    path.join(dir, live ? "live-cli-report.json" : "cloud-cli-report.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(JSON.stringify(report));
}
