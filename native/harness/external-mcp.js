import * as mcpClient from '@deepseek-ai/dsh-mcp-client';
import { toolNamespace } from './tool-namespace.js';

const startupTimeoutMs = 8000;
const id = /^[A-Za-z0-9_-]{1,96}$/;
const tokenRef = /^WORKBENCH_MCP_[A-Z0-9_]+$/;

export function externalMcpConfig(value) {
  let entries;
  try { entries = typeof value === 'string' ? JSON.parse(value) : value; }
  catch { return { servers: [], error: '外部 MCP 配置不是有效 JSON，请联系部署者。' }; }
  if (entries === undefined) return { servers: [] };
  if (!Array.isArray(entries)) return { servers: [], error: '外部 MCP 配置必须是连接列表，请联系部署者。' };
  const names = new Map();
  const errors = [];
  for (const [index, entry] of entries.entries()) {
    let name;
    try { name = toolNamespace(entry?.name); }
    catch { errors.push(`第 ${index + 1} 个外部 MCP 的名称无效`); continue; }
    if (name === 'workbench') {
      errors.push('外部 MCP 使用了内置连接名称 workbench');
      continue;
    }
    if (names.has(name)) {
      names.set(name, { name, error: '连接名称重复' });
      continue;
    }
    let url;
    try { url = new URL(entry.url); }
    catch { names.set(name, { name, error: '服务地址无效' }); continue; }
    if (url.username || url.password || url.search || url.hash ||
        (url.protocol !== 'https:' && (url.protocol !== 'http:' || url.hostname !== '127.0.0.1'))) {
      names.set(name, { name, error: '服务地址无效' });
      continue;
    }
    if (typeof entry.tokenEnv !== 'string' || !tokenRef.test(entry.tokenEnv) ||
        (entry.personal !== undefined && typeof entry.personal !== 'boolean') ||
        (entry.projects !== undefined && (!Array.isArray(entry.projects) || !entry.projects.every(project => typeof project === 'string' && id.test(project)))) ||
        (entry.customerTools !== undefined && (!Array.isArray(entry.customerTools) || !entry.customerTools.every(tool => typeof tool === 'string' && tool.length > 0))) ||
        !Array.isArray(entry.sources) || !entry.sources.length || entry.sources.length > 32 || !entry.sources.every(ref =>
          ref && typeof ref.resourceId === 'string' && id.test(ref.resourceId) && typeof ref.section === 'string' && id.test(ref.section) &&
          (ref.version === undefined || ref.version === '' || (typeof ref.version === 'string' && id.test(ref.version))))) {
      names.set(name, { name, error: '请检查凭证引用、会话范围和来源配置' });
      continue;
    }
    names.set(name, { ...entry, url: url.href, endpoint: url.origin });
  }
  return {
    servers: [...names.values()],
    ...(errors.length ? { error: errors.join('、') + '，请联系部署者。' } : {}),
  };
}

export function externalMcpFailure(error) {
  for (let cause = error, depth = 0; cause && depth < 6; cause = cause.cause, depth++) {
    if ([401, 403, 'CLIENT_HTTP_AUTHENTICATION', 'CLIENT_HTTP_FORBIDDEN'].some(code => [cause.status, cause.code].includes(code)))
      return '凭证被拒绝，请联系部署者检查令牌和访问权限。';
    if (cause.name === 'TimeoutError' || cause.code === 'REQUEST_TIMEOUT')
      return '连接超时，请检查服务后重新连接。';
  }
  return '无法连接或读取工具目录，请联系部署者检查服务后重新连接。';
}

// Cordis returns a disposable fiber immediately. Keeping it before awaiting
// startup lets this scope cancel a slow discovery or a disposed Agent.
export function attachExternalMcp(ctx, server, headers, hasRegisteredTools) {
  let fiber, pending, closed = false;
  let state = { status: 'unchecked' };
  const dispose = async () => {
    const current = fiber;
    fiber = undefined;
    await current?.dispose();
  };
  const reconnect = () => {
    if (closed || pending) return;
    const credential = process.env[server.tokenEnv];
    state = { status: 'connecting' };
    pending = (async () => {
      let timer;
      try {
        await dispose();
        if (closed) return;
        if (!credential) {
          state = { status: 'failed', message: '缺少凭证，请联系部署者配置并重启工作台服务。' };
          return;
        }
        fiber = ctx.plugin(mcpClient, {
          serverName: server.name,
          transport: 'streamable-http',
          url: server.url,
          headers: { ...headers, authorization: 'Bearer ' + credential },
          toolCallTimeoutMs: 60000,
          failOnStartupError: true,
        });
        await Promise.race([
          fiber.await(),
          new Promise((_, reject) => {
            timer = setTimeout(() => reject(new DOMException('MCP startup timeout', 'TimeoutError')), startupTimeoutMs);
            timer.unref();
          }),
        ]);
        if (!closed) state = { status: 'loaded' };
      } catch (error) {
        try { await dispose(); }
        finally { if (!closed) state = { status: 'failed', message: externalMcpFailure(error) }; }
      } finally {
        clearTimeout(timer);
        pending = undefined;
      }
    })();
    // A teardown failure still belongs to this optional connection.
    void pending.catch(() => {});
  };
  reconnect();
  return {
    reconnect,
    status() {
      if ((state.status === 'loaded' || state.status === 'call-failed') && !hasRegisteredTools())
        return { status: 'no-tools', message: 'SuperAgent 当前没有此连接的工具。可测试目录，或在服务恢复后重新连接。' };
      return state;
    },
    observe() {
      const current = fiber;
      return result => {
        if (closed || !current || fiber !== current || state.status === 'connecting' || !result) return;
        state = result.isError
          ? { status: 'call-failed', message: '最近一次调用失败。若服务已恢复，可重新连接；如仍失败，请联系部署者。' }
          : { status: 'loaded' };
      };
    },
    async dispose() {
      closed = true;
      await Promise.allSettled([dispose(), pending]);
    },
  };
}
