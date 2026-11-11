import test from 'node:test';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {createGateway,verifyGateway}from './wecom-gateway.mjs';

const config={id:'test-gateway',accountId:'account',tenantId:'tenant',baseUrl:'https://example.com/api/v1',apiKeyEnv:'WORKBENCH_GATEWAY_TEST_API',webhookSecretEnv:'WORKBENCH_GATEWAY_TEST_HOOK'};
process.env.WORKBENCH_GATEWAY_TEST_API='test-api';process.env.WORKBENCH_GATEWAY_TEST_HOOK='synthetic-hook-secret';
function signed(event){const raw=Buffer.from(JSON.stringify(event));const t=String(Math.floor(Date.now()/1000));return[raw,{'x-gateway-event-id':event.eventId,'x-gateway-signature':'t='+t+',v1='+crypto.createHmac('sha256',process.env.WORKBENCH_GATEWAY_TEST_HOOK).update(t+'.').update(raw).digest('hex')}]}
function event(type='message.received'){return{eventId:'event1',type,accountId:'account',tenantId:'tenant',occurredAt:new Date().toISOString(),data:{message:{type:'text',messageId:'message1',accountId:'account',sender:{contactId:'contact',name:'User'},content:{type:'text',text:'Question'}}}}}
test('signed input binds trusted account, preserves human events and dedups by platform message identity',async()=>{
 const received=[];const g=createGateway(config,async(path,body)=>received.push({path,body}));
 await g.ingest(...signed(event()));const duplicate=event();duplicate.eventId='another-event';await g.ingest(...signed(duplicate));
 assert.equal(received[0].body.eventId,received[1].body.eventId);assert.equal(received[0].body.actorId,'contact');
 const manual=event('message.self_sent');manual.data.message.content={type:'unsupported'};await g.ingest(...signed(manual));assert.equal(received[2].body.kind,'human');assert.equal(received[2].body.text,'');
 const other=event();other.accountId='another-account';assert.throws(()=>verifyGateway(config,...signed(other)));
 const [raw,headers]=signed(event());raw[0]=32;assert.throws(()=>verifyGateway(config,raw,headers));
});
test('unknown sends never retry and original receipt lookup cannot cross accounts',async()=>{
 const calls=[];const g=createGateway(config,async()=>{},async(url,options)=>{calls.push({url:String(url),options});if(options.method==='POST')throw Error('lost response');return{ok:true,json:async()=>({messageId:'saved-id',accountId:'another-account',status:'sent'})}});
 const input={idempotencyKey:'stable-id',target:{actorId:'contact',conversationId:'contact',replyTo:'original'},text:'Answer'};
 assert.equal((await g.send(input)).state,'unknown');assert.equal(calls.length,1);assert.equal(calls[0].options.headers['Idempotency-Key'],'stable-id');
 assert.equal((await g.receipt({receipt:'saved-id'})).state,'unknown');assert.equal(calls.length,2);assert.equal(calls[1].options.method,'GET');
 assert.equal((await g.receipt({})).state,'unknown');assert.equal(calls.length,2);
});
test('gateway acceptance and sent receipts are different facts; no invented recipient',async()=>{
 let state='queued';const g=createGateway(config,async()=>{},async()=>({ok:true,json:async()=>({messageId:'saved-id',accountId:'account',status:state})}));
 assert.equal((await g.send({idempotencyKey:'stable-id',target:{actorId:'a',conversationId:'b',replyTo:'original'},text:'No'})).state,'failed');
 assert.equal((await g.send({idempotencyKey:'stable-id',target:{actorId:'a',conversationId:'a',replyTo:'original'},text:'Yes'})).state,'accepted');
 state='sent';assert.equal((await g.receipt({receipt:'saved-id'})).state,'sent');
});
test('event polling preserves the old webhook, orders pages and advances only after persistence',async()=>{
 const abort=new AbortController(),received=[],paths=[];let state={head:'old'};
 const a=event();a.eventId='newer';a.data.message.messageId='second';
 const foreign=event();foreign.eventId='foreign';foreign.accountId='another';
 const b=event();b.eventId='older';b.data.message.messageId='first';
 const old=event();old.eventId='old';
 const g=createGateway(config,async(path,body)=>{if(path.startsWith('/connector-state?'))return state;if(path==='/connector-state'){state=body.value;abort.abort()}else received.push(body.eventId)},async(url,options)=>{assert.equal(options.method,'GET');paths.push(String(url));return{ok:true,json:async()=>String(url).includes('cursor=page2')?{items:[{event:b},{event:foreign},{event:old}]}:{items:[{event:a}],nextCursor:'page2'}}});
 await g.poll(abort.signal);assert.deepEqual(received,['msg-first','msg-second']);assert.equal(state.head,'newer');assert.equal(paths.length,2);
 const failedAbort=new AbortController();let advanced=false;
 const failing=createGateway(config,async(path)=>{if(path.startsWith('/connector-state?'))return{head:'old'};if(path==='/messages'){failedAbort.abort();throw Error('worker temporarily stopped')}advanced=true},async()=>({ok:true,json:async()=>({items:[{event:a},{event:old}]})}));
 await failing.poll(failedAbort.signal);assert.equal(advanced,false);
});
test('isolated contact list is applied before receiving and before sending',async()=>{
 let calls=0;const g=createGateway({...config,allowedContacts:['self']},async()=>{calls++},async()=>{calls++;throw Error('should not send')});
 await g.ingest(...signed(event()));assert.equal(calls,0);
 assert.equal((await g.send({idempotencyKey:'test',target:{actorId:'other',conversationId:'other',replyTo:'msg'},text:'No'})).state,'failed');assert.equal(calls,0);
});

