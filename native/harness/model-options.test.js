import test from 'node:test';
import assert from 'node:assert/strict';
import { sessionModelOptions, applyProjectReasoning } from './model-options.js';

test('project reasoning configuration preserves other projects, provider and model', () => {
  const config = {provider: 'same-provider', model: 'same-model', reasoningEffortByProject: {p_reply: 'high'}};
  assert.deepEqual(sessionModelOptions(config, 'p_reply'), {provider: 'same-provider', model: 'same-model', reasoningEffort: 'high', maxTokens: 4000});
  assert.equal(sessionModelOptions(config, 'p_today').reasoningEffort, 'off');
  assert.equal(sessionModelOptions({...config, reasoningEffort: 'low'}, 'p_today').reasoningEffort, 'low');
  assert.equal(sessionModelOptions({...config, reasoningEffort: 'low'}, 'p_reply').reasoningEffort, 'high');
  assert.throws(() => sessionModelOptions({...config, reasoningEffortByProject: {p_reply: 'unbounded'}}, 'p_reply'), /Unsupported/);
});

test('explicit project policy replaces recorded effort on resume without replacing model or other projects', () => {
  const selected=[];
  const agent={model:{current:()=>({provider:'recorded-provider',model:'recorded-model',reasoningEffort:'off'}),select:x=>selected.push(x)}};
  const config={provider:'default-provider',model:'default-model',reasoningEffortByProject:{p_reply:'high'}};
  applyProjectReasoning(agent,config,'p_reply');
  assert.deepEqual(selected,[{provider:'recorded-provider',model:'recorded-model',reasoningEffort:'high'}]);
  applyProjectReasoning(agent,config,'p_today');
  assert.equal(selected.length,1);
});
