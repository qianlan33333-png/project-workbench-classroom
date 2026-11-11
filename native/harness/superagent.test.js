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