test('only text incoming messages start inference; non-text human replies still pause',async()=>{
 const calls=[];const g=createGateway(config,async(path,body)=>calls.push(body));const v=event();v.data.message.type='unsupported';await g.ingest(...signed(v));assert.equal(calls.length,0);v.type='message.self_sent';await g.ingest(...signed(v));assert.equal(calls[0].kind,'human');
});

test('account metadata uses paged provider records and refreshed account state, never the static label',async()=>{
 const urls=[];const g=createGateway({...config,name:'待联测'},async()=>{},async url=>{urls.push(String(url));let value;if(String(url).includes('/accounts/account'))value={accountId:'account',name:'Actual account',tenantId:'tenant',status:'offline',sendEnabled:false};else value=String(url).includes('cursor=next')?{items:[{accountId:'account',name:'Old name'}]}:{items:[{accountId:'other',name:'Other'}],nextCursor:'next'};return{ok:true,json:async()=>value}});
 const v=await g.describe();assert.equal(v.accountId,'account');assert.equal(v.name,'Actual account');assert.equal(v.status,'offline');assert.equal(v.sendEnabled,false);assert.equal(urls.length,3);
});
test('account description rejects a repeated catalogue cursor or mismatched refreshed identity',async()=>{
 const repeated=createGateway(config,async()=>{},async()=>({ok:true,json:async()=>({items:[],nextCursor:'repeat'})}));await assert.rejects(repeated.describe());
 const changed=createGateway(config,async()=>{},async url=>({ok:true,json:async()=>String(url).includes('/accounts/account')?{accountId:'other',name:'Wrong',tenantId:'tenant'}:{items:[{accountId:'account',name:'Actual'}]}}));await assert.rejects(changed.describe());
});
test('documented account remark remains the readable label while offline',async()=>{
 const g=createGateway(config,async()=>{},async url=>({ok:true,json:async()=>String(url).includes('/accounts/account')?{accountId:'account',remark:'qianlan',status:'offline',sendEnabled:false,wecomIdentity:{corpId:'corp',userId:'staff'}}:{items:[{accountId:'account',remark:'Old remark',status:'online'}]}}));
 const v=await g.describe();assert.equal(v.name,'qianlan');assert.equal(v.status,'offline');assert.equal(v.sendEnabled,false);
});

test('contact identity uses the exact account relationship and only ready mappings',async()=>{
 let items=[{accountId:'other',contactId:'c',externalUserId:'wrong',idMapping:{status:'ready'}},{accountId:'account',contactId:'c',externalUserId:'wm-ok',unionId:'unverified-not-used',idMapping:{status:'ready'}}];
 // The test connector base path is intentionally independent of the provider.
 const baseConfig={...config,baseUrl:'https://example.com/api/v1'};
 const h=createGateway(baseConfig,async()=>{},async(url)=>{assert.equal(new URL(url).searchParams.get('contactId'),'c');return Response.json({items})});
 assert.deepEqual(await h.identity({contactId:'c'}),{externalUserId:'wm-ok'});
 items.push({...items[1]});await assert.rejects(h.identity({contactId:'c'}));
 items=[{...items[1],idMapping:{status:'pending'}}];await assert.rejects(h.identity({contactId:'c'}));
 items=[];await assert.rejects(h.identity({contactId:'c'}));
});
