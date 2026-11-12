import { randomUUID } from 'node:crypto';
import { link, mkdir, readFile, rename, unlink, writeFile } from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import { importSPKI, jwtVerify } from 'jose';

export const activationAudience = 'qianlan-workbench';
export const workbenchTitle = '浅蓝的一号位工作台';
export const activationCodePattern = /^[A-Za-z0-9_-]{32}$/;
export const installationPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

async function existing(path) {
  try { return await readFile(path, 'utf8'); }
  catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
}

async function persist(path, value, replace) {
  const temporary = path + '.' + randomUUID();
  try {
    await writeFile(temporary, value, { mode: 0o600, flag: 'wx', flush: true });
    if (replace) await rename(temporary, path);
    else await link(temporary, path);
  } finally {
    await unlink(temporary).catch(error => { if (error.code !== 'ENOENT') throw error; });
  }
}

function issuerOrigin(value) {
  const url = new URL(value);
  if (url.protocol !== 'https:' || url.href !== url.origin + '/') {
    throw new Error('Activation issuer must be a bare HTTPS origin');
  }
  return url.origin;
}

export async function createActivation({ directory, issuer, publicKey, fetcher = fetch }) {
  if (!isAbsolute(directory)) throw new Error('Activation directory must be absolute');
  issuer = issuerOrigin(issuer);
  const key = await importSPKI(publicKey, 'EdDSA');
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const identityPath = join(directory, 'installation');
  let installation = await existing(identityPath);
  if (installation === undefined) {
    try { await persist(identityPath, randomUUID(), false); }
    catch (error) { if (error.code !== 'EEXIST') throw error; }
    installation = await existing(identityPath);
  }
  if (!installationPattern.test(installation)) throw new Error('Invalid local installation identity');

  const grantPath = join(directory, 'grant.jwt');
  async function verify(grant) {
    await jwtVerify(grant, key, {
      algorithms: ['EdDSA'],
      typ: 'workbench-activation+jwt',
      audience: activationAudience,
      issuer,
      subject: installation,
      requiredClaims: ['iat'],
    });
  }

  let active = false;
  const saved = await existing(grantPath);
  if (saved !== undefined) {
    try { await verify(saved); active = true; }
    catch { /* A broken or foreign grant requires activation. */ }
  }
  let redeeming;
  return {
    get active() { return active; },
    activate(code) {
      if (active) return Promise.resolve();
      if (!activationCodePattern.test(code)) throw activationError('请输入完整的激活码。');
      if (redeeming) throw activationError('正在验证，请稍后再试。', 409);
      redeeming = redeem(code).finally(() => { redeeming = undefined; });
      return redeeming;
    },
  };

  async function redeem(code) {
    let grant;
    try {
      const response = await fetcher(issuer + '/workbench-activation/redeem', {
        method: 'POST',
        redirect: 'error',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ code, installation }),
        signal: AbortSignal.timeout(10000),
      });
      if ([404, 409, 410].includes(response.status)) {
        throw activationError({
          404: '激活码无效，请检查后重试。',
          409: '这个激活码已用于另一台工作台。',
          410: '激活码已过期，请获取新码。',
        }[response.status], response.status);
      }
      if (!response.ok) throw new Error('Issuer rejected activation');
      const responseBody = await response.text();
      if (responseBody.length > 8192) throw new Error('Unexpected response size');
      ({ grant } = JSON.parse(responseBody));
      await verify(grant);
      await persist(grantPath, grant, true);
    } catch (error) {
      if (error.activationStatus) throw error;
      throw activationError('暂时无法验证激活码，请稍后重试。', 503);
    }
    active = true;
  }
}

export const activationError = (message, status = 400) =>
  Object.assign(new Error(message), { activationStatus: status });

export function sameOrigin(req) {
  try { return new URL(req.headers.origin).host === req.headers.host; }
  catch { return false; }
}

export async function setupBody(req, limit = 2048) {
  let bytes = 0;
  const chunks = [];
  for await (const chunk of req) {
    bytes += chunk.length;
    if (bytes > limit) throw activationError('请求过大。', 413);
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString('utf8');
}

export const escapeHtml = value => String(value).replace(/[&<>"']/g, character => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
})[character]);

