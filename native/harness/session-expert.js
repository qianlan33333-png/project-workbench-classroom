import { createUserMessage } from '@deepseek-ai/dsh-llm';
import { FileSystemSkillProvider } from '@deepseek-ai/dsh-skill-filesystem';
import { z } from 'zod';
import { personalExpertId } from './expert-library.js';

const snapshotSchema = z.object({
  turn: z.number(), input: z.string(), id: z.string(), name: z.string(), revision: z.string(),
  description: z.string(), content: z.string(), personal: z.boolean(),
  modelInvocable: z.boolean(), userInvocable: z.boolean(),
});

// The last effective method lives in the native log. The projection lets a
// resumed turn read it even after the managed publication pointer changes.
export const expertTurnProjection = {
  key: 'workbenchExpertTurn', stateVersion: 1,
  stateSchema: z.object({ snapshot: snapshotSchema.nullable() }),
  init: () => ({ snapshot: null }),
  apply(state, event) {
    if (event.type !== 'user/message' || event.data.source?.kind !== 'workbench-expert') return state;
    const raw = event.data.content?.find(block => block.type === 'text')?.text;
    const text = typeof raw === 'string' && raw.slice(raw.indexOf('\n', raw.indexOf('\n') + 1) + 1, -'\n</system-reminder>'.length);
    const parsed = snapshotSchema.safeParse({ ...event.data.source, content: text });
    return parsed.success ? { snapshot: parsed.data } : state;
  },
};

function materialize(expert, personal) {
  if (!expert) return null;
  const linked = new Set([...expert.content.matchAll(/\]\(\.?\/?references\/([^\s)#]+)(?:#[^)]*)?\)/g)].map(match => match[1]));
  const notes = (expert.references || []).filter(ref => linked.has(ref.name));
  const content = notes.length ? [expert.content, ...notes.map(ref => `## ${ref.name}\n\n${ref.content}`)].join('\n\n') : expert.content;
  return { id: expert.id, name: expert.name, revision: expert.revision, description: expert.description, content, personal,
    modelInvocable: expert.invocation?.modelInvocable !== false, userInvocable: expert.invocation?.userInvocable !== false };
}

function definition(snapshot) {
  return { name: snapshot.name, description: snapshot.description, content: snapshot.content,
    provider: 'workbench-expert', source: 'runtime', invocation: { modelInvocable: snapshot.modelInvocable, userInvocable: snapshot.userInvocable } };
}

export function installSessionExpert(agent, { principal, readRule, projection, resolve, compatibility, locked, resolveExpert }) {
  let active;
  let invalidation;
  const personal = principal.kind === 'personal';
  const registry = agent.ctx.get('skills');
  if (!registry) throw Error('Harness 的 Skill 注册表不可用');
  const release = registry.registerProvider(control => {
    invalidation = control.invalidate;
    const legacy = compatibility && new FileSystemSkillProvider(agent.ctx, control, { agentsHome: process.env.DSH_HOME + '/agents' });
    return {
      name: 'workbench-expert',
      async list(options) {
        if (active) return active.snapshot && !active.snapshot.personal ? [{ ...definition(active.snapshot), locator: active.snapshot.revision, rank: 0 }] : [];
        if (personal) return [];
        const selected = await locked(options.signal);
        if (selected) {
          const expert = await resolveExpert(selected);
          const snapshot = materialize(expert, false);
          if (snapshot) return [{ ...definition(snapshot), locator: snapshot.revision, rank: 0 }];
        } else if (legacy) {
          const found = await legacy.list(options);
          const candidates = Array.isArray(found) ? found : found.candidates;
          const mapped = candidates.map(item => ({ ...item, provider: 'workbench-expert', locator: { legacy: item } }));
          return Array.isArray(found) ? mapped : { candidates: mapped, complete: found.complete };
        }
        return [];
      },
      async get(candidate, options) {
        if (candidate.locator?.legacy && !active && !personal && compatibility && !await locked(options.signal)) {
          const value = await legacy.get(candidate.locator.legacy, options);
          return value && { ...value, provider: 'workbench-expert' };
        }
        const id = !active && !personal ? await locked(options.signal) : '';
        const snapshot = active ? active.snapshot : id ? materialize(await resolveExpert(id), false) : undefined;
        if (!snapshot || snapshot.personal || candidate.locator !== snapshot.revision || candidate.name !== snapshot.name) return undefined;
        return definition(snapshot);
      },
    };
  });
  const stop = agent.ctx.on('agent/turn-stopping', ({ turn }) => {
    if (active?.turn === turn) {
      active = undefined;
      invalidation();
    }
  });
  const dispose = agent.ctx.on('agent/pre-step', async (payload, next) => {
    let inject;
    if (!active || active.turn !== payload.turn) {
      const request = payload.messages.findLast(message => ['user', 'workbench-incoming', 'workbench-delegation', 'workbench-owner', 'workbench-result'].includes(message.source?.kind));
      const input = request?.source?.runId || request?.id || '';
      const saved = projection.stateOf(agent.session).snapshot;
      let snapshot;
      if (saved && (saved.turn === payload.turn || input && saved.input === input)) {
        snapshot = saved;
      } else if (personal) {
        const rules = await readRule('AGENT.md');
        snapshot = materialize({ ...rules, id: personalExpertId, name: 'superagent', description: '本人主会话的工作方法' }, true);
      } else {
        const expert = await resolve(request?.source, payload.signal);
        if (expert === undefined && compatibility && !await locked(payload.signal)) return next();
        snapshot = materialize(expert, false);
      }
      active = { turn: payload.turn, snapshot };
      invalidation();
      if (snapshot && (saved?.turn !== payload.turn || saved?.revision !== snapshot.revision)) {
        const { content, ...meta } = snapshot;
        // The log records the effective instructions before the model call.
        inject = createUserMessage({ content: [{ type: 'text', text: '<system-reminder>\n以下是当前轮次固定的专家方法。历史中的其他方法版本不适用于本轮。\n' + content + '\n</system-reminder>' }],
          source: { ...meta, turn: payload.turn, input, kind: 'workbench-expert' } });
      }
    }
    const decision = await next();
    if (decision.kind !== 'enter' || !inject) return decision;
    // Project skill loading still uses the native tool and scoped provider.
    // This durable method record is also available when the model never calls it.
    return { ...decision, messages: [...decision.messages, inject] };
  });
  return () => { dispose(); stop(); release(); };
}
