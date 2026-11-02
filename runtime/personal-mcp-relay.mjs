// Only transport bytes; the workbench resolves the owner's command and starts
// the MCP in a separate container without Harness credentials or project data.
import http from "node:http";
const request = http.get({
  socketPath: "/bridge.sock",
  path: (process.argv[3] === "gateway" ? "/gateway-stdio/" : "/global-mcp/") + encodeURIComponent(process.argv[2]),
  headers: { Connection: "Upgrade", Upgrade: "pwb-stdio" },
});
request.on("upgrade", (_res, socket, head) => {
  if (head.length) process.stdout.write(head);
  socket.pipe(process.stdout);
  process.stdin.pipe(socket);
  socket.on("error", () => process.exit(1));
  socket.on("close", () => process.exit(0));
});
request.on("response", res => { process.stderr.write("MCP unavailable: " + res.statusCode); process.exit(1); });
request.on("error", () => process.exit(1));
