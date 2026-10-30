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
