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
const dir = path.resolve("artifacts/composer");
await fs.mkdir(dir, { recursive: true });
const prefix = live ? "live-" : url.endsWith(":3094") ? "cloud-" : "";
async function api(method, route, body) {
  const r = await fetch(url + "/api/v1" + route, {
    method,
    headers: { Authorization: auth, "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  const v = await r.json();
  assert.equal(r.status, 200, JSON.stringify(v));
  return v;
}
async function wait(fn, label, timeout = 90000) {
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
  throw Error(label + ": " + err);
}
const browser = await chromium.launch({
  headless: true,
  executablePath:
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const projects = [];
let expert, plugin;
const report = { passed: false, url, checks: [], runs: [] };
try {
  const p = await api("POST", "/projects", {
    name: "斜杠交互 A " + Date.now(),
  });
  projects.push(p);
  const q = await api("POST", "/projects", {
    name: "斜杠交互 B " + Date.now(),
  });
  projects.push(q);
  const original = await api("GET", "/sessions/" + p.defaultSessionId);
  expert = await api("POST", "/experts", {
    name: "斜杠方法验收",
    description: "仅用于方法选择验收",
    instructions:
      "方法校验口令是 SKILL_SLASH_314。用户询问方法口令时必须先读本 Skill 入口，再原样回答此口令。不执行外部业务操作。",
  });
  await api("PATCH", "/projects/" + p.id, { expertIds: [expert.id] });
  const cli = (await api("GET", "/projects/" + p.id + "/capabilities")).find(
    (c) =>
      c.kind === "cli" && c.id === (url.endsWith(":3091") ? "fixture" : "lark"),
  );
  assert(cli, "CLI missing");
  if (!live) {
    plugin = await api("POST", "/plugins", {
      name: "斜杠MCP验收",
      transport: "stdio",
      command: url.endsWith(":3091")
        ? path.resolve(".toolchain/node-v24.22.0-darwin-arm64/bin/node")
        : "node",
      args: [
        url.endsWith(":3091")
          ? path.resolve("runtime/test-mcp.mjs")
          : "/opt/workbench-runtime/test-mcp.mjs",
      ],
      enabled: true,
    });
  }
  await page.goto(url + "/#project=" + p.id + "&session=" + p.defaultSessionId);
  await page.getByLabel("密码", { exact: true }).fill(cfg.password);
  await page.getByRole("button", { name: "进入工作台" }).click();
  const input = page.getByLabel("发送消息", { exact: true });
  await input.waitFor();
  const select = page.getByLabel("当前专家", { exact: true });
  assert.equal(await select.locator("option").count(), 2);
  assert.equal(
    await select.locator("option").filter({ hasText: "通用助手" }).count(),
    1,
  );
  assert.equal(await select.inputValue(), "general");
  await input.fill("/");
  const menu = page.getByRole("listbox", { name: "选择本条消息的能力" });
  await menu.getByRole("option").filter({ hasText: cli.name }).waitFor();
  await menu.getByRole("option").filter({ hasText: expert.name }).waitFor();
  if (plugin)
    await menu.getByRole("option").filter({ hasText: plugin.name }).waitFor();
  await page.screenshot({ path: path.join(dir, prefix + "slash-menu.png") });
  await input.press("Escape");
  await wait(async () => assert.equal(await menu.count(), 0), "escape closes");
  assert.equal(await input.inputValue(), "/");
  await input.fill("/" + cli.command);
  await menu.getByRole("option").filter({ hasText: cli.name }).waitFor();
  await input.press("Tab");
  await page
    .getByRole("button", { name: "移除 CLI " + cli.name, exact: true })
    .waitFor();
  assert.equal(await input.inputValue(), "");
  assert.equal(
    (await api("GET", "/runs?sessionId=" + p.defaultSessionId)).length,
    0,
  );
  await input.fill("A 的未发送草稿");
  await page
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: q.name, exact: true })
    .click();
  assert.equal(await input.inputValue(), "");
  assert.equal(
    await page
      .getByRole("button", { name: "移除 CLI " + cli.name, exact: true })
      .count(),
    0,
  );
  await input.fill("/");
  await menu.getByRole("option").filter({ hasText: cli.name }).waitFor();
  assert.equal(
    await menu.getByRole("option").filter({ hasText: expert.name }).count(),
    0,
  );
  await input.press("Escape");
  await input.fill("B 的未发送草稿");
  await page
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: p.name, exact: true })
    .click();
  assert.equal(await input.inputValue(), "A 的未发送草稿");
  await page
    .getByRole("button", { name: "移除 CLI " + cli.name, exact: true })
    .waitFor();
  report.checks.push(
    "Slash catalog respects project Skill scope; keyboard selection and Escape do not submit; capability chips and text are isolated by conversation draft",
  );
  async function sendAndWait(text, kind, id, expected) {
    await input.fill(text);
    await page.getByRole("button", { name: "发送", exact: true }).click();
    const r = await wait(
      async () => {
        const rows = await api("GET", "/runs?sessionId=" + p.defaultSessionId);
        const r = rows.find((r) => r.text === text);
        assert(r);
        assert.equal(r.state, "completed", r.error);
        return r;
      },
      "real " + kind,
      120000,
    );
    assert.equal(r.capabilities.length, 1);
    assert.equal(r.capabilities[0].kind, kind);
    assert.equal(r.capabilities[0].id, id);
    assert(r.result.includes(expected), r.result);
    assert(
      (await api("GET", "/events?json=1&sessionId=" + p.defaultSessionId)).some(
        (e) => e.runId === r.id && e.type === "harness.tool/call",
      ),
    );
    report.runs.push({ kind, runId: r.id, result: r.result });
    assert.equal(
      (await api("GET", "/sessions/" + p.defaultSessionId)).harnessId,
      original.harnessId,
    );
    await wait(
      async () => assert.equal(await page.locator(".pending-turn").count(), 0),
      "receipt reconciles",
    );
    assert.equal(
      await page.locator(".composer .capability-chips button").count(),
      0,
    );
  }
  await sendAndWait(
    "请实际调用我选中的 CLI 的 --version，只报告版本，不登录、不修改配置、不进行任何业务操作。",
    "cli",
    cli.id,
    cli.id === "fixture" ? "FIXTURE_CLI_VERSION_1" : "1.0.98",
  );
  const crm = (await api("GET", "/projects/" + p.id + "/capabilities")).find(
    (c) => c.kind === "cli" && c.id === "aicrm",
  );
  if (crm) {
    await input.fill("/" + crm.command);
    await menu.getByRole("option").filter({ hasText: crm.name }).click();
    await sendAndWait(
      "请实际调用我选中的 CLI 的 version 子命令，只报告客户端版本，不登录、不修改配置、不进行任何业务操作。",
      "cli",
      crm.id,
      "0.6.1",
    );
  }
  await input.fill("/" + expert.name);
  await menu.getByRole("option").filter({ hasText: expert.name }).click();
  await sendAndWait(
    "请读取我指定 Skill 的入口，告诉我方法里的校验口令。不要联网或修改文件。",
    "skill",
    expert.id,
    "SKILL_SLASH_314",
  );
  if (plugin) {
    await input.fill("/" + plugin.name);
    await menu.getByRole("option").filter({ hasText: plugin.name }).click();
    await sendAndWait(
      "使用我指定 MCP 的 echo 工具，传 text=SLASH_MCP_271，原样回复工具结果。不要调用其他服务。",
      "mcp",
      plugin.id,
      "GLOBAL_MCP:SLASH_MCP_271",
    );
  }
  assert.equal(
    (await api("GET", "/sessions/" + p.defaultSessionId)).expertId,
    "",
  );
  report.checks.push(
    plugin
      ? "Real Harness CLI, selected Skill and MCP use the same persistent Session; selection clears after send and never changes the default expert"
      : "Real Harness Feishu and CRM CLI and selected Skill use the same persistent Session; selection clears after send and never changes the default expert",
  );
  await api("PATCH", "/projects/" + p.id, { defaultExpertId: expert.id });
  await page.reload();
  await input.waitFor();
  await wait(
    async () => assert.equal(await select.inputValue(), expert.id),
    "effective project default",
  );
  assert.equal(await select.locator("option").count(), 2);
  assert.equal(
    await select.locator("option").filter({ hasText: expert.name }).count(),
    1,
  );
  await input.fill("https://example.com/a/b");
  assert.equal(await menu.count(), 0);
  await input.press("Shift+Enter");
  assert((await input.inputValue()).includes("\n"));
  await input.fill("/" + cli.command);
  await menu.getByRole("option").filter({ hasText: cli.name }).waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(200);
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.screenshot({ path: path.join(dir, prefix + "slash-mobile.png") });
  await input.press("Escape");
  await page.setViewportSize({ width: 1440, height: 1000 });
  let currentJobs = [
    {
      id: "history-check",
      toolId: cli.id,
      action: "check",
      state: "completed",
      output: "old diagnostic output",
    },
  ];
  await page.route("**/api/v1/tool-jobs", (r) =>
    r.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(currentJobs),
    }),
  );
  await page
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: "设置", exact: true })
    .click();
  const globalTools = page
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "全局 CLI 工具" }) });
  await globalTools.getByText(cli.name, { exact: true }).waitFor();
  assert.equal(await globalTools.locator(".tool-job").count(), 0);
  assert.equal(await globalTools.locator(".tool-current-operation").count(), 0);
  assert(!(await globalTools.innerText()).includes("old diagnostic output"));
  await globalTools.screenshot({
    path: path.join(dir, prefix + "clean-tool-settings.png"),
  });
  currentJobs = [
    {
      id: "active-auth",
      toolId: cli.id,
      action: "auth",
      state: "running",
      output: '{"verification_url":"https://example.com/authorize?code=TEST"}',
    },
  ];
  await globalTools.getByRole("link", { name: "打开授权链接" }).waitFor();
  assert.equal(
    await globalTools
      .getByRole("link", { name: "打开授权链接" })
      .getAttribute("href"),
    "https://example.com/authorize?code=TEST",
  );
  currentJobs[0].state = "completed";
  await wait(
    async () =>
      assert.equal(
        await globalTools.locator(".tool-current-operation").count(),
        0,
      ),
    "completed operation disappears",
  );
  report.checks.push(
    "One effective expert option per mode; completed CLI history is hidden while an active authorization link remains usable; 390px has no horizontal overflow",
  );
  report.passed = true;
} catch (e) {
  report.error = String(e);
  await page.screenshot({ path: path.join(dir, prefix + "failure.png") });
  throw e;
} finally {
  await browser.close();
  for (const p of projects) {
    for (const r of await api("GET", "/runs?sessionId=" + p.defaultSessionId)) {
      if (["queued", "running", "waiting", "stopping"].includes(r.state)) {
        await api("POST", "/runs/" + r.id + "/stop", {});
        await wait(
          async () =>
            assert(
              !["queued", "running", "waiting", "stopping"].includes(
                (await api("GET", "/runs/" + r.id)).state,
              ),
            ),
          "stop cleanup",
        );
      }
    }
    await api("DELETE", "/projects/" + p.id);
  }
  if (expert)
    await api("DELETE", "/experts/" + expert.id, { baseDigest: expert.digest });
  if (plugin) await api("DELETE", "/plugins/" + plugin.id);
  await fs.writeFile(
    path.join(dir, prefix + "report.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(JSON.stringify(report));
}