export function setupPage(res, status, title, body, script = '') {
  const nonce = randomUUID();
  res.writeHead(status, {
    'content-type': 'text/html; charset=utf-8',
    'cache-control': 'no-store',
    'referrer-policy': 'same-origin',
    'x-content-type-options': 'nosniff',
    'content-security-policy': `default-src 'none'; img-src 'self'; style-src 'nonce-${nonce}'; script-src 'nonce-${nonce}'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'`,
  });
  res.end(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>${title} · ${workbenchTitle}</title>
<style nonce="${nonce}">
*{box-sizing:border-box}body{margin:0;background:#fff;color:#101828;font:16px/1.55 system-ui,sans-serif}
main{width:min(100% - 40px,360px);margin:clamp(44px,17vh,160px) auto 32px}
.brand{display:flex;align-items:center;justify-content:center;gap:12px;font-size:20px;font-weight:650}
h1{text-align:center;font-size:28px;margin:48px 0 34px}form{display:grid;gap:12px}
input,button{font:inherit;border-radius:8px;min-height:44px;padding:10px 12px}input{width:100%;border:1px solid #d0d5dd}
button{cursor:pointer;border:0;background:#0052e8;color:#fff;font-weight:600;margin-top:10px}button:focus-visible,input:focus-visible{outline:2px solid #1360ff;outline-offset:2px}
[role=alert]{color:#a42020;margin:0}output{display:block;overflow-wrap:anywhere;margin-top:20px}
</style>
<main><div class="brand"><img src="/workbench-brand.png" alt="" width="36" height="36">${workbenchTitle}</div>
<h1>${title}</h1>${body}</main>${script && `<script nonce="${nonce}">${script}</script>`}</html>`);
}

function redirect(res, location) {
  res.writeHead(303, { location, 'cache-control': 'no-store' });
  res.end();
}

export function installActivation(ctx, activation) {
  ctx.effect(() => {
    const connection = ctx.connection;
    const original = connection.authorizeIndex;
    const originalAdmitConnection = connection.admitConnection;
    const gate = function (req, res) {
      if (!original.call(this, req, res)) return false;
      if (activation.active) return true;
      redirect(res, '/activation');
      return false;
    };
    const admitConnection = function (req) {
      return activation.active ? originalAdmitConnection.call(this, req) : { rejection: 403 };
    };
    connection.authorizeIndex = gate;
    connection.admitConnection = admitConnection;
    return () => {
      if (connection.authorizeIndex === gate) connection.authorizeIndex = original;
      if (connection.admitConnection === admitConnection) connection.admitConnection = originalAdmitConnection;
    };
  }, 'workbench: activation entry');
  ctx.on('connection/request', (_req, res, next) => {
    if (activation.active) return next();
    res.writeHead(403, { 'cache-control': 'no-store' }).end('Activation required');
  });
  ctx.effect(() => ctx.webServer.register({
    kind: 'exact', path: '/activation',
    handler: async (req, res) => {
      const admission = ctx.connection.admit(req);
      if ('rejection' in admission || admission.peer !== ctx.connection.browserPeer) {
        res.writeHead('rejection' in admission ? admission.rejection : 403).end();
        return;
      }
      if (activation.active) return redirect(res, '/');
      let error;
      if (req.method === 'POST') {
        if (!sameOrigin(req) || req.headers['content-type']?.split(';')[0] !== 'application/x-www-form-urlencoded') {
          res.writeHead(403).end();
          return;
        }
        try {
          const code = new URLSearchParams(await setupBody(req)).get('code')?.trim();
          await activation.activate(code);
          return redirect(res, '/');
        } catch (failure) { error = failure; }
      } else if (req.method !== 'GET') {
        res.writeHead(405, { allow: 'GET, POST' }).end();
        return;
      }
      setupPage(res, error?.activationStatus || (error ? 503 : 200), '激活工作台',
        `<form method="post" action="/activation"><label for="code">激活码</label>
<input id="code" name="code" autocomplete="off" spellcheck="false" autocapitalize="none" maxlength="64" required placeholder="请输入激活码">
${error ? `<p role="alert">${escapeHtml(error.activationStatus ? error.message : '暂时无法验证激活码，请稍后重试。')}</p>` : ''}
<button>激活工作台</button></form>`);
    },
  }), 'workbench: activation page');
}
