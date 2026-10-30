import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { zipSync, strToU8 } from "../web/node_modules/fflate/esm/browser.js";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";
const url = process.env.PW_UI_URL || "http://127.0.0.1:3091";
const password = process.env.PWB_PASSWORD || "workbench-ux-test";
const artifacts = path.resolve("artifacts/workspace-ui");
await fs.mkdir(artifacts, { recursive: true });
const authorization =
  "Basic " + Buffer.from("owner:" + password).toString("base64");
const request = async (method, route, body) => {
  const r = await fetch(url + "/api/v1" + route, {
    method,
    headers: {
      Authorization: authorization,
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const v = await r.json();
  assert.equal(r.status, 200, JSON.stringify(v));
  return v;
};
const tag = Date.now();
const editedName = "编辑后的方法专家 " + tag;
const fixtureDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "pwb-ui-"));
const created = [],
  experts = [];
const failures = [];
const screenshots = [];
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.PW_CHROME ||
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
});
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
  deviceScaleFactor: 1,
});
const page = await context.newPage();
page.on("pageerror", (e) => failures.push(e.message));
page.on("dialog", (dialog) => dialog.accept());
const check = async (fn, label) => {
  const start = Date.now();
  for (;;) {
    try {
      await fn();
      return;
    } catch (e) {
      if (Date.now() - start > 15000) throw new Error(label + ": " + e.message);
      await new Promise((r) => setTimeout(r, 100));
    }
  }
};
const screenshot = async (name) => {
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise((r) =>
      requestAnimationFrame(() => requestAnimationFrame(r)),
    );
  });
  await page.waitForTimeout(200);
  const file = path.join(artifacts, name + ".png");
  await page.screenshot({ path: file, fullPage: true });
  screenshots.push(file);
};
try {
  const p = await request("POST", "/projects", {
    name: "交互验收 " + Date.now(),
    description: "测试项目，验收结束自动清理",
  });
  created.push(p.id);
  const p2 = await request("POST", "/projects", {
    name: "第二个验收项目 " + Date.now(),
  });
  created.push(p2.id);
  const ex = await request("POST", "/experts", {
    name: "方法专家验收 " + Date.now(),
    description: "方法组合包测试",
    instructions: "先阅读当前项目资料，再输出清晰的行动清单。",
  });
  experts.push(ex.id);
  await request("PUT", `/projects/${p.id}/file`, {
    path: "README.md",
    content:
      "# 项目资料\n\n这是独立的界面验收项目。\n\n- 对话在中间\n- 文档在右侧\n",
    baseHash: "",
  });
  await page.goto(url, { waitUntil: "domcontentloaded" });
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "进入工作台" }).click();
  await page
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: p.name, exact: true })
    .click();
  await check(async () => {
    assert.equal(
      await page
        .locator(".rail")
        .evaluate((el) => Math.round(el.getBoundingClientRect().width)),
      80,
    );
    assert.equal(await page.locator(".project-dot").count(), 0);
    assert.equal(
      await page.locator(".panel-tabs .selected").innerText(),
      "对话",
    );
  }, "Default layout");
  await page.getByLabel("发送消息").fill("这是一段尚未发送的草稿");
  await page.evaluate(() => {
    window.__chatNode = document.querySelector(".chat");
  });
  await page.getByRole("button", { name: "文件", exact: true }).click();
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await check(async () => {
    const a = await page.locator(".chat").boundingBox(),
      b = await page.locator(".inspector").boundingBox();
    assert(a && b && a.x < b.x);
    assert.equal(
      await page.getByLabel("发送消息").inputValue(),
      "这是一段尚未发送的草稿",
    );
    assert(
      await page.evaluate(
        () => window.__chatNode === document.querySelector(".chat"),
      ),
    );
  }, "Document is right of stable chat");
  const beforeResize = await page.locator(".inspector").boundingBox();
  const handle = page.getByRole("separator", { name: "调整详情宽度" });
  if (await handle.count()) {
    const box = await handle.boundingBox();
    await page.mouse.move(box.x + 2, box.y + 50);
    await page.mouse.down();
    await page.mouse.move(box.x - 45, box.y + 50);
    await page.mouse.up();
    assert(
      await page.evaluate(
        () => window.__chatNode === document.querySelector(".chat"),
      ),
    );
    assert.equal(
      await page.getByLabel("发送消息").inputValue(),
      "这是一段尚未发送的草稿",
    );
  }
  for (const width of [1920, 1440, 1280, 1024, 390]) {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 });
    await check(async () => {
      const sizes = await page.evaluate(() => ({
        scroll: document.documentElement.scrollWidth,
        client: document.documentElement.clientWidth,
      }));
      assert(sizes.scroll <= sizes.client + 1);
    }, "No horizontal overflow " + width);
    await screenshot("workspace-" + width);
  }
  await page.getByRole("button", { name: "返回对话", exact: false }).click();
  await check(
    async () => assert(await page.getByLabel("发送消息").isVisible()),
    "Mobile back to chat",
  );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  await page
    .locator(".inspector .cm-content")
    .fill("# 已修改\n\n保留本次编辑。");
  await page.getByTitle("关闭编辑器").click();
  await page.getByRole("button", { name: "取消", exact: true }).click();
  assert.equal(await page.locator(".inspector").count(), 1);
  await page.getByTitle("关闭编辑器").click();
  await page.getByRole("button", { name: "保存并继续", exact: true }).click();
  await check(
    async () => assert.equal(await page.locator(".inspector").count(), 0),
    "Save and close",
  );
  assert.equal(
    (await request("GET", `/projects/${p.id}/file?path=README.md`)).content,
    "# 已修改\n\n保留本次编辑。",
  );
  assert.equal(
    await page.getByLabel("发送消息").inputValue(),
    "这是一段尚未发送的草稿",
  );
  await page.getByLabel("上传项目文件", { exact: true }).setInputFiles([
    {
      name: "中文.MD",
      mimeType: "text/markdown",
      buffer: Buffer.from("# 中文上传\n\n兼容普通文件。"),
    },
    {
      name: "archive.zip",
      mimeType: "application/zip",
      buffer: Buffer.from([80, 75, 5, 6, 0, 0]),
    },
    {
      name: "unknown.bin",
      mimeType: "application/octet-stream",
      buffer: Buffer.from([0, 255, 7]),
    },
  ]);
  await check(async () => {
    const rows = await request("GET", `/projects/${p.id}/files`);
    assert(rows.some((f) => f.path === "中文.MD"));
    assert(rows.some((f) => f.path === "archive.zip"));
    assert(rows.some((f) => f.path === "unknown.bin"));
  }, "Mixed file upload");
  await page.getByLabel("上传项目文件", { exact: true }).setInputFiles([
    {
      name: "中文.MD",
      mimeType: "text/markdown",
      buffer: Buffer.from("# 第二份"),
    },
  ]);
  await page.getByRole("button", { name: "保留两份", exact: true }).click();
  await check(
    async () =>
      assert(
        (await request("GET", `/projects/${p.id}/files`)).some(
          (f) => f.path === "中文 (2).MD",
        ),
      ),
    "Conflict keep both",
  );
  assert.equal(await page.locator(".attachment-chips").count(), 0);
  await page.getByRole("button", { name: "unknown.bin", exact: true }).click();
  await page.getByRole("button", { name: "让助手处理", exact: true }).click();
  await check(
    async () => assert.equal(await page.locator(".inspector").count(), 0),
    "File assistant entry returns to chat",
  );
  assert.equal(
    await page.locator(".attachment-chips").innerText(),
    "unknown.bin",
  );
  assert.equal(
    (await request("GET", `/runs?sessionId=${p.defaultSessionId}`)).length,
    0,
  );
  await page.locator(".attachment-chips button").click();
  const folder = path.join(fixtureDirectory, "资料库");
  await fs.mkdir(path.join(folder, "中文目录"), { recursive: true });
  await fs.writeFile(
    path.join(folder, "中文目录", "定位.markdown"),
    "---\ntitle: 品牌\n---\n[[README]]\n",
  );
  await page
    .getByLabel("上传项目文件夹", { exact: true })
    .setInputFiles(folder);
  await check(
    async () =>
      assert(
        (await request("GET", `/projects/${p.id}/files`)).some(
          (f) => f.path === "资料库/中文目录/定位.markdown",
        ),
      ),
    "Folder preserves top directory",
  );
  const archive = zipSync({
    ".obsidian/app.json": strToU8('{"legacy":true}'),
    "笔记.md": strToU8("# 归档\n[[README]]"),
    "空目录/": new Uint8Array(0),
  });
  await page.getByLabel("导入项目 ZIP", { exact: true }).setInputFiles({
    name: "vault.zip",
    mimeType: "application/zip",
    buffer: Buffer.from(archive),
  });
  await page.getByLabel("ZIP 目标目录", { exact: true }).fill("导入验收");
  await page.getByRole("button", { name: "解压导入", exact: true }).click();
  await check(
    async () =>
      assert(
        (await request("GET", `/projects/${p.id}/files`)).some(
          (f) => f.path === "导入验收/.obsidian/app.json",
        ),
      ),
    "ZIP target directory and hidden configuration",
  );
  await page.getByRole("button", { name: "README.md", exact: true }).click();
  await page.getByRole("button", { name: "对话", exact: true }).click();
  await page
    .getByRole("button", { name: "另开对话", exact: true })
    .first()
    .click();
  await page.getByLabel("对话名称", { exact: true }).fill("专项验收");
  await page.getByRole("button", { name: "保存", exact: true }).last().click();
  await check(
    async () =>
      assert(
        (await request("GET", `/projects/${p.id}/sessions`)).some(
          (s) => s.title === "专项验收",
        ),
      ),
    "Topic created",
  );
  assert.equal(await page.locator(".inspector").count(), 1);
  await page.getByRole("button", { name: /项目助手.*默认项目助手/ }).click();
  assert.equal(await page.locator(".inspector").count(), 1);
  await page.getByRole("button", { name: "展开导航", exact: true }).click();
  await check(
    async () =>
      assert.equal(
        await page
          .locator(".rail")
          .evaluate((el) => Math.round(el.getBoundingClientRect().width)),
        216,
      ),
    "Expanded navigation",
  );
  await page.reload({ waitUntil: "domcontentloaded" });
  await check(
    async () =>
      assert.equal(
        await page
          .locator(".rail")
          .evaluate((el) => Math.round(el.getBoundingClientRect().width)),
        216,
      ),
    "Navigation preference persists",
  );
  await page.getByRole("button", { name: "收起导航", exact: true }).click();
  await page.getByRole("button", { name: "专家库", exact: true }).click();
  await page.getByRole("button", { name: new RegExp(ex.name) }).click();
  await page.getByRole("button", { name: "基本信息", exact: true }).click();
  await page.getByLabel("专家名称", { exact: true }).fill(editedName);
  await page
    .getByLabel("能力简介", { exact: true })
    .fill("界面编辑已保存，方法按版本复用。");
  await page.getByRole("button", { name: "保存信息", exact: true }).click();
  await check(
    async () =>
      assert.equal(
        (await request("GET", `/experts/${ex.id}`)).expert.name,
        editedName,
      ),
    "Expert metadata edit",
  );
  await page.getByRole("button", { name: "能力文件", exact: true }).click();
  await check(
    async () =>
      assert(
        (
          await page.locator(".expert-content .cm-content").innerText()
        ).includes(editedName),
      ),
    "Metadata is synchronized to editor",
  );
  await page.reload({ waitUntil: "domcontentloaded" });
  await check(
    async () =>
      assert.equal(
        await page.locator(".expert-heading h1").innerText(),
        editedName,
      ),
    "Expert detail deep link",
  );
  await check(
    async () =>
      assert(
        await page
          .getByRole("button", { name: "SKILL.md", exact: true })
          .isEnabled(),
      ),
    "Expert package finished loading",
  );
  await page.getByLabel("上传专家文件", { exact: true }).setInputFiles([
    {
      name: "参考.md",
      mimeType: "text/markdown",
      buffer: Buffer.from("# 参考资料\n保持项目范围独立。"),
    },
  ]);
  await check(
    async () =>
      assert(
        (await request("GET", `/experts/${ex.id}/files`)).some(
          (f) => f.path === "参考.md",
        ),
      ),
    "Expert file upload",
  );
  await page.getByRole("button", { name: "参考.md", exact: true }).click();
  await page.getByRole("button", { name: "重命名", exact: true }).click();
  await page.getByLabel("专家文件路径", { exact: true }).fill("新的参考.md");
  await page.getByRole("button", { name: "确认", exact: true }).click();
  await check(
    async () =>
      assert(
        (await request("GET", `/experts/${ex.id}/files`)).some(
          (f) => f.path === "新的参考.md",
        ),
      ),
    "Expert rename",
  );
  await page.getByRole("button", { name: "使用情况", exact: true }).click();
  await page.getByLabel("添加专家到项目", { exact: true }).selectOption(p.id);
  await page
    .getByRole("button", { name: "添加到项目", exact: true })
    .last()
    .click();
  await check(
    async () =>
      assert(
        (await request("GET", `/projects/${p.id}`)).expertIds.includes(ex.id),
      ),
    "Expert project enable",
  );
  await screenshot("expert-usage");
  await page.getByRole("button", { name: "能力文件", exact: true }).click();
  await screenshot("expert-detail");
  await page.setViewportSize({ width: 390, height: 844 });
  await check(
    async () =>
      assert(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ),
    "Expert mobile layout",
  );
  await screenshot("expert-mobile");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("button", { name: "复制", exact: true }).click();
  await check(
    async () =>
      assert.equal(
        await page.locator(".expert-heading h1").innerText(),
        editedName + " 副本",
      ),
    "Expert copy opens its own detail",
  );
  const copies = (await request("GET", "/experts")).filter(
    (e) => e.name === editedName + " 副本",
  );
  for (const e of copies) experts.push(e.id);
  await page.getByRole("button", { name: "删除", exact: true }).click();
  await check(
    async () => assert.equal(await page.locator(".expert-detail").count(), 0),
    "Expert delete",
  );
  assert.deepEqual(failures, []);
  await fs.rm(path.join(artifacts, "failure.png"), { force: true });
  await fs.rm(path.join(artifacts, "failure.txt"), { force: true });
  console.log(
    JSON.stringify(
      {
        passed: true,
        screenshots,
        checks: [
          "layout",
          "breakpoints",
          "draft preservation",
          "unsaved save/cancel",
          "mixed upload",
          "conflict",
          "navigation preference",
          "expert CRUD",
          "deep link",
          "expert usage",
          "folder and ZIP targets",
          "same-project topic preserves document",
          "explicit file-to-chat attachment",
        ],
      },
      null,
      2,
    ),
  );
  await fs.writeFile(
    path.join(artifacts, "report.json"),
    JSON.stringify(
      { passed: true, url, screenshots, errors: failures },
      null,
      2,
    ),
  );
} catch (e) {
  await screenshot("failure");
  await fs.writeFile(
    path.join(artifacts, "failure.txt"),
    e.stack + "\n" + JSON.stringify(failures),
  );
  console.error(e);
  process.exitCode = 1;
} finally {
  for (const id of experts) {
    const current = await fetch(url + "/api/v1/experts/" + id, {
      headers: { Authorization: authorization },
    });
    if (current.ok) {
      const d = await current.json();
      await request("DELETE", "/experts/" + id, {
        baseDigest: d.expert.digest,
      });
    }
  }
  for (const id of created) await request("DELETE", "/projects/" + id);
  await fs.rm(fixtureDirectory, { recursive: true, force: true });
  await browser.close();
}
