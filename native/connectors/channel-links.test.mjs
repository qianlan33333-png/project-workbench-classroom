import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { createChannelLinks } from './channel-links.mjs';
import { createChannels } from './channels.mjs';

test('private-chat pairing sends one test reply and only enables the owner route after confirmation', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'workbench-channel-link-'));
  const abort = new AbortController();
  const writes = [];
  const sends = [];
  const started = [];
  const config = [];
  let retirementDenied = false;
  let loseRetirementResponse = false;
  let boundReplacement;
  const channels = {
    startOne(connection, signal) {
      started.push(connection);
      return new Promise(resolve => signal.addEventListener('abort', resolve, { once: true }));
    },
    async sendTest(connection, input) {
      sends.push({ connection, input });
      return { state: 'sent', receipt: 'fixture-platform-receipt' };
    },
  };
  const workerCall = async (path, data) => {
    if (path === '/personal/settings') return { sessionId: 'fixture-superagent' };
    if (path === '/notification-targets') return boundReplacement ? [boundReplacement] : [];
    writes.push({ path, data });
    if (path === '/transports/retire-personal' && retirementDenied) throw Object.assign(Error('project uses this channel'), { status: 403 });
    if (path === '/transports/retire-personal' && loseRetirementResponse) {
      boundReplacement = { connectionId: data.replacement, target: { actorId: 'ou_other', conversationId: 'oc_other' } };
      throw Object.assign(Error('connection lost after Worker commit'), { status: 503 });
    }
  };
  const setup = () => createChannelLinks({
    config, channels, directory, workerCall, signal: abort.signal, endpoint: 'http://127.0.0.1:7383/mcp',
  });
  async function waitForPair(links) {
    for (let i = 0; i < 100 && links.snapshot('feishu').step === 'starting'; i++) await delay(5);
    const flow = links.snapshot('feishu');
    assert.equal(flow.step, 'test');
    return flow;
  }
  try {
    const links = await setup();
    await links.begin({ kind: 'feishu', method: 'existing', appId: 'cli_fixture', appSecret: 'fixture-app-secret' });
    const pairing = await waitForPair(links);
    assert.equal(config.length, 0);
    const connection = started.at(-1);
    links.health(connection, 'ready');
    const input = { actorId: 'ou_owner', conversationId: 'oc_owner', eventId: 'om_test', text: pairing.text.split(' ')[1] };
    assert.equal(await links.receive(connection, { ...input, text: 'normal message before pairing' }), true);
    assert.equal(await links.receive({ kind: 'wechat' }, input), true, 'a code pasted in the other channel is consumed');
    assert.deepEqual(writes, []);
    await Promise.all([links.receive(connection, input), links.receive(connection, input)]);
    assert.equal(sends.length, 1);
    assert.equal(links.snapshot('feishu').step, 'received');
    assert.equal(links.snapshot('feishu').receipt, undefined);
    assert.equal(links.snapshot('feishu').text, undefined);
    assert.deepEqual(writes, []);
    await links.confirm({ kind: 'feishu', id: pairing.id });
    assert.equal(links.snapshot('feishu').step, 'linked');
    assert.deepEqual(config, [connection]);
    assert.equal(writes.find(w => w.path === '/message-routes')?.data.principal.sessionId, 'fixture-superagent');
    assert.equal(await links.receive(connection, { ...input, actorId: 'ou_other', text: 'unbound chat' }), true);
    assert.equal(await links.receive(connection, { ...input, text: 'normal owner message' }), false);
    assert.equal(await links.receive(connection, input), true, 'consumed code stays out of the session');
    const unknownCode = input.text === '123456ABCDEF' ? '123456ABCDEE' : '123456ABCDEF';
    assert.equal(await links.receive(connection, { ...input, text: unknownCode }), false);
    const secondary = { kind: 'feishu', id: 'secondary', enabled: true, receive: true };
    config.push(secondary);
    assert.equal(await links.receive(secondary, { ...input, text: 'independent channel' }), false);
    assert.equal(links.manages(secondary), false);

    links.health(connection, 'reconnect');
    await links.begin({ kind: 'feishu', method: 'existing', appId: 'cli_fixture', appSecret: 'rotated-app-secret' });
    const rotation = await waitForPair(links);
    const rotated = started.at(-1);
    assert.notEqual(rotated, connection);
    assert.equal(rotated.id, connection.id, 'worker dedupe, cursor, notification and delivery all use this ID');
    assert.deepEqual(config, [connection, secondary], 'production send still uses the established credentials before confirmation');
    const writesBeforeRotation = writes.length;
    assert.equal(await links.receive(rotated, { ...input, text: 'normal owner message' }), false);
    assert.equal(await links.receive(rotated, input), true, 'superseded code stays out of the session');
    assert.equal(await links.receive(rotated, { ...input, actorId: 'ou_other', text: rotation.text }), true);
    assert.equal(sends.length, 1);
    await links.receive(rotated, { ...input, eventId: 'om_rotated', text: rotation.text });
    assert.equal(sends.at(-1).connection, rotated);
    await assert.rejects(links.confirm({ kind: 'feishu', id: pairing.id }), { status: 409 });
    assert.deepEqual(config, [connection, secondary]);
    await links.confirm({ kind: 'feishu', id: rotation.id });
    assert.deepEqual(config, [rotated, secondary]);
    assert.equal(writes.length, writesBeforeRotation, 'existing worker transport, route and destination require no changes');

    links.health(rotated, 'reconnect');
    await links.begin({ kind: 'feishu', method: 'existing', appId: 'cli_other', appSecret: 'other-app-secret' });
    const switched = await waitForPair(links);
    const other = started.at(-1);
    assert.notEqual(other.id, rotated.id);
    assert.equal(await links.receive(rotated, { ...input, text: 'normal owner message' }), false);
    assert.equal(await links.receive(other, { ...input, text: 'unconfirmed app message' }), true);
    await links.receive(other, { actorId: 'ou_other', conversationId: 'oc_other', eventId: 'om_other', text: switched.text });
    await assert.rejects(links.confirm({ kind: 'feishu', id: rotation.id }), { status: 409 });
    assert.deepEqual(config, [rotated, secondary]);
    assert.equal(writes.length, writesBeforeRotation);

    retirementDenied = true;
    await assert.rejects(links.confirm({ kind: 'feishu', id: switched.id }), { status: 409 });
    assert.deepEqual(config, [rotated, secondary], 'Worker rejection leaves the old channel operational');
    assert.equal(writes.filter(w => w.path === '/message-routes').at(-1).data.enabled, false);
    assert.equal(await links.receive(rotated, { ...input, text: 'still operating after Worker rejection' }), false);
    retirementDenied = false;
    loseRetirementResponse = true;
    await assert.rejects(links.confirm({ kind: 'feishu', id: switched.id }), { status: 503 });
    const otherMessage = { actorId: 'ou_other', conversationId: 'oc_other', eventId: 'om_new', text: 'new owner message' };
    assert.equal(await links.receive(other, otherMessage), false, 'the exact replacement route is active in Worker');
    assert.equal(await links.receive(other, { ...otherMessage, actorId: 'ou_unbound' }), true);
    const sender = createChannels(config, workerCall, undefined, credentials => {
      assert.equal(credentials, other);
      return { im: { message: { create: async () => ({ code: 0, data: { message_id: 'fixture-reply' } }) } } };
    });
    const reply = await sender.send({ connectionId: other.id, idempotencyKey: 'reply-after-commit',
      target: boundReplacement.target, text: 'reply after Worker commit' });
    assert.equal(reply.state, 'sent');
    assert.match(reply.receipt, /fixture-reply/);
    loseRetirementResponse = false;

    const restartedConfig = [];
    const restarted = await createChannelLinks({ config: restartedConfig, channels, directory, workerCall,
      signal: abort.signal, endpoint: 'http://127.0.0.1:7383/mcp' });
    assert.equal(restarted.snapshot('feishu'), null, 'restart finishes a previously confirmed switch');
    assert.deepEqual(await restarted.confirm({ kind: 'feishu', id: switched.id }), { id: switched.id, step: 'linked' });
    assert.deepEqual(await restarted.confirm({ kind: 'feishu', id: switched.id }), { id: switched.id, step: 'linked' });
    assert.equal(restartedConfig.find(c => c.id === rotated.id)?.receiptOnly, true);
    assert.equal(restartedConfig.find(c => c.id === other.id)?.receiptOnly, undefined);
    assert.deepEqual(writes.at(-1), { path: '/transports/retire-personal', data: { id: rotated.id, replacement: other.id } });
  } finally {
    abort.abort();
    await rm(directory, { recursive: true });
  }
});

