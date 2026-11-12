import { randomBytes, randomUUID } from 'node:crypto';
import { mkdir, readFile, rename, unlink, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import QRCode from 'qrcode';
import { registerApp } from '@larksuite/channel';
import { feishuAuthURL, feishuCLI, feishuScopes } from './feishu-cli.mjs';
import { linkWechat } from './wechat-link.mjs';

const labels = { feishu: '飞书', wechat: '微信' };
const botScopes = ['im:message.p2p_msg:readonly', 'im:message:send_as_bot'];
const terminalSteps = new Set(['error', 'received', 'linked', 'done']);
const pairingLifetime = 10 * 60 * 1000;

function error(message, status = 400) {
  return Object.assign(new Error(message), { status, public: true });
}

export async function createChannelLinks({ config, channels, directory, workerCall, endpoint, signal, wechatLink = linkWechat }) {
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const providers = Object.keys(labels);
  const staticConnections = new Map(config.map(connection => [connection.id, connection]));
  const originals = Object.fromEntries(providers.map(kind => [kind, config.find(c => c.kind === kind && c.enabled)]));
  const records = {};
  const attempts = {};
  const receivers = new Map();
  const committing = new Set();
  const health = new Map();

  async function stopReceiver(connection) {
    const receiver = connection && receivers.get(connection.id);
    if (!receiver) return;
    receiver.controller.abort();
    await receiver.done;
  }
  function startReceiver(connection) {
    if (!connection.receive || receivers.has(connection.id)) return;
    const controller = new AbortController();
    const receiver = { controller };
    receivers.set(connection.id, receiver);
    receiver.done = channels.startOne(connection, AbortSignal.any([signal, controller.signal])).catch(() => {
      if (!controller.signal.aborted && !signal.aborted) health.set(connection.id, 'reconnect');
    }).finally(() => {
      if (receivers.get(connection.id) === receiver) receivers.delete(connection.id);
    });
  }

  function configure(connection) {
    const index = config.findIndex(c => c.id === connection.id);
    if (index < 0) config.push(connection);
    else config[index] = connection;
  }

  const resolve = record => record?.legacy ? staticConnections.get(record.connection.id) : record?.connection;
  const active = kind => records[kind]?.previous || (records[kind]?.staged ? undefined : records[kind]);
  const connectionOf = kind => resolve(active(kind)) || originals[kind];
  const receiverOf = record => resolve(record);

  async function save(kind, value) {
    const path = join(directory, kind + '.json');
    const temporary = path + '.' + randomUUID();
    try {
      await writeFile(temporary, JSON.stringify(value), { mode: 0o600, flag: 'wx', flush: true });
      await rename(temporary, path);
    } finally {
      await unlink(temporary).catch(failure => { if (failure.code !== 'ENOENT') throw failure; });
    }
    records[kind] = value;
  }

  for (const kind of providers) {
    try {
      const record = JSON.parse(await readFile(join(directory, kind + '.json'), 'utf8'));
      if (record.connection.kind !== kind) throw new Error('Invalid linked channel configuration');
      if (record.legacy && originals[kind]?.id !== record.connection.id) continue;
      records[kind] = record;
      for (const entry of record.retired || []) {
        const connection = resolve(entry);
        if (connection) configure({ ...connection, receiptOnly: true });
      }
      const current = connectionOf(kind);
      if (current) configure(current);
      if (record.pairing?.step === 'sending') {
        await save(kind, { ...record, pairing: { id: record.pairing.id, step: 'error', error: '测试回复结果待核实，请重新发起验证。' } });
      }
      if (record.retire) await finishRetirement(kind).catch(() => {});
    } catch (failure) {
      if (failure.code !== 'ENOENT') throw failure;
    }
  }

  function check(kind) {
    if (!Object.hasOwn(labels, kind)) throw error('未知渠道。');
  }
  async function status() {
    const feishu = connectionOf('feishu');
    const cli = feishu ? await feishuCLI(directory, feishu.appId).status(active('feishu')?.target?.actorId || feishu.ownerId, signal)
      : { state: 'unlinked', missing: feishuScopes };
    return {
      feishu: { bot: chatStatus('feishu'), cli },
      wechat: chatStatus('wechat'),
      active: ['feishu', 'wechat', 'cli'].map(kind => ({ kind, flow: pending(kind) }))
        .find(({ flow }) => flow && !['linked', 'done', 'error'].includes(flow.step)),
    };
  }

  function chatStatus(kind) {
    const record = records[kind];
    const connection = connectionOf(kind);
    if (record?.retire) return { state: 'pending' };
    if (record?.staged) {
      const pair = record.pairing;
      const retry = pair?.step === 'error' || pair?.step === 'test' &&
        (health.get(record.connection.id) === 'reconnect' || pair.expiresAt < Date.now());
      return retry ? { state: 'reconnect', canScan: true } : { state: 'pending' };
    }
    if (!connection) return { state: 'unlinked' };
    if (health.get(connection.id) === 'reconnect') return { state: 'reconnect', canScan: true };
    if (record?.verified && health.get(connection.id) === 'ready') return { state: 'linked' };
    return { state: 'pending' };
  }

  async function boundTarget(connection) {
    if (connection) {
      const targets = await workerCall('/notification-targets');
      const matches = targets.filter(target => target.connectionId === connection.id);
      if (matches.length === 1 && matches[0].target?.actorId && matches[0].target?.conversationId) return matches[0].target;
    }
  }

  async function stage(kind, credentials, id, legacy = false) {
    const old = records[kind];
    const current = active(kind);
    const established = connectionOf(kind);
    const sameAccount = established && (kind === 'feishu'
      ? established.appId === credentials.appId
      : established.ownerId === credentials.ownerId && (!established.accountId || established.accountId === credentials.accountId));
    const target = (legacy || sameAccount) && (current?.target || await boundTarget(established));
    if ((legacy || sameAccount) && (!target || credentials.ownerId && credentials.ownerId !== target.actorId ||
        kind === 'wechat' && target.conversationId !== target.actorId)) {
      throw error('该渠道的原本人会话无法核对，请检查当前绑定。', 409);
    }
    const connection = legacy ? { kind, id: credentials.id } : {
      kind, id: sameAccount ? established.id : 'linked-' + kind + '-' + randomUUID(), enabled: true, receive: true, ...credentials,
    };
    const previous = !legacy && established && (current
      ? { connection: current.connection, legacy: current.legacy }
      : { connection: { kind, id: established.id }, legacy: true });
    if (previous) previous.target = target || current?.target;
    const record = { connection, legacy, staged: !legacy, previous: previous || undefined,
      retired: old?.retired, target: target || undefined, verified: false, pairing: pairing(id) };
    await save(kind, record);
    if (old?.staged) await stopReceiver(receiverOf(old));
    if (sameAccount && !legacy) await stopReceiver(established);
    if (established && established.id !== connection.id) startReceiver(established);
    if (!legacy) {
      health.delete(connection.id);
      startReceiver(connection);
    }
  }

  function pairing(id) {
    return { id, step: 'test', text: '链接工作台 ' + randomBytes(6).toString('hex').toUpperCase(), expiresAt: Date.now() + pairingLifetime };
  }

  function snapshot(key) {
    const attempt = attempts[key];
    if (!attempt) return null;
    const current = attempt.view;
    const record = records[key];
    const { receipt: _receipt, expiresAt, ...visible } = current.step === 'pairing' ? record?.pairing || { step: 'error' } : current;
    if (visible.step === 'test') {
      if (expiresAt < Date.now()) return { id: attempt.id, step: 'error', error: '测试口令已过期，请重新验证。' };
      if (record?.staged && health.get(record.connection.id) === 'reconnect') {
        return { id: attempt.id, step: 'error', error: '渠道暂时无法连接，请重新扫码或检查应用凭证。' };
      }
    }
    const appId = visible.step === 'test' && key === 'feishu' && resolve(record)?.appId;
    return { id: attempt.id, ...visible,
      ...(appId ? { botURL: 'https://applink.feishu.cn/client/bot/open?appId=' + encodeURIComponent(appId) } : {}) };
  }

  function pending(key) {
    if (key !== 'cli') check(key);
    if (attempts[key]) return snapshot(key);
    const pair = records[key]?.pairing;
    if (!pair || terminalSteps.has(pair.step) && pair.step !== 'received' && !records[key].retire) return null;
    attempts[key] = { id: pair.id, abort: new AbortController(), view: { step: 'pairing' } };
    return snapshot(key);
  }

  async function begin({ kind, method, appId, appSecret }) {
    const key = kind === 'cli' ? 'cli' : (check(kind), kind);
    if (!['scan', 'existing', 'verify', 'authorize'].includes(method) ||
      (kind === 'cli') !== (method === 'authorize') || (method === 'existing' && kind !== 'feishu')) throw error('未知链接方式。');
    if (committing.has(key) || records[key]?.retire) throw error('正在保存链接，请完成当前验证。', 409);
    const previous = attempts[key];
    if (previous?.busy) return snapshot(key);
    if (previous?.view.step === 'pairing' && records[key]?.pairing?.step === 'sending') return snapshot(key);
    previous?.abort.abort();
    const abort = new AbortController();
    const flowSignal = AbortSignal.any([abort.signal, signal, AbortSignal.timeout(15 * 60 * 1000)]);
    const attempt = { id: randomUUID(), abort, busy: true, view: { step: 'starting' } };
    attempts[key] = attempt;
    const update = view => { attempt.view = view; };
    const showQR = async (url, step = 'scan') => {
      if (typeof url !== 'string' || url.length > 8192) throw Error('Invalid QR payload');
      const view = { step, ...(key !== 'wechat' ? { url } : {}) };
      update(view);
      const qr = await QRCode.toDataURL(url, { width: 320, margin: 4, errorCorrectionLevel: 'L' });
      if (!flowSignal.aborted && attempt.view === view) update({ ...view, qr });
    };
    void (async () => {
      if (kind === 'cli') {
        const connection = connectionOf('feishu');
        if (!connection) throw error('请先链接飞书机器人。');
        const ownerId = active('feishu')?.target?.actorId || connection.ownerId;
        if (!ownerId) throw error('请先完成飞书机器人的私聊验证。');
        const result = await feishuCLI(directory, connection.appId).authorize({
          appSecret: connection.appSecret, ownerId, signal: flowSignal,
          update: ({ url }) => {
            update({ step: 'authorize', url });
            void showQR(url, 'authorize').catch(() => {});
          },
        });
        update({ step: 'done', result });
        return;
      }
      if (method === 'verify') {
        const record = records[kind];
        if (record) await save(kind, { ...record, pairing: pairing(attempt.id) });
        else if (originals[kind]) await stage(kind, originals[kind], attempt.id, true);
        else throw error('请先链接渠道。');
      } else {
        const established = connectionOf(kind);
        if (established && health.get(established.id) !== 'reconnect' && !records[kind]?.staged) throw error('已有渠道，请先验证当前链接。', 409);
        if (kind === 'feishu') {
          const credential = method === 'existing' ? { client_id: appId, client_secret: appSecret } : await registerApp({
            createOnly: true, signal: flowSignal, source: 'personal-workbench',
            appPreset: { name: '浅蓝的一号位工作台' },
            addons: { preset: false, scopes: { tenant: botScopes, user: feishuScopes }, events: { items: { tenant: ['im.message.receive_v1'] } } },
            onQRCodeReady: ({ url }) => { void showQR(feishuAuthURL(url)).catch(() => {}); },
          });
          if (!/^cli_[a-zA-Z0-9]+$/.test(credential.client_id || '') ||
              typeof credential.client_secret !== 'string' || credential.client_secret.length < 8 || credential.client_secret.length > 256) {
            throw error('飞书应用凭证不完整。');
          }
          await stage(kind, { appId: credential.client_id, appSecret: credential.client_secret, ownerId: credential.user_info?.open_id }, attempt.id);
        } else {
        const credential = await wechatLink({
            signal: flowSignal,
            update: ({ step, qr }) => {
              if (qr) void showQR(qr, step).catch(() => {});
              else update({ ...attempt.view, step });
            },
            pin: () => new Promise((resolve, reject) => {
              attempt.pin = resolve;
              flowSignal.addEventListener('abort', () => reject(flowSignal.reason), { once: true });
            }),
          });
          await stage(kind, credential, attempt.id);
        }
      }
      update({ step: 'pairing' });
    })().catch(failure => {
      update({ step: 'error', error: failure.public ? failure.message : '暂时无法链接，请检查平台配置后重试。' });
    }).finally(() => { attempt.busy = false; });
    return snapshot(key);
  }

  async function receive(connection, message) {
    const kind = connection.kind;
    const record = records[kind];
    if (!record) return false;
    const candidate = resolve(record);
    const current = connectionOf(kind);
    if (connection !== candidate) return !config.includes(connection) || Boolean(connection.receiptOnly);
    const pair = record.pairing;
    const operational = record.legacy || record.verified || record.staged && current?.id === connection.id;
    if (typeof message.actorId !== 'string' || !message.actorId || !message.conversationId || typeof message.text !== 'string') return !operational;
    const trusted = record.target || { actorId: connection.ownerId };
    const trustedMessage = (!trusted.actorId || trusted.actorId === message.actorId) &&
      (!trusted.conversationId || trusted.conversationId === message.conversationId);
    const isTest = pair?.step === 'test' && message.text.trim() === pair.text && pair.expiresAt >= Date.now();
    if (!isTest) {
      if (operational) return !trustedMessage && !staticConnections.has(connection.id);
      if (!record.retire || !trustedMessage) return true;
      // A confirmed switch can commit in MySQL even if its response was lost.
      // Deliver only once the durable route has enabled this exact target.
      const bound = await boundTarget(connection);
      return bound?.actorId !== message.actorId || bound?.conversationId !== message.conversationId;
    }
    if (!trustedMessage || attempts[kind]?.busy) return true;

    // Claim before making the external call. A restart or ambiguous send never
    // replays the same test message.
    pair.step = 'sending';
    try {
      await save(kind, { ...record, pairing: pair });
      const target = { actorId: message.actorId, conversationId: message.conversationId,
        ...(kind === 'feishu' ? { replyTo: message.eventId } : {}) };
      const receipt = await channels.sendTest(connection, {
        idempotencyKey: 'link-' + pair.id + '-' + message.eventId, target, text: '链接验证成功。请回到工作台确认已收到回复。',
      });
      if (!['sent', 'accepted'].includes(receipt.state) || !receipt.receipt) throw error('测试回复未获平台确认，请重新验证。');
      await save(kind, { ...record, target: { actorId: target.actorId, conversationId: target.conversationId },
        pairing: { id: pair.id, step: 'received', receipt: receipt.receipt } });
    } catch (failure) {
      await save(kind, { ...record, pairing: { id: pair.id, step: 'error', error: failure.public ? failure.message : '测试回复结果待核实，请重新发起验证。' } });
    }
    return true;
  }

  async function finishRetirement(kind) {
    const record = records[kind];
    if (!record.retire) return;
    // Only the Worker enables the replacement route. Have its credentials in
    // memory before that commit so an accepted inbound can always be answered.
    configure(record.connection);
    try {
      await workerCall('/transports/retire-personal', { id: record.retire, replacement: record.connection.id });
    } catch (failure) {
      if (failure.status !== 403) throw failure;
      const index = config.indexOf(record.connection);
      if (index >= 0) config.splice(index, 1);
      await save(kind, { ...record, retire: undefined });
      throw error('原渠道仍有其他用途，无法替换。原连接仍可使用。', 409);
    }
    const old = record.previous;
    const former = resolve(old);
    await save(kind, { ...record, staged: undefined, previous: undefined, retire: undefined, verified: true,
      retired: [...(record.retired || []), old], pairing: { ...record.pairing, step: 'linked' } });
    if (former) {
      configure({ ...former, receiptOnly: true });
      await stopReceiver(former);
    }
  }

  async function confirm({ kind, id }) {
    check(kind);
    const record = records[kind];
    const pair = record?.pairing;
    if (!id || id !== pair?.id) throw error('这次链接验证已失效，请打开当前验证。', 409);
    if (!['received', 'linked'].includes(pair.step) || !pair.receipt || !record.target) throw error('请先完成私聊测试。', 409);
    if (committing.has(kind) || attempts[kind]?.busy) throw error('正在保存链接，请稍后重试。', 409);
    committing.add(kind);
    try {
      if (pair.step === 'linked') return { id, step: 'linked' };
      if (record.retire) {
        await finishRetirement(kind);
        return { id, step: 'linked' };
      }
      const connection = record.connection;
      const old = record.previous;
      const replaces = old && old.connection.id !== connection.id;
      if (record.staged && (!old || replaces)) {
        const { sessionId } = await workerCall('/personal/settings');
        await workerCall('/transports', { id: connection.id, kind, name: labels[kind], endpoint, tokenEnv: 'WORKBENCH_CONNECTOR_TOKEN', enabled: true, projects: [] });
        await workerCall('/message-routes', { id: connection.id, connectionId: connection.id,
          ...record.target, principal: { kind: 'personal', sessionId }, enabled: !replaces, autoReply: true });
      }
      if (replaces) {
        await save(kind, { ...record, retire: old.connection.id });
        await finishRetirement(kind);
      } else {
        await save(kind, { ...record, staged: undefined, previous: undefined, verified: true, pairing: { ...pair, step: 'linked' } });
        if (!record.legacy) configure(connection);
      }
    } finally { committing.delete(kind); }
    return { id, step: 'linked' };
  }

  function pin({ id, code }) {
    const attempt = attempts.wechat;
    if (attempt?.id !== id || attempt.view.step !== 'pin' || !attempt.pin || !/^\d{4,8}$/.test(code)) throw error('请输入微信显示的配对码。');
    const resolve = attempt.pin;
    attempt.pin = undefined;
    attempt.view = { ...attempt.view, step: 'confirm' };
    resolve(code);
    return snapshot('wechat');
  }

  return {
    status, begin, pin, confirm, receive,
    snapshot: pending,
    async read({ operation, params }) {
      const connection = connectionOf('feishu');
      const ownerId = active('feishu')?.target?.actorId || connection?.ownerId;
      if (!connection || !ownerId) throw error('请先链接飞书并完成 CLI 授权。', 409);
      try { return await feishuCLI(directory, connection.appId).read({ ownerId, operation, params, signal }); }
      catch { throw error('飞书读取失败，请检查授权、数据权限或输入。', 409); }
    },
    health(connection, state) { health.set(connection.id, state); },
    manages(connection) {
      return connectionOf(connection.kind)?.id === connection.id ||
        (records[connection.kind]?.retired?.some(entry => entry.connection.id === connection.id) ?? false);
    },
    start() {
      for (const kind of providers) {
        const connection = connectionOf(kind);
        const candidate = records[kind]?.staged && resolve(records[kind]);
        if (connection && connection.id !== candidate?.id) startReceiver(connection);
        if (candidate) startReceiver(candidate);
      }
    },
  };
}
