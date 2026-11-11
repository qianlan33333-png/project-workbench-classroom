// Adapted from dsh-superagent-prototype src/app/delivery.ts at
// 8c32a9dc5d72c033816c6a8110b37f40cf94fba4. See SUPERAGENT-LICENSE.
import { freezeMessage } from '@deepseek-ai/dsh-llm';

export const superAgentMessageId = requestId => `dsh-superagent:${requestId}`;

function deliveryId(message) {
  const source = message.source;
  if (source?.kind === 'dsh-superagent' ||
      (source?.kind === 'plugin' && source.plugin === 'dsh-superagent') ||
      source?.producer === 'dsh-superagent') return message.id;
  if (source?.kind === 'workbench-result' && Number.isSafeInteger(source.sequence)) {
    return superAgentMessageId('result:' + source.sequence);
  }
  if (['workbench-owner', 'workbench-delegation'].includes(source?.kind) &&
      typeof source.runId === 'string') {
    return superAgentMessageId('dispatch:' + source.runId);
  }
  if (source?.kind === 'user' && typeof source.rpcId === 'string') {
    return superAgentMessageId(source.rpcId);
  }
}

// Upstream's native Session delivery, retaining workbench provenance for its
// existing projections. MySQL remains the owner of pending task and result IDs.
export class AgentDelivery {
  #seen = new WeakMap();

  constructor(ctx) {
    this.ctx = ctx;
    ctx.on('session/event', (session, event) => {
      if (event.type !== 'user/message') return;
      const id = deliveryId(event.data);
      if (id) this.#seen.get(session)?.ids.add(id);
    });
  }

  async #history(agent) {
    let state = this.#seen.get(agent.session);
    if (!state) {
      state = { ids: new Set() };
      this.#seen.set(agent.session, state);
    }
    state.loaded ??= (async () => {
      await this.ctx.sessionPersistence.flush();
      await using handle = await this.ctx.sessionPersistence.open(agent.id, 'read');
      for (let offset = 0; ; ) {
        const { events } = await handle.read(offset, 512);
        if (!events.length) break;
        for (const event of events) {
          if (event.type !== 'user/message') continue;
          const id = deliveryId(event.data);
          if (id) state.ids.add(id);
        }
        offset += events.length;
      }
    })().catch(error => {
      state.loaded = undefined;
      throw error;
    });
    await state.loaded;
    return state;
  }

  async send(agent, requestId, text, source) {
    const seen = await this.#history(agent);
    if (this.ctx.agents.get(agent.id) !== agent) throw Error('Delivery target was disposed');
    const id = superAgentMessageId(requestId);
    const pending = [...agent.inbox.nextTurn, ...agent.inbox.nextStep]
      .some(message => deliveryId(message) === id);
    if (!seen.ids.has(id) && !pending) {
      agent.followup(freezeMessage({
        id, role: 'user', content: [{ type: 'text', text }],
        source: { ...source, producer: 'dsh-superagent' },
      }));
    }
    if (!await this.ctx.sessions.flush(agent.session)) {
      throw Error('SuperAgent message was not persisted');
    }
  }
}