test('pairing a legacy Feishu connection keeps its existing project private messages', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'workbench-feishu-shared-'));
  const abort = new AbortController();
  const legacy = { kind: 'feishu', id: 'legacy-feishu', appId: 'cli_fixture', appSecret: 'fixture-secret', enabled: true, receive: true };
  let replies = 0;
  const links = await createChannelLinks({ config: [legacy], directory, signal: abort.signal, endpoint: 'http://127.0.0.1:7383/mcp',
    workerCall: async path => {
      assert.equal(path, '/notification-targets');
      return [{ connectionId: legacy.id, target: { actorId: 'ou_owner', conversationId: 'oc_owner' } }];
    },
    channels: { async sendTest() { replies++; return { state: 'sent', receipt: 'private-test-proof' }; } },
  });
  try {
    const projectMessage = { actorId: 'ou_project', conversationId: 'oc_project', eventId: 'om_project', text: 'existing project question' };
    assert.equal(await links.receive(legacy, projectMessage), false);
    assert.equal(await links.receive(legacy, { ...projectMessage, text: '链接工作台 123456ABCDEF' }), false);
    await links.begin({ kind: 'feishu', method: 'verify' });
    for (let i = 0; i < 100 && links.snapshot('feishu').step === 'starting'; i++) await delay(5);
    const pair = links.snapshot('feishu');
    assert.equal(pair.step, 'test');
    assert.equal(await links.receive(legacy, projectMessage), false);
    const unknownCode = pair.text.endsWith('123456ABCDEF') ? '123456ABCDEE' : '123456ABCDEF';
    const unrelatedCommand = '链接工作台 ' + unknownCode;
    assert.equal(await links.receive(legacy, { ...projectMessage, text: unrelatedCommand }), false);
    assert.equal(await links.receive(legacy, { ...projectMessage, actorId: 'ou_owner', conversationId: 'oc_owner',
      text: unrelatedCommand }), true);
    assert.equal(await links.receive(legacy, { ...projectMessage, text: pair.text }), true);
    assert.equal(replies, 0);
    await links.receive(legacy, { actorId: 'ou_owner', conversationId: 'oc_owner', eventId: 'om_owner', text: pair.text });
    await links.confirm({ kind: 'feishu', id: pair.id });
    assert.equal(replies, 1);
    assert.equal(await links.receive(legacy, projectMessage), false);
    const path = join(directory, 'feishu.json');
    const record = JSON.parse(await readFile(path, 'utf8'));
    record.pairing = { id: pair.id, text: pair.text, step: 'test', expiresAt: Date.now() - 1 };
    await writeFile(path, JSON.stringify(record));
    const restarted = await createChannelLinks({ config: [legacy], directory, signal: abort.signal,
      channels: { async sendTest() { assert.fail('expired pairing must never send'); } } });
    assert.equal(restarted.snapshot('feishu').step, 'error');
    assert.equal(await restarted.receive(legacy, { actorId: 'ou_owner', conversationId: 'oc_owner', eventId: 'om_expired',
      text: pair.text.split(' ')[1] }), true);
  } finally {
    abort.abort();
    await rm(directory, { recursive: true });
  }
});

