import test from 'node:test';
import assert from 'node:assert/strict';
import { deliveryProjection as p } from './delivery-state.js';

test('customer admission and completion survive persisted replay without waking another model',()=>{
 let state=p.init();state=p.apply(state,{type:'agent/inbox/spliced',data:{inserted:[{source:{kind:'workbench-incoming',runId:'customer-1'}}]}});
 assert.equal(state.pendingIncoming,'customer-1');assert.deepEqual(state.dispatches,['customer-1']);
 state=p.apply(state,{type:'turn/end',data:{reason:{kind:'completed'}}});assert.equal(state.pendingIncoming,'');assert.equal(state.incomingReport,'customer-1');
 state=p.apply(state,{type:'user/message',data:{source:{kind:'workbench-result',sequence:17},content:[]}});assert.equal(state.through,17);
 assert.deepEqual(p.stateSchema.parse(JSON.parse(JSON.stringify(state))),state);
});

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


test('late channel receipts do not turn an active customer reply into read-only feedback', () => {
  let state=p.apply(p.init(),{type:'user/message',data:{source:{kind:'workbench-incoming',runId:'next'},content:[]}});
  state=p.apply(state,{type:'agent/inbox/spliced',data:{inserted:[{source:{kind:'workbench-incoming',runId:'next'}}]}});
  state=p.apply(state,{type:'user/message',data:{source:{kind:'workbench-channel-result',sequence:42},content:[]}});
  assert.equal(state.feedback,false);assert.equal(state.pendingIncoming,'next');assert.equal(state.through,42);
  state=p.apply(state,{type:'turn/end',data:{reason:{kind:'completed'}}});assert.equal(state.incomingReport,'next');
  const feedback={...state,feedback:true};
  assert.equal(p.apply(feedback,{type:'user/message',data:{source:{kind:'workbench-channel-result',sequence:43},content:[]}}).feedback,true);
});
