import test from 'node:test';
import assert from 'node:assert/strict';
import { deliveryProjection as p } from './delivery-state.js';

test('persisted delegation report survives replay and auxiliary messages do not lift read-only feedback', () => {
  const events = [
    { type:'user/message', data:{source:{kind:'workbench-delegation',runId:'dispatch-1'}, content:[]} },
    { type:'user/message', data:{source:{kind:'time-context'},content:[]} },
    { type:'assistant/message', data:{message:{content:[{type:'text',text:'已核对三份资料。'}]}} },
    { type:'turn/end', data:{reason:{kind:'completed'}} },
    { type:'user/message', data:{source:{kind:'workbench-result'},content:[]} },
    { type:'user/message', data:{source:{kind:'time-context'},content:[]} },
    { type:'assistant/message', data:{message:{content:[{type:'text',text:'其他反馈'}]}} },
    { type:'turn/end', data:{reason:{kind:'completed'}} },
  ];
  const state = events.reduce(p.apply,p.init());
  assert.deepEqual(state.report,{runId:'dispatch-1',text:'已核对三份资料。',reason:'completed'});
  assert.equal(state.feedback,true);
  assert.deepEqual(p.stateSchema.parse(JSON.parse(JSON.stringify(state))),state);
  assert.equal(p.apply(state,{type:'user/message',data:{id:'owner-2',source:{kind:'user'},content:[]}}).feedback,false);
});
