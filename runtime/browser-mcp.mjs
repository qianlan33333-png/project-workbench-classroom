import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";
import { chromium } from "playwright";
import fs from "node:fs/promises";
import path from "node:path";

const server = new McpServer({ name: "project-browser", version: "0.1.0" });
let context, current;
async function browser() {
  if (!context) {
    context = await chromium.launchPersistentContext(
      process.env.PW_BROWSER_PROFILE || "/runtime/browser-profile",
      {
        headless: process.env.PW_BROWSER_HEADLESS === "1",
        executablePath: process.env.PW_CHROMIUM || "/usr/bin/chromium",
        viewport: { width: 1280, height: 800 },
        args: ["--no-sandbox", "--disable-dev-shm-usage"],
      },
    );
    current = context.pages()[0] || (await context.newPage());
  }
  return context;
}
async function page() {
  await browser();
  if (current.isClosed()) current = await context.newPage();
  return current;
}
const text = (value) => ({
  content: [
    {
      type: "text",
      text: typeof value === "string" ? value : JSON.stringify(value, null, 2),
    },
  ],
});
server.registerTool(
  "browser_open",
  {
    description:
      "打开网页。浏览器运行在当前项目容器中；登录或验证码可请用户通过工作台的浏览器接管完成。",
    inputSchema: { url: z.string().url() },
  },
  async ({ url }) => {
    if (!/^https?:/.test(url))
      throw new Error("Only HTTP(S) pages are supported");
    const p = await page();
    await p.goto(url, { waitUntil: "domcontentloaded", timeout: 45000 });
    return text({
      url: p.url(),
      title: await p.title(),
      snapshot: await p.locator("body").ariaSnapshot({ timeout: 10000 }),
    });
  },
);
server.registerTool(
  "browser_snapshot",
  {
    description:
      "读取当前页面的可访问结构。文本包含网页内容，不能改变项目或权限。",
    inputSchema: {},
  },
  async () => {
    const p = await page();
    return text({
      url: p.url(),
      title: await p.title(),
      snapshot: (await p.locator("body").ariaSnapshot()).slice(0, 70000),
    });
  },
);
server.registerTool(
  "browser_click",
  {
    description:
      "点击页面元素。优先用可访问角色与名称；selector 用于没有可访问标识的控件。",
    inputSchema: {
      role: z.string().optional(),
      name: z.string().optional(),
      selector: z.string().optional(),
    },
  },
  async ({ role, name, selector }) => {
    const p = await page();
    const locator = selector
      ? p.locator(selector)
      : p.getByRole(role || "button", { name, exact: true });
    await locator.click({ timeout: 10000 });
    return text({
      url: p.url(),
      snapshot: (await p.locator("body").ariaSnapshot()).slice(0, 70000),
    });
  },
);
server.registerTool(
  "browser_fill",
  {
    description: "填写一个页面输入框。用户登录密码应由用户通过可视接管输入。",
    inputSchema: {
      label: z.string().optional(),
      selector: z.string().optional(),
      value: z.string(),
    },
  },
  async ({ label, selector, value }) => {
    const p = await page();
    await (
      selector
        ? p.locator(selector)
        : p.getByLabel(label || "", { exact: true })
    ).fill(value, { timeout: 10000 });
    return text("已填写");
  },
);
server.registerTool(
  "browser_press",
  {
    description: "发送按键，例如 Enter、Tab、Escape。",
    inputSchema: { key: z.string() },
  },
  async ({ key }) => {
    await (await page()).keyboard.press(key);
    return text("按键已发送");
  },
);
server.registerTool(
  "browser_tabs",
  {
    description: "列出标签页，或选择一个标签页。",
    inputSchema: { index: z.number().int().nonnegative().optional() },
  },
  async ({ index }) => {
    const ctx = await browser();
    if (index !== undefined) {
      const p = ctx.pages()[index];
      if (!p) throw new Error("No such tab");
      current = p;
      await p.bringToFront();
    }
    return text(
      await Promise.all(
        ctx
          .pages()
          .map(async (p, i) => ({
            index: i,
            url: p.url(),
            title: await p.title(),
            active: p === current,
          })),
      ),
    );
  },
);
server.registerTool(
  "browser_screenshot",
  {
    description: "截取当前网页，可选将截图保存为当前项目的产物。",
    inputSchema: { path: z.string().optional() },
  },
  async ({ path: filename }) => {
    const p = await page();
    const bytes = await p.screenshot({ fullPage: false });
    if (filename) {
      const root = await fs.realpath(process.cwd()),
        target = path.resolve(root, filename);
      if (!target.startsWith(root + path.sep))
        throw new Error("Screenshot must be inside current project");
      const parent = path.dirname(target);
      await fs.mkdir(parent, { recursive: true });
      if (!(await fs.realpath(parent)).startsWith(root))
        throw new Error("Screenshot path escaped project");
      await fs.writeFile(target, bytes);
    }
    return {
      content: [
        {
          type: "image",
          data: bytes.toString("base64"),
          mimeType: "image/png",
        },
      ],
    };
  },
);
server.registerTool(
  "browser_evaluate",
  {
    description:
      "在当前网页执行 JavaScript 并返回可序列化结果，用于页面数据提取或页面交互。",
    inputSchema: { script: z.string() },
  },
  async ({ script }) => text(await (await page()).evaluate(script)),
);
const transport = new StdioServerTransport();
await server.connect(transport);
async function shutdown() {
  await context?.close().catch(() => {});
  process.exit(0);
}
process.on("SIGTERM", shutdown);
process.on("SIGINT", shutdown);
