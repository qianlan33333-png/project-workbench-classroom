import fs from "node:fs/promises";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { chromium } = require(
  process.env.PLAYWRIGHT_MODULE ||
    "/Users/qianlan/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright",
);
const base = process.env.WORKBENCH_URL || "http://127.0.0.1:3082/";
const password = (
  await fs.readFile(process.env.PASSWORD_FILE || ".var/test-password", "utf8")
).trim();
await fs.mkdir("artifacts", { recursive: true });
const browser = await chromium.launch({ headless: true, channel: "chrome" });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
try {
  await page.goto(base);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "进入工作台" }).click();
  await page.getByRole("heading", { name: "项目", exact: true }).waitFor();
  const name = "浏览器验收 " + Date.now();
  await page
    .getByRole("button", { name: "新建项目", exact: true })
    .first()
    .click();
  await page.getByLabel("项目名称").fill(name);
  await page.getByLabel("简短说明").fill("文件与持久对话的浏览器验收");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "保存", exact: true })
    .click();
  await page.getByRole("button", { name: "新建文档", exact: true }).waitFor();
  await page.getByRole("button", { name: "新建文档", exact: true }).click();
  await page.getByLabel("文件路径").fill("资料/验收.md");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "保存", exact: true })
    .click();
  await page.locator(".cm-content").waitFor();
  await page.locator(".cm-content").click();
  await page.keyboard.insertText(
    "# 浏览器验收\n\n中文资料可编辑，[[相关文档]] 保留。",
  );
  await page
    .locator(".editor-pane")
    .getByRole("button", { name: "保存", exact: true })
    .click();
  await page.getByRole("alert").filter({ hasText: "已保存" }).waitFor();
  await page.reload();
  await page.getByRole("heading", { name: "项目", exact: true }).waitFor();
  await page
    .getByRole("button", { name: new RegExp(name) })
    .last()
    .click();
  await page.getByRole("button", { name: "验收.md", exact: true }).click();
  if (
    !(await page.locator(".cm-content").innerText()).includes("中文资料可编辑")
  )
    throw new Error("Saved document did not survive refresh");
  await page.getByRole("button", { name: "预览", exact: true }).click();
  await page
    .locator(".document-preview")
    .getByRole("heading", { name: "浏览器验收" })
    .waitFor();
  await page.screenshot({
    path: "artifacts/workspace-desktop.png",
    fullPage: true,
  });
  await page.getByRole("button", { name: "关闭编辑器" }).click();
  await page
    .getByRole("button", { name: "另开对话", exact: true })
    .first()
    .click();
  await page.getByLabel("对话名称").fill("专题：招聘");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "保存", exact: true })
    .click();
  await page.getByLabel("当前对话").selectOption({ label: "专题：招聘" });
  await page.getByRole("button", { name: "专家库", exact: true }).click();
  await page.getByRole("heading", { name: "专家库", exact: true }).waitFor();
  await page.getByRole("button", { name: "创建专家", exact: true }).click();
  await page.getByLabel("名称", { exact: true }).fill("验收专家");
  await page.getByLabel("能力简介").fill("只提供通用方法");
  await page
    .getByLabel("方法与工作准则")
    .fill("先读取当前项目资料，明确目标，再给出结果。");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "保存", exact: true })
    .click();
  await page.getByRole("heading", { name: "验收专家", exact: true }).waitFor();
  await page.screenshot({
    path: "artifacts/experts-desktop.png",
    fullPage: true,
  });
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("heading", { name: "执行核心" }).waitFor();
  await page.screenshot({
    path: "artifacts/settings-desktop.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "打开导航" }).click();
  await page.getByRole("button", { name: "项目", exact: true }).click();
  await page.screenshot({
    path: "artifacts/workspace-mobile.png",
    fullPage: true,
  });
  if (errors.length) throw new Error(errors.join("\n"));
  console.log(
    "PASS: login, empty workspace, create project, edit/save/reload, preview, topic, expert, settings, responsive layout; no page errors",
  );
} finally {
  await browser.close();
}
