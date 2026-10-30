import fs from "node:fs/promises";
import crypto from "node:crypto";
import { fileURLToPath } from "node:url";
import * as lark from "@larksuiteoapi/node-sdk";

// No inbound history is assembled here. The app persists and attributes each
// event before acknowledging it, and replies come exclusively from the outbox.
export function normalizeWechat(c, msg) {
  if (msg.from_user_id !== c.ownerId || msg.message_type !== 1) return null;
  const id = msg.message_id;
  if (id === undefined || id === null) return null;
  const text = (msg.item_list ?? [])
    .filter((i) => i.type === 1)
    .map((i) => i.text_item?.text ?? "")
    .join("")
    .trim();
  const unsupported = (msg.item_list ?? []).some((i) => i.type !== 1);
  return {
    source: "wechat",
    connectionId: c.id,
    actorId: msg.from_user_id,
    conversationId: msg.from_user_id,
    eventId: String(id),
    text:
      text ||
      (unsupported
        ? "[收到暂不支持解析的微信媒体，请通过网页上传文件，或在微信中发送文字说明。]"
        : ""),
    replyTo: "",
  };
}
export function normalizeFeishu(c, event) {
  const { sender, message: m } = event;
  if (
    !m ||
    m.chat_type !== "p2p" ||
    sender?.sender_type !== "user" ||
    sender.sender_id?.open_id !== c.ownerId ||
    !m.message_id
  )
    return null;
  let content;
  try {
    content = JSON.parse(m.content);
  } catch {
    return null;
  }
  let text = "";
  if (m.message_type === "text") text = content.text ?? "";
  if (m.message_type === "post") {
    const post = content.zh_cn ?? content.en_us ?? content;
    text = [
      post.title ?? "",
      ...(post.content ?? []).map((line) =>
        line.map((item) => item.text ?? item.href ?? "").join(""),
      ),
    ].join("\n");
  }
  const attachment = ["image", "file", "audio", "media"].includes(
    m.message_type,
  )
    ? {
        key: content.file_key ?? content.image_key,
        name:
          content.file_name ??
          `${m.message_id}.${m.message_type === "image" ? "png" : "bin"}`,
        type: m.message_type === "image" ? "image" : "file",
      }
    : null;
  return {
    incoming: {
      source: "feishu",
      connectionId: c.id,
      actorId: c.ownerId,
      conversationId: m.chat_id,
      eventId: m.message_id,
      replyTo: m.parent_id ?? "",
      text:
        text.trim() ||
        (attachment
          ? "请查看本次附件。"
          : "[收到此版本尚不支持的飞书消息类型，请发送文字或文件。]"),
    },
    attachment,
  };
}
export function chunks(text, max = 1600) {
  const out = [];
  let part = "";
  for (const char of text) {
    if (part.length + char.length > max) {
      out.push(part);
      part = "";
    }
    part += char;
  }
  if (part) out.push(part);
  return out;
}
const delay = (ms) => new Promise((r) => setTimeout(r, ms));
export class Bridge {
  constructor(config) {
    this.config = config;
    this.controllers = new Map();
    this.stopped = false;
  }
  async call(path, body, method) {
    const res = await fetch(
      `${this.config.url.replace(/\/$/, "")}/api/v1/channel${path}`,
      {
        method: method ?? (body === undefined ? "GET" : "POST"),
        headers: {
          Authorization: `Bearer ${this.config.token}`,
          ...(body instanceof FormData
            ? {}
            : { "Content-Type": "application/json" }),
        },
        body:
          body === undefined
            ? undefined
            : body instanceof FormData
              ? body
              : JSON.stringify(body),
        signal: AbortSignal.timeout(20000),
      },
    );
    const data = await res.json();
    if (!res.ok) throw new Error(data.error ?? "Workbench unavailable");
    return data;
  }
  async status(c, status) {
    try {
      await this.call(`/connections/${c.id}/heartbeat`, { status });
    } catch {}
  }
  async start() {
    while (!this.stopped) {
      try {
        const configurations = await this.call("/connections");
        const enabled = new Set();
        for (const item of configurations) {
          if (!item.connection.enabled) continue;
          const id = item.connection.id;
          enabled.add(id);
          const fingerprint = crypto
            .createHash("sha256")
            .update(JSON.stringify(item))
            .digest("hex");
          const existing = this.controllers.get(id);
          if (
            existing &&
            existing.credentials === JSON.stringify(item.secrets) &&
            existing.ownerId === item.connection.ownerId
          )
            continue;
          if (existing) existing.abort.abort();
          const abort = new AbortController();
          const record = {
            abort,
            credentials: JSON.stringify(item.secrets),
            ownerId: item.connection.ownerId,
          };
          this.controllers.set(id, record);
          const run =
            item.connection.kind === "wechat"
              ? this.wechat(item, abort.signal)
              : this.feishu(item, abort.signal);
          run
            .catch(() => this.status(item.connection, "disconnected"))
            .finally(() => {
              if (this.controllers.get(id) === record)
                this.controllers.delete(id);
            });
        }
        for (const [id, record] of this.controllers) {
          if (!enabled.has(id)) {
            record.abort.abort();
            this.controllers.delete(id);
          }
        }
      } catch {
        console.error("Channel configuration temporarily unavailable");
      }
      await delay(5000);
    }
  }
  async outbox(c, signal, send) {
    while (!signal.aborted && !this.stopped) {
      try {
        const { delivery } = await this.call(`/connections/${c.id}/claim`, {});
        if (!delivery) {
          await delay(1000);
          continue;
        }
        let outcome;
        try {
          outcome = await send(delivery);
        } catch {
          outcome = {
            state: "unknown",
            receipt: "",
            error: "发送未获得确定回执；不会自动重发。请核对平台。",
          };
        }
        try {
          await this.call(`/deliveries/${delivery.id}/finish`, outcome);
        } catch {
          console.error(
            "Delivery receipt could not be saved; left sending for reconciliation",
          );
        }
      } catch {
        await delay(2000);
      }
    }
  }
  async wechat({ connection: c, secrets }, signal) {
    const base = new URL(secrets.baseUrl ?? "https://ilinkai.weixin.qq.com");
    if (
      base.protocol !== "https:" ||
      base.hostname !== "ilinkai.weixin.qq.com" ||
      !secrets.token
    )
      throw new Error("Invalid WeChat configuration");
    let state = await this.call(`/connections/${c.id}/state`);
    state.contextTokens ??= {};
    state.seen ??= [];
    const platform = async (endpoint, body, timeout = 45000) => {
      const res = await fetch(new URL(`ilink/bot/${endpoint}`, base), {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          AuthorizationType: "ilink_bot_token",
          Authorization: `Bearer ${secrets.token}`,
          "X-WECHAT-UIN": Buffer.from(
            String(crypto.randomBytes(4).readUInt32BE()),
          ).toString("base64"),
          "iLink-App-Id": "bot",
          "iLink-App-ClientVersion": String((2 << 16) | (4 << 8) | 10),
        },
        body: JSON.stringify({
          ...body,
          base_info: {
            channel_version: "2.4.10",
            bot_agent: "ProjectWorkbench",
          },
        }),
        signal: AbortSignal.any([signal, AbortSignal.timeout(timeout)]),
      });
      if (!res.ok) throw new Error("WeChat HTTP failure");
      return res.json();
    };
    const outbox = this.outbox(c, signal, async (d) => {
      const context = state.contextTokens[d.actorId];
      if (!context)
        return {
          state: "failed",
          receipt: "",
          error: "需要本人先在微信发一条消息，建立回复窗口。",
        };
      const parts = chunks(d.text);
      for (let i = 0; i < parts.length; i++) {
        const clientID = `pw-${d.id}-${i}`;
        const result = await platform(
          "sendmessage",
          {
            msg: {
              from_user_id: "",
              to_user_id: d.actorId,
              client_id: clientID,
              message_type: 2,
              message_state: 2,
              item_list: [{ type: 1, text_item: { text: parts[i] } }],
              context_token: context,
            },
          },
          20000,
        );
        if (result.ret && result.ret !== 0)
          return {
            state: i === 0 ? "failed" : "unknown",
            receipt: "",
            error: `平台拒绝第 ${i + 1} 段；已发送段落不会自动重发。`,
          };
      }
      return { state: "accepted", receipt: `client:${d.id}`, error: "" };
    });
    try {
      while (!signal.aborted && !this.stopped) {
        try {
          const result = await platform("getupdates", {
            get_updates_buf: state.get_updates_buf ?? "",
          });
          if (result.ret === -14) {
            await this.status(c, "login-required");
            await delay(5000);
            continue;
          }
          if (result.ret && result.ret !== 0)
            throw new Error("WeChat polling failure");
          for (const msg of result.msgs ?? []) {
            const incoming = normalizeWechat(c, msg);
            if (!incoming || !incoming.text) continue;
            if (msg.context_token)
              state.contextTokens[msg.from_user_id] = msg.context_token;
            if (state.seen.includes(incoming.eventId)) continue;
            await this.call("/messages", incoming);
            state.seen.push(incoming.eventId);
            state.seen = state.seen.slice(-5000);
          }
          if (typeof result.get_updates_buf === "string")
            state.get_updates_buf = result.get_updates_buf;
          await this.call(`/connections/${c.id}/state`, state, "PUT");
          await this.status(c, "connected");
        } catch {
          if (signal.aborted) break;
          await this.status(c, "reconnecting");
          await delay(3000);
        }
      }
    } finally {
      await outbox;
      await this.status(c, "disconnected");
    }
  }
  async feishu({ connection: c, secrets }, signal) {
    if (!secrets.appId || !secrets.appSecret)
      throw new Error("Invalid Feishu credentials");
    const options = {
      appId: secrets.appId,
      appSecret: secrets.appSecret,
      domain: secrets.domain === "lark" ? lark.Domain.Lark : lark.Domain.Feishu,
      loggerLevel: lark.LoggerLevel.error,
    };
    const client = new lark.Client(options);
    const ws = new lark.WSClient(options);
    let state = await this.call(`/connections/${c.id}/state`);
    state.seen ??= [];
    let serial = Promise.resolve();
    const handler = async (event) => {
      const normalized = normalizeFeishu(c, event);
      if (!normalized) return;
      const { incoming, attachment } = normalized;
      if (state.seen.includes(incoming.eventId)) return;
      if (attachment?.key) {
        const response = await client.im.messageResource.get({
          path: { message_id: incoming.eventId, file_key: attachment.key },
          params: { type: attachment.type },
        });
        const stream = response.getReadableStream();
        const buffers = [];
        let size = 0;
        for await (const part of stream) {
          size += part.length;
          if (size > 128 * 1024 * 1024) throw new Error("Attachment too large");
          buffers.push(part);
        }
        const form = new FormData();
        form.append("file", new Blob(buffers), attachment.name);
        form.append("connectionId", c.id);
        form.append("actorId", c.ownerId);
        const upload = await this.call("/upload", form);
        incoming.attachments = [upload.path];
      }
      await this.call("/messages", incoming);
      state.seen.push(incoming.eventId);
      state.seen = state.seen.slice(-5000);
      await this.call(`/connections/${c.id}/state`, state, "PUT");
    };
    const dispatcher = new lark.EventDispatcher({}).register({
      "im.message.receive_v1": (event) => {
        const result = serial.then(() => handler(event));
        serial = result.catch(() => {
          console.error("Feishu inbound persistence failed");
        });
        return result;
      },
    });
    await ws.start({ eventDispatcher: dispatcher });
    await this.status(c, "connected");
    const timer = setInterval(() => this.status(c, "connected"), 30000);
    const outbox = this.outbox(c, signal, async (d) => {
      const parts = chunks(d.text, 12000),
        receipts = [];
      for (let i = 0; i < parts.length; i++) {
        const uuid = crypto
          .createHash("sha256")
          .update(`${d.id}:${i}`)
          .digest("hex")
          .slice(0, 32);
        const content = JSON.stringify({ text: parts[i] });
        const result = d.replyTo
          ? await client.im.message.reply({
              path: { message_id: d.replyTo },
              data: { msg_type: "text", content, uuid },
            })
          : await client.im.message.create({
              params: { receive_id_type: "chat_id" },
              data: {
                receive_id: d.conversationId,
                msg_type: "text",
                content,
                uuid,
              },
            });
        if (result.code !== 0 || !result.data?.message_id)
          return {
            state: receipts.length ? "unknown" : "failed",
            receipt: "",
            error: "飞书未确认全部消息发送成功",
          };
        receipts.push(result.data.message_id);
      }
      return { state: "sent", receipt: receipts.at(-1), receipts, error: "" };
    });
    try {
      if (!signal.aborted)
        await new Promise((r) =>
          signal.addEventListener("abort", r, { once: true }),
        );
      await serial;
      await outbox;
    } finally {
      clearInterval(timer);
      ws.close({ force: true });
      await this.status(c, "disconnected");
    }
  }
  async stop() {
    this.stopped = true;
    for (const c of this.controllers.values()) c.abort.abort();
  }
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const config = JSON.parse(
    await fs.readFile(
      process.env.WORKBENCH_CHANNEL_CONFIG ||
        "/etc/project-workbench/channels.json",
      "utf8",
    ),
  );
  const bridge = new Bridge(config);
  process.on("SIGTERM", () => bridge.stop());
  process.on("SIGINT", () => bridge.stop());
  await bridge.start();
}
