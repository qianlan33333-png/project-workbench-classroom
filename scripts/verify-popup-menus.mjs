import assert from "node:assert/strict";
import { createServer } from "node:http";
import fs from "node:fs/promises";
import path from "node:path";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";

const baseline = process.env.PW_POPUP_BASELINE === "1";
const dir = path.resolve(
  "artifacts/popup-audit/" + (baseline ? "baseline" : "candidate"),
);
const dist = path.resolve(
  baseline ? "artifacts/popup-audit/baseline-dist" : "web/dist",
);
await fs.mkdir(dir, { recursive: true });
const primary = {
  id: "p",
  name: "弹出菜单排查",
  description: "隔离数据",
  aliases: [],
  defaultSessionId: "s1",
  expertIds: ["e"],
  defaultExpertId: "",
  pluginIds: [],
  archived: false,
};
let projects = [
  primary,
  ...Array.from({ length: 18 }, (_, i) => ({
    ...primary,
    id: "p" + i,
    name: "导航长列表项目 " + i,
    defaultSessionId: "unused",
  })),
];
let sessions = [
  {
    id: "s1",
    projectId: "p",
    title: "菜单排查对话",
    kind: "assistant",
    updatedAt: 1,
  },
  {
    id: "s2",
    projectId: "p",
    title: "第二段对话",
    kind: "topic",
    updatedAt: 1,
  },
];
const expert = {
  id: "e",
  name: "菜单排查专家",
  description: "隔离专家",
  digest: "current-version",
  tools: [],
};
const catalog = Array.from({ length: 22 }, (_, i) => ({
  kind: "cli",
  id: "cap" + i,
  name: `工具能力 ${i} · 较长的选择标签`,
  description: "用于检查能力选择浮层边界",
  command: "fixture" + i,
}));
let events = [],
  sequence = 0,
  runs = [],
  capabilityMode = "ready";
const clients = new Set(),
  errors = [],
  unknown = [],
  requests = [];
