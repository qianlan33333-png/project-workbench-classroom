import { spawn } from 'node:child_process';
import { mkdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const feishuScopes = Object.freeze([
  'docx:document:readonly', 'docs:document.content:read', 'docs:document.comment:read',
  'search:docs:read', 'drive:drive.metadata:readonly', 'space:document:retrieve',
  'wiki:node:read', 'wiki:node:retrieve', 'wiki:space:read', 'wiki:space:retrieve',
  'base:app:read', 'base:dashboard:read', 'base:field:read', 'base:form:read',
  'base:record:read', 'base:table:read', 'base:template:read', 'base:view:read',
  'sheets:spreadsheet:read', 'sheets:spreadsheet.meta:read', 'slides:presentation:read',
  'calendar:calendar:read', 'calendar:calendar.event:read', 'calendar:calendar.free_busy:read',
  'task:task:read', 'task:tasklist:read', 'task:comment:read', 'task:section:read', 'task:custom_field:read',
  'vc:meeting.search:read', 'vc:note:read', 'vc:record:readonly',
  'minutes:minutes.basic:read', 'minutes:minutes.artifacts:read', 'minutes:minutes.search:read', 'minutes:minutes:readonly',
  'im:chat:read', 'im:message:readonly', 'im:message.group_msg:get_as_user',
  'im:message.p2p_msg:get_as_user', 'search:message',
  'board:whiteboard:node:read', 'drive:file:download', 'mindnote:node:read',
]);

const command = fileURLToPath(new URL('../node_modules/@larksuite/cli/bin/lark-cli', import.meta.url));

export function feishuAuthURL(value) {
  const url = new URL(value);
  if (url.protocol !== 'https:' || url.username || url.password || url.port ||
      !['feishu.cn', 'larksuite.com'].some(domain => url.hostname === domain || url.hostname.endsWith('.' + domain))) {
    throw new Error('Unexpected Feishu authorization origin');
  }
  return url.href;
}

export function feishuCLI(directory, appId) {
  if (!/^cli_[a-zA-Z0-9]+$/.test(appId)) throw new Error('Invalid Feishu application');
  const profile = join(directory, 'feishu-cli', appId);
  const env = {
    PATH: process.env.PATH,
    HOME: process.env.HOME,
    LANG: process.env.LANG || 'en_US.UTF-8',
    LARKSUITE_CLI_CONFIG_DIR: profile,
  };

  async function run(args, { input, signal, timeout = 15000, maxBytes = 128 * 1024, acceptFailure = false, rawJSON = false } = {}) {
    return new Promise((resolve, reject) => {
      const deadline = AbortSignal.timeout(timeout);
      const child = spawn(command, args[0] === 'config' ? args : ['--profile=workbench', ...args], {
        cwd: profile, env, stdio: ['pipe', 'pipe', 'ignore'],
        signal: signal ? AbortSignal.any([signal, deadline]) : deadline,
      });
      const chunks = [];
      let bytes = 0;
      child.stdout.on('data', chunk => {
        bytes += chunk.length;
        if (bytes > maxBytes) child.kill();
        else chunks.push(chunk);
      });
      child.on('error', reject);
      child.on('close', code => {
        if (bytes > maxBytes || (code !== 0 && !acceptFailure)) {
          reject(new Error('Feishu CLI request failed'));
          return;
        }
        try {
          const text = Buffer.concat(chunks).toString('utf8');
          const value = JSON.parse(text);
          resolve(rawJSON ? text : value);
        }
        catch { reject(new Error('Feishu CLI returned an invalid response')); }
      });
      child.stdin.on('error', () => {});
      child.stdin.end(input);
    });
  }

  async function status(ownerId, signal, verify = true) {
    try {
      await readFile(join(profile, 'config.json'));
      const result = await run(['auth', 'status', '--json', ...(verify ? ['--verify'] : [])], { signal });
      const user = result.identities?.user;
      const sameOwner = !ownerId || !user?.available || ownerId === user.openId;
      const scopes = user?.available && (!verify || user.verified === true) && sameOwner
        ? new Set(String(user.scope || '').split(/[ ,]+/)) : new Set();
      const missing = feishuScopes.filter(scope => !scopes.has(scope));
      return {
        state: missing.length === 0 ? 'linked' : missing.length < feishuScopes.length ? 'partial'
          : user?.available ? 'reconnect' : 'unlinked',
        missing,
        ...(sameOwner ? {} : { error: '请使用链接飞书机器人的账号授权。' }),
      };
    } catch (error) {
      return { state: error.code === 'ENOENT' ? 'unlinked' : 'reconnect', missing: feishuScopes };
    }
  }

  return {
    status,
    async authorize({ appSecret, ownerId, update, signal }) {
      await mkdir(profile, { recursive: true, mode: 0o700 });
      const current = await status(ownerId, signal);
      if (current.state === 'linked') return current;
      await run(['config', 'init', '--app-id', appId, '--app-secret-stdin', '--brand', 'feishu', '--name', 'workbench'], { input: appSecret, signal });
      const enabled = await run(['auth', 'scopes', '--json'], { signal }).catch(() => null);
      const appScopes = enabled?.scopes ?? enabled?.userScopes;
      const available = Array.isArray(appScopes)
        ? new Set(appScopes.map(scope => typeof scope === 'string' ? scope : scope.scope ?? scope.name)) : null;
      const scopes = available ? feishuScopes.filter(scope => available.has(scope)) : feishuScopes;
      if (!scopes.length) return { ...current, error: '请先在飞书开放平台为应用开通所需权限。' };
      const flow = await run(['auth', 'login', '--scope', scopes.join(' '), '--no-wait', '--json'], { signal });
      if (!flow.device_code) throw new Error('Missing Feishu device code');
      update({ url: feishuAuthURL(flow.verification_url), expiresIn: flow.expires_in });
      try {
        await run(['auth', 'login', '--device-code', flow.device_code, '--json'], {
          signal, timeout: Math.min(900, Number(flow.expires_in) || 600) * 1000, acceptFailure: true,
        });
      } catch (error) {
        if (signal.aborted) throw error;
      }
      return status(ownerId, signal);
    },
    async read({ ownerId, operation, params, signal }) {
      if (!ownerId) throw new Error('Feishu owner is unavailable');
      const auth = await status(ownerId, signal, false);
      if (!['linked', 'partial'].includes(auth.state)) throw new Error('Feishu CLI needs authorization');
      const definition = readOperations[operation];
      if (!definition || !Object.hasOwn(readOperations, operation)) throw new Error('Unsupported Feishu read operation');
      const [base, allowed] = definition;
      const args = [...base, '--as=user', '--format=json'];
      for (const [name, value] of Object.entries(params || {})) {
        if (!allowed.includes(name) || typeof value !== 'string' || !value.trim() || value.length > 1024 ||
            value.startsWith('@') || /[\x00-\x1f]/.test(value)) throw new Error('Invalid Feishu read input');
        args.push('--' + name + '=' + value);
      }
      return run(args, { signal, rawJSON: true });
    },
  };
}

const readOperations = Object.freeze({
  document: [['docs', '+fetch', '--doc-format=markdown'], ['doc']],
  search_documents: [['docs', '+search', '--page-size=15'], ['query', 'page-token']],
  drive_files: [['drive', 'files', 'list', '--page-size=20'], ['folder-token', 'page-token']],
  wiki_node: [['wiki', '+node-get'], ['node-token']],
  base_records: [['base', '+record-list', '--limit=20'], ['base-token', 'table-id', 'view-id', 'offset']],
  spreadsheet: [['sheets', '+cells-get', '--max-chars=50000'], ['spreadsheet-token', 'url', 'range', 'sheet-id', 'sheet-name']],
  presentation: [['slides', '+xml-get'], ['presentation', 'slide-id']],
  agenda: [['calendar', '+agenda'], ['calendar-id', 'start', 'end']],
  tasks: [['task', 'tasks', 'list', '--page-size=20'], ['page-token', 'completed']],
  meeting: [['vc', '+detail'], ['meeting-ids']],
  meeting_note: [['note', '+detail'], ['note-id']],
  minutes: [['minutes', '+detail', '--summary', '--chapter', '--todo'], ['minute-tokens']],
  chats: [['im', '+chat-list', '--types=p2p,group', '--page-size=20'], ['page-token']],
  chat_messages: [['im', '+chat-messages-list', '--page-size=20'], ['chat-id', 'start', 'end', 'page-token']],
  whiteboard: [['whiteboard', '+export', '--output-type=raw'], ['whiteboard-token']],
  markdown: [['markdown', '+fetch'], ['file-token']],
  mindnote: [['mindnotes', 'nodes', 'list'], ['mindnote-id']],
});
