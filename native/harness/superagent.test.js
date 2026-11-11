import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { AgentDelivery } from './superagent-delivery.js';
import { superAgentSessionId } from './superagent.js';

test('SuperAgent retains the existing Today session identity', () => {
  const path = '/test/workspaces/today';
  assert.equal(superAgentSessionId(path),
    'today_' + createHash('sha256').update(path).digest('hex').slice(0, 24));
});

test('SuperAgent retries an earlier missed result after a later result and deduplicates on reload', async () => {
  let persisted = [
    { type: 'user/message', data: { id: 'old-random-id', source: { kind: 'workbench-result', sequence: 15 } } },
  ];
  let flushWorks = true;
  let active;
  const ctx = {
    on() {},
    agents: { get: () => active },
    sessionPersistence: {
      async flush() {},
      async open() {
        return {
          async read(offset, size) { return { events: persisted.slice(offset, offset + size) }; },
          async [Symbol.asyncDispose]() {},
        };
      },
    },
    sessions: { async flush() { return flushWorks; } },
  };
  const makeAgent = () => {
    const nextTurn = [];
    return {
      id: 'today_test',
      session: {},
      inbox: { nextTurn, nextStep: [] },
      followup(message) { nextTurn.push(message); },
    };
  };
  const source = sequence => ({ kind: 'workbench-result', sequence });
  active = makeAgent();
  let delivery = new AgentDelivery(ctx);
  await delivery.send(active, 'result:15', 'legacy result', source(15));
  assert.equal(active.inbox.nextTurn.length, 0);
  flushWorks = false;
  await assert.rejects(delivery.send(active, 'result:14', 'missing result', source(14)), /persisted/);
  flushWorks = true;
  await delivery.send(active, 'result:14', 'retry', source(14));
  assert.equal(active.inbox.nextTurn.length, 1);
  assert.equal(active.inbox.nextTurn[0].content[0].text, 'missing result');
  assert.equal(active.inbox.nextTurn[0].source.kind, 'workbench-result');
  persisted = [...persisted, ...active.inbox.nextTurn.map(data => ({ type: 'user/message', data }))];

  active = makeAgent();
  delivery = new AgentDelivery(ctx);
  await delivery.send(active, 'result:14', 'retry after restart', source(14));
  assert.equal(active.inbox.nextTurn.length, 0);
});

test('a removed queued result remains delivered on retry and restart', async () => {
  let listener;
  let agent;
  const events = [];
  const ctx = {
    on(_name, callback) { listener = callback; },
    agents: { get() { return agent; } },
    sessionPersistence: {
      async flush() {},
      async open() {
        return {
          async read(offset, length) { return { events: events.slice(offset, offset + length) }; },
          async [Symbol.asyncDispose]() {},
        };
      },
    },
    sessions: { async flush() { return true; } },
  };
  const makeAgent = () => ({
    id: 'today_test',
    session: {},
    inbox: { nextTurn: [], nextStep: [] },
    followup(message) {
      this.inbox.nextTurn.push(message);
      const event = { type: 'agent/inbox/spliced', data: { inserted: [message] } };
      events.push(event);
      listener(this.session, event);
    },
  });

  agent = makeAgent();
  let delivery = new AgentDelivery(ctx);
  const source = { kind: 'workbench-result', sequence: 17 };
  await delivery.send(agent, 'result:17', 'queued result', source);
  assert.equal(agent.inbox.nextTurn.length, 1);
  agent.inbox.nextTurn.pop();
  const removal = { type: 'agent/inbox/spliced', data: { inserted: [] } };
  events.push(removal);
  listener(agent.session, removal);
  await delivery.send(agent, 'result:17', 'retry after acknowledgement failed', source);
  assert.equal(agent.inbox.nextTurn.length, 0);

  agent = makeAgent();
  delivery = new AgentDelivery(ctx);
  await delivery.send(agent, 'result:17', 'retry after restart', source);
  assert.equal(agent.inbox.nextTurn.length, 0);
  assert.equal(events.filter(event => event.data.inserted.length > 0).length, 1);
});
