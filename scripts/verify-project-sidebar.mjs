// Isolated browser acceptance. The fixture API must use a disposable data directory.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";

const directory = ".var/sidebar-preview";
const { url, password } = JSON.parse(
  await fs.readFile(directory + "/browser.json", "utf8"),
);
assert.match(url, /^http:\/\/127\.0\.0\.1:\d+$/);
const browser = await chromium.launch({
  executablePath:
    process.env.PW_CHROME ||
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
});
const context = await browser.newContext({
  viewport: { width: 1440, height: 960 },
});
const page = await context.newPage();
page.setDefaultTimeout(8000);
const errors = [];
page.on("pageerror", (error) => errors.push(error.message));
page.on("console", (msg) => {
  if (msg.type() === "error" && /same key|React error/.test(msg.text()))
    errors.push(msg.text());
});
async function api(route, method = "GET", data) {
  const response = await context.request.fetch(url + "/api/v1" + route, {
    method,
    data,
  });
  assert(response.ok(), route + " " + (await response.text()));
  return response.json();
}
async function seed(name, file) {
  const project = await api("/projects", "POST", { name });
  await api("/projects/" + project.id + "/file", "PUT", {
    path: file,
    content: "# " + name,
    baseHash: "",
  });
  const task = await api("/projects/" + project.id + "/tasks", "POST", {
    title: name + "任务",
    operation: crypto.randomUUID(),
  });
  await api("/schedules", "POST", {
    projectId: project.id,
    name: name + "定时",
    instruction: "只做核对",
    cron: "0 9 * * *",
    enabled: false,
  });
  return { project, task };
}
const navigation = page.getByRole("navigation", {
  name: "项目导航",
  exact: true,
});
const select = (name) =>
  navigation.getByRole("button", { name, exact: true }).click();
const projectLink = (p, extra = {}) =>
  url +
  "/#" +
  new URLSearchParams({ project: p.id, session: p.defaultSessionId, ...extra });
