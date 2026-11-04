// Supplemental state and navigation regression checks; isolated fixture only.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { chromium } from "../runtime/node_modules/playwright/index.mjs";
const dir = process.cwd() + "/artifacts/workbench-white-suite/",
  m = JSON.parse(await fs.readFile(dir + "preview.json"));
const b = await chromium.launch({
  executablePath:
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  headless: true,
});
const p = await b.newPage({ viewport: { width: 1440, height: 960 } });
p.setDefaultTimeout(7000);
const checks = [];
let prompts = 0,
  approve = false;
p.on("dialog", (d) => {
  prompts++;
  approve ? d.accept() : d.dismiss();
});
const check = (n, v = true) => {
  assert(v, n);
  checks.push(n);
  console.log(n);
};
const wait = async (f) => {
  for (let i = 0; i < 80; i++) {
    if (await f()) return;
    await p.waitForTimeout(100);
  }
  throw Error("not converged");
};
const open = async (h) => {
  await p.goto(m.url + "/" + h);
  await p.reload();
  await p.locator(".rail").first().waitFor({ state: "attached" });
  await p.waitForTimeout(300);
};
const shot = (n) => p.screenshot({ path: dir + "screens/" + n + ".png" });
try {
  await open("#project=" + m.projects[0].id);
  await p.getByRole("button", { name: "打开资料", exact: true }).click();
  await p.locator('button[title="资料/访谈纪要.md"]').click();
  await p.getByRole("button", { name: "编辑", exact: true }).click();
  await p.locator(".inspector .cm-content").fill("临时草稿，仅验收关闭保护");
  await p.getByRole("button", { name: "打开资料", exact: true }).click();
  await p
    .getByRole("alertdialog")
    .getByRole("button", { name: "取消", exact: true })
    .click();
  check(
    "context switch preserves dirty document after cancel",
    await p.locator(".inspector .cm-content").isVisible(),
  );
  await p.getByRole("button", { name: "打开资料", exact: true }).click();
  await p
    .getByRole("alertdialog")
    .getByRole("button", { name: "放弃修改", exact: true })
    .click();
  await p.locator(".inspector").waitFor({ state: "detached" });
  check(
    "document and context drawer remain exclusive",
    await p
      .getByRole("complementary", { name: "资料", exact: true })
      .isVisible(),
  );
  await p.getByRole("button", { name: "关闭资料", exact: true }).click();
  await p.getByRole("button", { name: "项目操作", exact: true }).click();
  await p.getByRole("menuitem", { name: "项目设置", exact: true }).click();
  let d = p.getByRole("dialog", { name: "项目设置" });
  await d
    .getByLabel("名称", { exact: true })
    .fill("产品验证（界面验收 " + Date.now() + "）");
  await d.getByRole("button", { name: "关闭项目设置" }).click();
  check("project metadata close protects edit", await d.isVisible());
  await d.getByRole("button", { name: "保存名称与别名" }).click();
  await p.waitForTimeout(250);
  await d.getByLabel("简短说明", { exact: true }).fill("已保存的虚构项目说明");
  await d.getByRole("button", { name: "保存说明", exact: true }).click();
  await p.waitForTimeout(250);
  const before = prompts;
  await d.getByRole("button", { name: "关闭项目设置" }).click();
  await d.waitFor({ state: "detached" });
  check(
    "saved project fields close without discard prompt",
    prompts === before,
  );
  await p
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: "任务", exact: true })
    .click();
  await p.getByRole("heading", { name: "任务", exact: true }).waitFor();
  await p
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: "设置", exact: true })
    .click();
  await p.getByRole("button", { name: /^CLI/ }).click();
  await p.getByRole("button", { name: "添加 CLI", exact: true }).click();
  await p.getByLabel("名称", { exact: true }).fill("保留 CLI 草稿");
  await p.getByRole("button", { name: "关闭CLI 工具" }).click();
  const n = prompts;
  await p.goBack();
  await p.waitForTimeout(400);
  check(
    "browser back prompts once and cancel keeps settings",
    prompts === n + 1 &&
      (await p.getByRole("heading", { name: "设置", exact: true }).isVisible()),
  );
  await p.getByRole("button", { name: /^CLI/ }).click();
  check(
    "closed CLI panel retains draft",
    (await p.getByLabel("名称", { exact: true }).inputValue()) ===
      "保留 CLI 草稿",
  );
  await p.getByRole("button", { name: "取消", exact: true }).click();
  await p.getByRole("button", { name: "关闭CLI 工具" }).click();
  await p.goBack();
  await p.getByRole("heading", { name: "任务", exact: true }).waitFor();
  check("browser back proceeds after draft cleared");
  const versions = await (
    await p.request.get(
      m.url + "/api/v1/experts/" + m.experts[0].id + "/versions",
    )
  ).json();
  if (versions.length < 2) {
    const ex = await (
      await p.request.get(m.url + "/api/v1/experts/" + m.experts[0].id)
    ).json();
    const changed = await p.request.post(
      m.url + "/api/v1/experts/" + m.experts[0].id + "/changes",
      {
        data: {
          baseDigest: ex.expert.digest,
          metadata: {
            name: ex.expert.name,
            description: ex.expert.description + " 隔离版本验收。",
          },
        },
      },
    );
    assert(changed.ok());
  }
  await open("#expert=" + m.experts[0].id);
  await p
    .getByRole("button", { name: "专家操作", exact: true })
    .press("ArrowDown");
  await p.getByRole("menuitem", { name: "复制", exact: false }).waitFor();
  await wait(
    async () =>
      await p
        .getByRole("menuitem", { name: "复制", exact: false })
        .evaluate((el) => el === document.activeElement),
  );
  await p.keyboard.press("ArrowDown");
  check(
    "export anchor participates in menu keyboard",
    await p
      .getByRole("menuitem", { name: "导出", exact: true })
      .evaluate((el) => el === document.activeElement),
  );
  await p.keyboard.press("Escape");
  await p.getByRole("button", { name: /个文件/ }).click();
  await shot("review-expert-files");
  await p.getByRole("button", { name: "版本", exact: true }).click();
  await p
    .locator(".expert-versions .usage-row")
    .last()
    .getByRole("button", { name: "查看", exact: true })
    .click();
  await p.getByText(/正在查看历史版本/).waitFor();
  check(
    "expert historical version remains read-only",
    await p.locator(".expert-file-header button").first().isDisabled(),
  );
  await shot("review-expert-history");
  const rr = await p.request.post(
    m.url + "/api/v1/projects/" + m.projects[0].id + "/tasks",
    {
      data: {
        title: "可删除的界面验收",
        description: "虚构数据",
        state: "pending",
        operation: crypto.randomUUID(),
      },
    },
  );
  const tt = await rr.json();
  await open("#page=tasks&task=" + tt.id);
  await p.getByRole("button", { name: "编辑", exact: true }).click();
  approve = true;
  await p.getByRole("button", { name: "删除", exact: true }).click();
  await p
    .getByRole("dialog", { name: "编辑任务" })
    .waitFor({ state: "detached" });
  approve = false;
  check(
    "deleting selected task closes stale details",
    (await p.getByRole("complementary", { name: "任务详情" }).count()) === 0,
  );
  await open("#page=tasks&tab=ai");
  await p.getByRole("button", { name: "新建 AI 任务", exact: true }).click();
  await p.getByLabel("任务名称", { exact: true }).fill("未保存的规则");
  const n2 = prompts;
  await p
    .getByRole("complementary", { name: "全局导航" })
    .getByRole("button", { name: "日历", exact: true })
    .click();
  check(
    "navigation keeps unsaved AI rule after cancel",
    prompts === n2 + 1 &&
      (await p.getByLabel("任务名称", { exact: true }).isVisible()),
  );
  approve = true;
  await p.getByRole("button", { name: "关闭新建 AI 任务" }).click();
  approve = false;
  await p.route("**/api/v1/auth", (r) =>
    r.fulfill({
      status: 200,
      contentType: "application/json",
      body: '{"authenticated":false}',
    }),
  );
  await p.goto(m.url + "/#page=projects");
  await p.reload();
  await p.getByRole("heading", { name: "登录工作台" }).waitFor();
  await shot("review-login-desktop");
  await p.setViewportSize({ width: 390, height: 740 });
  await shot("review-login-mobile");
  check(
    "login remains readable without promo panel",
    !(await p.locator(".login-art").isVisible()),
  );
  await fs.writeFile(
    dir + "review-receipt.json",
    JSON.stringify({ checks, prompts, at: new Date().toISOString() }, null, 2),
  );
} catch (e) {
  await shot("review-failure");
  throw e;
} finally {
  await b.close();
}
