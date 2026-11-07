import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createChannels,parseWechatJSON,parts} from './channels.mjs';

test('large platform IDs retain precision and Unicode segments join exactly',()=>{
 assert.equal(parseWechatJSON('{"message_id":9007199254740993}').message_id,'9007199254740993');
 const text='你好😀'.repeat(1000);assert.equal(parts(text,1600).join(''),text);
 assert.ok(parts(text,1600).every(p=>p.length<=1600));
});
test('partial WeChat acceptance stays unknown; receipt checking never sends',async()=>{
 let sends=0;
 const channel=createChannels([{id:'w',kind:'wechat',enabled:true,ownerId:'owner',token:'test'}],async()=>({contextTokens:{owner:'fixture'}}),async()=>{
  if(++sends===2)throw Error('response lost');return new Response('{"ret":0}');
 });
 const result=await channel.send({connectionId:'w',idempotencyKey:'one',target:{actorId:'owner'},text:'a'.repeat(1700)});
 assert.equal(result.state,'unknown');assert.equal(sends,2);
 assert.equal((await channel.receipt({connectionId:'w',...result,target:{actorId:'owner'}})).state,'unknown');assert.equal(sends,2);
 assert.equal((await channel.send({connectionId:'w',idempotencyKey:'two',target:{actorId:'customer'},text:'x'})).state,'failed');assert.equal(sends,2);
});
test('all WeChat segments accepted is only acceptance, never delivery',async()=>{
 const channel=createChannels([{id:'w',kind:'wechat',enabled:true,ownerId:'owner'}],async()=>({contextTokens:{owner:'fixture'}}),async()=>new Response('{"ret":0}'));
 const result=await channel.send({connectionId:'w',idempotencyKey:'one',target:{actorId:'owner'},text:'test'});
 assert.equal(result.state,'accepted');assert.equal((await channel.receipt({connectionId:'w',...result,target:{}})).state,'accepted');
});
test('partial Feishu send cannot become sent after querying only successful segments',async()=>{
 let sends=0,reads=0;const client={im:{message:{create:async()=>{if(++sends===2)throw Error('network');return{code:0,data:{message_id:'one'}}},get:async()=>{reads++;return{code:0,data:{items:[{message_id:'one'}]}}}}}};
 const channel=createChannels([{id:'f',kind:'feishu',enabled:true}],async()=>({}),fetch,()=>client);
 const result=await channel.send({connectionId:'f',idempotencyKey:'one',target:{conversationId:'room'},text:'x'.repeat(13000)});
 assert.equal(result.state,'unknown');assert.equal((await channel.receipt({connectionId:'f',...result,target:{}})).state,'unknown');assert.equal(reads,0);assert.equal(sends,2);
});
test('WeCom acceptance remains phone pending until platform result confirms send',async()=>{
 let posts=0;const channel=createChannels([{id:'q',kind:'wecom',enabled:true}],async()=>({}),async(url)=>{
  const path=new URL(url).pathname;if(path.endsWith('gettoken'))return Response.json({access_token:'fixture'});
  if(path.endsWith('add_msg_template')){posts++;return Response.json({errcode:0,msgid:'task'})}
  return Response.json({errcode:0,send_list:[{external_userid:'customer',status:1}]});
 });
 const target={sender:'staff',externalUserId:'customer'};
 const result=await channel.send({connectionId:'q',idempotencyKey:'one',target,text:'hello'});
 assert.equal(result.state,'pending_confirmation');assert.equal((await channel.receipt({connectionId:'q',...result,target})).state,'sent');assert.equal(posts,1);
});

test('receiver handoff ignores only previously consumed Feishu IDs and persists new input',async()=>{
 const {ingestFeishu}=await import('./channels.mjs');const received=[];
 const call=async(path,data)=>{if(path.startsWith('/connector-state'))return{seen:['old-consumed']};if(path!=='/messages')throw Error('unexpected path');received.push(data);return{state:'received'}};
 const event=id=>({sender:{sender_type:'user',sender_id:{open_id:'owner'}},message:{message_id:id,chat_id:'chat',chat_type:'p2p',message_type:'text',content:JSON.stringify({text:'new work'}),create_time:String(Date.now())}});
 await ingestFeishu({id:'feishu'},event('old-consumed'),call);assert.equal(received.length,0);
 await ingestFeishu({id:'feishu'},event('new-event'),call);assert.equal(received.length,1);assert.equal(received[0].eventId,'new-event');assert.equal(received[0].text,'new work');
 await assert.rejects(()=>ingestFeishu({id:'feishu'},event('another'),async()=>{throw Error('state unavailable')}));
});
