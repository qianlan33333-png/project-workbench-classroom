import test from 'node:test';import assert from 'node:assert/strict';import {replyEvidenceProjection as p}from './reply-evidence.js';
const incoming=runId=>({type:'user/message',data:{source:{kind:'workbench-incoming',runId}}});
test('customer evidence replays multiple tools, preserves resumed evidence and excludes later owner/feedback tools',()=>{
 let s=p.init();const apply=e=>s=p.apply(s,e);
 apply(incoming('one'));apply({type:'tool/call',data:{callId:'call1',name:'skill',arguments:'{"name":"qa"}'}});apply({type:'tool/result',data:{message:{toolCallId:'call1',content:[{type:'text',text:'method'}]}}});
 apply({type:'turn/end'});apply(incoming('one'));assert.equal(s.rounds[0].calls[0].output,'method');
 apply({type:'tool/call',data:{callId:'call2',name:'qa_read',arguments:'{"ids":["source"]}'}});apply({type:'tool/result',data:{message:{toolCallId:'call2',content:[{type:'text',text:'exact evidence'}],isError:true}}});
 assert.equal(s.rounds[0].calls.length,2);assert.equal(s.rounds[0].calls[1].failed,true);
 apply({type:'user/message',data:{source:{kind:'workbench-result'}}});apply({type:'tool/call',data:{callId:'feedback',name:'run_get',arguments:'{}'}});assert.equal(s.rounds[0].calls.length,2);
 apply(incoming('two'));assert.equal(s.rounds[1].calls.length,0);assert.equal(s.rounds[0].calls.length,2);
});
test('queue claim and passive delivery events do not hide real tool calls or attach unrelated ones',()=>{
 let s=p.init();const apply=e=>s=p.apply(s,e);
 const tool=(callId,name)=>apply({type:'tool/call',data:{callId,name,arguments:'{}'}});
 apply({type:'agent/inbox/spliced',data:{inserted:[incoming('customer').data]}});
 tool('before','ignored');assert.equal(s.rounds.length,0);
 apply({type:'turn/start',data:{turn:1}});
 apply({type:'agent/inbox/spliced',data:{inserted:[]}});
 apply(incoming('customer'));
 apply({type:'user/message',data:{source:{kind:'runtime-context'}}});
 tool('search','qa_search');
 apply({type:'user/message',data:{source:{kind:'workbench-channel-result'}}});
 apply({type:'agent/inbox/spliced',data:{inserted:[]}});
 tool('reply','message_reply');
 apply({type:'turn/end',data:{turn:1}});
 apply({type:'turn/start',data:{turn:2}});
 tool('after','unrelated');
 assert.deepEqual(s.rounds[0].calls.map(c=>c.id),['search','reply']);
});
test('context pruning does not overwrite a recorded tool result',()=>{
 let s=p.apply(p.init(),incoming('customer'));
 s=p.apply(s,{type:'tool/call',data:{callId:'search',name:'qa_search',arguments:'{}'}});
 const result=text=>({type:'tool/result',data:{message:{toolCallId:'search',content:[{type:'text',text}],isError:false}}});
 s=p.apply(s,result('original evidence'));
 s=p.apply(s,{...result('pruned context'),surfaceOp:{op:'replace'}});
 assert.equal(s.rounds[0].calls[0].output,'original evidence');
});
test('evidence projection bounds retained rounds, calls and raw observations',()=>{
 let s=p.init();for(let i=0;i<105;i++)s=p.apply(s,incoming('round-'+i));assert.equal(s.rounds.length,100);
 for(let i=0;i<70;i++)s=p.apply(s,{type:'tool/call',data:{callId:'c'+i,name:'read',arguments:'x'.repeat(8000)}});
 assert.equal(s.rounds.at(-1).calls.length,64);assert.equal(s.rounds.at(-1).calls[0].input.length,6000);assert.ok(p.stateSchema.safeParse(s).success);
});
