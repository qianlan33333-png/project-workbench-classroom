import { z } from 'zod';

const textOf = content => (content || []).filter(b => b.type === 'text').map(b => b.text).join('\n');
const primary = new Set(['user', 'workbench-owner', 'workbench-incoming', 'workbench-delegation', 'workbench-result']);
export const deliveryProjection = {
  key: 'workbenchDelivery', stateVersion: 4,
  stateSchema: z.object({
    through: z.number(), feedback: z.boolean(),
    ownerRequest: z.object({ id: z.string(), text: z.string() }), dispatches: z.array(z.string()),
    activeDispatch: z.string(), lastReply: z.string(),
    files: z.array(z.object({ attachmentId:z.string(),name:z.string(),bytes:z.number() })),
    report: z.object({ runId: z.string(), text: z.string(), reason: z.string() }).nullable(),
  }),
  init: () => ({ through: 0, feedback: false, ownerRequest: { id: '', text: '' }, dispatches: [], activeDispatch: '', lastReply: '', report: null,files:[] }),
  apply(state, event) {
    if (event.type === 'agent/inbox/spliced') {
      const inserted = event.data.inserted;
      return { ...state,
        dispatches: [...new Set([...state.dispatches, ...inserted.filter(m => ['workbench-delegation', 'workbench-owner', 'workbench-incoming'].includes(m.source?.kind)).map(m => m.source.runId)])],
        through: Math.max(state.through, ...inserted.map(m => m.source?.kind === 'workbench-result' ? Number(m.source.sequence) || 0 : 0)),
      };
    }
    if (event.type === 'user/message') {
      const kind = event.data.source?.kind;
      // Time/context injections are auxiliary messages, not a fresh owner request.
      if (!primary.has(kind)) return state;
      return { ...state, feedback: kind === 'workbench-result',
        files:['user','workbench-owner'].includes(kind)?[...new Map([...state.files,...(event.data.content||[]).filter(b=>b.type==='file').map(b=>b.attachment)].map(ref=>[ref.attachmentId,ref])).values()]:state.files,
        ownerRequest: ['user', 'workbench-owner'].includes(kind) ? { id: event.data.id, text: textOf(event.data.content) } : state.ownerRequest,
        ...(kind === 'workbench-delegation' ? { activeDispatch: event.data.source.runId, lastReply: '' } : {}),
      };
    }
    if (event.type === 'assistant/message' && state.activeDispatch) {
      const text = textOf(event.data.message.content);
      return text ? { ...state, lastReply: text } : state;
    }
    if (event.type === 'turn/end' && state.activeDispatch) {
      return { ...state, activeDispatch: '', lastReply: '', report: {
        runId: state.activeDispatch, text: state.lastReply.slice(0, 24000), reason: event.data.reason?.kind || 'unknown',
      } };
    }
    return state;
  },
};
