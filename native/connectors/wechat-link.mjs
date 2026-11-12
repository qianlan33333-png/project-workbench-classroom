// The self-binding QR protocol follows Tencent/openclaw-weixin (MIT).
import { randomBytes } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';

export const wechatOrigin = 'https://ilinkai.weixin.qq.com';
const clientVersion = String(3 << 16);

export function wechatAPIOrigin(value = wechatOrigin) {
  const url = new URL(value);
  if (url.protocol !== 'https:' || !url.hostname.endsWith('.weixin.qq.com') ||
      url.username || url.password || url.port || url.href !== url.origin + '/') {
    throw new Error('Unexpected WeChat API origin');
  }
  return url.origin;
}

export function wechatHeaders() {
  return {
    'content-type': 'application/json',
    AuthorizationType: 'ilink_bot_token',
    'X-WECHAT-UIN': Buffer.from(String(randomBytes(4).readUInt32BE())).toString('base64'),
    'iLink-App-Id': 'bot',
    'iLink-App-ClientVersion': clientVersion,
  };
}

export async function linkWechat({ signal, update, pin, fetcher = fetch }) {
  async function request(origin, endpoint, options, timeout) {
    const response = await fetcher(origin + '/ilink/bot/' + endpoint, {
      ...options, redirect: 'error', headers: wechatHeaders(),
      signal: AbortSignal.any([signal, AbortSignal.timeout(timeout)]),
    });
    if (!response.ok) throw new Error('WeChat QR request failed');
    return response.json();
  }

  const qr = await request(wechatOrigin, 'get_bot_qrcode?bot_type=3', { method: 'POST', body: '{}' }, 15000);
  if (!qr.qrcode || typeof qr.qrcode_img_content !== 'string') throw new Error('WeChat QR is missing');
  update({ step: 'scan', qr: qr.qrcode_img_content });
  let origin = wechatOrigin;
  let verifyCode;
  const expires = Date.now() + 5 * 60 * 1000;
  while (!signal.aborted && Date.now() < expires) {
    const query = new URLSearchParams({ qrcode: qr.qrcode });
    if (verifyCode) query.set('verify_code', verifyCode);
    verifyCode = undefined;
    let result;
    try { result = await request(origin, 'get_qrcode_status?' + query, {}, 35000); }
    catch {
      await delay(1500, undefined, { signal });
      continue;
    }
    if (result.status === 'confirmed') {
      if (!result.bot_token || !result.ilink_user_id || !result.ilink_bot_id) {
        throw new Error('WeChat returned incomplete credentials');
      }
      return {
        token: result.bot_token, ownerId: result.ilink_user_id, accountId: result.ilink_bot_id,
        origin: wechatAPIOrigin(result.baseurl || origin),
      };
    }
    if (result.status === 'expired') break;
    if (result.status === 'verify_code_blocked' || result.status === 'binded_redirect') {
      throw Object.assign(new Error('请在微信中检查当前绑定后重试。'), { public: true });
    }
    if (result.status === 'scaned_but_redirect' && result.redirect_host) {
      origin = wechatAPIOrigin('https://' + result.redirect_host);
    }
    if (result.status === 'scaned' || result.status === 'scaned_but_redirect') {
      update({ step: 'confirm' });
    } else if (['need_verifycode', 'confirmed_but_need_verifycode'].includes(result.status)) {
      update({ step: 'pin' });
      verifyCode = await pin();
    } else {
      await delay(1000, undefined, { signal });
    }
  }
  throw Object.assign(new Error('二维码已过期，请重新获取。'), { public: true });
}
