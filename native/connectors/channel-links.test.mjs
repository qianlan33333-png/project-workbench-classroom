import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { createChannelLinks } from './channel-links.mjs';

test('private-chat pairing sends one test reply and only enables the owner route after confirmation', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'workbench-channel-link-'));
  const abort = new AbortController();
  const writes = [];
  const sends = [];
  const started = [];
  const config = [];
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
    writes.push({ path, data });
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
    const input = { actorId: 'ou_owner', conversationId: 'oc_owner', eventId: 'om_test', text: pairing.text };
    assert.equal(await links.receive(connection, { ...input, text: 'normal message before pairing' }), true);
    assert.deepEqual(writes, []);
    await Promise.all([links.receive(connection, input), links.receive(connection, input)]);
    assert.equal(sends.length, 1);
    assert.equal(links.snapshot('feishu').step, 'received');
    assert.equal(links.snapshot('feishu').receipt, undefined);
    assert.deepEqual(writes, []);
    await links.confirm({ kind: 'feishu', id: pairing.id });
    assert.equal(links.snapshot('feishu').step, 'linked');
    assert.deepEqual(config, [connection]);
    assert.equal(writes.find(w => w.path === '/message-routes')?.data.principal.sessionId, 'fixture-superagent');
    assert.equal(await links.receive(connection, { ...input, actorId: 'ou_other', text: 'unbound chat' }), true);
    assert.equal(await links.receive(connection, { ...input, text: 'normal owner message' }), false);

    links.health(connection, 'reconnect');
    await links.begin({ kind: 'feishu', method: 'existing', appId: 'cli_fixture', appSecret: 'rotated-app-secret' });
    const rotation = await waitForPair(links);
    const rotated = started.at(-1);
    assert.notEqual(rotated, connection);
    assert.equal(rotated.id, connection.id, 'worker dedupe, cursor, notification and delivery all use this ID');
    assert.deepEqual(config, [connection], 'production send still uses the established credentials before confirmation');
    const writesBeforeRotation = writes.length;
    assert.equal(await links.receive(rotated, { ...input, text: 'normal owner message' }), false);
    assert.equal(await links.receive(rotated, { ...input, actorId: 'ou_other', text: rotation.text }), true);
    assert.equal(sends.length, 1);
    await links.receive(rotated, { ...input, eventId: 'om_rotated', text: rotation.text });
    assert.equal(sends.at(-1).connection, rotated);
    await assert.rejects(links.confirm({ kind: 'feishu', id: pairing.id }), { status: 409 });
    assert.deepEqual(config, [connection]);
    await links.confirm({ kind: 'feishu', id: rotation.id });
    assert.deepEqual(config, [rotated]);
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
    assert.deepEqual(config, [rotated]);
    assert.equal(writes.length, writesBeforeRotation);

    const restartedConfig = [];
    const restarted = await createChannelLinks({ config: restartedConfig, channels, directory, workerCall,
      signal: abort.signal, endpoint: 'http://127.0.0.1:7383/mcp' });
    assert.equal(restarted.snapshot('feishu').id, switched.id, 'the confirmation refers to the same receipt after restart');
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
