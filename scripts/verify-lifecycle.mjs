import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
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
const dir = path.resolve("artifacts/lifecycle");
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
const wait = async (fn, label, timeout = 90000) => {
  const end = Date.now() + timeout;
  let err;
  while (Date.now() < end) {
    try {
      return await fn();
    } catch (e) {
      err = e;
      await new Promise((r) => setTimeout(r, 200));
    }
  }
  throw new Error(label + ": " + err);
};
const browser = await chromium.launch({
  headless: true,
  executablePath:
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
let p;
let ss;
const report = { passed: false, url, checks: [] };
try {
  p = await api("POST", "/projects", { name: "生命周期验收 " + Date.now() });
  ss = await api("POST", `/projects/${p.id}/sessions`, {
    title: "预算与培训计划",
  });
  await api("POST", `/projects/${p.id}/sessions`, { title: "后创建的对话" });
  await page.goto(url + "/#project=" + p.id + "&session=" + ss.id);
  await page.getByLabel("密码", { exact: true }).fill(cfg.password);
  await page.getByRole("button", { name: "进入工作台" }).click();
  await page.getByLabel("发送消息", { exact: true }).waitFor();
  let row = page.locator(".session-row").filter({ hasText: "预算与培训计划" });
  await row.waitFor();
  await row.getByRole("button", { name: /^对话操作：/ }).click();
  await row.getByRole("menuitem", { name: "置顶", exact: true }).click();
  await wait(
    async () =>
      assert.equal(
        await page
          .locator(".session-row")
          .first()
          .getAttribute("data-session-id"),
        ss.id,
      ),
    "pin order",
  );
  await page.reload();
  await wait(
    async () =>
      assert.equal(
        await page
          .locator(".session-row")
          .first()
          .getAttribute("data-session-id"),
        ss.id,
      ),
    "pin survives reload",
  );
  await page
    .getByLabel("发送消息", { exact: true })
    .fill(
      "已确认事实：客户代号松果314，预算8000元。待办是周五完成培训计划。请仅回复已记录，不调用工具。",
    );
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await wait(async () => {
    const runs = await api("GET", "/runs?sessionId=" + ss.id);
    assert.equal(runs.length, 1);
    assert.equal(runs[0].state, "completed", runs[0].error);
  }, "first task");
  report.checks.push("Pin and refresh preserve session identity and order");
  row = page.locator(".session-row").filter({ hasText: "预算与培训计划" });
  await row.getByRole("button", { name: /^对话操作：/ }).click();
  await row.getByRole("menuitem", { name: "归档", exact: true }).click();
  await page.getByRole("button", { name: "开始归档", exact: true }).click();
  const recycled = await wait(
    async () => {
      const rows = await api("GET", "/recycle-bin");
      const hit = rows.find((s) => s.id === ss.id);
      assert(hit);
      return hit;
    },
    "native archive",
    120000,
  );
  assert.equal(recycled.recycleReason, "archive");
  assert(recycled.archivePath.startsWith("对话归档/"));
  const file = await api(
    "GET",
    `/projects/${p.id}/file?path=${encodeURIComponent(recycled.archivePath)}`,
  );
  assert(file.content.includes("松果314"));
  assert(file.content.includes("8000"));
  report.archivePath = recycled.archivePath;
  assert.equal((await api("GET", "/runs?sessionId=" + ss.id)).length, 0);
  assert.equal(
    (await api("GET", `/projects/${p.id}/history-search?q=松果314`)).length,
    0,
  );
  await wait(
    async () =>
      assert.equal(
        await page
          .locator(".session-row")
          .filter({ hasText: "预算与培训计划" })
          .count(),
        0,
      ),
    "sidebar removes original",
  );
  report.checks.push(
    "Archive persists factual MD before recycle; original disappears from sidebar and history search",
  );
  await page
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: "设置", exact: true })
    .click();
  const bin = page.locator("section").filter({
    has: page.getByRole("heading", { name: "对话回收站", exact: true }),
  });
  const trashed = bin
    .locator(".setting-row")
    .filter({ hasText: "预算与培训计划" });
  await trashed.waitFor();
  await bin.screenshot({
    path: path.join(dir, (live ? "live-" : "") + "recycle.png"),
  });
  await trashed.getByRole("button", { name: "找回", exact: true }).click();
  await wait(async () => {
    const restored = await api("GET", "/sessions/" + ss.id);
    assert.equal(restored.harnessId, ss.harnessId);
    assert(!restored.recycledAt);
  }, "restore stable history");
  const before = await api("GET", "/runs?sessionId=" + ss.id);
  assert.equal(before.length, 1);
  assert.equal(before[0].state, "completed");
  assert.equal(
    (
      await api(
        "GET",
        `/projects/${p.id}/file?path=${encodeURIComponent(recycled.archivePath)}`,
      )
    ).content,
    file.content,
  );
  await page.goto(url + "/#project=" + p.id + "&session=" + ss.id);
  await page.reload();
  await page.getByLabel("发送消息", { exact: true }).waitFor();
  await wait(
    async () => assert.equal(await page.locator(".user-message p").count(), 1),
    "restored history shown",
  );
  row = page.locator(".session-row").filter({ hasText: "预算与培训计划" });
  await row.getByRole("button", { name: /^对话操作：/ }).click();
  await row.getByRole("menuitem", { name: "删除", exact: true }).click();
  await page.getByRole("button", { name: "移入回收站", exact: true }).click();
  await wait(
    async () =>
      assert(
        (await api("GET", "/recycle-bin")).some(
          (s) => s.id === ss.id && s.recycleReason === "delete",
        ),
      ),
    "delete to recycle",
  );
  assert.equal(
    (
      await api(
        "GET",
        `/projects/${p.id}/file?path=${encodeURIComponent(recycled.archivePath)}`,
      )
    ).content,
    file.content,
  );
  await api("POST", "/recycle-bin/" + ss.id + "/restore", {});
  await page.goto(url + "/#project=" + p.id + "&session=" + ss.id);
  await page.reload();
  await page.getByLabel("发送消息", { exact: true }).waitFor();
  await page.screenshot({
    path: path.join(dir, (live ? "live-" : "") + "restored.png"),
  });
  await page
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: "设置", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "全局 CLI 工具", exact: false })
    .waitFor();
  const globalTools = page
    .locator("section")
    .filter({
      has: page.getByRole("heading", { name: "全局 CLI 工具", exact: false }),
    });
  await globalTools.getByText("飞书 CLI", { exact: true }).waitFor();
  await globalTools.getByText("CRM CLI", { exact: true }).waitFor();
  const installedTools = await api("GET", "/cli-tools");
  if (live || url.endsWith(":3094")) {
    for (const id of ["lark", "aicrm"])
      assert(installedTools.some((t) => t.id === id && t.installed && t.enabled && t.authStatus === "ready"), "CLI login not ready: " + id);
    await wait(async () => assert.equal(await globalTools.getByText(/已安装 · .* · 全局启用 · 已授权/).count(), 2), "installed CLI status visible");
  }
  await globalTools.screenshot({
    path: path.join(dir, (live ? "live-" : "") + "global-tools.png"),
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(350);
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await globalTools.screenshot({
    path: path.join(dir, (live ? "live-" : "") + "mobile-tools.png"),
  });
  report.checks.push(
    "Restore retains Harness ID, history and MD without replay; delete also retains project files; global tools settings and mobile layout accessible",
  );
  report.passed = true;
} catch (e) {
  report.error = String(e);
  await page.screenshot({
    path: path.join(dir, (live ? "live-" : "") + "failure.png"),
  });
  throw e;
} finally {
  await browser.close();
  if (p) {
    const all = await api("GET", "/runs");
    for (const r of all.filter(
      (r) =>
        r.projectId === p.id &&
        ["running", "waiting", "queued"].includes(r.state),
    )) {
      await api("POST", "/runs/" + r.id + "/stop", {});
      await wait(
        async () =>
          assert(
            !["running", "waiting", "stopping", "queued"].includes(
              (await api("GET", "/runs/" + r.id)).state,
            ),
          ),
        "stop cleanup",
      );
    }
    await api("DELETE", "/projects/" + p.id);
  }
  await fs.writeFile(
    path.join(dir, (live ? "live-" : "") + "report.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(JSON.stringify(report));
}
