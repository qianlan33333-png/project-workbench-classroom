// Production acceptance for an explicitly authorized frontend release.
// Uses the dedicated fixture project created by deployment preflight; no mocks.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { createHash, randomUUID } from "node:crypto";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";

const cfg = JSON.parse(
  await fs.readFile(
    path.join(os.homedir(), ".config/project-workbench/cli.json"),
    "utf8",
  ),
);
const fixture = JSON.parse(
  await fs.readFile(".var/popup-live-fixture.json", "utf8"),
);
const manifest = JSON.parse(
  await fs.readFile(".var/popup-release-manifest.json", "utf8"),
);
const url = cfg.url.replace(/\/$/, "");
assert.equal(fixture.url, url);
assert(fixture.project.name.startsWith("菜单上线隔离验收 "));
const pid = fixture.project.id,
  s1 = fixture.s1,
  s2 = fixture.s2;
const authorization =
  "Basic " + Buffer.from("owner:" + cfg.password).toString("base64");
const redact = (value) => String(value).replaceAll(cfg.password, "[redacted]");
const dir = path.resolve("artifacts/popup-audit/live");
await fs.mkdir(dir, { recursive: true });
const report = {
  passed: false,
  url,
  frontendRevision: manifest.frontendRevision,
  project: pid,
  checks: [],
  screenshots: [],
  consoleErrors: [],
  expectedHistorical404: 0,
  sseResponses: [],
  models: [],
};
const api = async (route, body, method) => {
  const response = await fetch(url + "/api/v1" + route, {
    method: method || (body === undefined ? "GET" : "POST"),
    headers: {
      Authorization: authorization,
      "Content-Type": "application/json",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  assert.equal(response.status, 200, `${response.status} ${route}`);
  return response.json();
};
const wait = async (fn, timeout = 20000) => {
  const end = Date.now() + timeout;
  for (;;) {
    try {
      return await fn();
    } catch (error) {
      if (Date.now() >= end) throw error;
      await new Promise((resolve) => setTimeout(resolve, 150));
    }
  }
};
const check = async (name, fn) => {
  await fn();
  report.checks.push(name);
  console.log("passed: " + name);
};
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex");
let browser, page, testExpert;
const row = (id) => page.locator(`[data-session-id="${id}"]`);
const trigger = (id) => row(id).getByRole("button", { name: /^对话操作：/ });
const menu = () => page.locator(".session-menu-popover:popover-open");
const closed = () => wait(async () => assert.equal(await menu().count(), 0));
const open = async (id) => {
  await trigger(id).click();
  await menu().waitFor();
  await page.waitForFunction(
    () => document.activeElement?.getAttribute("role") === "menuitem",
  );
};
const rect = (id) =>
  trigger(id).evaluate((el) => {
    const { x, y, width, height } = el.getBoundingClientRect();
    return { x, y, width, height };
  });
const screenshot = async (name) => {
  await page.mouse.move(10, 10);
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise((resolve) =>
      requestAnimationFrame(() => requestAnimationFrame(resolve)),
    );
  });
  const file = path.join(dir, name + ".png");
  await page.screenshot({ path: file });
  report.screenshots.push(file);
};
const bounds = async () => {
  const b = await menu().boundingBox(),
    v = page.viewportSize();
  assert(
    b.x >= 7 &&
      b.y >= 7 &&
      b.x + b.width <= v.width - 7 &&
      b.y + b.height <= v.height - 7,
  );
  await menu()
    .getByRole("menuitem", { name: "删除", exact: true })
    .click({ trial: true });
};
const cancelDialog = async (name) => {
  const dialog = page.getByRole("dialog", { name });
  await dialog.waitFor();
  await closed();
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
};
try {
  await check(
    "Public index and every referenced asset match the accepted artifact",
    async () => {
      report.publicAssets = {};
      for (const [name, digest] of Object.entries(manifest.files)) {
        const response = await fetch(url + "/" + name, { cache: "no-store" });
        assert.equal(response.status, 200);
        const actual = sha(Buffer.from(await response.arrayBuffer()));
        assert.equal(actual, digest, name);
        report.publicAssets[name] = actual;
      }
    },
  );
  browser = await chromium.launch({
    headless: true,
    executablePath:
      process.env.PW_CHROME ||
      "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  });
  const context = await browser.newContext({
    viewport: { width: 1280, height: 720 },
    deviceScaleFactor: 1,
    hasTouch: true,
  });
  page = await context.newPage();
  page.setDefaultTimeout(15000);
  page.on("pageerror", (error) =>
    report.consoleErrors.push(redact(error.message)),
  );
  page.on("console", (message) => {
    if (message.type() !== "error") return;
    const source = message.location().url;
    if (
      /\/api\/v1\/sessions\//.test(source) &&
      !source.includes(s1) &&
      !source.includes(s2) &&
      /404|400/.test(message.text())
    )
      report.expectedHistorical404++;
    else report.consoleErrors.push(redact(message.text()));
  });
  page.on("response", (response) => {
    if (
      response.url().startsWith(url + "/api/v1/events") &&
      response.headers()["content-type"]?.includes("text/event-stream")
    )
      report.sseResponses.push({
        status: response.status(),
        contentType: response.headers()["content-type"],
        encoding: response.headers()["content-encoding"] || "identity",
      });
  });
  await page.goto(url + "/");
  await page.getByLabel("密码", { exact: true }).fill(cfg.password);
  await page.getByRole("button", { name: "进入工作台" }).click();
  await page
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: fixture.project.name, exact: true })
    .click();
  await trigger(s2).waitFor();
  await page.getByLabel("发送消息", { exact: true }).waitFor();
  await page
    .locator(".assistant-message")
    .getByText(fixture.token, { exact: false })
    .waitFor();
  const history = await api(`/events?json=1&sessionId=${s1}`),
    after = Math.max(0, ...history.map((e) => e.seq));
  await page.evaluate(
    ({ pid, ids, after }) => {
      window.__menuLiveSSE = { open: false, events: [] };
      const stream = new EventSource(`/api/v1/events?after=${after}`);
      stream.onopen = () => {
        window.__menuLiveSSE.open = true;
      };
      stream.onmessage = (message) => {
        const event = JSON.parse(message.data);
        if (ids.includes(event.sessionId) || event.data?.projectId === pid)
          window.__menuLiveSSE.events.push({
            seq: event.seq,
            type: event.type,
            runId: event.runId,
          });
      };
      window.__menuLiveStream = stream;
    },
    { pid, ids: [s1, s2], after },
  );
  await page.waitForFunction(() => window.__menuLiveSSE.open);
  await check("Four live menu actions remain fully visible", async () => {
    await open(s1);
    assert.equal(await menu().getByRole("menuitem").count(), 4);
    await bounds();
    await screenshot("desktop-menu");
  });
  await check(
    "Repeat click, outside click, Escape and Tab dismiss live menus",
    async () => {
      await trigger(s1).click();
      await closed();
      await open(s1);
      const box = await page.locator(".chat").boundingBox();
      await page.mouse.click(box.x + box.width - 12, box.y + 180);
      await closed();
      await open(s1);
      await page.keyboard.press("Escape");
      await closed();
      assert(await trigger(s1).evaluate((el) => el === document.activeElement));
      await open(s1);
      await page.keyboard.press("Tab");
      await closed();
    },
  );
  await check(
    "Keyboard navigation, mutual exclusion and confirmations work",
    async () => {
      await open(s2);
      await open(s1);
      assert.equal(await menu().count(), 1);
      await page.keyboard.press("End");
      assert.equal(await page.locator(":focus").innerText(), "删除");
      await page.keyboard.press("Home");
      assert.equal(await page.locator(":focus").innerText(), "取消置顶");
      await page.keyboard.press("Escape");
      for (const action of ["重命名", "归档", "删除"]) {
        await open(s1);
        await menu()
          .getByRole("menuitem", { name: action, exact: true })
          .click();
        await cancelDialog(action + "对话");
      }
    },
  );
  await check(
    "Real Harness and native SSE auto-scroll preserve an actionable menu",
    async () => {
      await page
        .locator(".chat-scroll")
        .evaluate((el) => el.scrollTo({ top: 0, behavior: "instant" }));
      await open(s2);
      const before = await rect(s2);
      const token = "POST_" + fixture.token;
      const message = await api("/messages", {
        sessionId: s1,
        text: `仅回复 ${token}。这是网页菜单与 SSE 验收，不使用工具、不修改文件、不执行外部操作。`,
        eventId: "menu-live-" + randomUUID(),
      });
      const completed = await wait(async () => {
        const runs = await api(`/runs?sessionId=${s1}`),
          run = runs.find((r) => r.messageId === message.id);
        assert(run);
        assert.equal(
          run.state,
          "completed",
          "Own acceptance run state: " + run.state,
        );
        assert(run.result.includes(token));
        return run;
      }, 180000);
      await page
        .locator(".assistant-message")
        .getByText(token, { exact: false })
        .waitFor();
      await wait(async () =>
        assert(
          await page
            .locator(".chat-scroll")
            .evaluate((el) => el.scrollTop > 20),
        ),
      );
      assert.equal(await menu().count(), 1);
      assert.deepEqual(await rect(s2), before);
      assert(
        await page.evaluate(
          (id) => window.__menuLiveSSE.events.some((e) => e.runId === id),
          completed.id,
        ),
      );
      report.models.push({
        id: completed.id,
        state: completed.state,
        tokenVerified: true,
      });
      await screenshot("live-harness-sse-menu-open");
      await menu().getByRole("menuitem", { name: "归档", exact: true }).click();
      await cancelDialog("归档对话");
    },
  );
  await check(
    "External live pin SSE closes moved menus and reopens at the new anchor",
    async () => {
      await open(s2);
      const before = await rect(s2),
        start = Date.now();
      await api(`/sessions/${s2}/pin`, { pinned: true }, "PUT");
      await wait(async () =>
        assert.equal(
          await page
            .locator(".session-row")
            .first()
            .getAttribute("data-session-id"),
          s2,
        ),
      );
      assert.notEqual((await rect(s2)).y, before.y);
      await closed();
      report.pinSSEVisibleMs = Date.now() - start;
      await open(s2);
      await bounds();
      const anchor = await rect(s2),
        panel = await menu().boundingBox();
      assert(Math.abs(panel.y - anchor.y - anchor.height - 4) < 1);
      await screenshot("live-sse-pin-reopened");
      await page.keyboard.press("Escape");
    },
  );
  await check(
    "Live title wrapping SSE closes a moved lower-row menu",
    async () => {
      await open(s1);
      const before = await rect(s1);
      const title = "上线验收标题换行，用于核对菜单锚点。".repeat(3);
      assert(Array.from(title).length <= 80);
      await api(`/sessions/${s2}`, { title }, "PUT");
      await wait(async () => assert.notEqual((await rect(s1)).y, before.y));
      await closed();
      await open(s1);
      await bounds();
      await screenshot("live-sse-title-wrapping");
      await page.keyboard.press("Escape");
      await api(`/sessions/${s2}`, { title: "上线验收 · 第二条菜单" }, "PUT");
      await wait(async () =>
        assert(
          (await row(s2).locator(".session-select").innerText()).includes(
            "第二条菜单",
          ),
        ),
      );
    },
  );
  await check(
    "Stable live metadata and a new trailing session keep the menu open",
    async () => {
      await open(s2);
      const before = await rect(s2);
      const previous = await page.evaluate(
        () => window.__menuLiveSSE.events.length,
      );
      await api(`/sessions/${s1}`, { title: "上线验收 · 固定首行" }, "PUT");
      await page.waitForFunction(
        (n) => window.__menuLiveSSE.events.length > n,
        previous,
      );
      assert.deepEqual(await rect(s2), before);
      assert.equal(await menu().count(), 1);
      const added = await api(`/projects/${pid}/sessions`, {
        title: "上线验收 · 新增后序会话",
      });
      await row(added.id).waitFor();
      assert.deepEqual(await rect(s2), before);
      assert.equal(await menu().count(), 1);
      await screenshot("live-sse-stable-anchor");
      await page.keyboard.press("Escape");
    },
  );
  await check(
    "Lower live list flips upward; sidebar scrolling closes the menu",
    async () => {
      for (let i = 0; i < 7; i++)
        await api(`/projects/${pid}/sessions`, {
          title: "上线验收 · 长列表 " + i,
        });
      await wait(async () =>
        assert.equal(await page.locator(".session-row").count(), 10),
      );
      await page.setViewportSize({ width: 1280, height: 480 });
      const last = await page
        .locator(".session-row")
        .last()
        .getAttribute("data-session-id");
      await trigger(last).scrollIntoViewIfNeeded();
      await open(last);
      await bounds();
      const anchor = await rect(last),
        panel = await menu().boundingBox();
      assert(panel.y + panel.height <= anchor.y);
      await screenshot("live-bottom-menu");
      await page.locator(".session-list").evaluate((el) => {
        el.scrollTop -= 60;
      });
      await closed();
    },
  );
  await check(
    "Mobile live viewport, touch dismissal and drafts are preserved",
    async () => {
      await page
        .getByLabel("发送消息", { exact: true })
        .fill("上线菜单验收草稿，不发送");
      for (const width of [390, 320]) {
        await page.setViewportSize({
          width,
          height: width === 390 ? 844 : 568,
        });
        await page.getByRole("button", { name: "打开项目侧栏" }).tap();
        await trigger(s2).scrollIntoViewIfNeeded();
        await trigger(s2).tap();
        await menu().waitFor();
        await bounds();
        assert(
          await page.evaluate(
            () => document.documentElement.scrollWidth <= window.innerWidth,
          ),
        );
        await screenshot("live-mobile-" + width);
        await page.touchscreen.tap(width - 12, 550);
        await closed();
        if (await page.locator(".project-panel.open").count())
          await page.locator(".drawer-close").tap();
      }
      assert.equal(
        await page.getByLabel("发送消息", { exact: true }).inputValue(),
        "上线菜单验收草稿，不发送",
      );
    },
  );
  await check(
    "Project file add menu dismisses, stays in bounds and preserves file creation",
    async () => {
      await page.setViewportSize({ width: 1280, height: 720 });
      await page.getByRole("button", { name: "文件", exact: true }).click();
      const add = page.getByRole("button", {
        name: "添加项目文件",
        exact: true,
      });
      const panel = page.locator(".add-menu:popover-open");
      await add.click();
      assert.equal(await panel.getByRole("menuitem").count(), 5);
      await screenshot("project-add");
      await page.keyboard.press("Escape");
      assert.equal(await panel.count(), 0);
      await add.click();
      await page.mouse.click(1240, 80);
      assert.equal(await panel.count(), 0);
      await add.click();
      const [chooser] = await Promise.all([
        page.waitForEvent("filechooser"),
        panel.getByRole("menuitem", { name: "上传文件", exact: true }).click(),
      ]);
      assert(chooser);
      assert.equal(await panel.count(), 0);
      await add.click();
      await panel
        .getByRole("menuitem", { name: "新建文件", exact: true })
        .click();
      assert.equal(await panel.count(), 0);
      await page
        .locator(".inline-create")
        .getByRole("button", { name: "取消", exact: true })
        .click();
      await page.setViewportSize({ width: 1280, height: 340 });
      await add.click();
      await page.waitForFunction(
        () => document.activeElement?.getAttribute("role") === "menuitem",
      );
      const b = await panel.boundingBox();
      assert(b.y >= 8 && b.y + b.height <= 332);
      await panel.getByRole("menuitem").last().click({ trial: true });
      await screenshot("project-add-short");
      await page.keyboard.press("Escape");
      await page.getByRole("button", { name: "对话", exact: true }).click();
    },
  );
  await check(
    "Capability picker keeps the draft through live SSE and keyboard selection",
    async () => {
      await page.setViewportSize({ width: 1280, height: 720 });
      const input = page.getByLabel("发送消息", { exact: true });
      await input.fill("/");
      const list = page.getByRole("listbox"),
        option = list.getByRole("option").first();
      await option.waitFor();
      const title = "外部 SSE 更新，能力选择继续";
      await api(`/sessions/${s2}`, { title }, "PUT");
      await wait(async () => assert((await row(s2).locator(".session-select").innerText()).includes(title)));
      assert.equal(await list.count(), 1);
      assert.equal(await input.inputValue(), "/");
      assert(await input.evaluate((el) => el === document.activeElement));
      await page
        .locator(".chat-scroll")
        .evaluate((el) => el.scrollTo({ top: 0, behavior: "instant" }));
      const token = "CAP_" + fixture.token;
      const message = await api("/messages", {
        sessionId: s1,
        text: `仅回复 ${token}。菜单验收，不使用工具、不修改文件、不执行外部操作。`,
        eventId: "popup-cap-live-" + randomUUID(),
      });
      const completed = await wait(async () => {
        const run = (await api(`/runs?sessionId=${s1}`)).find(
          (r) => r.messageId === message.id,
        );
        assert.equal(run?.state, "completed");
        assert(run.result.includes(token));
        return run;
      }, 180000);
      await page
        .locator(".assistant-message")
        .getByText(token, { exact: false })
        .waitFor();
      await wait(async () =>
        assert(
          await page
            .locator(".chat-scroll")
            .evaluate((el) => el.scrollTop > 10),
        ),
      );
      assert.equal(await list.count(), 1);
      assert.equal(await input.inputValue(), "/");
      assert(await input.evaluate((el) => el === document.activeElement));
      report.models.push({
        id: completed.id,
        state: completed.state,
        tokenVerified: true,
      });
      await input.evaluate((el) => {
        el.dispatchEvent(
          new KeyboardEvent("keydown", {
            key: "Enter",
            bubbles: true,
            cancelable: true,
          }),
        );
        Object.getOwnPropertyDescriptor(
          HTMLTextAreaElement.prototype,
          "value",
        ).set.call(el, "/");
        el.setSelectionRange(1, 1);
        el.dispatchEvent(
          new InputEvent("input", {
            bubbles: true,
            inputType: "insertText",
            data: "/",
          }),
        );
      });
      await page.evaluate(
        () =>
          new Promise((r) =>
            requestAnimationFrame(() => requestAnimationFrame(r)),
          ),
      );
      assert.equal(await input.evaluate((el) => el.selectionStart), 1);
      assert.equal(await list.count(), 1);
      await screenshot("capability-live-sse");
      await input.press("Escape");
      assert.equal(await list.count(), 0);
      assert.equal(await input.inputValue(), "/");
      await page.getByRole("button", { name: /^移除 / }).click();
      await input.fill("上线菜单验收草稿，不发送");
      await page.setViewportSize({ width: 390, height: 568 });
      await input.fill("/");
      await list.waitFor();
      const b = await list.boundingBox();
      assert(
        b.x >= 8 && b.y >= 8 && b.x + b.width <= 382 && b.y + b.height <= 560,
      );
      await screenshot("capability-mobile");
      await input.press("Escape");
      await page.setViewportSize({ width: 1280, height: 720 });
    },
  );
  await check(
    "Project name tip reflects a real project rename SSE and dismisses with Escape",
    async () => {
      const link = page
        .locator(".project-links")
        .getByRole("button", { name: fixture.project.name, exact: true });
      await link.focus();
      await page.getByRole("tooltip").waitFor();
      const name = fixture.project.name + " SSE";
      await api(`/projects/${pid}`, { name }, "PATCH");
      await page
        .locator(".project-links")
        .getByRole("button", { name, exact: true })
        .waitFor();
      await wait(async () =>
        assert.equal(await page.getByRole("tooltip").innerText(), name),
      );
      await screenshot("project-tip-live-sse");
      await page.keyboard.press("Escape");
      assert.equal(await page.getByRole("tooltip").count(), 0);
    },
  );
  report.sseEvents = await page.evaluate(() => window.__menuLiveSSE.events);
  assert(report.sseEvents.some((e) => e.type === "session.updated"));
  assert(report.sseEvents.some((e) => e.type === "session.created"));
  assert(
    report.sseResponses.length > 0 &&
      report.sseResponses.every(
        (r) => r.status === 200 && r.encoding === "identity",
      ),
  );
  await check(
    "Expert file add menu dismisses and remains usable on desktop and mobile",
    async () => {
      testExpert = await api("/experts", {
        name: "菜单隔离验收专家 " + fixture.token,
        description: "仅用于弹出菜单验收",
        instructions: "菜单验收，不执行任务。",
      });
      await page.goto(url + "/#expert=" + testExpert.id);
      await page.locator(".expert-file-header").waitFor();
      await wait(async () =>
        assert(
          await page
            .getByRole("button", { name: "版本记录", exact: true })
            .isEnabled(),
        ),
      );
      const add = page.getByRole("button", {
          name: "添加专家文件",
          exact: true,
        }),
        panel = page.locator(".add-menu:popover-open");
      await add.click();
      assert.equal(await panel.getByRole("menuitem").count(), 4);
      await screenshot("expert-add");
      await page.keyboard.press("Escape");
      assert.equal(await panel.count(), 0);
      await add.click();
      await page.mouse.click(1200, 160);
      assert.equal(await panel.count(), 0);
      await add.click();
      await panel
        .getByRole("menuitem", { name: "新建文件", exact: true })
        .click();
      assert.equal(await panel.count(), 0);
      await page.getByRole("button", { name: "取消", exact: true }).click();
      for (const viewport of [
        { width: 1280, height: 340 },
        { width: 390, height: 568 },
      ]) {
        await page.setViewportSize(viewport);
        await add.click();
        await page.waitForFunction(
          () => document.activeElement?.getAttribute("role") === "menuitem",
        );
        const b = await panel.boundingBox();
        assert(
          b.x >= 8 &&
            b.y >= 8 &&
            b.x + b.width <= viewport.width - 8 &&
            b.y + b.height <= viewport.height - 8,
        );
        await panel.getByRole("menuitem").last().click({ trial: true });
        await screenshot("expert-add-" + viewport.width);
        await page.keyboard.press("Escape");
      }
    },
  );
  assert.deepEqual(report.consoleErrors, []);
  report.connectionsAfter = (await api("/connections")).map((c) => ({
    kind: c.kind,
    status: c.status,
    enabled: c.enabled,
    lastHeartbeat: c.lastHeartbeat,
  }));
  assert(
    report.connectionsAfter
      .filter((c) => c.enabled)
      .every((c) => c.status === "connected"),
  );
  report.passed = true;
} catch (error) {
  report.error = redact(error.stack || error);
  if (page) await screenshot("live-failure").catch(() => {});
} finally {
  if (browser) await browser.close();
  try {
    if (testExpert) {
      await api(`/experts/${testExpert.id}`, undefined, "DELETE");
      assert(!(await api("/experts")).some((e) => e.id === testExpert.id));
      report.testExpertCleaned = true;
    }
    const own = (await api("/runs")).filter(
      (r) =>
        r.projectId === pid &&
        ["queued", "running", "waiting", "stopping"].includes(r.state),
    );
    for (const run of own) await api(`/runs/${run.id}/stop`, {});
    await wait(
      async () =>
        assert.equal(
          (await api("/runs")).filter(
            (r) =>
              r.projectId === pid &&
              ["queued", "running", "waiting", "stopping"].includes(r.state),
          ).length,
          0,
        ),
      45000,
    );
    await api(`/projects/${pid}`, undefined, "DELETE");
    const remaining = await api("/projects");
    assert(!remaining.some((p) => p.id === pid));
    assert(
      fixture.originalProjectIds.every((id) =>
        remaining.some((p) => p.id === id),
      ),
    );
    report.testProjectCleaned = true;
    report.originalProjectsPreserved = true;
  } catch (error) {
    report.passed = false;
    report.cleanupError = redact(error.stack || error);
  }
  await fs.writeFile(
    path.join(dir, "report.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(JSON.stringify(report, null, 2));
  if (!report.passed) process.exitCode = 1;
}
