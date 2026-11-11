// Trust native RPC provenance, never text supplied as an MCP argument.
export function nativeLibraryCommand(message) {
  const source = message?.source;
  if (message?.role !== "user" || source?.kind !== "user" ||
      typeof source.rpcId !== "string" || !source.rpcId || !Array.isArray(message.content) ||
      !message.content.length || message.content.some(block => block.type !== "text" || typeof block.text !== "string")) return;
  const text = message.content.map(block => block.text).join("\n").trim();
  if (text.length > 0 && text.length <= 2000) return { requestId: source.rpcId, text };
}
export function activeLibraryCommand(messages, fresh) {
  if (!fresh || Date.now() - fresh.received > 120000) return;
  const user = messages.findLast(message => message.role === "user");
  const current = nativeLibraryCommand(user);
  if (current?.requestId === fresh.requestId && current.text === fresh.text) return current;
}
