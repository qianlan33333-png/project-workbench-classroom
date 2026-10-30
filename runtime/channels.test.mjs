import { test } from "node:test";
import assert from "node:assert/strict";
import { normalizeWechat, normalizeFeishu, chunks } from "./channels.mjs";
const c = { id: "connection", ownerId: "owner" };
test("WeChat only accepts owner inbound; payload instruction stays text", () => {
  assert.equal(
    normalizeWechat(c, {
      from_user_id: "other",
      message_type: 1,
      message_id: 1,
    }),
    null,
  );
  assert.equal(
    normalizeWechat(c, {
      from_user_id: "owner",
      message_type: 2,
      message_id: 1,
    }),
    null,
  );
  const m = normalizeWechat(c, {
    from_user_id: "owner",
    message_type: 1,
    message_id: 3,
    item_list: [{ type: 1, text_item: { text: "切换项目" } }],
  });
  assert.equal(m.text, "切换项目");
  assert.equal(m.sessionId, undefined);
});
test("Feishu rejects groups and other senders, preserves explicit parent", () => {
  const event = {
    sender: { sender_type: "user", sender_id: { open_id: "owner" } },
    message: {
      message_id: "m",
      chat_type: "p2p",
      chat_id: "c",
      parent_id: "reply",
      message_type: "text",
      content: JSON.stringify({ text: "第三条改一下" }),
    },
  };
  const m = normalizeFeishu(c, event);
  assert.equal(m.incoming.replyTo, "reply");
  assert.equal(m.incoming.text, "第三条改一下");
  event.message.chat_type = "group";
  assert.equal(normalizeFeishu(c, event), null);
});
test("Unicode chunks never split surrogate pairs", () => {
  const text = "你好😀".repeat(2000);
  const parts = chunks(text);
  assert.equal(parts.join(""), text);
  assert(parts.every((s) => s.length <= 1600 && !s.includes("\uFFFD")));
});

test("WeChat group owner messages are rejected and 64-bit IDs are lossless", async () => {
  const { parseWechatJSON } = await import("./channels.mjs");
  assert.equal(
    parseWechatJSON('{"message_id":9223372036854775807}').message_id,
    "9223372036854775807",
  );
  assert.equal(
    normalizeWechat(c, {
      from_user_id: "owner",
      message_type: 1,
      message_id: "m",
      group_id: "g",
    }),
    null,
  );
});
test("WeChat attachments decode both supported AES key encodings", async () => {
  const { decryptWechatMedia } = await import("./channels.mjs");
  const { default: crypto } = await import("node:crypto");
  const key = crypto.randomBytes(16),
    plain = Buffer.from("中文附件\n[[链接]]");
  const cipher = crypto.createCipheriv("aes-128-ecb", key, null),
    encrypted = Buffer.concat([cipher.update(plain), cipher.final()]);
  assert.deepEqual(
    decryptWechatMedia(encrypted, key.toString("base64")),
    plain,
  );
  assert.deepEqual(
    decryptWechatMedia(
      encrypted,
      Buffer.from(key.toString("hex")).toString("base64"),
    ),
    plain,
  );
  assert.deepEqual(
    decryptWechatMedia(encrypted, "", key.toString("hex")),
    plain,
  );
});

test("release symlink starts the channel entrypoint", async () => {
  const fs = await import("node:fs/promises");
  const { tmpdir } = await import("node:os");
  const path = await import("node:path");
  const { fileURLToPath } = await import("node:url");
  const { spawnSync } = await import("node:child_process");
  const dir = await fs.mkdtemp(path.join(tmpdir(), "pw-entry-"));
  try {
    const entry = path.join(dir, "channels.mjs");
    await fs.symlink(
      fileURLToPath(new URL("./channels.mjs", import.meta.url)),
      entry,
    );
    const result = spawnSync(process.execPath, [entry], {
      env: {
        ...process.env,
        WORKBENCH_CHANNEL_CONFIG: path.join(dir, "missing-config.json"),
      },
      encoding: "utf8",
      timeout: 10000,
    });
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /missing-config\.json/);
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
});
