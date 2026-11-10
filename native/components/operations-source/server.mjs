import http from 'node:http';
import { createRequire } from 'node:module';
import { timingSafeEqual } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { runJSONChild } from '../answer-corpus/child-json.mjs';

const require = createRequire(process.env.WORKBENCH_OPERATIONS_RUNTIME ?? new URL('../../runtime/package.json', import.meta.url));
const { McpServer } = require('@modelcontextprotocol/sdk/server/mcp.js');
const { StreamableHTTPServerTransport } = require('@modelcontextprotocol/sdk/server/streamableHttp.js');
const { z } = require('zod');
const {
  WORKBENCH_OPERATIONS_PROJECT: project,
  WORKBENCH_OPERATIONS_TOKEN: token,
  WORKBENCH_OPERATIONS_REFS: refs,
  WORKBENCH_OPERATIONS_STATE: state,
  WORKBENCH_OPERATIONS_ARCHIVE_AUTH: archiveAuth,
  WORKBENCH_OPERATIONS_SSH_CONFIG: sshConfig,
} = process.env;
if (!project || !token || token.length < 32 || (!sshConfig && !(refs && state && archiveAuth))) {
  throw Error('operations source configuration missing');
}

const collector = fileURLToPath(new URL('./collect.py', import.meta.url));
const auth = Buffer.from('Bearer ' + token);
const active = new Set();
const listener = http.createServer(async (req, res) => {
  const presented = Buffer.from(req.headers.authorization || '');
  if (req.url !== '/mcp' || req.method !== 'POST') {
    res.writeHead(404).end();
    return;
  }
  if (presented.length !== auth.length || !timingSafeEqual(presented, auth) ||
      req.headers['x-workbench-kind'] !== 'project' ||
      req.headers['x-workbench-project'] !== project ||
      req.headers['x-workbench-session'] !== 'operations-' + project) {
    res.writeHead(403).end();
    return;
  }
  const controller = new AbortController();
  active.add(controller);
  res.on('close', () => controller.abort());
  const server = new McpServer({name: 'operations-source', version: '1.0.0'});
  const transport = new StreamableHTTPServerTransport({sessionIdGenerator: undefined});
  server.registerTool('operations_refresh', {
    description: 'Read the authorized archive and HXC pilot references.',
    inputSchema: {operationKey: z.string().regex(/^[A-Za-z0-9_-]{1,96}$/)},
  }, async ({operationKey}) => {
    if (operationKey !== req.headers['x-workbench-run']) {
      return {isError: true, content: [{type:'text', text:'Invalid source run.'}]};
    }
    try {
      const result = sshConfig
        ? await runJSONChild(['-F', sshConfig, 'workbench-operations-source', 'read', operationKey], undefined, controller.signal, 300_000, {command: '/usr/bin/ssh'})
        : await runJSONChild([collector, '--refs', refs, '--limit', '50', '--operation-key', operationKey, '--state-dir', state], undefined, controller.signal, 300_000);
      return {structuredContent: result, content: [{type:'text', text:'Source records collected.'}]};
    } catch {
      return {isError: true, content: [{type:'text', text:'Source unavailable; retry with the same run.'}]};
    }
  });
  if (sshConfig) server.registerTool('operations_read', {
    description: 'Read original operations plans, candidates or published archives. No generation or sending.',
    inputSchema: {view: z.enum(['catalog', 'members', 'profiles', 'profile']), params: z.record(z.string(), z.string()).default({})},
  }, async input => {
    try {
      const result = await runJSONChild(['-F', sshConfig, 'workbench-operations-source', 'directory'], input, controller.signal, 85_000, {command: '/usr/bin/ssh'});
      return {structuredContent: result, content: [{type:'text', text:'Read original operations data.'}]};
    } catch {
      return {isError: true, content: [{type:'text', text:'Original operations data is unavailable.'}]};
    }
  });
  try {
    await server.connect(transport);
    await transport.handleRequest(req, res);
  } catch {
    if (!res.headersSent) res.writeHead(500).end();
  } finally {
    active.delete(controller);
    await server.close();
  }
});
listener.listen(Number(process.env.WORKBENCH_OPERATIONS_SOURCE_PORT || '7396'), '127.0.0.1');
for (const signal of ['SIGTERM','SIGINT']) {
  process.once(signal, () => {
    for (const controller of active) controller.abort();
    listener.close();
  });
}
