import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";

async function catalog(env) {
  const transport = new StdioClientTransport({
    command: process.execPath,
    args: [fileURLToPath(new URL("./app-mcp.mjs", import.meta.url))],
    env: { PW_PERSONAL: "true", PW_GATEWAY: "false", PW_FOLLOWUP: "false", ...env },
  });
  const client = new Client({ name: "local-contract-test", version: "1" });
  try { await client.connect(transport); return (await client.listTools()).tools; }
  finally { await client.close(); }
}
test("interactive Today retains its tools and can explicitly create a followup", {timeout: 10000}, async () => {
  const tools = await catalog({});
  for (const name of ["run_global_cli", "change_calendar", "remember_explicit", "manage_followup", "list_followups"])
    assert(tools.some(t => t.name === name), name);
});
test("autonomous followup advertises only read tools and scoped transitions", {timeout: 10000}, async () => {
  const tools = await catalog({ PW_FOLLOWUP: "true" });
  assert.deepEqual(tools.map(t => t.name).sort(), ["project_contexts", "personal_memories", "find_project_sources", "read_project_source", "query_calendar", "list_followups", "manage_followup"].sort());
  const change = tools.find(t => t.name === "manage_followup").inputSchema;
  assert(change.required.includes("id"));
  assert(change.required.includes("version"));
  assert.equal(tools.find(t => t.name === "query_calendar").inputSchema.properties.remote.const, false);
});
test("external customer catalog does not gain Today tools", {timeout: 10000}, async () => {
  const tools = await catalog({ PW_GATEWAY: "true", PW_PERSONAL: "false" });
  assert.deepEqual(tools.map(t => t.name).sort(), ["search_history", "read_run", "read_paused_history"].sort());
});

test("read-only expert catalog exposes managed reads and cannot confirm or cancel",{timeout:10000},async()=>{
 const tools=await catalog({PW_PERSONAL:"false",PW_COMMUNICATION_READONLY:"true"});
 for(const name of ["list_components","call_component","read_component_results"])assert(tools.some(t=>t.name===name),name);
 for(const name of ["confirm_component","cancel_component","component_status","manage_cli","save_schedule","run_global_cli"])assert(!tools.some(t=>t.name===name),name);
});
