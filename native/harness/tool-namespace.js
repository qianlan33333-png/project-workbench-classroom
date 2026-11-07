// MCP uses double underscores between server and tool names. Keeping server
// namespaces free of underscores makes source attribution unambiguous even
// when an external tool's own name contains that delimiter.
export function toolNamespace(name) {
  if(typeof name!=='string'||!/^[A-Za-z0-9][A-Za-z0-9-]{0,31}$/.test(name))throw Error('Invalid MCP namespace');
  return name;
}
export function namespaceOfTool(name) {
  return /^mcp__([A-Za-z0-9][A-Za-z0-9-]{0,31})__/.exec(name)?.[1];
}
