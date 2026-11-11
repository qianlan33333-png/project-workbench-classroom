import crypto from 'node:crypto';
import {createCRM} from './crm-cli.mjs';
import {createGateway} from './wecom-gateway.mjs';
import {createConversationControl} from './conversation-control.mjs';
import * as lark from '@larksuiteoapi/node-sdk';

export function parseWechatJSON(text){return JSON.parse(text,(key,value,context)=>key==='message_id'&&typeof value==='number'&&context?.source?context.source:value)}
export function platformTime(value){const ms=Number(value);if(!Number.isFinite(ms)||ms<1000000000000||ms>Date.now()+60000)return undefined;return new Date(ms).toISOString()}
export function parts(text,max){const out=[];let part='';for(const char of text){if(part.length+char.length>max){out.push(part);part=''}part+=char}if(part)out.push(part);return out}
// A receiver handoff can retain consumed event IDs without importing old
// conversations. New events still use the Worker's durable identity/digest.
export async function ingestFeishu(c,{sender,message:m},workerCall){
 if(!m||m.chat_type!=='p2p'||sender?.sender_type!=='user'||!m.message_id)return;
 let content;try{content=JSON.parse(m.content)}catch{return};if(m.message_type!=='text')return;
 const state=await workerCall('/connector-state?id='+encodeURIComponent(c.id));
 if(state.seen?.includes(m.message_id))return;
 await workerCall('/messages',{connectionId:c.id,eventId:m.message_id,actorId:sender.sender_id?.open_id,conversationId:m.chat_id,text:content.text||'',replyTo:m.parent_id||'',occurredAt:platformTime(m.create_time)});
}
const unknown=receipt=>({state:'unknown',receipt:receipt||'',error:'结果未明确；连接器不会自动重发。'});
export function createChannels(config,workerCall,fetcher=fetch,feishuFactory){
 const clients=new Map(),tokens=new Map();
 const crms=new Map(config.filter(c=>c.enabled&&c.kind==='crm-cli').map(c=>[c.id,createCRM(c,workerCall)]));
 const controls=new Map();
 for(const c of config.filter(c=>c.crm&&c.crm.enabled!==false)){
  const control=createConversationControl(c,workerCall,fetcher);
  for(const id of new Set([c.id,c.crm.hookId].filter(Boolean))){
   if(!/^[a-zA-Z0-9_-]{1,96}$/.test(id)||controls.has(id))throw Error('Duplicate or invalid CRM hook identity');
   controls.set(id,control);
  }
 }
 const gateways=new Map(config.filter(c=>c.enabled&&c.kind==='wecom-gateway').map(c=>[c.id,createGateway(c,workerCall,fetcher)]));
 const entry=id=>{const c=config.find(c=>c.id===id&&c.enabled);if(!c)throw Error('Connector unavailable');return c};
 const platformWechat=async(c,endpoint,data,signal)=>{
  const r=await fetcher('https://ilinkai.weixin.qq.com/ilink/bot/'+endpoint,{method:'POST',redirect:'error',signal:AbortSignal.any([signal||new AbortController().signal,AbortSignal.timeout(45000)]),headers:{'content-type':'application/json',AuthorizationType:'ilink_bot_token',Authorization:'Bearer '+c.token,'X-WECHAT-UIN':Buffer.from(String(crypto.randomBytes(4).readUInt32BE())).toString('base64'),'iLink-App-Id':'bot','iLink-App-ClientVersion':String((2<<16)|(4<<8)|10)},body:JSON.stringify({...data,base_info:{channel_version:'2.4.10',bot_agent:'PersonalWorkbench'}})});
  if(!r.ok)throw Error('WeChat transport unavailable');return parseWechatJSON(await r.text());
 };
 const platformWecom=async(c,endpoint,data)=>{
  let token=tokens.get(c.id);
  if(!token||token.until<Date.now()){
   const u=new URL('https://qyapi.weixin.qq.com/cgi-bin/gettoken');u.searchParams.set('corpid',c.corpId);u.searchParams.set('corpsecret',c.secret);
   const r=await fetcher(u,{signal:AbortSignal.timeout(20000),redirect:'error'});const v=await r.json();if(!r.ok||v.errcode||!v.access_token)throw Error('WeCom credentials unavailable');token={value:v.access_token,until:Date.now()+(Math.max(60,v.expires_in||7200)-60)*1000};tokens.set(c.id,token);
  }
  const u=new URL('https://qyapi.weixin.qq.com/cgi-bin/'+endpoint);u.searchParams.set('access_token',token.value);
  const r=await fetcher(u,{method:'POST',redirect:'error',signal:AbortSignal.timeout(20000),headers:{'content-type':'application/json'},body:JSON.stringify(data)});if(!r.ok)throw Error('WeCom transport unavailable');return r.json();
 };
 const feishu=c=>{if(feishuFactory)return feishuFactory(c);if(!clients.has(c.id))clients.set(c.id,new lark.Client({appId:c.appId,appSecret:c.appSecret,appType:lark.AppType.SelfBuild,domain:lark.Domain.Feishu,loggerLevel:lark.LoggerLevel.error}));return clients.get(c.id)};
 async function send({connectionId,idempotencyKey,target,text},options){
  if(crms.has(connectionId))return crms.get(connectionId).send({idempotencyKey,target,text},options);
  if(target.kind==='conversation.mode.changed')return controls.has(connectionId)?controls.get(connectionId).notify({target,text}):{state:'failed',error:'CRM control is not configured'};
  const c=entry(connectionId);if(!text||text.length>16000)throw Error('Invalid message');
  if(gateways.has(c.id))return gateways.get(c.id).send({connectionId,idempotencyKey,target,text});
  if(c.kind==='wechat'){
   if(target.actorId!==c.ownerId)return{state:'failed',error:'此微信连接只允许本人通知。'};
   const state=await workerCall('/connector-state?id='+encodeURIComponent(c.id));const context=state.contextTokens?.[target.actorId];if(!context)return{state:'failed',error:'本人需要先建立微信回复窗口。'};
   const accepted=[],segments=parts(text,1600);const proof=()=>JSON.stringify({parts:accepted,total:segments.length,complete:accepted.length===segments.length});
   for(const [i,textPart] of segments.entries()){
    const clientId='wb-'+idempotencyKey+'-'+i;
    let result;try{result=await platformWechat(c,'sendmessage',{msg:{from_user_id:'',to_user_id:target.actorId,client_id:clientId,message_type:2,message_state:2,item_list:[{type:1,text_item:{text:textPart}}],context_token:context}})}catch{return unknown(proof())}
    if(result.ret||result.errcode)return{state:accepted.length?'unknown':'failed',receipt:proof(),error:'微信未确认全部消息段，错误码：'+String(result.errcode||result.ret)};
    accepted.push(clientId);
   }
   return{state:'accepted',receipt:proof()};
  }
  if(c.kind==='feishu'){
   const client=feishu(c),receipts=[],segments=parts(text,12000);const proof=()=>JSON.stringify({parts:receipts,total:segments.length,complete:receipts.length===segments.length});
   for(const [i,part] of segments.entries()){
    const data={msg_type:'text',content:JSON.stringify({text:part}),uuid:crypto.createHash('sha256').update(idempotencyKey+':'+i).digest('hex').slice(0,32)};
    let result;try{result=target.replyTo?await client.im.message.reply({path:{message_id:target.replyTo},data}):await client.im.message.create({params:{receive_id_type:'chat_id'},data:{...data,receive_id:target.conversationId}})}catch{return unknown(proof())}
    if(result.code!==0||!result.data?.message_id)return{state:receipts.length?'unknown':'failed',receipt:proof(),error:'飞书未确认全部消息段。'};receipts.push(result.data.message_id);
   }
   return{state:'sent',receipt:proof()};
  }
  if(c.kind==='wecom'){
   if(!target.sender||!target.externalUserId)return{state:'failed',error:'发送员工与客户标识不完整。'};
   // Official endpoint creates a phone-confirmation task; it is not a send receipt.
   let v;try{v=await platformWecom(c,'externalcontact/add_msg_template',{chat_type:'single',external_userid:[target.externalUserId],sender:target.sender,text:{content:text}})}catch{return unknown()}
   if(v.errcode||!v.msgid||(v.fail_list||[]).includes(target.externalUserId))return{state:'failed',error:'企微未接受发送任务：'+String(v.errcode||'missing_msgid')};
   return{state:'pending_confirmation',receipt:v.msgid};
  }
  throw Error('Unsupported connector');
 }
 async function receipt({connectionId,idempotencyKey,receipt,target,state,text}){
  if(crms.has(connectionId))return crms.get(connectionId).receipt({idempotencyKey,receipt,target,text});
  // Only state.changed is idempotent at the receiver. Customer sends never
  // enter this branch and are never retried as a receipt query.
  if(target.kind==='conversation.mode.changed')return controls.has(connectionId)?controls.get(connectionId).notify({target,text}):{state:'failed',error:'CRM control is not configured'};
  const c=entry(connectionId);
  if(gateways.has(c.id))return gateways.get(c.id).receipt({receipt,target,state});
  if(!receipt)return unknown();
  if(c.kind==='wechat'){let proof;try{proof=JSON.parse(receipt)}catch{return unknown(receipt)};if(!proof.complete||proof.parts?.length!==proof.total||!proof.total)return unknown(receipt);return{state:'accepted',receipt,error:'微信接口提供受理回执，尚无客户送达查询接口。'}};
  if(c.kind==='feishu'){
   let ids;try{const proof=JSON.parse(receipt);if(!proof.complete||proof.parts?.length!==proof.total)return unknown(receipt);ids=proof.parts}catch{return unknown(receipt)}
   if(!Array.isArray(ids)||!ids.length)return unknown(receipt);
   for(const id of ids){const v=await feishu(c).im.message.get({path:{message_id:id}});if(v.code!==0||!v.data?.items?.some(item=>item.message_id===id))return unknown(receipt)}
   return{state:'sent',receipt};
  }
  if(c.kind==='wecom'){
   let cursor='';
   for(let page=0;page<100;page++){
    const v=await platformWecom(c,'externalcontact/get_groupmsg_send_result',{msgid:receipt,userid:target.sender,limit:1000,cursor});if(v.errcode)return unknown(receipt);
    const row=(v.send_list||[]).find(r=>r.external_userid===target.externalUserId);
    if(row){if(row.status===1)return{state:'sent',receipt};if(row.status===0)return{state:'pending_confirmation',receipt};return{state:'failed',receipt,error:'企微发送结果：'+row.status}}
    if(!v.next_cursor)return{state:'pending_confirmation',receipt};if(v.next_cursor===cursor)return unknown(receipt);cursor=v.next_cursor;
   }
   return unknown(receipt);
  }
  return unknown(receipt);
 }
 async function pollWechat(c,signal){
  while(!signal.aborted){
   try{
    const state=await workerCall('/connector-state?id='+encodeURIComponent(c.id));state.contextTokens??={};
    const result=await platformWechat(c,'getupdates',{get_updates_buf:state.get_updates_buf||''},signal);if(result.ret||result.errcode)throw Error('Wechat polling unavailable');
    for(const m of result.msgs||[]){
     if(m.message_type!==1||m.group_id||m.from_user_id!==c.ownerId||m.message_id==null)continue;
     if(m.context_token){state.contextTokens[m.from_user_id]=m.context_token;await workerCall('/connector-state',{id:c.id,value:state})}
     const text=(m.item_list||[]).filter(i=>i.type===1||i.type===3).map(i=>i.text_item?.text||i.voice_item?.text||'').join('').trim();
     if(!text)continue;
     await workerCall('/messages',{connectionId:c.id,eventId:String(m.message_id),actorId:m.from_user_id,conversationId:m.from_user_id,text,occurredAt:platformTime(m.create_time_ms)});
    }
    state.get_updates_buf=result.get_updates_buf??state.get_updates_buf;
    await workerCall('/connector-state',{id:c.id,value:state});
   }catch{if(!signal.aborted)await new Promise(r=>setTimeout(r,3000))}
  }
 }
 async function pollFeishu(c,signal){
  const ws=new lark.WSClient({appId:c.appId,appSecret:c.appSecret,loggerLevel:lark.LoggerLevel.error});
  const dispatcher=new lark.EventDispatcher({}).register({'im.message.receive_v1':event=>ingestFeishu(c,event,workerCall)});
  await ws.start({eventDispatcher:dispatcher});if(!signal.aborted)await new Promise(r=>signal.addEventListener('abort',r,{once:true}));ws.close({force:true});
 }
 return{send,receipt,operationsMaterialVersions:({connectionId,ids,drafts},options)=>{
  const crm=crms.get(connectionId);
  if(!crm)throw Error('CRM material connector unavailable');
  return crm.materialVersions({ids,drafts},options);
 },operationsBatch:({connectionId,runId},send,options)=>{
  const crm=crms.get(connectionId);
  if(!crm)throw Error('CRM batch connector unavailable');
  return crm.batch({runId},send,options);
 },identity:async(input,options)=>{entry(input.connectionId);const g=gateways.get(input.connectionId);if(!g)throw Error('Contact lookup unavailable');return g.identity(input,options)},describe:async({connectionId})=>{entry(connectionId);const g=gateways.get(connectionId);if(!g)throw Error('This connection has no account directory');return g.describe()},ingestControl:async(id,raw,headers)=>{const c=controls.get(id);if(!c)throw Object.assign(Error('not_bound'),{status:404,code:'not_bound'});return c.handle(raw,headers)},ingestGateway:async(id,raw,headers)=>{const g=gateways.get(id);if(!g)throw Error('Unknown gateway');return g.ingest(raw,headers)},start:async signal=>Promise.all(config.filter(c=>c.enabled&&c.receive).map(c=>c.kind==='wechat'?pollWechat(c,signal):c.kind==='feishu'?pollFeishu(c,signal):c.kind==='wecom-gateway'&&c.receiveMode==='poll'?gateways.get(c.id).poll(signal):Promise.resolve()))};
}