const emit = (type, data, runId = "") => {
  assert(clients.size > 0, "Expected native EventSource");
  const event = {
    seq: ++sequence,
    sessionId: "s1",
    runId,
    createdAt: 1,
    type,
    data,
  };
  events.push(event);
  for (const res of clients)
    res.write("data: " + JSON.stringify(event) + "\n\n");
};
const server = createServer(async (req, res) => {
  try {
    const parsed = new URL(req.url, "http://fixture"),
      pathname = parsed.pathname;
    if (pathname === "/favicon.ico") {
      res.writeHead(204);
      res.end();
      return;
    }
    if (pathname.startsWith("/api/v1/")) {
      const route = pathname.slice(7);
      if (route === "/events" && !parsed.searchParams.has("json")) {
        res.writeHead(200, { "Content-Type": "text/event-stream" });
        res.flushHeaders();
        res.write(": fixture\n\n");
        clients.add(res);
        res.on("close", () => clients.delete(res));
        return;
      }
      let result;
      if (route === "/auth") result = { authenticated: true };
      else if (route === "/notices/count") result = { unread: 0 };
      else if (route === "/projects") result = projects;
      else if (route === "/projects/p/sessions") result = sessions;
      else if (route.startsWith("/sessions/"))
        result = sessions.find((s) => s.id === route.split("/")[2]);
      else if (/\/capabilities$/.test(route)) {
        if (capabilityMode === "loading")
          await new Promise((resolve) => setTimeout(resolve, 600));
        if (capabilityMode === "error") {
          res.writeHead(503, { "Content-Type": "application/json" });
          res.end(JSON.stringify({ error: "隔离能力目录暂时不可用" }));
          return;
        }
        result = catalog;
      } else if (route === "/experts") result = [expert];
      else if (route === "/experts/e")
        result = {
          expert,
          usage: { projects: [], sessions: [], schedules: [] },
        };
      else if (route === "/experts/e/files")
        result = [
          { path: "SKILL.md", hash: "hash", size: 20, directory: false },
        ];
      else if (route === "/experts/e/file")
        result = {
          path: "SKILL.md",
          hash: "hash",
          content: "# 菜单排查专家\n\n隔离能力说明。",
          size: 40,
          mime: "text/markdown",
          binary: false,
          tooLarge: false,
        };
      else if (route === "/experts/e/versions")
        result = [expert, { ...expert, digest: "old-version" }];
      else if (route === "/today")
        result = {
          sessionId: "personal-session",
          runs: [],
          decisions: [],
          channelNotes: [],
          tasks: [],
          projects: [],
          daily: null,
        };
      else if (route === "/events") result = events;
      else if (route === "/runs") result = runs;
      else if (
        [
          "/inbox",
          "/interactions",
          "/tasks",
          "/projects/p/files",
          "/projects/p/tasks",
        ].includes(route)
      )
        result = [];
      else {
        unknown.push(req.method + " " + route);
        result = [];
      }
      if (req.method !== "GET") requests.push({ method: req.method, route });
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify(result));
      return;
    }
    const file = path.resolve(
      dist,
      "." + (pathname === "/" ? "/index.html" : pathname),
    );
    assert(file.startsWith(dist + path.sep));
    res.writeHead(200, {
      "Content-Type": file.endsWith(".js")
        ? "text/javascript"
        : file.endsWith(".css")
          ? "text/css"
          : "text/html",
    });
    res.end(await fs.readFile(file));
  } catch (error) {
    errors.push(error.message);
    res.writeHead(500);
    res.end("fixture error");
  }
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const url = `http://127.0.0.1:${server.address().port}/`;
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.PW_CHROME ||
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
});
const page = await browser.newPage({
  viewport: { width: 1280, height: 720 },
  deviceScaleFactor: 1,
  hasTouch: true,
});
page.setDefaultTimeout(6000);
page.on("pageerror", (e) => errors.push(e.message));
page.on("console", (e) => {
  if (
    e.type() === "error" &&
    !(
      capabilityMode === "error" &&
      /\/capabilities/.test(e.location().url) &&
      /503/.test(e.text())
    )
  )
    errors.push(e.text());
});
const report = {
  passed: false,
  baseline,
  checks: [],
  failures: [],
  screenshots: [],
  measurements: [],
};
const check = async (name, fn) => {
  try {
    await fn();
    report.checks.push(name);
    console.log("passed: " + name);
  } catch (error) {
    report.failures.push({ name, error: error.message });
    if (!baseline) throw error;
  }
};
const wait = async (fn) => {
  for (let i = 0; ; i++) {
    try {
      return await fn();
    } catch (e) {
      if (i >= 60) throw e;
      await new Promise((r) => setTimeout(r, 50));
    }
  }
};
const shot = async (name) => {
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise((r) =>
      requestAnimationFrame(() => requestAnimationFrame(r)),
    );
  });
  const file = path.join(dir, name + ".png");
  await page.screenshot({ path: file });
  report.screenshots.push(file);
};
let navigation = 0;
const go = async (
  hash = "project=p&session=s1",
  viewport = { width: 1280, height: 720 },
) => {
  await page.setViewportSize(viewport);
  await page.goto(url + "?case=" + ++navigation + "#" + hash);
  await page
    .getByRole("complementary", { name: "全局导航", includeHidden: true })
    .waitFor({ state: "attached" });
};
const projectFiles = async (viewport) => {
  await go(undefined, viewport);
  if (page.viewportSize().width < 768)
    await page.getByRole("button", { name: "打开项目侧栏" }).click();
  await page.getByRole("button", { name: "文件", exact: true }).click();
};
const expertFiles = async (viewport) => {
  await go("expert=e", viewport);
  await page.locator(".expert-file-header").waitFor();
  await wait(async () =>
    assert(
      await page
        .getByRole("button", { name: "版本记录", exact: true })
        .isEnabled(),
    ),
  );
};
const add = () =>
  page.locator(".file-actions").getByText("添加", { exact: true });
const addPanel = () => page.locator(".add-menu");
const actions = () =>
  addPanel().getByRole(baseline ? "button" : "menuitem", {
    includeHidden: true,
  });
const action = (name) =>
  addPanel().getByRole(baseline ? "button" : "menuitem", {
    name,
    exact: true,
    includeHidden: true,
  });
