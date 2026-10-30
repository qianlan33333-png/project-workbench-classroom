import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";
const cfg = JSON.parse(
  await fs.readFile(
    process.env.PWB_CONFIG ||
      path.join(os.homedir(), ".config/project-workbench/cli.json"),
    "utf8",
  ),
);
const url = cfg.url.replace(/\/$/, "");
const auth = "Basic " + Buffer.from("owner:" + cfg.password).toString("base64");
const api = async (method, route, body) => {
  const r = await fetch(url + "/api/v1" + route, {
    method,
    headers: { Authorization: auth, "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  assert.equal(r.status, 200, await r.clone().text());
  return r.json();
};
const dir = path.resolve("artifacts/message-feedback");
await fs.mkdir(dir, { recursive: true });
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.PW_CHROME ||
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
let project, run;
const report = { passed: false, url };
try {
  project = await api("POST", "/projects", { name: "回显实测 " + Date.now() });
  await page.goto(
    url + "/#project=" + project.id + "&session=" + project.defaultSessionId,
    { waitUntil: "domcontentloaded" },
  );
  await page.getByLabel("密码", { exact: true }).fill(cfg.password);
  await page.getByRole("button", { name: "进入工作台" }).click();
  await page
    .getByLabel("发送消息", { exact: true })
    .fill("回显验收：请只回复“收到”，不要调用工具。");
  const started = Date.now();
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.locator(".user-message p").waitFor();
  report.messageVisibleMs = Date.now() - started;
  assert(report.messageVisibleMs < 1000, "message bubble is still delayed");
  await page.screenshot({ path: path.join(dir, "production-sending.png") });
  await page.locator(".turn[data-message-id]").waitFor({ timeout: 15000 });
  report.runVisibleMs = Date.now() - started;
  for (let i = 0; i < 40; i++) {
    const rows = await api(
      "GET",
      "/runs?sessionId=" + project.defaultSessionId,
    );
    if (rows.length) {
      assert.equal(rows.length, 1);
      run = rows[0];
      if (
        ["completed", "failed", "cancelled", "interrupted"].includes(run.state)
      )
        break;
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  report.backendState = run?.state;
  report.modelResult = run?.result;
  assert.equal(run?.state, "completed", run?.error);
  assert(run.result.includes("收到"));
  assert.equal(await page.locator(".user-message p").count(), 1);
  await page
    .locator(".assistant-message .markdown")
    .filter({ hasText: "收到" })
    .waitFor({ timeout: 10000 });
  report.completedMs = Date.now() - started;
  report.runId = run.id;
  report.serverQueueMs = run.startedAt - run.createdAt;
  report.passed = true;
  await page.screenshot({ path: path.join(dir, "production-completed.png") });
} catch (error) {
  report.error = String(error);
  await page.screenshot({ path: path.join(dir, "production-failure.png") });
  throw error;
} finally {
  await browser.close();
  if (project) {
    const rows = await api(
      "GET",
      "/runs?sessionId=" + project.defaultSessionId,
    );
    for (const r of rows)
      if (["queued", "running", "waiting", "stopping"].includes(r.state)) {
        await api("POST", "/runs/" + r.id + "/stop", {});
        for (let i = 0; i < 30; i++) {
          if (
            !["queued", "running", "waiting", "stopping"].includes(
              (await api("GET", "/runs/" + r.id)).state,
            )
          )
            break;
          await new Promise((r) => setTimeout(r, 1000));
        }
      }
    await api("DELETE", "/projects/" + project.id);
  }
  await fs.writeFile(
    path.join(dir, "production-report.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(JSON.stringify(report, null, 2));
}