const files = page.getByPlaceholder("搜索资料，回车查全文…", { exact: true });
try {
  await api("/login", "POST", { password });
  const a = await seed("右栏甲", "甲.md");
  const b = await seed("右栏乙", "乙.md");
  await page.goto(projectLink(a.project));
  await navigation
    .getByRole("button", { name: "对话", exact: true, pressed: true })
    .waitFor();
  const input = page.getByLabel("发送消息", { exact: true });
  await input.fill("保留输入草稿");
  await page.evaluate(
    () => (window.originalChat = document.querySelector(".chat")),
  );
  for (let i = 0; i < 8; i++) {
    for (const [name, count] of [
      ["资料", 1],
      ["任务", 0],
      ["定时", 0],
      ["对话", 0],
    ]) {
      await select(name);
      assert.equal(await files.count(), count, name + " 残留了资料区");
      assert.equal(
        await page.getByText("合并前的记录与配置", { exact: true }).count(),
        0,
      );
      assert.equal(await page.locator(".project-context-view").count(), 1);
    }
  }
  await select("资料");
  const late = "**/api/v1/projects/" + a.project.id + "/file?view=1&path=*";
  let resolveFile;
  const intercepted = new Promise((resolve) => {
    resolveFile = resolve;
  });
  await page.route(late, (route) => resolveFile(route));
  await page.locator('button[title="甲.md"]').click();
  const pending = await intercepted;
  await select("任务");
  await pending.continue();
  await page.unroute(late);
  await page.getByRole("button", { name: "新增任务", exact: true }).waitFor();
  assert.equal(await page.locator(".project-inline-detail").count(), 0);
  await select("资料");
  await page.locator('button[title="甲.md"]').click();
  await page
    .getByRole("button", { name: "返回资料列表", exact: true })
    .waitFor();
  assert.equal(await files.count(), 0);
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  await page.locator(".project-inline-detail .cm-content").fill("未保存草稿");
  await select("任务");
  await page.getByRole("button", { name: "取消", exact: true }).click();
  assert.equal(
    await page.locator(".project-inline-detail .cm-content").innerText(),
    "未保存草稿",
  );
  await select("任务");
  await page.getByRole("button", { name: /放弃/ }).click();
  await page.getByRole("button", { name: /右栏甲任务/ }).click();
  await page.getByRole("button", { name: "返回任务列表", exact: true }).click();
  await page.getByRole("button", { name: "新增任务", exact: true }).click();
  const editor = page.getByRole("dialog", { name: "新增任务", exact: true });
  await editor.getByLabel("标题", { exact: true }).fill("跨断点保留的任务草稿");
  await page.setViewportSize({ width: 900, height: 900 });
  assert.equal(
    await editor.getByLabel("标题", { exact: true }).inputValue(),
    "跨断点保留的任务草稿",
  );
  page.once("dialog", (dialog) => dialog.accept());
  await editor
    .getByRole("button", { name: "关闭新增任务", exact: true })
    .click();
  await editor.waitFor({ state: "detached" });
  await page.setViewportSize({ width: 1440, height: 960 });
  await select("定时");
  await page.getByRole("button", { name: /右栏甲定时/ }).click();
  await page.getByRole("button", { name: "返回定时列表", exact: true }).click();
  assert.equal(await input.inputValue(), "保留输入草稿");
  assert(
    await page.evaluate(
      () => window.originalChat === document.querySelector(".chat"),
    ),
  );
  await page.getByRole("button", { name: "收起项目侧栏", exact: true }).click();
  await page.reload();
  await navigation.waitFor();
  assert.equal(await page.locator(".project-context-body").count(), 0);
  await select("资料");
  await page.screenshot({ path: directory + "/desktop.png" });
  await input.fill("项目甲专属草稿");
  await page.goto(projectLink(b.project, { file: "乙.md" }));
  await page
    .getByRole("button", { name: "返回资料列表", exact: true })
    .waitFor();
  assert.equal(
    await page
      .locator(".project-inline-detail header")
      .getByText("乙.md")
      .count(),
    1,
  );
  assert.equal(await input.inputValue(), "");
  await page.goto(
    url +
      "/#" +
      new URLSearchParams({ project: a.project.id, task: a.task.id }),
  );
  await page
    .getByRole("button", { name: "返回任务列表", exact: true })
    .waitFor();
  assert.equal(
    await page
      .getByRole("heading", { name: "右栏甲任务", exact: true })
      .count(),
    1,
  );
  assert.equal(await input.inputValue(), "项目甲专属草稿");
  await page.evaluate(
    (id) => localStorage.setItem("pwb.projectPanelClosed." + id, "true"),
    a.project.id,
  );
  await page.reload();
  await page
    .getByRole("button", { name: "返回任务列表", exact: true })
    .waitFor();
  await page.setViewportSize({ width: 900, height: 900 });
  await select("资料");
  await page.getByRole("dialog", { name: "项目侧栏", exact: true }).waitFor();
  await page.keyboard.press("Escape");
  await page.setViewportSize({ width: 390, height: 844 });
  await select("资料");
  const drawer = page.getByRole("dialog", { name: "项目侧栏", exact: true });
  await drawer.waitFor();
  assert.equal(await files.count(), 1);
  await page.screenshot({ path: directory + "/mobile.png" });
  await page.keyboard.press("Escape");
  await drawer.waitFor({ state: "detached" });
  assert(await input.isVisible());
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  );
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      passed: true,
      switches: 32,
      desktop: 1440,
      tablet: 900,
      mobile: 390,
      pageErrors: errors.length,
    }),
  );
} catch (error) {
  console.error(
    JSON.stringify({
      url: page.url(),
      controls: await navigation.innerText().catch(() => ""),
      errors,
    }),
  );
  await page.screenshot({ path: directory + "/failure.png" });
  throw error;
} finally {
  await browser.close();
}
