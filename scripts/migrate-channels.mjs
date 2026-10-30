#!/usr/bin/env node
// Run on the server as root. --inspect only verifies bindings without changes.
// --commit requires both old consumers to have stopped and migrates no business history.
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { DatabaseSync } from "node:sqlite";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";
const require = createRequire(
  "/opt/project-workbench/current/runtime/package.json",
);
const lark = require("@larksuiteoapi/node-sdk");
const legacy = new DatabaseSync(
  "/var/lib/yihaowei-workbench/data/workbench.sqlite",
  { readOnly: true },
);
const accounts = legacy
  .prepare("SELECT id,channel FROM cloud_channel_accounts")
  .all();
const verified = (kind, actor) =>
  accounts.some(
    (a) =>
      a.channel === kind &&
      a.id ===
        crypto.createHash("sha256").update(`${kind}:${actor}`).digest("hex"),
  );
const targets = legacy
  .prepare("SELECT channel,peer FROM cloud_delivery_targets")
  .all();
const stateRoot = "/srv/deepseek-workspace/relay/state";
const read = (file, fallback = {}) =>
  fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : fallback;
const account = read(`${stateRoot}/account.json`);
if (!account.token || !verified("wechat", account.userId))
  throw new Error("Existing WeChat owner binding could not be verified");
const env = {};
for (const line of fs
  .readFileSync("/srv/deepseek-workspace/feishu-bot/.env", "utf8")
  .split("\n")) {
  const m = line.match(/^\s*([A-Z_]+)\s*=\s*(.*?)\s*$/);
  if (m) env[m[1]] = m[2].replace(/^(["'])([\s\S]*)\1$/, "$2");
}
const app = {
  appId: env.FEISHU_APP_ID,
  appSecret: env.FEISHU_APP_SECRET,
  domain: env.FEISHU_DOMAIN,
};
const client = new lark.Client({
  ...app,
  domain: app.domain === "lark" ? lark.Domain.Lark : lark.Domain.Feishu,
  logger: { error() {}, warn() {}, info() {}, debug() {}, trace() {} },
});
const candidates = new Set(
  (env.FEISHU_DM_ALLOWLIST || "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean),
);
for (const { channel, peer } of targets) {
  if (channel !== "feishu") continue;
  if (peer.startsWith("ou_")) candidates.add(peer);
  if (peer.startsWith("oc_")) {
    const response = await client.im.chatMembers.get({
      path: { chat_id: peer },
      params: { member_id_type: "open_id", page_size: 100 },
    });
    if (response.code === 0)
      for (const item of response.data?.items || [])
        if (item.member_id) candidates.add(item.member_id);
  }
}
const owners = [...candidates].filter((id) => verified("feishu", id));
if (owners.length !== 1)
  throw new Error(
    "Feishu requires a fresh owner pairing: existing actor hash could not be uniquely resolved",
  );
console.log(
  JSON.stringify({
    verifiedOwners: { wechat: 1, feishu: 1 },
    businessHistoryImported: false,
  }),
);
if (!process.argv.includes("--commit")) process.exit(0);
for (const service of ["workbench-wechat", "workbench-feishu", "deepseek-harness"]) {
  if (spawnSync("systemctl", ["is-active", "--quiet", service]).status === 0)
    throw new Error(`Stop ${service} before cursor migration`);
}
const db = new DatabaseSync("/var/lib/project-workbench/workbench.sqlite");
db.exec("PRAGMA busy_timeout=5000;PRAGMA foreign_keys=ON;BEGIN IMMEDIATE");
try {
  const old = db
    .prepare("SELECT value FROM settings WHERE key='legacy_channel_migration'")
    .get();
  if (old)
    throw new Error(
      "Channel state was already migrated; refusing to overwrite newer cursors",
    );
  // Old runs are keyed by sha256(channel + ':' + eventId). Preserve only these
  // hashes, so a replay after transport cutover cannot execute old business work.
  let dedupe = 0;
  for (const { name } of legacy
    .prepare("SELECT name FROM sqlite_master WHERE type='table'")
    .all()) {
    if (!/run|message/i.test(name)) continue;
    const columns = legacy
      .prepare("SELECT name FROM pragma_table_info(?)")
      .all(name)
      .map((c) => c.name);
    for (const col of ["id", "run_id"])
      if (columns.includes(col)) {
        const ident = '"' + name.replaceAll('"', '""') + '"';
        for (const row of legacy
          .prepare(
            `SELECT "${col}" AS value FROM ${ident} WHERE "${col}" LIKE 'channel-%'`,
          )
          .all()) {
          const match = String(row.value).match(/^channel-([a-f0-9]{64})$/);
          if (match)
            dedupe += Number(
              db
                .prepare("INSERT OR IGNORE INTO legacy_consumed VALUES(?)")
                .run(match[1]).changes,
            );
        }
      }
  }
  for (const kind of ["wechat", "feishu"]) {
    const id = crypto.randomUUID(),
      ownerId = kind === "wechat" ? account.userId : owners[0];
    const connection = {
      id,
      kind,
      label: kind === "wechat" ? "我的微信" : "我的飞书",
      ownerId,
      enabled: true,
      status: "disconnected",
      lastHeartbeat: 0,
    };
    const secrets =
      kind === "wechat"
        ? {
            token: account.token,
            baseUrl: account.baseUrl,
            accountId: account.accountId,
          }
        : app;
    const prior = legacy
      .prepare("SELECT event_id FROM cloud_channel_replies WHERE channel=?")
      .all(kind)
      .map((r) => r.event_id);
    const state =
      kind === "wechat"
        ? {
            ...read(`${stateRoot}/sync.json`),
            seen: [
              ...new Set([
                ...read(`${stateRoot}/seen.json`, []).map(String),
                ...prior,
              ]),
            ],
            contextTokens: read(`${stateRoot}/context-tokens.json`),
          }
        : { seen: prior };
    db.prepare("INSERT INTO connections VALUES(?,?,?)").run(
      id,
      kind,
      JSON.stringify(connection),
    );
    db.prepare("INSERT INTO connection_secrets VALUES(?,?)").run(
      id,
      JSON.stringify(secrets),
    );
    db.prepare("INSERT INTO settings VALUES(?,?)").run(
      `channel_state:${id}`,
      JSON.stringify(state),
    );
  }
  db.prepare("INSERT INTO settings VALUES(?,?)").run(
    "legacy_channel_migration",
    JSON.stringify({ at: Date.now(), oldPendingSendsReplayed: false }),
  );
  db.exec("COMMIT");
  console.log(
    JSON.stringify({
      migrated: true,
      legacyDedupHashes: dedupe,
      oldPendingSendsReplayed: false,
    }),
  );
} catch (e) {
  db.exec("ROLLBACK");
  throw e;
}
