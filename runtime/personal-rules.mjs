// Owner configuration is current-turn context, not a user message or memory.
// Harness replaces this named context snapshot when a saved version changes.
export const name = 'workbench-personal-rules';
export const inject = ['systemPrompt'];
export function apply(ctx, config) {
  ctx.systemPrompt.context({
    name: 'workbench-owner-rules', order: -1000, required: true,
    interpolate: false, text: config.text,
  });
}
