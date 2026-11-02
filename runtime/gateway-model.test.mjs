import test from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { createGatewayModelServer } from "./gateway-model.mjs";

test("customer model hop streams via private socket and strips supplied credentials", async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "pwg-"));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const socket = path.join(dir, "bridge.sock");
  let calls = 0;
  const host = http.createServer((req, res) => {
    calls++;
    assert.equal(req.url, "/gateway-model/v1/messages");
    assert.equal(req.headers["x-api-key"], undefined);
    assert.equal(req.headers.authorization, undefined);
    assert.equal(req.headers["anthropic-version"], "2023-06-01");
    res.writeHead(200, { "content-type": "text/event-stream" });
    req.resume();
    req.on("end", () => { res.write("data: first\n\n"); setImmediate(() => res.end("data: second\n\n")); });
  });
  await new Promise(resolve => host.listen(socket, resolve));
  t.after(() => { host.closeAllConnections(); host.close(); });
  const proxy = createGatewayModelServer(socket);
  await new Promise(resolve => proxy.listen(0, "127.0.0.1", resolve));
  t.after(() => { proxy.closeAllConnections(); proxy.close(); });
  const base = `http://127.0.0.1:${proxy.address().port}`;
  const result = await fetch(base + "/v1/messages", { method: "POST", headers: {
    "x-api-key": "untrusted-placeholder", authorization: "Bearer untrusted", "anthropic-version": "2023-06-01",
  }, body: JSON.stringify({ model: "offline-model", messages: [] }) });
  assert.equal(result.status, 200);
  assert.equal(await result.text(), "data: first\n\ndata: second\n\n");
  for (const url of ["/v1/files", "/v1/messages?target=https://evil.example", "/other"]) {
    assert.equal((await fetch(base + url, { method: "POST", body: "{}" })).status, 403);
  }
  assert.equal((await fetch(base + "/v1/messages")).status, 403);
  assert.equal(calls, 1);
});

test("missing private model bridge returns a bounded generic error", async t => {
  const proxy = createGatewayModelServer("/nonexistent/gateway-test.sock");
  await new Promise(resolve => proxy.listen(0, "127.0.0.1", resolve));
  t.after(() => { proxy.closeAllConnections(); proxy.close(); });
  const res = await fetch(`http://127.0.0.1:${proxy.address().port}/v1/messages`, { method: "POST", body: "{}" });
  assert.equal(res.status, 502);
  assert.equal(await res.text(), "model bridge unavailable");
});

test("closing the customer stream cancels the private upstream hop", async t => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "pwg-"));
  t.after(() => fs.rm(dir, { recursive: true, force: true }));
  const socket = path.join(dir, "bridge.sock");
  let closed;
  const upstreamClosed = new Promise(resolve => { closed = resolve; });
  const host = http.createServer((req, res) => {
    req.resume();
    req.on("end", () => {
      res.writeHead(200, { "content-type": "text/event-stream" });
      res.write("data: first\n\n");
      res.once("close", closed);
    });
  });
  await new Promise(resolve => host.listen(socket, resolve));
  t.after(() => { host.closeAllConnections(); host.close(); });
  const proxy = createGatewayModelServer(socket);
  await new Promise(resolve => proxy.listen(0, "127.0.0.1", resolve));
  t.after(() => { proxy.closeAllConnections(); proxy.close(); });
  await new Promise((resolve, reject) => {
    const request = http.request(`http://127.0.0.1:${proxy.address().port}/v1/messages`, { method: "POST" }, reply => {
      reply.once("data", () => { request.destroy(); resolve(); });
    });
    request.once("error", reject);
    request.end("{}");
  });
  let timer;
  try {
    await Promise.race([upstreamClosed, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("upstream did not close")), 1500); })]);
  } finally { clearTimeout(timer); }
});
