// Project identity is supplied by the host from the registered workspace path.
export function sessionModelOptions(config, projectId) {
  const reasoningEffort = config.reasoningEffortByProject?.[projectId] ?? config.reasoningEffort ?? 'off';
  if (!['off', 'low', 'high', 'max'].includes(reasoningEffort)) {
    throw new Error('Unsupported configured reasoning effort');
  }
  return { provider: config.provider, model: config.model, reasoningEffort, maxTokens: 4000 };
}

export function applyProjectReasoning(agent, config, projectId) {
  // Native history wins over creation defaults on resume. Apply only an
  // explicit project policy through the SDK; retain the selected model.
  if (config.reasoningEffortByProject?.[projectId] === undefined) return;
  const options = sessionModelOptions(config, projectId);
  const selection = agent.model.current();
  agent.model.select({provider: selection?.provider ?? options.provider,
    model: selection?.model ?? options.model, reasoningEffort: options.reasoningEffort});
}
