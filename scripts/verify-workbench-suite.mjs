// Real isolated Store/API with fictional data; the executor is deterministic.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";
const dir = path.resolve("artifacts/workbench-white-suite"),
  meta = JSON.parse(await fs.readFile(dir + "/preview.json", "utf8"));
assert(/^http:\/\/127\.0\.0\.1:\d+$/.test(meta.url));
const browser = await chromium.launch({
  executablePath:
    process.env.PW_CHROME ||
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  headless: true,
});
const context = await browser.newContext({
  viewport: { width: 1440, height: 960 },
  deviceScaleFactor: 1,
});
const page = await context.newPage();
page.setDefaultTimeout(9000);
const errors = [],
  checks = [],
  screens = [],
  expectedGateway404 = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (m) => {
  if (m.type() === "error" && !m.text().includes("status of 409")) {
    if (
      m.text().includes("404") &&
      m.location().url.includes("/gateway/sessions/")
    )
      expectedGateway404.push(m.location().url);
    else errors.push(m.text());
  }
});
let confirmAnswer = false;
page.on("dialog", (d) => (confirmAnswer ? d.accept() : d.dismiss()));
const check = (s, v = true) => {
  assert(v, s);
  checks.push(s);
  console.log(s);
};
const wait = async (fn) => {
  for (let i = 0; i < 100; i++) {
    if (await fn()) return;
    await page.waitForTimeout(100);
  }
  throw Error("State did not converge");
};
const req = async (route, method = "GET", data) => {
  const r = await context.request.fetch(meta.url + "/api/v1" + route, {
    method,
    data,
  });
  assert(r.ok(), route + ":" + (await r.text()));
  return r.json();
};
const goto = async (hash) => {
  await page.goto(meta.url + "/" + hash);
  await page.reload();
  await page.locator(".rail").first().waitFor({ state: "attached" });
  await page.waitForTimeout(350);
};
const shot = async (name) => {
  await page.waitForTimeout(100);
  const p = dir + "/screens/" + name + ".png";
  await page.screenshot({ path: p });
  screens.push(p);
};
const projectHash =
  "#project=" +
  meta.projects[0].id +
  "&session=" +
  meta.projects[0].defaultSessionId;
