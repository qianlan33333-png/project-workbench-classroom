// A private loopback endpoint inside each task container. Real provider
// credentials are injected only by the host's existing per-run Unix bridge.
import http from "node:http";

export function createGatewayModelServer(socketPath) {
  return http.createServer((req, res) => {
    if (req.method !== "POST" || req.url !== "/v1/messages") {
      res.writeHead(403).end("model request refused");
      return;
    }
    const headers = { "content-type": "application/json" };
    for (const name of ["anthropic-version", "anthropic-beta"])
      if (req.headers[name]) headers[name] = req.headers[name];
    const upstream = http.request({
      socketPath, path: "/gateway-model/v1/messages", method: "POST", headers,
    }, reply => {
      res.writeHead(reply.statusCode, { "content-type": reply.headers["content-type"] || "application/json" });
      reply.on("error", () => res.destroy());
      reply.pipe(res);
    });
    upstream.on("error", () => {
      if (!res.headersSent) res.writeHead(502).end("model bridge unavailable");
      else res.destroy();
    });
    req.on("aborted", () => upstream.destroy());
    req.on("error", () => upstream.destroy());
    res.on("close", () => { if (!res.writableFinished) upstream.destroy(); });
    req.pipe(upstream);
  });
}

export const name = "workbench-gateway-model";
export async function apply(ctx) {
  const server = createGatewayModelServer("/bridge.sock");
  ctx.on("dispose", () => new Promise(resolve => {
    server.close(resolve);
    server.closeAllConnections();
  }));
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(18433, "127.0.0.1", resolve);
  });
}
