// Project identity is supplied by the host from the registered workspace path.
import { BlockAssembler, createUserMessage } from '@deepseek-ai/dsh-llm';

export async function verifyModel(llm, selection, signal) {
  const assembler = new BlockAssembler();
  let finished = false;
  try {
    const prepared = await llm.prepareCall({
      provider: selection.provider, model: selection.model, reasoningEffort: selection.reasoningEffort, maxTokens: 96,
    }, signal);
    const prompt = createUserMessage({ content: [{ type: 'text', text: 'Reply with OK.' }], source: { kind: 'user' } });
    for await (const chunk of prepared.stream({
      ...prepared.config, messages: [prompt], purpose: 'workbench-model-check', signal,
    })) {
      assembler.push(chunk);
      if (chunk.type === 'finish') finished = true;
    }
    if (finished && assembler.finish.kind === 'stop' && assembler.blocks().some(block => block.type === 'text' && block.text.trim()))
      return { code: 'verified' };
    if (finished && assembler.finish.kind === 'max-tokens') return { code: 'probe-output-limit' };
    return { code: assembler.finish.failure?.code ?? 'unavailable' };
  } catch (error) {
    return { code: signal.aborted ? 'TIMEOUT' : error?.code ?? 'unavailable' };
  }
}

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
