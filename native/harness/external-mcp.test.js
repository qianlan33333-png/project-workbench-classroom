import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import { Context } from '@deepseek-ai/cordis';
import { SystemPrompt } from '@deepseek-ai/dsh-system-prompt';
import { ToolsService } from '@deepseek-ai/dsh-tools';
import { attachExternalMcp, externalMcpConfig } from './external-mcp.js';

const entry = (name, url = 'http://127.0.0.1:1/mcp') => ({
  name, url, tokenEnv: 'WORKBENCH_MCP_REGRESSION', personal: true,
  sources: [{ resourceId: 'docs', section: 'text', version: 'v1' }],
});

test('invalid external entries cannot replace built-in or valid connections', () => {
  assert.ok(externalMcpConfig('not JSON').error);
  const good = entry('healthy');
  const { servers, error } = externalMcpConfig(JSON.stringify([
    entry('workbench'), entry('duplicate'), entry('duplicate'), entry('unsafe', 'https://example.test/?private=value'), good,
  ]));
  assert.match(error, /workbench/);
  assert.deepEqual(servers.filter(row => !row.error), [{ ...good, endpoint: 'http://127.0.0.1:1' }]);
  assert.equal(servers.filter(row => row.error).length, 2);
  assert.doesNotMatch(JSON.stringify(servers), /private=value/);
});

test('native MCP auth failure, recovery and tool removal preserve other registered tools', async t => {
  const key = 'WORKBENCH_MCP_REGRESSION';
  const previous = process.env[key];
  process.env[key] = 'isolated-fixture';
  t.after(() => {
    if (previous === undefined) delete process.env[key];
    else process.env[key] = previous;
  });
  let reject = true;
  let listAffectedTool = true;
  let affectedEvents;
  const server = createServer(async (req, res) => {
    if (req.method === 'GET' && req.url === '/affected' && !reject) {
      affectedEvents = res.writeHead(200, { 'content-type': 'text/event-stream' });
      affectedEvents.flushHeaders();
      return;
    }
    if (req.method !== 'POST') return res.writeHead(405).end();
    const request = JSON.parse(await Array.fromAsync(req).then(chunks => Buffer.concat(chunks)));
    if (req.url === '/affected' && reject) return res.writeHead(401).end();
    if (request.id === undefined) return res.writeHead(202).end();
    const result = {
      initialize: { protocolVersion: request.params?.protocolVersion, capabilities: { tools: { listChanged: true } }, serverInfo: { name: 'fixture', version: '1' } },
      'tools/list': { tools: req.url === '/affected' && !listAffectedTool ? [] : [{ name: 'read', inputSchema: { type: 'object', properties: {} } }] },
    }[request.method];
    res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify({
      jsonrpc: '2.0', id: request.id, ...(result ? { result } : { error: { code: -32601, message: 'Method not found' } }),
    }));
  }).listen(0, '127.0.0.1');
  t.after(() => { server.closeAllConnections(); server.close(); });
  await once(server, 'listening');
  const ctx = new Context();
  await ctx.plugin(SystemPrompt);
  await ctx.plugin(ToolsService);
  const connect = name => attachExternalMcp(ctx, entry(name, `http://127.0.0.1:${server.address().port}/${name}`), {},
    () => ctx.tools.definitions().some(tool => tool.name.startsWith(`mcp__${name}__`)));
  const affected = connect('affected');
  const healthy = connect('healthy');
  t.after(async () => { await Promise.all([affected.dispose(), healthy.dispose()]); });
  const until = async condition => {
    for (let i = 0; i < 100; i++) {
      if (condition()) return;
      await delay(20);
    }
    assert.fail('MCP condition did not settle');
  };
  const tools = () => ctx.tools.definitions().map(tool => tool.name).sort();
  await until(() => affected.status().status === 'failed' && healthy.status().status === 'loaded');
  assert.match(affected.status().message, /凭证被拒绝/);
  assert.deepEqual(tools(), ['mcp__healthy__read']);
  reject = false;
  affected.reconnect();
  await until(() => affected.status().status === 'loaded');
  assert.deepEqual(tools(), ['mcp__affected__read', 'mcp__healthy__read']);
  await until(() => affectedEvents);
  const notify = () => affectedEvents.write('data: {"jsonrpc":"2.0","method":"notifications/tools/list_changed"}\n\n');
  listAffectedTool = false;
  notify();
  await until(() => affected.status().status === 'no-tools');
  assert.deepEqual(tools(), ['mcp__healthy__read']);
  listAffectedTool = true;
  notify();
  await until(() => affected.status().status === 'loaded');
  assert.deepEqual(tools(), ['mcp__affected__read', 'mcp__healthy__read']);
});
