// Isolated browser regression: real compiled app, disposable in-memory API.
// No production URL, credentials, model calls or user data are used.
import assert from "node:assert/strict";
import { createServer } from "node:http";
import fs from "node:fs/promises";
import path from "node:path";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";

const baseline = process.env.PW_MENU_BASELINE === "1";
const p2Baseline = process.env.PW_MENU_P2_BASELINE === "1";
const sseOnly = p2Baseline || process.env.PW_MENU_SSE_ONLY === "1";
const keepPreview = process.env.PW_MENU_PREVIEW === "1" && !baseline;
const dir = path.resolve(p2Baseline ? "artifacts/session-menu-p2/baseline" : "artifacts/session-menu");
const dist = path.resolve(p2Baseline ? "artifacts/session-menu-p2/before-dist" : baseline ? "artifacts/session-menu/before-dist" : "web/dist");
await fs.mkdir(dir, { recursive: true });
const project = {
  id: "menu-fixture", name: "小花ERP", description: "隔离 UI 验收",
  aliases: [], defaultSessionId: "s1", expertIds: [], defaultExpertId: "",
  pluginIds: [], archived: false,
};
const initialSessions = () => [
  { id: "s1", projectId: project.id, title: "收敛小花ERP归档待办为今日任务", kind: "topic", updatedAt: 1 },
  { id: "s2", projectId: project.id, title: "项目助手", kind: "assistant", updatedAt: 1 },
];
let sessions = initialSessions();
let fixtureRuns = [], eventLog = [];
let eventSequence = 0, streamConnections = 0, refreshes = 0;
const streams = new Set();
const sentEvents = [];
const emit = (type, data, runId = "") => {
  const event = { seq: ++eventSequence, type, data, sessionId: data.projectId ? data.id : "s1", runId, createdAt: 1 };
  if (type.startsWith("harness.")) event.sessionId = "s1";
  eventLog.push(event);
  sentEvents.push({ seq: event.seq, type });
  assert(streams.size > 0, "Expected a native EventSource connection");
  for (const response of streams) response.write(`data: ${JSON.stringify(event)}\n\n`);
};
const requests = [], failures = [], unknown = [];
const server = createServer(async (req, res) => {
  try {
    const pathname = new URL(req.url, "http://fixture").pathname;
    if (pathname === "/favicon.ico") { res.writeHead(204); res.end(); return; }
    if (pathname.startsWith("/api/v1/")) {
      const route = pathname.slice("/api/v1".length);
      if (route === "/events" && !req.url.includes("json=1")) {
        res.writeHead(200, { "Content-Type": "text/event-stream; charset=utf-8", "Cache-Control": "no-cache" });
        res.flushHeaders();
        res.write(": isolated fixture\n\n");
        streams.add(res);
        streamConnections++;
        res.on("close", () => streams.delete(res));
        return;
      }
      let result;
      if (route === "/auth") result = { authenticated: true };
      else if (route === "/projects") { refreshes++; result = [project]; }
      else if (route === `/projects/${project.id}/sessions`) result = sessions;
      else if (route === "/runs") result = fixtureRuns;
      else if (route === "/events") result = eventLog;
      else if (["/experts", "/inbox", "/interactions",
        `/projects/${project.id}/files`, `/projects/${project.id}/tasks`,
        `/projects/${project.id}/capabilities`].includes(route)) result = [];
      else if (route.startsWith("/sessions/")) {
        const [, , id, operation] = route.split("/");
        const session = sessions.find(s => s.id === id);
        assert(session, `Unknown fixture session ${id}`);
        let body = "";
        for await (const chunk of req) body += chunk;
        const data = body ? JSON.parse(body) : {};
        if (req.method !== "GET") {
          requests.push({ route, method: req.method, data });
          if (operation === "pin") session.pinnedAt = data.pinned ? 5 : 0;
          else if (operation === "archive") delete session.lifecycle;
          else if (req.method === "DELETE") sessions = sessions.filter(s => s.id !== id);
          else if (req.method === "PUT") session.title = data.title;
          session.updatedAt++;
        }
        result = session;
      } else {
        unknown.push(`${req.method} ${route}`);
        res.writeHead(404, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: "Unknown fixture API" }));
        return;
      }
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify(result));
      return;
    }
    const file = path.resolve(dist, "." + (pathname === "/" ? "/index.html" : pathname));
    assert(file.startsWith(dist + path.sep));
    const contentType = file.endsWith(".js") ? "text/javascript" : file.endsWith(".css") ? "text/css" : "text/html";
    res.writeHead(200, { "Content-Type": contentType });
    res.end(await fs.readFile(file));
  } catch (error) {
    failures.push(error.message);
    res.writeHead(500);
    res.end("Fixture error");
  }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}/`;
const browser = await chromium.launch({ headless: true, executablePath:
  process.env.PW_CHROME || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" });
const page = await browser.newPage({ viewport: { width: 1280, height: 720 }, deviceScaleFactor: 1 });
page.setDefaultTimeout(8000);
page.on("pageerror", error => failures.push(error.message));
page.on("console", message => { if (message.type() === "error") failures.push(message.text()); });
const report = { passed: false, baseline, p2Baseline, checks: [], screenshots: [], viewports: [], regressionFailures: [] };
const check = async (name, run) => { await run(); report.checks.push(name); };
const checkSSE = async (name, run) => {
  try { await check(name, run); }
  catch (error) {
    if (!p2Baseline) throw error;
    report.regressionFailures.push({ name, error: error.message });
  }
};
const waitFor = async (run) => {
  for (let i = 0; i < 60; i++) {
    try { return await run(); } catch (error) {
      if (i === 59) throw error;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
  }
};
const capture = async (name) => {
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  const file = path.join(dir, name + ".png");
  await page.mouse.move(10, 10);
  await page.screenshot({ path: file });
  report.screenshots.push(file);
  if (name === "before-desktop" || name === "after-desktop") {
    const focused = path.join(dir, name.replace("desktop", "sidebar") + ".png");
    await page.locator(".project-panel").screenshot({ path: focused });
    report.screenshots.push(focused);
  }
};
const row = id => page.locator(`[data-session-id="${id}"]`);
const trigger = id => row(id).getByRole("button", { name: /^对话操作：/ });
const menu = () => page.locator(".session-menu-popover:popover-open");
const open = async (id = "s1") => {
  await trigger(id).click();
  await menu().waitFor();
  await waitFor(async () => assert(await menu().getByRole("menuitem").first().evaluate(el => el === document.activeElement)));
};
const closed = async () => waitFor(async () => assert.equal(await menu().count(), 0));
const outside = async () => {
  const chat = await page.locator(".chat").boundingBox();
  await page.mouse.click(chat.x + chat.width - 12, chat.y + 180);
};
let loadNumber = 0;
const load = async () => {
  await page.goto(url + `?fixture=${++loadNumber}#project=${project.id}&session=s1`);
  await row("s1").waitFor();
  await page.getByLabel("发送消息", { exact: true }).waitFor();
};
const loadSSE = async (runs = []) => {
  sessions = initialSessions(); fixtureRuns = runs; eventLog = [];
  const connections = streamConnections;
  await load();
  await waitFor(async () => assert(streamConnections > connections));
  await page.locator(".chat-scroll").evaluate(el => el.scrollTo({ top: el.scrollHeight, behavior: "instant" }));
  await waitFor(async () => assert(await page.locator(".chat-scroll").evaluate(el => el.scrollTop + el.clientHeight >= el.scrollHeight - 2)));
};
const anchorRect = async () => trigger("s2").evaluate(el => {
  const { x, y, width, height } = el.getBoundingClientRect();
  return { x, y, width, height };
});
const bounds = async () => {
  const rect = await menu().boundingBox();
  const viewport = page.viewportSize();
  assert(rect.x >= 7 && rect.y >= 7, JSON.stringify(rect));
  assert(rect.x + rect.width <= viewport.width - 7, JSON.stringify(rect));
  assert(rect.y + rect.height <= viewport.height - 7, JSON.stringify(rect));
  const deletion = menu().getByRole("menuitem", { name: "删除", exact: true });
  await deletion.click({ trial: true });
  assert(await deletion.evaluate(el => {
    const r = el.getBoundingClientRect();
    return el.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2));
  }));
};
try {
  await load();
  if (baseline) {
    await row("s1").locator("summary").click();
    await capture("before-desktop");
    await check("Old menu is clipped below the list", async () => {
      const list = await page.locator(".session-list").boundingBox();
      const deletion = await row("s1").getByRole("button", { name: "删除", exact: true }).boundingBox();
      assert(deletion.y + deletion.height > list.y + list.height);
    });
    await outside();
    await check("Old menu remains open after outside click and Escape", async () => {
      await page.keyboard.press("Escape");
      assert.equal(await page.locator(".session-menu[open]").count(), 1);
    });
    await row("s1").locator("summary").click();
    await row("s2").locator("summary").click();
    await capture("before-second-row");
    await row("s1").locator("summary").click();
    await check("Old menus can remain open together", async () => assert.equal(await page.locator(".session-menu[open]").count(), 2));
  } else if (!sseOnly) {
    await check("All four menu items escape list clipping", async () => {
      await open();
      assert.equal(await menu().getByRole("menuitem").count(), 4);
      await bounds();
      await capture("after-desktop");
    });
    await check("Same trigger closes; outside click closes", async () => {
      await trigger("s1").click(); await closed();
      await open(); await outside(); await closed();
    });
    await check("Escape closes and returns focus; Tab exits", async () => {
      await open(); await page.keyboard.press("Escape"); await closed();
      assert(await trigger("s1").evaluate(el => el === document.activeElement));
      await open(); await page.keyboard.press("Tab"); await closed();
      assert.equal(await page.locator(".session-menu-popover").evaluateAll(els => els.some(el => el.contains(document.activeElement))), false);
    });
    await check("Only one menu stays open", async () => {
      await open("s2"); await bounds(); await capture("after-second-row");
      await open();
      assert.equal(await menu().count(), 1);
      assert.equal(await trigger("s2").getAttribute("aria-expanded"), "false");
      await page.keyboard.press("Escape");
    });
    await check("Arrow keys, Home/End and Enter open/select", async () => {
      await trigger("s1").focus(); await page.keyboard.press("ArrowUp");
      await waitFor(async () => assert.equal(await page.locator(":focus").innerText(), "删除"));
      await page.keyboard.press("Home");
      assert.equal(await page.locator(":focus").innerText(), "置顶");
      await page.keyboard.press("ArrowDown");
      assert.equal(await page.locator(":focus").innerText(), "重命名");
      await page.keyboard.press("End");
      assert.equal(await page.locator(":focus").innerText(), "删除");
      await page.keyboard.press("Escape");
      await page.keyboard.press("Enter"); await menu().waitFor();
      await page.keyboard.press("Escape");
      await page.keyboard.press("Space"); await menu().waitFor();
      await page.keyboard.press("Shift+Tab"); await closed();
    });
    await check("Pin invokes existing API once and closes", async () => {
      await open(); await menu().getByRole("menuitem", { name: "置顶", exact: true }).click();
      await closed(); await waitFor(async () => assert.equal(requests.filter(r => r.route.endsWith("/pin")).length, 1));
      await open(); assert.equal(await menu().getByText("取消置顶", { exact: true }).count(), 1);
      await page.keyboard.press("Escape");
    });
    await check("Rename closes first and preserves exact session handler", async () => {
      await open(); await menu().getByRole("menuitem", { name: "重命名", exact: true }).click(); await closed();
      const dialog = page.getByRole("dialog", { name: "重命名对话" });
      await dialog.getByLabel("对话标题").fill("菜单修复验收对话");
      await dialog.getByRole("button", { name: "保存", exact: true }).click();
      await waitFor(async () => assert((await row("s1").locator(".session-select").innerText()).startsWith("菜单修复验收对话")));
    });
    await check("Archive/delete open confirmations without submitting", async () => {
      const before = requests.length;
      for (const action of ["归档", "删除"]) {
        await open(); await menu().getByRole("menuitem", { name: action, exact: true }).click(); await closed();
        const dialog = page.getByRole("dialog", { name: action + "对话" });
        await dialog.waitFor(); await dialog.getByRole("button", { name: "关闭", exact: true }).click();
      }
      assert.equal(requests.length, before);
    });
    await check("Context switch closes without requiring outside pointer", async () => {
      await page.getByLabel("发送消息", { exact: true }).fill("菜单验收草稿，不发送");
      await open(); await row("s2").locator(".session-select").evaluate(el => el.click()); await closed();
      await row("s1").locator(".session-select").click();
      assert.equal(await page.getByLabel("发送消息", { exact: true }).inputValue(), "菜单验收草稿，不发送");
      await open(); await page.getByRole("button", { name: "文件", exact: true }).click(); await closed();
      await page.getByRole("button", { name: "对话", exact: true }).click();
    });
    sessions = initialSessions(); sessions[0].lifecycle = "archiving"; await load();
    await check("Archiving preserves disabled items and stop action", async () => {
      await open();
      assert.equal(await menu().locator("button:disabled").count(), 4);
      const before = requests.length;
      await menu().locator("button:disabled").first().evaluate(el => el.click());
      assert.equal(requests.length, before);
      assert.equal(await menu().count(), 1);
      assert.equal(await page.locator(":focus").innerText(), "停止归档");
      await page.keyboard.press("ArrowDown");
      assert.equal(await page.locator(":focus").innerText(), "停止归档");
      await capture("after-archiving");
      await menu().getByRole("menuitem", { name: "停止归档" }).click(); await closed();
      await waitFor(async () => assert.equal(requests.filter(r => r.route.endsWith("/archive/stop")).length, 1));
    });
    sessions = [...initialSessions(), ...Array.from({ length: 28 }, (_, i) => ({ ...initialSessions()[1], id: `long-${i}`, title: `长列表对话 ${i}` }))];
    await page.setViewportSize({ width: 1280, height: 480 }); await load();
    await check("Bottom row flips upward and list scroll closes", async () => {
      await trigger("long-27").scrollIntoViewIfNeeded(); await open("long-27"); await bounds();
      const anchor = await trigger("long-27").boundingBox(), panel = await menu().boundingBox();
      assert(panel.y + panel.height <= anchor.y);
      await capture("after-bottom");
      await page.locator(".session-list").evaluate(el => { el.scrollTop -= 60; }); await closed();
    });
    await check("Resize closes stale placement", async () => {
      await trigger("s1").scrollIntoViewIfNeeded(); await open();
      await page.setViewportSize({ width: 1154, height: 828 }); await closed();
    });
    sessions = initialSessions(); await load();
    for (const viewport of [{ width: 1154, height: 828 }, { width: 390, height: 844 }, { width: 320, height: 568 }]) {
      await page.setViewportSize(viewport);
      if (viewport.width < 768) await page.getByRole("button", { name: "打开项目侧栏" }).click();
      await open("s2"); await bounds();
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
      await capture(`after-${viewport.width}`); report.viewports.push(viewport);
      await outside(); await closed();
      if (viewport.width < 768 && await page.locator(".project-panel.open").count())
        await page.locator(".drawer-close").click();
    }
    await check("Touch opens and dismisses the mobile menu", async () => {
      const context = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
      try {
        const mobile = await context.newPage();
        mobile.on("pageerror", error => failures.push(error.message));
        await mobile.goto(url + `#project=${project.id}&session=s1`);
        await mobile.getByRole("button", { name: "打开项目侧栏" }).tap();
        await mobile.locator('[data-session-id="s2"]').getByRole("button", { name: /^对话操作：/ }).tap();
        await mobile.locator(".session-menu-popover:popover-open").waitFor();
        await mobile.touchscreen.tap(378, 550);
        await waitFor(async () => assert.equal(await mobile.locator(".session-menu-popover:popover-open").count(), 0));
      } finally { await context.close(); }
    });
  }
  if (!baseline) {
    await page.setViewportSize({ width: 1280, height: 720 });
    await checkSSE("Native SSE chat auto-scroll preserves an actionable menu", async () => {
      const run = { id: "sse-run", sessionId: "s1", projectId: project.id, state: "completed", text: "SSE 菜单回归夹具",
        result: Array.from({ length: 60 }, (_, i) => `第 ${i} 段，验证聊天自动滚动不会关闭侧栏菜单。`).join("\n\n"),
        createdAt: 1, startedAt: 1, finishedAt: 2, source: "web" };
      await loadSSE([run]);
      await page.locator(".chat-scroll").evaluate(el => el.scrollTo({ top: 0, behavior: "instant" }));
      await waitFor(async () => assert.equal(await page.locator(".chat-scroll").evaluate(el => el.scrollTop), 0));
      await open("s2");
      const before = await anchorRect();
      await capture("sse-chat-before");
      await page.locator(".chat-scroll").evaluate(el => {
        window.__sseMenuScrollEnded = false;
        el.addEventListener("scrollend", () => { window.__sseMenuScrollEnded = true; }, { once: true });
      });
      emit("harness.tool/call", { callId: "sse-call", name: "菜单回归", arguments: {} }, run.id);
      await waitFor(async () => assert.match(await page.locator(".activity-summary").innerText(), /1\s*次工具调用/));
      await page.waitForFunction(() => window.__sseMenuScrollEnded);
      assert(await page.locator(".chat-scroll").evaluate(el => el.scrollTop > 10));
      assert.deepEqual(await anchorRect(), before);
      assert.equal(await menu().count(), 1, "Chat auto-scroll must not close the sidebar menu");
      await capture("sse-chat-after");
      await menu().getByRole("menuitem", { name: "归档", exact: true }).click();
      await closed();
      await page.getByRole("dialog", { name: "归档对话" }).getByRole("button", { name: "关闭", exact: true }).click();
    });
    await checkSSE("Native SSE pin reorder dismisses stale placement and reopens at its anchor", async () => {
      await loadSSE(); await open("s2");
      const before = await anchorRect();
      await capture("sse-pin-before");
      sessions[1] = { ...sessions[1], pinnedAt: 99, updatedAt: 99 };
      emit("session.updated", sessions[1]);
      await waitFor(async () => assert.equal(await page.locator(".session-row").first().getAttribute("data-session-id"), "s2"));
      assert.notEqual((await anchorRect()).y, before.y);
      await closed();
      await open("s2"); await bounds();
      const anchor = await trigger("s2").boundingBox(), panel = await menu().boundingBox();
      assert(Math.abs(panel.y - anchor.y - anchor.height - 4) < 1);
      await capture("sse-pin-reopened");
      await menu().getByRole("menuitem", { name: "重命名", exact: true }).click();
      const dialog = page.getByRole("dialog", { name: "重命名对话" });
      assert.equal(await dialog.getByLabel("对话标题").inputValue(), "项目助手");
      await dialog.getByRole("button", { name: "关闭", exact: true }).click();
    });
    await checkSSE("Native SSE title wrapping dismisses a menu whose row moved", async () => {
      await loadSSE(); await open("s2");
      const before = await anchorRect();
      sessions[0] = { ...sessions[0], title: "前序对话收到自动标题更新且换行。".repeat(5), updatedAt: 99 };
      emit("session.updated", sessions[0]);
      await waitFor(async () => assert.notEqual((await anchorRect()).y, before.y));
      await closed(); await open("s2"); await bounds();
      await capture("sse-wrapped-title");
    });
    await checkSSE("Native SSE metadata and refresh preserve a menu when its anchor stays put", async () => {
      await loadSSE(); await open("s2");
      const before = await anchorRect(), refreshed = refreshes;
      sessions[0] = { ...sessions[0], pinnedAt: 99, updatedAt: 99 };
      emit("session.updated", sessions[0]);
      await row("s1").locator(".session-select .lucide-pin").waitFor();
      await waitFor(async () => assert(refreshes > refreshed));
      assert.deepEqual(await anchorRect(), before);
      assert.equal(await menu().count(), 1);
      await capture("sse-stable-metadata");
    });
    await checkSSE("Native SSE appending a session preserves a menu at an unchanged anchor", async () => {
      await loadSSE(); await open("s2");
      const before = await anchorRect();
      const added = { ...sessions[1], id: "sse-added", title: "新到达的对话", updatedAt: 0 };
      sessions.push(added);
      emit("session.created", added);
      await row(added.id).waitFor();
      assert.deepEqual(await anchorRect(), before);
      assert.equal(await menu().count(), 1);
      await capture("sse-stable-append");
    });
  }
  assert.deepEqual(unknown, []);
  assert.deepEqual(failures, []);
  assert.deepEqual(report.regressionFailures, [], "SSE regression assertions failed");
  report.passed = true;
  if (keepPreview) report.previewURL = url + `#project=${project.id}&session=s1`;
} catch (error) {
  report.error = error.stack;
  await capture(baseline ? "before-failure" : "after-failure").catch(() => {});
  throw error;
} finally {
  report.requests = requests;
  report.consoleAndPageErrors = failures;
  report.unknownAPI = unknown;
  report.sse = { nativeConnections: streamConnections, sent: sentEvents };
  await fs.writeFile(path.join(dir, baseline ? "baseline.json" : "report.json"), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
  await browser.close();
  if (!keepPreview || !report.passed) {
    for (const response of streams) response.end();
    await new Promise(resolve => server.close(resolve));
  } else { sessions = initialSessions(); fixtureRuns = []; eventLog = []; }
}
