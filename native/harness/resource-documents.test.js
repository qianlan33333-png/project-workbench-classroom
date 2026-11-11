import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import test from 'node:test';
import {McpServer} from '@modelcontextprotocol/sdk/server/mcp.js';
import {StreamableHTTPServerTransport} from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import {resourceDocuments} from './resource-documents.js';

test('Vault reads standard MCP Markdown from the pinned source and rechecks access', async t => {
  const env = 'WORKBENCH_MCP_VAULT_FIXTURE';
  const token = 'isolated-fixture';
  process.env[env] = token;
  t.after(() => delete process.env[env]);
  const name = '答疑.md';
  const body = '# 原答疑\n\n[[关联说明]]';
  const headers = [];
  const server = createServer(async (req, res) => {
    headers.push(req.headers);
    if (req.method !== 'POST') {
      res.writeHead(405).end();
      return;
    }
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const mcp = new McpServer({name:'vault-fixture',version:'1'});
    const uri = 'corpus://knowledge/' + encodeURIComponent(name);
    mcp.registerResource(name, uri, {mimeType:'text/markdown'}, async () => ({
      contents:[{uri,mimeType:'text/markdown',text:body}],
    }));
    const transport = new StreamableHTTPServerTransport({sessionIdGenerator:undefined,enableJsonResponse:true});
    res.once('finish', () => {void transport.close();void mcp.close();});
    await mcp.connect(transport);
    await transport.handleRequest(req,res,JSON.parse(Buffer.concat(chunks).toString()));
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  const source = {resourceId:'answer-corpus',version:'published-v2',section:'catalog'};
  const config = {toolServers:[{projects:['support'],sources:[source],tokenEnv:env,
    url:'http://127.0.0.1:'+server.address().port+'/mcp'}]};
  const calls = [];
  const result = await resourceDocuments(config, async (path, principal, refs) => {
    calls.push({path,principal,refs});
  }, 'support', source.resourceId, undefined, {all:true,version:'published-v1'});
  assert.deepEqual(result.notes, [{path:name,text:body}]);
  assert.equal(result.version,'published-v1');
  assert.deepEqual(calls.map(call => call.path), ['/session/external-sources','/session/external-sources']);
  assert.equal(calls[1].refs[0].version,'published-v1');
  assert.ok(headers.every(h => h.authorization==='Bearer '+token && h['x-workbench-project']==='support'));
  let access = 0;
  await assert.rejects(resourceDocuments(config, async () => {
    if (++access===2) throw Error('source revoked');
  }, 'support', source.resourceId, undefined, {all:true,version:'published-v1'}), /source revoked/);
});
