import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";
const url = process.env.PW_UI_URL || "http://127.0.0.1:3091";
const password = process.env.PWB_PASSWORD || "workbench-ux-test";
const auth = "Basic " + Buffer.from("owner:" + password).toString("base64");
const dir = path.resolve("artifacts/message-feedback");
await fs.mkdir(dir, { recursive: true });
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
const wait = async (fn, label) => {
  for (let i = 0; i < 150; i++) {
    try {
      await fn();
      return;
    } catch (e) {
      if (i === 149) throw new Error(label + ": " + e.message);
      await new Promise((r) => setTimeout(r, 100));
    }
  }
};
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.PW_CHROME ||
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
});
const projects = [],
  contexts = [];
const report = { passed: false, checks: [], timings: {} };
let page;
const login = async (p, quiet = false) => {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
  });
  contexts.push(context);
  const next = await context.newPage();
  if (quiet)
    await next.route("**/api/v1/events", (route) =>
      route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: ": test without live transport\n\n",
      }),
    );
  await next.goto(
    url + "/#project=" + p.id + "&session=" + p.defaultSessionId,
    { waitUntil: "domcontentloaded" },
  );
  await next.getByLabel("密码", { exact: true }).fill(password);
  await next.getByRole("button", { name: "进入工作台" }).click();
  await next.getByLabel("发送消息", { exact: true }).waitFor();
  await next
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: p.name, exact: true })
    .waitFor();
  return next;
};
try {
  const a = await api("POST", "/projects", {
    name: "发送反馈 A " + Date.now(),
  });
  projects.push(a);
  const b = await api("POST", "/projects", {
    name: "发送反馈 B " + Date.now(),
  });
  projects.push(b);
  page = await login(a);
  let release, posted;
  const gate = new Promise((r) => (release = r));
  await page.route("**/api/v1/messages", async (route) => {
    posted = JSON.parse(route.request().postData());
    await gate;
    await route.continue().catch(() => {});
  });
  const text = "延迟验收：只回复收到";
  await page.getByLabel("发送消息", { exact: true }).fill(text);
  const start = Date.now();
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.locator(".user-message p").filter({ hasText: text }).waitFor();
  report.timings.optimisticBubbleMs = Date.now() - start;
  assert(report.timings.optimisticBubbleMs < 500);
  assert.equal(
    await page
      .locator(".submission-status")
      .getByText("发送中", { exact: true })
      .count(),
    1,
  );
  await page.screenshot({ path: path.join(dir, "sending.png") });
  await page
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: b.name, exact: true })
    .click();
  await page
    .getByLabel("发送消息", { exact: true })
    .fill("这是 B 对话尚未发送的草稿");
  await new Promise((r) =>
    setTimeout(r, Math.max(0, 5000 - (Date.now() - start))),
  );
  release();
  await wait(
    async () =>
      assert(
        (await api("GET", "/runs?sessionId=" + a.defaultSessionId)).some(
          (r) => r.eventId === posted.eventId,
        ),
      ),
    "delayed request was queued",
  );
  await page.waitForTimeout(700);
  assert.equal(
    await page.getByLabel("发送消息", { exact: true }).inputValue(),
    "这是 B 对话尚未发送的草稿",
  );
  assert.equal(
    await page.locator(".user-message p").filter({ hasText: text }).count(),
    0,
  );
  await page
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: a.name, exact: true })
    .click();
  await wait(async () => {
    assert.equal(
      await page.locator(".user-message p").filter({ hasText: text }).count(),
      1,
    );
    assert.equal(await page.locator(".pending-turn").count(), 0);
  }, "single reconciled turn");
  await page.unroute("**/api/v1/messages");
  report.checks.push(
    "Immediate bubble while POST is delayed 5 seconds",
    "Late receipt cannot erase another conversation draft",
    "No duplicate after run reconciliation",
  );
  // A stalled background snapshot must not stall visible run state.
  let unblock;
  const background = new Promise((r) => (unblock = r));
  await page.route("**/api/v1/inbox", async (route) => {
    await background;
    await route.continue().catch(() => {});
  });
  const second = "事件流验收：只回复收到";
  await page.getByLabel("发送消息", { exact: true }).fill(second);
  const eventStart = Date.now();
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await wait(
    async () =>
      assert.equal(
        await page
          .locator(".turn[data-message-id]")
          .filter({ hasText: second })
          .count(),
        1,
      ),
    "run appears despite blocked snapshot",
  );
  report.timings.runWithBlockedRefreshMs = Date.now() - eventStart;
  assert(report.timings.runWithBlockedRefreshMs < 3000);
  unblock();
  await page.unroute("**/api/v1/inbox");
  report.checks.push("Run appears without waiting for whole-page refresh");
  // The request reached the server, but its HTTP receipt and SSE are lost.
  page = await login(b, true);
  let posts = 0;
  await page.route("**/api/v1/messages/by-event/*", (route) =>
    route.abort("failed"),
  );
  await page.route("**/api/v1/messages", async (route) => {
    posts++;
    await route.fetch();
    await route.abort("failed");
  });
  const lost = "丢失回执验收：只回复收到";
  await page.getByLabel("发送消息", { exact: true }).fill(lost);
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.getByRole("button", { name: "核对并重试", exact: true }).waitFor();
  await page.getByLabel("发送消息", { exact: true }).fill("不要被旧请求清空");
  await page.screenshot({ path: path.join(dir, "unknown-receipt.png") });
  await page.unroute("**/api/v1/messages/by-event/*");
  await page.getByRole("button", { name: "核对并重试", exact: true }).click();
  await wait(async () => {
    assert.equal(
      await page
        .locator(".turn[data-message-id]")
        .filter({ hasText: lost })
        .count(),
      1,
    );
    assert.equal(await page.locator(".pending-turn").count(), 0);
  }, "lost receipt reconciles persisted run");
  assert.equal(posts, 1);
  assert.equal(
    await page.getByLabel("发送消息", { exact: true }).inputValue(),
    "不要被旧请求清空",
  );
  assert.equal(
    (await api("GET", "/runs?sessionId=" + b.defaultSessionId)).filter(
      (r) => r.text === lost,
    ).length,
    1,
  );
  await page.reload({ waitUntil: "domcontentloaded" });
  await wait(
    async () =>
      assert.equal(
        await page.locator(".user-message p").filter({ hasText: lost }).count(),
        1,
      ),
    "refresh keeps one turn",
  );
  report.checks.push(
    "Lost receipt is checked without a second POST",
    "Draft survives reconciliation",
    "Reload retains one server turn",
  );
  // A truly missing request is retried with exactly the original event ID.
  await page.unroute("**/api/v1/messages");
  let attempts = [];
  await page.route("**/api/v1/messages", async (route) => {
    attempts.push(JSON.parse(route.request().postData()));
    if (attempts.length === 1) await route.abort("failed");
    else await route.continue().catch(() => {});
  });
  const retry = "未送达验收：只回复收到";
  await page.getByLabel("发送消息", { exact: true }).fill(retry);
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.getByRole("button", { name: "核对并重试", exact: true }).waitFor();
  await page.getByRole("button", { name: "核对并重试", exact: true }).click();
  await wait(
    async () =>
      assert.equal(
        await page
          .locator(".turn[data-message-id]")
          .filter({ hasText: retry })
          .count(),
        1,
      ),
    "retry reaches one run",
  );
  assert.equal(attempts.length, 2);
  assert.deepEqual(attempts[0], attempts[1]);
  assert.equal(
    (await api("GET", "/runs?sessionId=" + b.defaultSessionId)).filter(
      (r) => r.text === retry,
    ).length,
    1,
  );
  report.checks.push(
    "Confirmed missing request retries with identical event ID and body",
  );
  report.passed = true;
  await page.screenshot({ path: path.join(dir, "reconciled.png") });
} catch (e) {
  if (page) await page.screenshot({ path: path.join(dir, "failure.png") });
  report.error = e.stack;
  process.exitCode = 1;
  console.error(e);
} finally {
  await browser.close();
  for (const p of projects) {
    for (const s of await api("GET", "/projects/" + p.id + "/sessions")) {
      for (const r of await api("GET", "/runs?sessionId=" + s.id))
        if (["queued", "running", "waiting", "stopping"].includes(r.state))
          await api("POST", "/runs/" + r.id + "/stop", {});
      await wait(
        async () =>
          assert(
            (await api("GET", "/runs?sessionId=" + s.id)).every(
              (r) =>
                !["queued", "running", "waiting", "stopping"].includes(r.state),
            ),
          ),
        "cleanup waits for stop",
      );
    }
    await api("DELETE", "/projects/" + p.id);
  }
  await fs.writeFile(
    path.join(dir, "report.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(JSON.stringify(report, null, 2));
}
