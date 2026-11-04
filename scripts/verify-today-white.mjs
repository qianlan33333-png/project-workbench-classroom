// Isolated real Store/API and fixed parsers; the preview executor is not a model.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";
const dir = path.resolve("artifacts/today-white"),
  urls = JSON.parse(await fs.readFile(dir + "/preview.json"));
assert(urls.current.startsWith("http://127.0.0.1:"));
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.PW_CHROME ||
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
});
const page = await browser.newPage({
  viewport: { width: 1487, height: 1058 },
  deviceScaleFactor: 1,
});
page.setDefaultTimeout(12000);
const errors = [],
  checks = [],
  writes = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("request", (r) => {
  if (r.method() !== "GET")
    writes.push({ method: r.method(), path: new URL(r.url()).pathname });
});
const origin = new URL(urls.current).origin,
  api = async (endpoint) =>
    (await page.request.get(origin + "/api/v1/" + endpoint)).json();
const check = (name, value = true) => {
  assert(value, name);
  checks.push(name);
  console.log(name);
};
const shot = (filename) => page.screenshot({ path: dir + "/" + filename });
const wait = async (fn) => {
  for (let i = 0; i < 120; i++) {
    if (await fn()) return;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw Error("State did not converge");
};
const open = () =>
  page.getByRole("button", { name: "个性化", exact: true }).click();
const dialog = () => page.getByRole("dialog");
const close = () =>
  page.getByRole("button", { name: "关闭个性化", exact: true }).click();
const composer = () => page.locator(".today-composer textarea");
const send = () =>
  page.getByRole("button", { name: "发送个人消息", exact: true });
const file = async (name, text) =>
  page
    .locator('input[type=file][aria-label="选择本地附件"]')
    .setInputFiles({
      name,
      mimeType: "text/markdown",
      buffer: Buffer.from(text),
    });
const ready = async (name) => {
  await page
    .locator(".personal-attachment.ready")
    .filter({ hasText: name })
    .waitFor();
};
try {
  await page.goto(urls.current);
  await page.getByRole("button", { name: "个性化", exact: true }).waitFor();
  check(
    "removed Today heading, date row, refresh and helper",
    (await page.locator(".today-header").count()) === 0 &&
      !(await page
        .getByText("Enter 发送 · Shift + Enter 换行", { exact: true })
        .count()) &&
      !(await page.getByRole("button", { name: "刷新", exact: true }).count()),
  );
  check(
    "white background and neutral reply",
    (await page
      .locator(".today-layout")
      .evaluate(
        (e) => getComputedStyle(e).backgroundColor === "rgb(255, 255, 255)",
      )) &&
      (await page
        .locator(".personal-answer .markdown")
        .first()
        .evaluate((e) => getComputedStyle(e).color === "rgb(23, 23, 23)")),
  );
  check(
    "wide composer",
    (await page
      .locator(".today-composer")
      .evaluate((e) => e.getBoundingClientRect().width)) >
      (await page
        .locator(".personal-answer")
        .first()
        .evaluate((e) => e.getBoundingClientRect().width)),
  );
  check(
    "arrangements closed initially",
    (await page.locator(".today-arrangements").count()) === 0,
  );
  await file("会议记录.md", "会议记录：校验码7319，明天复查。");
  await ready("会议记录.md");
  await shot("01-main.png");
  await composer().fill("尚未发送的草稿");
  await open();
  await dialog().getByLabel("怎么合作", { exact: true }).waitFor();
  await shot("02-personalization.png");
  check(
    "simplified two fields",
    (await dialog().getByLabel("怎么合作", { exact: true }).count()) === 1 &&
      (await dialog().getByLabel("怎么陪我", { exact: true }).count()) === 1 &&
      !(await dialog().innerText()).includes("SOUL.md"),
  );
  await dialog()
    .getByLabel("怎么合作", { exact: true })
    .fill("结论先行；未经确认不改变项目正文。");
  await page.getByRole("button", { name: "我的记忆", exact: true }).click();
  await page.getByRole("button", { name: /记忆操作：复杂任务/ }).click();
  await shot("03-memory.png");
  check(
    "memory secondary page with compact actions",
    (await dialog()
      .getByRole("heading", { name: "我的记忆", exact: true })
      .isVisible()) &&
      (await dialog()
        .getByRole("button", { name: "查看依据", exact: true })
        .isVisible()),
  );
  await dialog().getByRole("button", { name: "查看依据", exact: true }).click();
  await page
    .getByRole("complementary", { name: "记忆依据" })
    .getByRole("button", { name: "读取完整依据" })
    .first()
    .waitFor();
  check(
    "original evidence readable",
    await page
      .getByRole("complementary", { name: "记忆依据" })
      .innerText()
      .then((s) => s.includes("复杂任务")),
  );
  await page.getByRole("button", { name: "收起依据" }).click();
  await page.getByRole("button", { name: "纠正", exact: true }).click();
  await page.getByLabel("纠正这段记忆").fill("需要先确认范围，校验记忆7319。");
  page.once("dialog", (d) => d.dismiss());
  await dialog().getByRole("button", { name: "个性化", exact: true }).click();
  check(
    "unsaved memory back can be cancelled",
    (await page.getByLabel("纠正这段记忆").inputValue()) ===
      "需要先确认范围，校验记忆7319。",
  );
  await page.getByRole("button", { name: "保存纠正" }).click();
  await wait(async () =>
    (await api("personal/understanding")).paragraphs.some((p) =>
      p.text.includes("校验记忆7319"),
    ),
  );
  await dialog().getByRole("button", { name: "个性化", exact: true }).click();
  check(
    "rules draft survives secondary page",
    (await dialog().getByLabel("怎么合作", { exact: true }).inputValue()) ===
      "结论先行；未经确认不改变项目正文。",
  );
  await page.route(
    "**/api/v1/personal/rules",
    (route) =>
      route.continue({
        postData: JSON.stringify({
          ...route.request().postDataJSON(),
          version: 0,
        }),
      }),
    { times: 1 },
  );
  await dialog().getByRole("button", { name: "保存", exact: true }).click();
  await dialog().getByRole("alert").waitFor();
  check(
    "409 retains rules draft",
    (await dialog().getByLabel("怎么合作", { exact: true }).inputValue()) ===
      "结论先行；未经确认不改变项目正文。",
  );
  page.once("dialog", (d) => d.dismiss());
  await close();
  check("unsaved close protected", await dialog().isVisible());
  page.once("dialog", (d) => d.accept());
  await dialog().getByRole("button", { name: "重新读取" }).click();
  await dialog()
    .getByLabel("怎么合作", { exact: true })
    .fill("正式保存的隔离规则7319");
  await dialog().getByRole("button", { name: "保存", exact: true }).click();
  await wait(
    async () =>
      (await api("personal/rules")).agent === "正式保存的隔离规则7319",
  );
  await close();
  check(
    "composer draft and attachment survive settings",
    (await composer().inputValue()) === "尚未发送的草稿" &&
      (await page.locator(".personal-attachment.ready").count()) === 1,
  );
  await wait(async () =>
    page
      .getByRole("button", { name: "个性化", exact: true })
      .evaluate((e) => e === document.activeElement),
  );
  check(
    "focus returns to settings button",
    await page
      .getByRole("button", { name: "个性化", exact: true })
      .evaluate((e) => e === document.activeElement),
  );
  await page.reload();
  await ready("会议记录.md");
  check("uploaded draft restored after reload");
  await composer().fill("");
  check("file-only send enabled", await send().isEnabled());
  await send().click();
  await wait(async () =>
    (await api("today")).runs.some(
      (r) => r.result?.includes("7319") && r.attachments?.length,
    ),
  );
  check("file-only upload reaches actual content reader");
  await page.route(
    "**/api/v1/personal/attachments",
    (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: "隔离上传故障" }),
      }),
    { times: 1 },
  );
  await file("重试.md", "UPLOAD RETRY 7319");
  await page
    .getByRole("button", { name: "重试上传：重试.md", exact: true })
    .waitFor();
  check("failed upload blocks send", await send().isDisabled());
  await page
    .getByRole("button", { name: "重试上传：重试.md", exact: true })
    .click();
  await ready("重试.md");
  await page
    .getByRole("button", { name: "移除附件：重试.md", exact: true })
    .click();
  await page.locator(".today-main").evaluate((e) => {
    const d = new DataTransfer();
    d.items.add(new File(["DROP 7319"], "拖入.txt", { type: "text/plain" }));
    e.dispatchEvent(
      new DragEvent("drop", {
        bubbles: true,
        cancelable: true,
        dataTransfer: d,
      }),
    );
  });
  await ready("拖入.txt");
  await page
    .getByRole("button", { name: "移除附件：拖入.txt", exact: true })
    .click();
  check("local drag and removal work");
  const png = await fs.readFile(dir + "/fixtures/proof.png");
  await composer().evaluate((e, b) => {
    const data = Uint8Array.from(atob(b), (c) => c.charCodeAt(0));
    const d = new DataTransfer();
    d.items.add(new File([data], "粘贴.png", { type: "image/png" }));
    e.dispatchEvent(
      new ClipboardEvent("paste", {
        bubbles: true,
        cancelable: true,
        clipboardData: d,
      }),
    );
  }, png.toString("base64"));
  await ready("粘贴.png");
  await send().click();
  await wait(async () =>
    (await api("today")).runs.some(
      (r) => r.result?.includes("7319") && r.result?.includes("ocr"),
    ),
  );
  check("pasted image is OCRed with limitation stated");
  await page
    .locator("input[type=file]")
    .setInputFiles(dir + "/fixtures/proof.pdf");
  await ready("proof.pdf");
  await send().click();
  await wait(async () =>
    (await api("today")).runs.some(
      (r) => r.result?.includes("7319") && r.result?.includes("pdf"),
    ),
  );
  check("PDF attachment text extracted");
  await composer().fill("中文输入法草稿");
  const before = (await api("today")).runs.length;
  await composer().dispatchEvent("compositionstart");
  await composer().dispatchEvent("keydown", {
    key: "Enter",
    code: "Enter",
    isComposing: true,
  });
  await composer().dispatchEvent("compositionend");
  check(
    "IME Enter does not send",
    (await composer().inputValue()) === "中文输入法草稿" &&
      (await api("today")).runs.length === before,
  );
  await page.getByRole("button", { name: "打开已有安排", exact: true }).click();
  await page.getByRole("complementary", { name: "已有安排" }).waitFor();
  await page.getByRole("button", { name: "关闭已有安排", exact: true }).click();
  check("bottom arrangements control works");
  const f = await api("personal/followups"),
    waiting = f.find((v) => v.state === "waiting_decision");
  await page.getByRole("button", { name: /持续跟进/ }).click();
  const item = page.getByRole("article", { name: waiting.title, exact: true });
  await item.getByRole("button", { name: new RegExp(waiting.title) }).click();
  await item.getByRole("radio").first().check();
  await item.getByRole("button", { name: "提交选择", exact: true }).click();
  await wait(async () =>
    ["completed", "needs_review"].includes(
      (await api("personal/followups")).find((v) => v.id === waiting.id).state,
    ),
  );
  if (
    (await api("personal/followups")).find((v) => v.id === waiting.id).state ===
    "needs_review"
  ) {
    check("changed memory invalidates old decision basis");
    await item.getByRole("button", { name: "继续核实", exact: true }).click();
    await wait(
      async () =>
        (await api("personal/followups")).find((v) => v.id === waiting.id)
          .state === "completed",
    );
  }
  check(
    "followup resumes on original session",
    (await api("today")).runs
      .filter((r) => r.followupId === waiting.id)
      .every((r) => r.sessionId === waiting.sessionId),
  );
  await open();
  await page.getByRole("button", { name: "我的记忆", exact: true }).click();
  await page.getByRole("button", { name: /记忆操作：需要先确认范围/ }).click();
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "忘记", exact: true }).click();
  await wait(async () =>
    (await api("personal/understanding")).paragraphs.every(
      (p) => !p.text.includes("校验记忆7319"),
    ),
  );
  check("forget removes corrected memory and underlying evidence");
  for (const [width, height] of [
    [390, 844],
    [320, 568],
    [1280, 600],
    [1440, 900],
  ]) {
    await page.setViewportSize({ width, height });
    check(
      `${width}x${height} settings fits`,
      (await dialog().evaluate((e) => {
        const b = e.getBoundingClientRect();
        return (
          b.left >= 0 &&
          b.right <= innerWidth + 1 &&
          b.top >= 0 &&
          b.bottom <= innerHeight + 1
        );
      })) &&
        (await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        )),
    );
    await shot(`memory-${width}.png`);
    await dialog().focus();
    await page.keyboard.press("Shift+Tab");
    check(
      `${width} focus stays in modal`,
      await dialog().evaluate((e) => e.contains(document.activeElement)),
    );
    await close();
    check(
      `${width}x${height} composer fits`,
      (await page.locator(".today-composer").evaluate((e) => {
        const b = e.getBoundingClientRect();
        return (
          b.left >= 0 &&
          b.right <= innerWidth + 1 &&
          b.bottom <= innerHeight + 1
        );
      })) &&
        (await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        )),
    );
    await shot(`main-${width}.png`);
    await open();
    await page.getByRole("button", { name: "我的记忆", exact: true }).click();
  }
  await close();
  check("no client exceptions", errors.length === 0);
  check(
    "no project mutation calls",
    !writes.some((r) => r.path.includes("/projects/")),
  );
  await fs.writeFile(
    dir + "/ui-report.json",
    JSON.stringify({ passed: true, checks, errors, writes, urls }, null, 2),
  );
  console.log(JSON.stringify({ passed: checks.length, errors }));
} catch (e) {
  await shot("ui-failure.png");
  await fs.writeFile(
    dir + "/ui-report.json",
    JSON.stringify(
      { passed: false, checks, errors, failure: String(e) },
      null,
      2,
    ),
  );
  throw e;
} finally {
  await browser.close();
}
