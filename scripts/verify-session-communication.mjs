import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";
const dir =
  (
    process.env.PW_COMMUNICATION_UI || "artifacts/session-communication"
  ).replace(/\/$/, "") + "/";
const meta = JSON.parse(await fs.readFile(dir + "preview.json", "utf8"));
const browser = await chromium.launch({
  executablePath:
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  headless: true,
});
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
});
const page = await context.newPage();
page.setDefaultTimeout(8000);
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("dialog", (d) => d.accept());
const req = async (route, body) => {
  const r = await context.request.fetch(meta.url + "/api/v1" + route, {
    method: body === undefined ? "GET" : "POST",
    data: body,
  });
  assert(r.ok(), route + ":" + (await r.text()));
  return r.json();
};
const [a, b] = meta.projects;
const seed = await req("/messages", {
  source: "web",
  sessionId: a.defaultSessionId,
  eventId: crypto.randomUUID(),
  text: "隔离页面验收的通信源",
});
let source;
for (let i = 0; i < 50; i++) {
  source = (await req("/runs?sessionId=" + a.defaultSessionId)).find(
    (r) => r.messageId === seed.id && r.state === "completed",
  );
  if (source) break;
  await page.waitForTimeout(100);
}
assert(source);
const baseline = (
  await req("/session-messages?sessionId=" + a.defaultSessionId)
).length;
const go = async (p) => {
  await page.goto(
    meta.url + "/#project=" + p.id + "&session=" + p.defaultSessionId,
  );
  await page.getByLabel("发送消息", { exact: true }).waitFor();
};
const send = async (to, mode = "context", execution = "read") =>
  req("/session-messages", {
    fromRunId: source.id,
    toSessionId: to,
    text: "自由文本协作测试：不需要回复来源，自行决定后续动作。",
    delivery: mode,
    execution,
    operationId: crypto.randomUUID(),
  });
try {
  await go(a);
  await page.screenshot({ path: dir + "baseline-desktop.png" });
  const passive = await send(b.defaultSessionId);
  await page.getByRole("region", { name: "会话通信" }).waitFor();
  const outcard = page
    .locator(".communication-card")
    .filter({ hasText: "#" + passive.id.slice(0, 8) });
  await outcard.locator("summary").click();
  await outcard.getByText("已投递 · 下轮读取", { exact: true }).waitFor();
  await page.screenshot({ path: dir + "source-desktop.png" });
  assert.equal((await req("/session-messages/" + passive.id)).runId, undefined);
  await go(b);
  const incard = page
    .locator(".communication-card")
    .filter({ hasText: "#" + passive.id.slice(0, 8) });
  if ((await incard.getAttribute("open")) === null)
    await incard.locator("summary").click();
  const composer = page.getByLabel("发送消息", { exact: true });
  await composer.fill("已有草稿 QA");
  await incard.getByRole("button", { name: "准备回复", exact: true }).click();
  assert.equal(await composer.inputValue(), "已有草稿 QA");
  await composer.fill("");
  await incard.getByRole("button", { name: "准备回复", exact: true }).click();
  assert((await composer.inputValue()).includes("replyTo=" + passive.id));
  assert.equal(
    (await req("/session-messages?sessionId=" + a.defaultSessionId)).length,
    baseline + 1,
  );
  await composer.fill("保留移动端草稿");
  if (await page.locator(".toast button").count())
    await page.locator(".toast button").last().click();
  await page.screenshot({ path: dir + "target-desktop.png" });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(300);
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.screenshot({ path: dir + "target-mobile.png" });
  assert.equal(await composer.inputValue(), "保留移动端草稿");
  await page.setViewportSize({ width: 1440, height: 1000 });
  const approval = await send(b.defaultSessionId, "wake", "execute");
  const card = page
    .locator(".communication-card")
    .filter({ hasText: "#" + approval.id.slice(0, 8) });
  await card.waitFor();
  await card.locator("summary").click();
  await card.getByRole("button", { name: "授权这次执行" }).click();
  await page.waitForTimeout(1500);
  assert(
    (await req("/session-messages/" + approval.id)).agent.authorizedAt > 0,
  );
  const today = await req("/today");
  const pm = await send(today.sessionId, "wake");
  await page.goto(meta.url + "/#page=today");
  await page
    .getByText("本人经接口发送 · 来自 产品验证 · 项目助手", { exact: true })
    .last()
    .waitFor();
  await page.screenshot({ path: dir + "today-desktop.png" });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(300);
  assert(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  );
  await page.screenshot({ path: dir + "today-mobile.png" });
  const all = await req("/session-messages?sessionId=" + a.defaultSessionId);
  assert(
    all.every((x) => x.agent.fromSessionId === a.defaultSessionId),
    "unexpected automatic reply",
  );
  assert.equal(all.length, baseline + 3);
  assert.equal(errors.length, 0, errors.join("\n"));
  console.log(
    JSON.stringify({
      passed: true,
      messages: all.length,
      noAutomaticReplies: true,
      draftPreserved: true,
      approvalRecorded: true,
      desktopMobile: true,
    }),
  );
} catch (e) {
  await page.screenshot({ path: dir + "failure.png" });
  console.log(await page.locator("body").innerText());
  throw e;
} finally {
  await browser.close();
}