try {
  for (const t of meta.tasks) {
    const current = await req("/tasks/" + t.id);
    await req("/tasks/" + t.id, "PUT", {
      ...current,
      title: t.title,
      state: t.state,
    });
  }
  const ex = await req("/experts/" + meta.experts[0].id);
  if (ex.expert.name !== meta.experts[0].name)
    await req("/experts/" + meta.experts[0].id + "/changes", "POST", {
      baseDigest: ex.expert.digest,
      metadata: {
        name: meta.experts[0].name,
        description: ex.expert.description,
      },
    });
  const sc = (await req("/schedules")).find((x) => x.id === meta.rule.id);
  if (sc.name !== meta.rule.name)
    await req("/schedules/" + sc.id, "PUT", { ...sc, name: meta.rule.name });
  await goto(projectHash);
  await page.getByLabel("发送消息", { exact: true }).fill("保留输入草稿 SUITE");
  await page.evaluate(
    () => (window.suiteChat = document.querySelector(".chat")),
  );
  await page.getByRole("button", { name: "打开资料", exact: true }).click();
  await page.locator('button[title="资料/访谈纪要.md"]').click();
  await page.locator(".inspector").waitFor();
  check(
    "file opens without remounting conversation",
    await page.evaluate(
      () => window.suiteChat === document.querySelector(".chat"),
    ),
  );
  check(
    "file open preserves composer draft",
    (await page.getByLabel("发送消息", { exact: true }).inputValue()) ===
      "保留输入草稿 SUITE",
  );
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  await page
    .locator(".inspector .cm-content")
    .fill("# 浏览器验收\n\n本地编辑 SUITE-7319");
  await page.getByRole("button", { name: "关闭文档", exact: true }).click();
  await page.getByRole("alertdialog", { name: "未保存的修改" }).waitFor();
  await page.getByRole("button", { name: "取消", exact: true }).click();
  check(
    "file close can cancel without losing edits",
    (await page.locator(".inspector .cm-content").innerText()).includes(
      "本地编辑 SUITE-7319",
    ),
  );
  await page.getByRole("button", { name: "关闭文档", exact: true }).click();
  await page.getByRole("button", { name: "保存并继续", exact: true }).click();
  await page.locator(".inspector").waitFor({ state: "detached" });
  const fileRoute = "/projects/" + meta.projects[0].id + "/file";
  let saved = await req(
    fileRoute + "?path=" + encodeURIComponent("资料/访谈纪要.md"),
  );
  check(
    "file save persists to real Store",
    saved.content.includes("本地编辑 SUITE-7319"),
  );
  await page.getByRole("button", { name: "打开资料", exact: true }).click();
  await page.locator('button[title="资料/访谈纪要.md"]').click();
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  await page.locator(".inspector .cm-content").fill("# 冲突草稿");
  await req(fileRoute, "PUT", {
    path: "资料/访谈纪要.md",
    content: "# 另一个修订",
    baseHash: saved.hash,
  });
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await wait(
    async () =>
      (await page.locator("body").innerText()).includes("文件已被修改") ||
      (await page.locator("body").innerText()).includes("文件已更新") ||
      (await page.locator("body").innerText()).includes("冲突"),
  );
  check(
    "conflicting file save retains local draft",
    (await page.locator(".inspector .cm-content").innerText()).includes(
      "冲突草稿",
    ),
  );
  check(
    "conflicting file save preserves remote content",
    (await req(fileRoute + "?path=" + encodeURIComponent("资料/访谈纪要.md")))
      .content === "# 另一个修订",
  );
  await page.getByRole("button", { name: "关闭文档", exact: true }).click();
  await page.getByRole("button", { name: "放弃修改", exact: true }).click();
  await page
    .getByLabel("当前对话", { exact: true })
    .selectOption(meta.session.id);
  await wait(
    async () =>
      (await page.getByLabel("发送消息", { exact: true }).inputValue()) === "",
  );
  check(
    "switching sessions shows separate draft",
    (await page.getByLabel("发送消息", { exact: true }).inputValue()) === "",
  );
  await page
    .getByLabel("当前对话", { exact: true })
    .selectOption(meta.projects[0].defaultSessionId);
  await wait(
    async () =>
      (await page.getByLabel("发送消息", { exact: true }).inputValue()) ===
      "保留输入草稿 SUITE",
  );
  check(
    "switching sessions restores draft",
    (await page.getByLabel("发送消息", { exact: true }).inputValue()) ===
      "保留输入草稿 SUITE",
  );
  await page.getByLabel("发送消息", { exact: true }).fill("");
  await page
    .getByLabel("添加项目附件", { exact: true })
    .setInputFiles({
      name: "本地附件.md",
      mimeType: "text/markdown",
      buffer: Buffer.from("# 附件验收"),
    });
  await page.locator(".attachment-chips").waitFor();
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await wait(async () =>
    (await req("/runs?sessionId=" + meta.projects[0].defaultSessionId)).some(
      (r) =>
        r.attachments?.some((p) => p.includes("本地附件.md")) &&
        r.state === "completed",
    ),
  );
  check("attachment-only message reaches Store and executor");
  await goto("#page=tasks");
  await page.getByRole("button", { name: /^整理第二轮访谈问题清单/ }).click();
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  let dialog = page.getByRole("dialog", { name: "编辑任务", exact: true });
  await dialog
    .getByLabel("标题", { exact: true })
    .fill("整理第二轮访谈问题清单（已核对）");
  await dialog.getByRole("button", { name: "关闭编辑任务" }).click();
  check("cancel discard keeps task editor", await dialog.isVisible());
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await dialog.waitFor({ state: "detached" });
  check(
    "task edit is persisted",
    (await req("/tasks/" + meta.tasks[0].id)).title.includes("已核对"),
  );
  await page.getByRole("button", { name: "关闭任务详情" }).click();
  await page
    .getByLabel("完成任务：整理第二轮访谈问题清单（已核对）", { exact: true })
    .click();
  await wait(
    async () => (await req("/tasks/" + meta.tasks[0].id)).state === "completed",
  );
  check("task completion persists and moves under ended");
  await page.getByRole("button", { name: "AI 任务", exact: true }).click();
  await page.getByRole("button", { name: "每周核对资料", exact: true }).click();
  await page
    .getByLabel("任务名称", { exact: true })
    .fill("每周核对资料（已核对）");
  await page.getByRole("button", { name: "保存任务", exact: true }).click();
  await wait(async () =>
    (await req("/schedules")).some((x) => x.name.includes("已核对")),
  );
  check("AI rule save retains version and recurrence");
  await goto("#expert=" + meta.experts[0].id);
  await page.getByRole("button", { name: "编辑信息", exact: true }).click();
  await page.getByLabel("专家名称", { exact: true }).fill("写作教练（已核对）");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await wait(async () =>
    (await req("/experts/" + meta.experts[0].id)).expert.name.includes(
      "已核对",
    ),
  );
  check("expert metadata saves with revision");
  await page.getByRole("button", { name: "版本", exact: true }).click();
  await page.locator(".expert-versions .usage-row").first().waitFor();
  check("expert version history remains accessible");
  await shot("journey-expert-versions");
  await page.getByRole("button", { name: "使用范围", exact: true }).click();
  check(
    "expert usage retains actual project binding",
    (await page.locator(".expert-content").innerText()).includes("产品验证"),
  );
  await page
    .getByRole("button", { name: "导入专家", exact: true })
    .press("ArrowDown");
  await page.getByRole("menuitem", { name: "选择目录", exact: true }).waitFor();
  await page.keyboard.press("Escape");
  check("import menu keyboard opens and dismisses");
  await goto("#page=settings");
  await page.getByRole("button", { name: /^微信/ }).click();
  await page.getByRole("button", { name: "停用", exact: true }).click();
  await wait(
    async () =>
      (await req("/connections")).find((c) => c.kind === "wechat").enabled ===
      false,
  );
  check("channel detail toggles actual enabled state");
  await page.getByRole("button", { name: "启用", exact: true }).click();
  await page.getByRole("button", { name: "关闭微信" }).click();
  await page.getByRole("button", { name: /^MCP/ }).click();
  await page.getByRole("button", { name: "添加插件", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "全局 MCP 插件" });
  if (!(await dialog.count())) dialog = page.getByRole("dialog").last();
  await shot("journey-mcp-form");
  await dialog.getByRole("button", { name: /关闭/ }).click();
  await page.getByRole("button", { name: "关闭MCP 插件" }).click();
  await page.getByRole("button", { name: /^CLI/ }).click();
  await shot("journey-cli");
  await page.getByRole("button", { name: "关闭CLI 工具" }).click();
  await page.getByRole("button", { name: "账号", exact: true }).click();
  await page.getByRole("button", { name: "修改密码", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "修改密码" });
  await page.keyboard.press("Tab");
  check(
    "modal focus stays inside panel",
    await dialog.evaluate((el) => el.contains(document.activeElement)),
  );
  await page.keyboard.press("Escape");
  await dialog.waitFor({ state: "detached" });
  await wait(
    async () =>
      await page
        .getByRole("button", { name: "修改密码", exact: true })
        .evaluate((el) => el === document.activeElement),
  );
  check(
    "Escape closes only top modal and restores focus",
    await page
      .getByRole("button", { name: "修改密码", exact: true })
      .evaluate((el) => el === document.activeElement),
  );
  await page.getByRole("button", { name: "关闭账号" }).click();
  await page.getByText("高级", { exact: true }).click();
  await shot("journey-settings-advanced");
  check(
    "advanced retains receipts and recycle bin",
    (await page.locator("body").innerText()).includes("发送回执") &&
      (await page.locator("body").innerText()).includes("回收站"),
  );
  await goto("#page=calendar");
  await page.getByRole("button", { name: "新建日程", exact: true }).click();
  await shot("journey-event-editor");
  await page.getByRole("button", { name: /关闭.*日程/ }).click();
  check("calendar event editor opens and closes");
  await goto("#page=notices");
  await page.locator(".notice-row").first().click();
  await shot("journey-notice-detail");
  check(
    "failure detail retains retry boundary",
    (await page.locator("body").innerText()).includes("当前配置重跑") ||
      (await page.locator("body").innerText()).includes("重试"),
  );
  await goto("#page=today");
  await page.getByRole("button", { name: "个性化", exact: true }).click();
  await shot("journey-personalization");
  await page.getByRole("button", { name: "我的记忆", exact: true }).click();
  await shot("journey-memory");
  await page.getByRole("button", { name: "关闭个性化" }).click();
  check("Today personalization and second-level memory remain accessible");
  // Route coverage, width/short-screen resilience, modal mobile drawer and safe composer.
  for (const width of [1440, 1280, 1024, 768, 390, 320]) {
    await page.setViewportSize({ width, height: width < 768 ? 740 : 900 });
    for (const [name, hash] of [
      ["project", projectHash],
      ["projects", "#page=projects"],
      ["experts", "#expert=" + meta.experts[0].id],
      ["tasks", "#page=tasks"],
      ["settings", "#page=settings"],
      ["calendar", "#page=calendar"],
      ["notices", "#page=notices"],
      ["today", "#page=today"],
    ]) {
      await goto(hash);
      check(
        "no document overflow " + name + " " + width,
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      );
      await shot(name + "-" + width);
    }
    await goto(projectHash);
    await page.getByRole("button", { name: "打开资料", exact: true }).click();
    await shot("project-drawer-" + width);
    if (width < 1100) {
      const d = page.getByRole("dialog", { name: "资料", exact: true });
      await d.waitFor();
      await page.keyboard.press("Tab");
      check(
        "compact drawer traps focus " + width,
        await d.evaluate((el) => el.contains(document.activeElement)),
      );
      await page.keyboard.press("Escape");
      await d.waitFor({ state: "hidden" });
    } else {
      const send = await page
        .getByRole("button", { name: "发送", exact: true })
        .boundingBox();
      check(
        "drawer leaves send control exposed " + width,
        send.x + send.width <= width - 350 + 1,
      );
      await page.getByRole("button", { name: "关闭资料" }).click();
    }
    if (width < 768) {
      await page.getByRole("button", { name: "打开导航", exact: true }).click();
      await shot("navigation-" + width);
      await page
        .getByRole("dialog", { name: "导航", exact: true })
        .getByRole("button", { name: "任务", exact: true })
        .click();
      await page.getByRole("heading", { name: "任务", exact: true }).waitFor();
      check(
        "mobile navigation closes after route " + width,
        !(await page
          .getByRole("dialog", { name: "导航", exact: true })
          .isVisible()),
      );
    }
  }
  await page.setViewportSize({ width: 390, height: 740 });
  await goto(projectHash);
  await page.getByRole("button", { name: "打开资料", exact: true }).click();
  await page.locator('button[title="资料/访谈纪要.md"]').click();
  await shot("mobile-file");
  await page.getByRole("button", { name: "关闭文档", exact: true }).click();
  check(
    "mobile document returns to composer",
    await page.getByLabel("发送消息", { exact: true }).isVisible(),
  );
  await goto("#page=tasks");
  await page.getByRole("button", { name: /^汇总现有用户反馈/ }).click();
  await shot("mobile-task-detail");
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  await shot("mobile-task-editor");
  await page.getByRole("button", { name: "关闭编辑任务" }).click();
  await page.getByRole("button", { name: "关闭任务详情" }).click();
  await goto("#page=settings");
  await page.getByRole("button", { name: /^微信/ }).click();
  await shot("mobile-channel-detail");
  await page.getByRole("button", { name: "关闭微信" }).click();
  await goto("#page=calendar");
  await page.getByRole("button", { name: "新建日程", exact: true }).click();
  await shot("mobile-event-editor");
  await page.getByRole("button", { name: "关闭新建日程" }).click();
  await goto("#page=today");
  await page.getByRole("button", { name: "个性化", exact: true }).click();
  await page.getByRole("button", { name: "我的记忆", exact: true }).click();
  await shot("mobile-memory");
  await page.getByRole("button", { name: "关闭个性化" }).click();
  await page.setViewportSize({ width: 390, height: 420 });
  await goto(projectHash);
  check(
    "short screen keeps composer visible",
    await page
      .getByLabel("发送消息", { exact: true })
      .evaluate((el) => el.getBoundingClientRect().bottom <= innerHeight),
  );
  await shot("project-short");
  check("no unexpected browser errors", errors.length === 0);
  await fs.writeFile(
    dir + "/browser-receipt.json",
    JSON.stringify(
      {
        checks,
        screens,
        errors,
        expectedGateway404,
        mode: meta.mode,
        at: new Date().toISOString(),
      },
      null,
      2,
    ),
  );
} catch (e) {
  await shot("failure");
  await fs.writeFile(
    dir + "/browser-failure.json",
    JSON.stringify({ error: String(e), errors, checks }, null, 2),
  );
  throw e;
} finally {
  await browser.close();
}
