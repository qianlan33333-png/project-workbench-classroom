import { readFile } from 'node:fs/promises';
import { importPKCS8, SignJWT } from 'jose';
import { activationAudience, activationCodePattern, installationPattern, sameOrigin, setupBody, setupPage } from './activation.js';

export async function installActivationIssuer(ctx, { origin, signingKeyFile, call }) {
  const url = new URL(origin);
  if (url.protocol !== 'https:' || url.href !== url.origin + '/') throw new Error('Activation issuer requires a bare HTTPS origin');
  const key = await importPKCS8(await readFile(signingKeyFile, 'utf8'), 'EdDSA');

  ctx.effect(() => ctx.webServer.register({
    kind: 'exact', path: '/workbench-activation/redeem',
    handler: async (req, res) => {
      const reply = (status, body) => res.writeHead(status, {
        'content-type': 'application/json; charset=utf-8',
        'cache-control': 'no-store',
        'x-content-type-options': 'nosniff',
      }).end(JSON.stringify(body));
      if (req.method !== 'POST') return reply(405, { error: 'method_not_allowed' });
      if (req.headers.host !== url.host || req.headers['content-type']?.split(';')[0] !== 'application/json') {
        return reply(400, { error: 'invalid_request' });
      }
      try {
        const { code, installation } = JSON.parse(await setupBody(req));
        if (typeof code !== 'string' || typeof installation !== 'string' ||
          !activationCodePattern.test(code) || !installationPattern.test(installation)) {
          return reply(400, { error: 'invalid_request' });
        }
        const { issuedAt } = await call('/activation/redeem', undefined, { code, installation });
        const grant = await new SignJWT({})
          .setProtectedHeader({ alg: 'EdDSA', typ: 'workbench-activation+jwt' })
          .setIssuer(url.origin).setAudience(activationAudience)
          .setSubject(installation).setIssuedAt(issuedAt).sign(key);
        return reply(200, { grant });
      } catch (error) {
        const status = [404, 409, 410].includes(error.status) ? error.status
          : error instanceof SyntaxError || error instanceof TypeError || error.activationStatus ? 400 : 503;
        return reply(status, { error: { 400: 'invalid_request', 404: 'invalid_code', 409: 'already_claimed', 410: 'expired', 503: 'unavailable' }[status] });
      }
    },
  }), 'workbench: activation issuer');

  ctx.effect(() => ctx.webServer.register({
    kind: 'exact', path: '/activation-codes',
    handler: async (req, res) => {
      const admission = ctx.connection.admit(req);
      if ('rejection' in admission || admission.peer !== ctx.connection.browserPeer) {
        res.writeHead('rejection' in admission ? admission.rejection : 403).end();
        return;
      }
      if (!['GET', 'POST'].includes(req.method)) {
        res.writeHead(405, { allow: 'GET, POST' }).end();
        return;
      }
      let result = '';
      let status = 200;
      if (req.method === 'POST') {
        if (!sameOrigin(req)) { res.writeHead(403).end(); return; }
        try {
          const { code } = await call('/activation/codes', undefined, {});
          if (!activationCodePattern.test(code)) throw new Error('Invalid code response');
          result = `<output><label for="new-code">激活码 · 7 天内领取</label><input id="new-code" value="${code}" readonly><button type="button" id="copy">复制激活码</button></output>`;
        } catch {
          status = 503;
          result = '<p role="alert">暂时无法生成激活码，请稍后重试。</p>';
        }
      }
      setupPage(res, status, '生成激活码', `<form method="post" action="/activation-codes"><button>生成激活码</button></form>${result}`,
        `document.querySelector('#copy')?.addEventListener('click',async event=>{const field=document.querySelector('#new-code');try{await navigator.clipboard.writeText(field.value);event.target.textContent='已复制'}catch{field.select()}})`);
    },
  }), 'workbench: owner activation codes');
}
