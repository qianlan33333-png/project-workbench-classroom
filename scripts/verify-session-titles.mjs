import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";
const live = process.env.PW_LIVE === "1";
const cfg = live
  ? JSON.parse(
      await fs.readFile(
        path.join(os.homedir(), ".config/project-workbench/cli.json"),
        "utf8",
      ),
    )
  : {
      url: process.env.PW_UI_URL || "http://127.0.0.1:3091",
      password: process.env.PWB_PASSWORD || "workbench-ux-test",
    };
const url = cfg.url.replace(/\/$/, "");
const auth = "Basic " + Buffer.from("owner:" + cfg.password).toString("base64");
const dir = path.resolve("artifacts/session-titles");
await fs.mkdir(dir, { recursive: true });
const api = async (method, route, body) => {
  const response = await fetch(url + "/api/v1" + route, {
    method,
    headers: { Authorization: auth, "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await response.json();
  assert.equal(response.status, 200, JSON.stringify(data));
  return data;
};
const wait = async (fn, label, timeout = 30000) => {
  const end = Date.now() + timeout;
  let error;
  while (Date.now() < end) {
    try {
      return await fn();
    } catch (e) {
      error = e;
      await new Promise((r) => setTimeout(r, 150));
    }
  }
  throw new Error(label + ": " + error);
};
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.PW_CHROME ||
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const report = { passed: false, url, checks: [] };
let project, release;
const currentId = async () =>
  new URLSearchParams(new URL(page.url()).hash.slice(1)).get("session");
const composer = page.getByLabel("发送消息", { exact: true });
const send = () =>
  page.getByRole("button", { name: "发送", exact: true }).click();
const openNew = () =>
  page.getByRole("button", { name: "另开对话", exact: true }).last().click();
try {
  project = await api("POST", "/projects", {
    name: "会话标题验收 " + Date.now(),
  });
  await page.goto(
    url + "/#project=" + project.id + "&session=" + project.defaultSessionId,
  );
  await page.getByLabel("密码", { exact: true }).fill(cfg.password);
  await page.getByRole("button", { name: "进入工作台" }).click();
  await composer.waitFor();
  await page.locator(".session-list button").waitFor();
  await composer.fill("默认助手里尚未发送的草稿");
  await openNew();
  await wait(
    async () => assert.notEqual(await currentId(), project.defaultSessionId),
    "blank session selected",
  );
  const abandoned = await currentId();
  assert.equal(await page.getByRole("dialog").count(), 0);
  assert.equal(await page.locator(".session-list button").count(), 1);
  assert.equal(await composer.inputValue(), "");
  await openNew();
  assert.equal(await currentId(), abandoned);
  await page.getByLabel("当前对话").selectOption(project.defaultSessionId);
  assert.equal(await composer.inputValue(), "默认助手里尚未发送的草稿");
  await openNew();
  await wait(
    async () => assert.notEqual(await currentId(), project.defaultSessionId),
    "second blank session",
  );
  const sessionId = await currentId();
  await page.reload();
  await composer.waitFor();
  assert.equal(await currentId(), sessionId);
  await wait(
    async () =>
      assert.equal(await page.locator(".session-list button").count(), 1),
    "sidebar loaded after reload",
  );
  const identity = (await api("GET", "/sessions/" + sessionId)).harnessId;
  await page.getByLabel("当前专家").selectOption("general");
  await wait(
    async () =>
      assert.equal(
        (await api("GET", "/sessions/" + sessionId)).expertId,
        "general",
      ),
    "expert selected",
  );
  report.checks.push(
    "No title dialog, unused sessions hidden, repeat empty click reused, refresh keeps ID, conversation drafts isolated",
  );
  let posted;
  const gate = new Promise((r) => (release = r));
  await page.route("**/api/v1/messages", async (route) => {
    posted = JSON.parse(route.request().postData());
    await gate;
    await route.continue().catch(() => {});
  });
  const prompt =
    "请帮我设计一份新员工入职第一周的培训计划，分三点简短回答，不使用工具。";
  await composer.fill(prompt);
  const started = Date.now();
  await send();
  await page
    .locator(".session-list button")
    .filter({ hasText: "请帮我设计一份新员工" })
    .waitFor();
  report.provisionalTitleMs = Date.now() - started;
  assert(report.provisionalTitleMs < 1000);
  assert.equal(await page.locator(".user-message p").count(), 1);
  assert.equal(
    (await api("GET", "/projects/" + project.id + "/sessions")).length,
    1,
  );
  await page.screenshot({
    path: path.join(dir, (live ? "live-" : "") + "provisional.png"),
  });
  release();
  const named = await wait(
    async () => {
      const s = await api("GET", "/sessions/" + sessionId);
      assert.equal(s.titleSource, "ai");
      return s;
    },
    "real AI title",
    60000,
  );
  report.generatedTitle = named.title;
  assert.equal(named.harnessId, identity);
  assert.equal(named.draft, undefined);
  await wait(
    async () =>
      assert(
        (
          await page
            .getByLabel("当前对话")
            .locator("option:checked")
            .textContent()
        ).includes(named.title),
      ),
    "AI title shown by SSE",
  );
  await wait(
    async () => {
      const rows = await api("GET", "/runs?sessionId=" + sessionId);
      assert.equal(rows.length, 1);
      assert.equal(rows[0].state, "completed", rows[0].error);
      assert.equal(rows[0].eventId, posted.eventId);
    },
    "first reply completed",
    60000,
  );
  assert.equal(await page.locator(".session-list button").count(), 2);
  await page.unroute("**/api/v1/messages");
  report.checks.push(
    "Immediate provisional title before receipt, native AI title in same session, single run, live sidebar update",
  );
  await page.getByRole("button", { name: "重命名对话", exact: true }).click();
  await page
    .getByLabel("对话标题", { exact: true })
    .fill("入职培训 · 我的计划");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await wait(
    async () =>
      assert.equal(
        (await api("GET", "/sessions/" + sessionId)).titleSource,
        "manual",
      ),
    "manual rename",
  );
  const manual = await api("GET", "/sessions/" + sessionId);
  assert.equal(manual.expertId, "general");
  assert.equal(manual.harnessId, identity);
  await composer.fill(
    "刚才讨论的培训面向哪类人？根据上文只用一句话回答，不使用工具。",
  );
  await send();
  await wait(
    async () => {
      const rows = await api("GET", "/runs?sessionId=" + sessionId);
      assert.equal(rows.length, 2);
      assert(
        rows.every((r) => r.state === "completed"),
        JSON.stringify(rows.map((r) => ({ state: r.state, error: r.error }))),
      );
      assert(rows.some((r) => r.result.includes("新员工")));
    },
    "continued native conversation",
    60000,
  );
  const after = await api("GET", "/sessions/" + sessionId);
  assert.equal(after.title, manual.title);
  assert.equal(after.harnessId, identity);
  await page.reload();
  await composer.waitFor();
  await wait(
    async () =>
      assert.equal(
        await page
          .getByLabel("当前对话")
          .locator("option:checked")
          .textContent(),
        manual.title,
      ),
    "rename survives reload",
  );
  await page.screenshot({
    path: path.join(dir, (live ? "live-" : "") + "named.png"),
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(350);
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  );
  await page.getByRole("button", { name: "重命名对话", exact: true }).waitFor();
  await page.screenshot({
    path: path.join(dir, (live ? "live-" : "") + "mobile.png"),
  });
  report.checks.push(
    "Manual rename preserves expert and Harness ID, next turn continues history, rename survives reload, mobile controls accessible",
  );
  report.passed = true;
} catch (error) {
  report.error = String(error);
  await page.screenshot({
    path: path.join(dir, (live ? "live-" : "") + "failure.png"),
  });
  throw error;
} finally {
  release?.();
  await browser.close();
  if (project) {
    const sessions = await api("GET", "/projects/" + project.id + "/sessions");
    for (const s of sessions) {
      const runs = await api("GET", "/runs?sessionId=" + s.id);
      for (const r of runs)
        if (["queued", "running", "waiting", "stopping"].includes(r.state)) {
          await api("POST", "/runs/" + r.id + "/stop", {});
          await wait(
            async () =>
              assert(
                !["queued", "running", "waiting", "stopping"].includes(
                  (await api("GET", "/runs/" + r.id)).state,
                ),
              ),
            "cleanup run",
          );
        }
    }
    await api("DELETE", "/projects/" + project.id);
  }
  await fs.writeFile(
    path.join(dir, (live ? "live-" : "") + "report.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(JSON.stringify(report));
}
