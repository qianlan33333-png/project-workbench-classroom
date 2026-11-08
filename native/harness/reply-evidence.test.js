import test from 'node:test';import assert from 'node:assert/strict';import {replyEvidenceProjection as p}from './reply-evidence.js';
const incoming=runId=>({type:'agent/inbox/spliced',data:{inserted:[{source:{kind:'workbench-incoming',runId}}]}});
test('customer evidence replays multiple tools, preserves resumed evidence and excludes later owner/feedback tools',()=>{
 let s=p.init();const apply=e=>s=p.apply(s,e);
 apply(incoming('one'));apply({type:'tool/call',data:{callId:'call1',name:'skill',arguments:'{"name":"qa"}'}});apply({type:'tool/result',data:{message:{toolCallId:'call1',content:[{type:'text',text:'method'}]}}});
 apply({type:'turn/end'});apply(incoming('one'));assert.equal(s.rounds[0].calls[0].output,'method');
 apply({type:'tool/call',data:{callId:'call2',name:'qa_read',arguments:'{"ids":["source"]}'}});apply({type:'tool/result',data:{message:{toolCallId:'call2',content:[{type:'text',text:'exact evidence'}],isError:true}}});
 assert.equal(s.rounds[0].calls.length,2);assert.equal(s.rounds[0].calls[1].failed,true);
 apply({type:'agent/inbox/spliced',data:{inserted:[{source:{kind:'workbench-event'}}]}});apply({type:'tool/call',data:{callId:'feedback',name:'run_get',arguments:'{}'}});assert.equal(s.rounds[0].calls.length,2);
 apply(incoming('two'));assert.equal(s.rounds[1].calls.length,0);assert.equal(s.rounds[0].calls.length,2);
});
test('evidence projection bounds retained rounds, calls and raw observations',()=>{
 let s=p.init();for(let i=0;i<105;i++)s=p.apply(s,incoming('round-'+i));assert.equal(s.rounds.length,100);
 for(let i=0;i<70;i++)s=p.apply(s,{type:'tool/call',data:{callId:'c'+i,name:'read',arguments:'x'.repeat(8000)}});
 assert.equal(s.rounds.at(-1).calls.length,64);assert.equal(s.rounds.at(-1).calls[0].input.length,6000);assert.ok(p.stateSchema.safeParse(s).success);
});
