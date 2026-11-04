// Real local Store/API + deterministic executor. Start TestFollowupPreview first.
// No production URL, provider credentials, user data or channel transport.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";

const dir = path.resolve("artifacts/today-followups");
const urls = JSON.parse(await fs.readFile(path.join(dir, "preview.json"), "utf8"));
assert(urls.current.startsWith("http://127.0.0.1:"));
const browser = await chromium.launch({ headless: true, executablePath: process.env.PW_CHROME || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" });
const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 1 });
const errors = [], checks = [], requests = [];
page.on("pageerror", e => errors.push(e.message));
page.on("console", m => { if (m.type() === "error") errors.push(m.text()); });
page.on("request", r => { if (r.method() !== "GET") requests.push({ method: r.method(), path: new URL(r.url()).pathname }); });
const origin = new URL(urls.current).origin;
const all = async () => (await page.request.get(origin + "/api/v1/personal/followups")).json();
const wait = async predicate => {
  for (let i = 0; i < 80; i++) { if (await predicate()) return; await new Promise(r => setTimeout(r, 100)); }
  throw new Error("Local state did not converge");
};
const shot = file => page.screenshot({ path: path.join(dir, file) });
const check = (name, value = true) => { assert(value, name); checks.push(name); };
try {
  await page.goto(urls.current);
  const toggle = page.getByRole("button", { name: /持续跟进.*3 项进行中/ });
  await toggle.waitFor();
  check("three active followups and one decision", (await toggle.innerText()).includes("1 项等你决定"));
  await shot("03-after-desktop-collapsed.png");
  const composer = page.locator(".today-composer textarea");
  await composer.fill("这段未发送的草稿应始终保留");
  await toggle.click();
  await shot("04-after-desktop-list.png");
  const choice = page.getByRole("article", { name: "选择反馈验证顺序", exact: true });
  await choice.getByRole("button", { name: "选择反馈验证顺序 等你决定", exact: true }).click();
  check("one decision entry", await page.getByRole("heading", { name: "先核对哪一项？", exact: true }).count() === 1);
  await shot("05-after-desktop-decision.png");
  await choice.getByRole("radio").first().check();
  await choice.getByRole("radio").first().screenshot({ path: path.join(dir, "decision-focus.png") });
  check("keyboard selection enables submit", await choice.getByRole("button", { name: "提交选择", exact: true }).isEnabled());

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "关闭已有安排", exact: true }).click();
  check("390px no horizontal overflow", await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await shot("06-after-mobile-decision.png");
  await choice.getByRole("button", { name: "提交选择", exact: true }).scrollIntoViewIfNeeded();
  await shot("07-mobile-submit-visible.png");
  const initial = await all(), asked = initial.find(f => f.title === "选择反馈验证顺序");
  await choice.getByRole("button", { name: "提交选择", exact: true }).click();
  await wait(async () => (await all()).find(f => f.id === asked.id).state === "completed");
  const replay = await page.request.post(origin + `/api/v1/personal/decisions/${asked.decisionId}/answer`, { data: { version: 1, answer: "A", other: "" } });
  check("duplicate decision accepted without a second run", replay.status() === 200);
  const data = await (await page.request.get(origin + "/api/v1/today")).json();
  check("one resumed run on stable personal session", data.runs.filter(r => r.followupId === asked.id).length === 1 && data.runs.find(r => r.followupId === asked.id).sessionId === asked.sessionId);

  // Keep the actual server involved: submit an outdated version once, then retry.
  const needs = page.getByRole("article", { name: "核实导出功能的实际结果", exact: true });
  await needs.getByRole("button", { name: /核实导出功能的实际结果/ }).click();
  await page.route("**/api/v1/personal/followups/*/continue", async route => {
    const body = route.request().postDataJSON();
    await route.continue({ postData: JSON.stringify({ ...body, version: 0 }) });
  }, { times: 1 });
  await needs.getByRole("button", { name: "继续核实", exact: true }).click();
  await page.getByRole("region", {name:"持续跟进",exact:true}).getByRole("alert").filter({ hasText: /更新|变化|冲突|版本/ }).waitFor();
  check("stale version shown and draft kept", await composer.inputValue() === "这段未发送的草稿应始终保留");
  // Expected 409 is a browser network diagnostic, not a client exception.
  await needs.getByRole("button", { name: "继续核实", exact: true }).click();
  const needsId = initial.find(f => f.title === "核实导出功能的实际结果").id;
  await wait(async () => (await all()).find(f => f.id === needsId).state === "completed");
  check("manual continuation completes with evidence", (await all()).find(f => f.id === needsId).evidence.includes("隔离演示"));

  const later = page.getByRole("article", { name: "下午核对资料更新", exact: true });
  await later.getByRole("button", { name: /下午核对资料更新/ }).click();
  await later.getByRole("button", { name: "取消跟进", exact: true }).click();
  await later.getByRole("button", { name: "保留跟进", exact: true }).click();
  check("cancel can be dismissed", await later.getByRole("button", { name: "取消跟进", exact: true }).isVisible());
  await later.getByRole("button", { name: "取消跟进", exact: true }).click();
  await later.getByRole("button", { name: "确认取消", exact: true }).click();
  const laterId = initial.find(f => f.title === "下午核对资料更新").id;
  await wait(async () => (await all()).find(f => f.id === laterId).state === "cancelled");
  const sc = await (await page.request.get(origin + "/api/v1/schedules")).json();
  check("cancel disables the original timer", sc.find(s => s.followupId === laterId).enabled === false);
  await page.getByText("可以在对话里说“帮我持续跟进这件事”或“下午再核对一次”。", { exact: true }).waitFor();
  check("empty active list retains history", await page.getByText("已结束 · 4 项", { exact: true }).isVisible());
  await page.getByText("已结束 · 4 项", { exact: true }).click();
  await page.getByRole("button", { name: "下午核对资料更新 已取消", exact: true }).click();
  check("closed followups have no resume button", await page.getByRole("article", { name: "下午核对资料更新", exact: true }).getByRole("button", { name: "继续核实", exact: true }).count() === 0);
  check("draft survives all mutations", await composer.inputValue() === "这段未发送的草稿应始终保留");
  await page.setViewportSize({ width: 320, height: 568 });
  check("320px no horizontal overflow", await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  check("320x568 composer remains fully on screen", await page.locator(".today-composer").evaluate(e=>e.getBoundingClientRect().bottom<=innerHeight+1));
  await shot("08-mobile-history.png");
  await page.setViewportSize({ width: 1280, height: 600 });
  check("short desktop composer remains fully on screen", await page.locator(".today-composer").evaluate(e=>e.getBoundingClientRect().bottom<=innerHeight+1));
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByRole("button", { name: /持续跟进.*暂无进行中事项/ }).click();
  await shot("09-desktop-final.png");
  check("no client exceptions", errors.filter(e => !e.includes("409")).length === 0);
  check("no project write calls", !requests.some(r => /\/projects\//.test(r.path)));
  await fs.writeFile(path.join(dir, "ui-report.json"), JSON.stringify({ passed: true, checks, errors, requests, urls }, null, 2));
  console.log(JSON.stringify({ passed: checks.length, errors }, null, 2));
} catch (e) {
  await shot("ui-failure.png");
  await fs.writeFile(path.join(dir, "ui-report.json"), JSON.stringify({ passed: false, checks, errors, failure: String(e) }, null, 2));
  throw e;
} finally { await browser.close(); }