const inViewport = async (selector) => {
  const b = await page.locator(selector).boundingBox(),
    v = page.viewportSize();
  report.measurements.push({ selector, viewport: v, rect: b });
  assert(
    b &&
      b.x >= 7 &&
      b.y >= 7 &&
      b.x + b.width <= v.width - 7 &&
      b.y + b.height <= v.height - 7,
    "Popup outside viewport: " + JSON.stringify(b),
  );
};
try {
  for (const [kind, enter] of [
    ["project", projectFiles],
    ["expert", expertFiles],
  ]) {
    await check(kind + " add: Escape and outside click dismiss", async () => {
      await enter();
      await add().click();
      await addPanel().waitFor();
      await shot("01-" + kind + "-add");
      await page.keyboard.press("Escape");
      const escaped = !(await addPanel().isVisible());
      await enter();
      await add().click();
      await page.mouse.click(1200, 160);
      const outside = !(await addPanel().isVisible());
      assert(
        escaped && outside,
        `Escape closed=${escaped}; outside closed=${outside}`,
      );
    });
    await check(
      kind + " add: selecting create closes before opening its form",
      async () => {
        await enter();
        await add().click();
        await action("新建文件").click();
        await shot("02-" + kind + "-create");
        assert.equal(await addPanel().isVisible(), false);
      },
    );
    await check(
      kind + " add: all actions remain reachable in a short window",
      async () => {
        await enter({ width: 1280, height: 340 });
        await add().click();
        await shot("03-" + kind + "-short");
        await inViewport(".add-menu");
        await actions().last().click({ trial: true });
      },
    );
  }
  await check(
    "project add: file chooser retains synchronous user activation",
    async () => {
      await projectFiles();
      await add().click();
      const [chooser] = await Promise.all([
        page.waitForEvent("filechooser"),
        action("上传文件").click(),
      ]);
      assert(chooser);
      assert.equal(await addPanel().isVisible(), false);
    },
  );
  for (const personal of [false, true]) {
    await check(
      (personal ? "personal" : "project") +
        " capability picker: eight tags still fit a small viewport",
      async () => {
        await go(personal ? "page=today" : undefined, {
          width: 390,
          height: 568,
        });
        const input = page.getByLabel(
          personal ? "和个人助手聊聊" : "发送消息",
          { exact: true },
        );
        await input.waitFor();
        for (let i = 0; i < 8; i++) {
          await input.fill("/");
          await page.getByRole("listbox").getByRole("option").first().waitFor();
          await input.press("Enter");
          await wait(async () =>
            assert.equal(
              await page.locator(".capability-chips button").count(),
              i + 1,
            ),
          );
          await page.evaluate(
            () => new Promise((resolve) => requestAnimationFrame(resolve)),
          );
        }
        await input.fill("/");
        await page.getByRole("listbox").waitFor();
        await shot(
          "04-" + (personal ? "personal" : "project") + "-capabilities",
        );
        await inViewport(".capability-menu");
        const before = await input.inputValue();
        await input.press("Escape");
        assert.equal(await page.getByRole("listbox").count(), 0);
        assert.equal(await input.inputValue(), before);
      },
    );
  }
  await check(
    "capability picker: empty results Escape and outside blur preserve draft",
    async () => {
      await go();
      const input = page.getByLabel("发送消息", { exact: true });
      await input.fill("/does-not-exist");
      await page.getByRole("listbox").waitFor();
      await input.press("Escape");
      assert.equal(await page.getByRole("listbox").count(), 0);
      assert.equal(await input.inputValue(), "/does-not-exist");
    },
  );
  await check(
    "capability picker: typing immediately after selection retains caret and suggestions",
    async () => {
      await go();
      const input = page.getByLabel("发送消息", { exact: true });
      await input.fill("/");
      await page.getByRole("listbox").getByRole("option").first().waitFor();
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
        ).set.call(el, "/工具");
        el.setSelectionRange(3, 3);
        el.dispatchEvent(
          new InputEvent("input", {
            bubbles: true,
            inputType: "insertText",
            data: "/工具",
          }),
        );
      });
      await page.evaluate(
        () =>
          new Promise((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(resolve)),
          ),
      );
      await shot("04-fast-selection");
      assert.equal(await input.inputValue(), "/工具");
      assert.equal(await input.evaluate((el) => el.selectionStart), 3);
      assert.equal(await page.getByRole("listbox").count(), 1);
    },
  );
  await check("project name tip: Escape dismisses", async () => {
    await go();
    await page.locator(".project-links button").first().focus();
    await page.getByRole("tooltip").waitFor();
    await shot("05-project-tip");
    await page.keyboard.press("Escape");
    assert.equal(await page.getByRole("tooltip").count(), 0);
  });
  await check("project name tip: scrolling its anchor dismisses", async () => {
    await go();
    await page.locator(".project-links button").first().focus();
    await page.getByRole("tooltip").waitFor();
    await page.locator(".project-links").evaluate((el) => {
      el.scrollTop += 65;
    });
    await shot("06-project-tip-scrolled");
    await wait(async () =>
      assert.equal(await page.getByRole("tooltip").count(), 0),
    );
  });
  await check(
    "project name tip: real SSE refresh shows the new project name",
    async () => {
      await go();
      await page.locator(".project-links button").first().focus();
      await page.getByRole("tooltip").waitFor();
      primary.name = "SSE 更新后的项目名称";
      projects = projects.map((p) => (p.id === "p" ? { ...primary } : p));
      emit("project.updated", primary);
      await page
        .locator(".project-links")
        .getByRole("button", { name: primary.name, exact: true })
        .waitFor();
      await shot("07-project-tip-sse");
      assert.equal(await page.getByRole("tooltip").innerText(), primary.name);
    },
  );
  if (!baseline) {
    await check(
      "capability picker: an older selection frame cannot steal focus after blur",
      async () => {
        await go();
        const input = page.getByLabel("发送消息", { exact: true });
        await input.fill("/");
        await page.getByRole("listbox").getByRole("option").first().waitFor();
        await input.evaluate((el) => {
          el.dispatchEvent(
            new KeyboardEvent("keydown", {
              key: "Enter",
              bubbles: true,
              cancelable: true,
            }),
          );
          document.querySelector(".rail-toggle").focus();
        });
        await page.evaluate(
          () =>
            new Promise((r) =>
              requestAnimationFrame(() => requestAnimationFrame(r)),
            ),
        );
        assert.equal(
          await input.evaluate((el) => el === document.activeElement),
          false,
        );
      },
    );
    for (const [kind, enter] of [
      ["project", projectFiles],
      ["expert", expertFiles],
    ]) {
      await check(
        kind + " add: keyboard, repeat trigger, resize and mobile",
        async () => {
          await enter();
          await add().focus();
          await page.keyboard.press("ArrowUp");
          await wait(async () =>
            assert.equal(
              await page.locator(":focus").innerText(),
              kind === "project" ? "新建文件夹" : "用 ZIP 更新",
            ),
          );
          await page.keyboard.press("Home");
          assert.equal(
            await page.locator(":focus").innerText(),
            kind === "project" ? "上传文件" : "新建文件",
          );
          await page.keyboard.press("Tab");
          assert.equal(await addPanel().isVisible(), false);
          await add().click();
          await add().click();
          assert.equal(await addPanel().isVisible(), false);
          await add().click();
          await page.setViewportSize({ width: 1154, height: 828 });
          await wait(async () =>
            assert.equal(await addPanel().isVisible(), false),
          );
          await enter({ width: 390, height: 568 });
          await add().click();
          await shot("08-" + kind + "-mobile");
          await inViewport(".add-menu");
          await actions().last().click({ trial: true });
          await page.keyboard.press("Escape");
        },
      );
    }
    await check(
      "project add: native run SSE closes on lock and exposes disabled actions",
      async () => {
        await projectFiles();
        await add().click();
        const run = {
          id: "lock-run",
          projectId: "p",
          sessionId: "s1",
          state: "running",
          text: "隔离写入任务",
          createdAt: 1,
        };
        runs = [run];
        emit("run.updated", run);
        await wait(async () => {
          assert.equal(await addPanel().isVisible(), false);
          assert(await action("新建文件").isDisabled());
        });
        await add().click();
        assert.equal(await actions().count(), 5);
        for (const item of await actions().all())
          assert(await item.isDisabled());
        await page.keyboard.press("Escape");
        runs = [];
        events = [];
      },
    );
    await check(
      "expert add: historical version remains read only",
      async () => {
        await expertFiles();
        await page
          .getByRole("button", { name: "版本记录", exact: true })
          .click();
        await page
          .locator(".expert-versions .usage-row")
          .last()
          .getByRole("button", { name: "查看", exact: true })
          .click();
        await page.getByText("正在查看历史版本", { exact: false }).waitFor();
        await add().click();
        assert.equal(await actions().count(), 4);
        for (const item of await actions().all())
          assert(await item.isDisabled());
        await page.keyboard.press("Escape");
        assert.equal(await addPanel().isVisible(), false);
      },
    );
    await check(
      "capability picker: loading and failed catalog can be dismissed without changing draft",
      async () => {
        for (const mode of ["loading", "error"]) {
          capabilityMode = mode;
          await go();
          const input = page.getByLabel("发送消息", { exact: true });
          await input.fill("/");
          await page
            .getByRole("listbox")
            .getByText(
              mode === "loading" ? "正在加载能力…" : "隔离能力目录暂时不可用",
              { exact: true },
            )
            .waitFor();
          await input.press("Escape");
          assert.equal(await page.getByRole("listbox").count(), 0);
          assert.equal(await input.inputValue(), "/");
        }
        capabilityMode = "ready";
      },
    );
    await check(
      "capability picker: inner scrolling, viewport resize, IME and outside click",
      async () => {
        await go();
        const input = page.getByLabel("发送消息", { exact: true });
        await input.fill("/");
        await page.getByRole("listbox").getByRole("option").first().waitFor();
        await page.getByRole("listbox").evaluate((el) => {
          el.scrollTop = 80;
        });
        await page.evaluate(() => new Promise((r) => requestAnimationFrame(r)));
        assert.equal(await page.getByRole("listbox").count(), 1);
        await page.setViewportSize({ width: 800, height: 568 });
        await page.evaluate(
          () =>
            new Promise((r) =>
              requestAnimationFrame(() => requestAnimationFrame(r)),
            ),
        );
        await inViewport(".capability-menu");
        await input.evaluate((el) =>
          el.dispatchEvent(
            new KeyboardEvent("keydown", {
              key: "Enter",
              isComposing: true,
              bubbles: true,
              cancelable: true,
            }),
          ),
        );
        assert.equal(await page.locator(".capability-chips button").count(), 0);
        assert.equal(await input.inputValue(), "/");
        await page.mouse.click(790, 30);
        assert.equal(await page.getByRole("listbox").count(), 0);
        assert.equal(await input.inputValue(), "/");
      },
    );
    await check(
      "capability picker: native chat SSE updates keep focus and suggestions",
      async () => {
        const run = {
          id: "chat-run",
          projectId: "p",
          sessionId: "s1",
          state: "completed",
          text: "SSE 菜单验证",
          result: "更新后的对话内容。\n\n".repeat(60),
          createdAt: 1,
          finishedAt: 2,
        };
        runs = [run];
        events = [];
        await go();
        await wait(async () =>
          assert(
            (await page.locator(".assistant-message .markdown p").count()) >=
              50,
          ),
        );
        await page
          .locator(".chat-scroll")
          .evaluate((el) =>
            el.scrollTo({ top: el.scrollHeight, behavior: "instant" }),
          );
        await wait(async () =>
          assert(
            await page
              .locator(".chat-scroll")
              .evaluate(
                (el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 2,
              ),
          ),
        );
        const input = page.getByLabel("发送消息", { exact: true });
        await input.fill("/");
        await page.getByRole("listbox").getByRole("option").first().waitFor();
        await page
          .locator(".chat-scroll")
          .evaluate((el) => el.scrollTo({ top: 0, behavior: "instant" }));
        await wait(async () =>
          assert(
            await page.locator(".chat-scroll").evaluate((el) => el.scrollTop <= 2),
          ),
        );
        emit(
          "harness.tool/call",
          { callId: "popup-call", name: "菜单验证", arguments: {} },
          run.id,
        );
        await wait(async () =>
          assert.match(
            await page.locator(".activity-summary").innerText(),
            /1\s*次工具调用/,
          ),
        );
        await wait(async () =>
          assert(
            await page
              .locator(".chat-scroll")
              .evaluate((el) => el.scrollTop > 10),
          ),
        );
        assert.equal(await page.getByRole("listbox").count(), 1);
        assert(await input.evaluate((el) => el === document.activeElement));
        await inViewport(".capability-menu");
        await shot("09-capability-sse");
        runs = [];
        events = [];
      },
    );
    await check(
      "project name tip: resize dismisses; original native select and disclosure keep their behavior",
      async () => {
        await go();
        await page.locator(".project-links button").first().focus();
        await page.getByRole("tooltip").waitFor();
        await page.setViewportSize({ width: 1000, height: 600 });
        await wait(async () =>
          assert.equal(await page.getByRole("tooltip").count(), 0),
        );
        const select = page.locator("select").first();
        await select.focus();
        await select.press("ArrowDown");
        await select.press("Escape");
        assert.equal(await select.evaluate((el) => el.tagName), "SELECT");
        const archived = page.locator("details.archived");
        if (await archived.count()) {
          await archived.locator("summary").click();
          assert(await archived.evaluate((el) => el.open));
        }
        assert.deepEqual(
          requests.filter((r) => r.route.endsWith("/messages")),
          [],
        );
      },
    );
  }
  assert.deepEqual(unknown, []);
  assert.deepEqual(errors, []);
  assert.deepEqual(report.failures, []);
  report.passed = true;
} catch (error) {
  report.error = error.stack;
  await shot("failure").catch(() => {});
  process.exitCode = 1;
} finally {
  report.consoleAndPageErrors = errors;
  report.unknownAPI = unknown;
  report.requests = requests;
  await fs.writeFile(
    path.join(dir, "report.json"),
    JSON.stringify(report, null, 2),
  );
  console.log(JSON.stringify(report, null, 2));
  await browser.close();
  for (const res of clients) res.end();
  await new Promise((resolve) => server.close(resolve));
}