test('legacy WeChat rotation proves the same private user and keeps the connection ID', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'workbench-wechat-rotation-'));
  const abort = new AbortController();
  const original = { kind: 'wechat', id: 'legacy-wechat', ownerId: 'owner', token: 'old-token', enabled: true, receive: true };
  const config = [original];
  const started = [];
  const links = await createChannelLinks({ config, directory, signal: abort.signal, endpoint: 'http://127.0.0.1:7383/mcp',
    wechatLink: async () => ({ ownerId: 'owner', accountId: 'platform-bot', token: 'new-token' }),
    workerCall: async path => {
      assert.equal(path, '/notification-targets', 'no Worker route or transport rewrite during rotation');
      return [{ connectionId: original.id, target: { actorId: 'owner', conversationId: 'owner' } }];
    },
    channels: {
      startOne(connection, signal) {
        started.push(connection);
        return new Promise(resolve => signal.addEventListener('abort', resolve, { once: true }));
      },
      async sendTest() { return { state: 'accepted', receipt: 'wechat-proof' }; },
    },
  });
  try {
    links.health(original, 'reconnect');
    await links.begin({ kind: 'wechat', method: 'scan' });
    for (let i = 0; i < 100 && links.snapshot('wechat').step === 'starting'; i++) await delay(5);
    const pair = links.snapshot('wechat');
    assert.equal(pair.step, 'test');
    const [candidate] = started;
    assert.equal(candidate.id, original.id);
    assert.equal(candidate.accountId, 'platform-bot');
    const input = { actorId: 'owner', conversationId: 'owner', eventId: 'wx-test', text: ' ' + pair.text.split(' ')[1].toLowerCase() + ' ' };
    assert.equal(await links.receive(candidate, { ...input, conversationId: 'different' }), true);
    assert.equal(links.snapshot('wechat').step, 'test');
    links.health(candidate, 'reconnect');
    assert.equal(links.snapshot('wechat').step, 'error');
    assert.deepEqual((await links.status()).wechat, { state: 'reconnect', canScan: true });
    links.health(candidate, 'ready');
    await links.receive(candidate, input);
    await links.confirm({ kind: 'wechat', id: pair.id });
    assert.deepEqual(config, [candidate]);
  } finally {
    abort.abort();
    await rm(directory, { recursive: true });
  }
});
