import {test} from 'node:test';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {createConversationControl} from './conversation-control.mjs';
import {createChannels} from './channels.mjs';

const key='test signing key only not a real credential';
process.env.WORKBENCH_CRM_TEST=key;
const clock=1794120000000;
const configuration=()=>({id:'gateway',enabled:true,kind:'wecom-gateway',baseUrl:'https://gateway.test/api/v1',accountId:'a',tenantId:'t',apiKeyEnv:'WORKBENCH_GATEWAY_TEST',webhookSecretEnv:'WORKBENCH_GATEWAY_TEST',allowedContacts:['contact'],crm:{keyId:'fixture',staffUserId:'staff',bindingVersion:1,callbackOrigin:'https://crm.test',callbackUrl:'https://crm.test/events',secretEnv:'WORKBENCH_CRM_TEST',contactMappings:{contact:'external'}}});
const signed=(q,extra={})=>{const raw=Buffer.from(JSON.stringify(q)),time=String(clock/1000);return[raw,{'x-ar-key':'fixture','x-ar-time':time,'x-ar-sig':crypto.createHmac('sha256',key).update('ar1\ncrm-to-wb\nfixture\n'+time+'\n').update(raw).digest('hex'),...extra}]};
function fixture(fetcher=async()=>Response.json({ok:true})){
 let state={};const calls=[];
 const call=async(path,data)=>{
  calls.push({path,data});
  if(path.startsWith('/connector-state?'))return structuredClone(state);
  if(path==='/connector-state/compare'){assert.deepEqual(data.expected,state);state=structuredClone(data.value);return{ok:true}}
  if(path==='/conversation-control')return{mode:data.op==='resume'?'auto':'human',version:data.op==='resume'?3:2,sessionId:'object-one',actorId:'contact'};
  throw Error('unexpected worker call');
 };
 return{call,calls,control:createConversationControl(configuration(),call,fetcher,()=>clock),state:()=>state};
}
test('existing CRM v1 status/resume stays bound and never exposes internal actor',async()=>{
 const f=fixture();const request={v:1,op:'status',staffUserId:'staff',externalUserId:'external',actor:'owner'};
 assert.deepEqual(await f.control.handle(...signed(request)),{mode:'human',version:2,bindingVersion:1,sessionId:'object-one'});
 const resume={...request,op:'resume',version:2,bindingVersion:1,sessionId:'object-one',requestId:crypto.randomUUID()};
 assert.equal((await f.control.handle(...signed(resume))).mode,'auto');
 assert.equal(f.calls.at(-1).data.contactId,'contact');
 await assert.rejects(()=>f.control.handle(...signed({...request,staffUserId:'other'})),{status:403});
 await assert.rejects(()=>f.control.handle(...signed(request,{'x-ar-sig':'0'.repeat(64)})),{status:401});
 await assert.rejects(()=>f.control.handle(...signed(request,{'x-gateway-event-id':'mixed'})),{status:400});
 await assert.rejects(()=>f.control.handle(...signed(request,{'x-ar-time':'1'})),{status:401});
});
test('CRM configure probes raw signed body and preserves previous callback on failure',async()=>{
 let good=true;const f=fixture(async(url,options)=>{
  assert.equal(options.redirect,'error');const raw=options.body,time=options.headers['X-AR-Time'];
  assert.equal(options.headers['X-AR-Sig'],crypto.createHmac('sha256',key).update('ar1\nwb-to-crm\nfixture\n'+time+'\n').update(raw).digest('hex'));
  const q=JSON.parse(raw);return Response.json({nonce:good?q.nonce:'wrong'});
 });
 const q={v:1,op:'configure',generation:1,callbackUrl:'https://crm.test/new',requestId:crypto.randomUUID()};
 assert.equal((await f.control.handle(...signed(q))).ok,true);
 assert.equal((await f.control.handle(...signed(q))).ok,true);
 good=false;
 await assert.rejects(()=>f.control.handle(...signed({...q,generation:2,callbackUrl:'https://crm.test/bad',requestId:crypto.randomUUID()})),{status:503});
 assert.equal(f.state().callbackUrl,'https://crm.test/new');
 await assert.rejects(()=>f.control.handle(...signed({...q,callbackUrl:'https://evil.test/events',requestId:crypto.randomUUID()})),{status:403});
});
test('a higher configure generation wins an overlapping older probe',async()=>{
 let release;const gate=new Promise(r=>release=r);let probes=0;
 const f=fixture(async(_url,opt)=>{const q=JSON.parse(opt.body);if(++probes===1)await gate;return Response.json({nonce:q.nonce})});
 const q={v:1,op:'configure',generation:1,callbackUrl:'https://crm.test/old',requestId:crypto.randomUUID()};
 const older=f.control.handle(...signed(q));await new Promise(r=>setImmediate(r));
 await f.control.handle(...signed({...q,generation:2,callbackUrl:'https://crm.test/latest',requestId:crypto.randomUUID()}));release();
 await assert.rejects(()=>older,{status:409});assert.equal(f.state().callbackUrl,'https://crm.test/latest');
});
test('control notifications never reach customer sender, even when paused; reconcile uses the same event',async()=>{
 const c=configuration();c.enabled=false;let posts=[];let fail=true;
 const channels=createChannels([c],async()=>({}),async(url,opt)=>{assert.equal(url,'https://crm.test/events');posts.push(JSON.parse(opt.body));if(fail)throw Error('ack lost');return Response.json({ok:true})});
 const input={connectionId:c.id,idempotencyKey:'run',target:{kind:'conversation.mode.changed',eventId:'event-1'},text:JSON.stringify({connectionId:c.id,actorId:'contact',sessionId:'object-one',version:2,mode:'human'})};
 assert.equal((await channels.send(input)).state,'unknown');fail=false;
 assert.equal((await channels.receipt({...input,receipt:'event-1',state:'unknown'})).state,'sent');
 assert.equal(posts.length,2);assert.deepEqual(posts[0],posts[1]);assert.equal(posts[0].externalUserId,'external');assert.equal(posts[0].mode,'human');assert.equal(posts[0].text,undefined);
 const denied={...input,text:JSON.stringify({connectionId:c.id,actorId:'other',sessionId:'object-one',version:2,mode:'human'})};
 assert.equal((await channels.send(denied)).state,'failed');assert.equal(posts.length,2);
 const unconfigured=createChannels([{id:'plain',enabled:true,kind:'wechat'}],async()=>({}),async()=>{throw Error('customer sender reached')});
 assert.equal((await unconfigured.send({...input,connectionId:'plain'})).state,'failed');
});

test('an existing configured hook alias resolves the same bound account without renaming its sessions',async()=>{
 const c=configuration();c.crm.hookId='old-hook';
 const f=fixture();const channels=createChannels([c],f.call,fetch);
 // Wall clock signs production path; no alternate account or model input.
 const q={v:1,op:'status',staffUserId:'staff',externalUserId:'external',actor:'owner'},raw=Buffer.from(JSON.stringify(q)),time=String(Math.floor(Date.now()/1000));
 const headers={'x-ar-key':'fixture','x-ar-time':time,'x-ar-sig':crypto.createHmac('sha256',key).update('ar1\ncrm-to-wb\nfixture\n'+time+'\n').update(raw).digest('hex')};
 assert.equal((await channels.ingestControl('old-hook',raw,headers)).sessionId,'object-one');
 assert.equal(f.calls.at(-1).data.connectionId,c.id);
 assert.throws(()=>createChannels([c,{...c,id:'another',crm:{...c.crm}}],f.call),/Duplicate/);
});
