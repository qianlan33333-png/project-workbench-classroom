import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";
const cfg = JSON.parse(
  await fs.readFile(
    path.join(os.homedir(), ".config/project-workbench/cli.json"),
    "utf8",
  ),
);
const auth = "Basic " + Buffer.from("owner:" + cfg.password).toString("base64");
const api = async (method, p, b) => {
  const r = await fetch(cfg.url + "/api/v1" + p, {
    method,
    headers: { Authorization: auth, "Content-Type": "application/json" },
    body: b ? JSON.stringify(b) : undefined,
  });
  if (!r.ok) throw Error(await r.text());
  return r.json();
};
const p = await api("POST", "/projects", { name: "事件流诊断 " + Date.now() });
const browser = await chromium.launch({
  headless: true,
  executablePath:
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
});
const pages = [];
const results = [];
try {
  for (const mode of ["compressed", "identity"]) {
    const context = await browser.newContext(
      mode === "identity"
        ? { extraHTTPHeaders: { "Accept-Encoding": "identity" } }
        : {},
    );
    const page = await context.newPage();
    pages.push({ mode, page });
    await page.addInitScript(() => {
      window.__sse = { events: [] };
      const Original = window.EventSource;
      window.EventSource = class extends Original {
        constructor(...args) {
          super(...args);
          this.addEventListener("message", (e) => {
            const x = JSON.parse(e.data);
            if (x.type === "file.changed")
              window.__sse.events.push({
                seq: x.seq,
                time: Date.now(),
                data: x.data,
              });
          });
        }
      };
    });
    page.on("response", async (r) => {
      if (new URL(r.url()).pathname.endsWith("/events"))
        console.log(
          mode,
          "encoding",
          (await r.allHeaders())["content-encoding"] || "none",
        );
    });
    await page.goto(cfg.url, { waitUntil: "domcontentloaded" });
    await page.getByLabel("密码", { exact: true }).fill(cfg.password);
    await page.getByRole("button", { name: "进入工作台" }).click();
    await page.waitForTimeout(1500);
  }
  const at = Date.now();
  await api("PUT", "/projects/" + p.id + "/file", {
    path: "stream-check.txt",
    content: "live event",
    baseHash: "",
  });
  await new Promise((r) => setTimeout(r, 4000));
  for (const { mode, page } of pages) {
    const matches = await page.evaluate(
      (id) => window.__sse.events.filter((e) => e.data.projectId === id),
      p.id,
    );
    const result = {
      mode,
      received: matches.length,
      delayMs: matches[0] ? matches[0].time - at : null,
    };
    results.push(result);
    console.log(JSON.stringify(result));
    assert.equal(result.received, 1);
    assert(result.delayMs < 2500, "SSE is buffered");
  }
  await fs.mkdir("artifacts/message-feedback", { recursive: true });
  await fs.writeFile(
    "artifacts/message-feedback/events-report.json",
    JSON.stringify({ passed: true, results }, null, 2),
  );
} finally {
  await browser.close();
  await api("DELETE", "/projects/" + p.id);
}
