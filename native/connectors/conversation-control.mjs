import crypto from 'node:crypto';
import fs from 'node:fs/promises';

const fail=(status,code)=>Object.assign(new Error(code),{status,code});
const canonical=value=>JSON.stringify(value,(_key,v)=>v&&typeof v==='object'&&!Array.isArray(v)?Object.fromEntries(Object.keys(v).sort().map(k=>[k,v[k]])):v);
const digest=value=>crypto.createHash('sha256').update(canonical(value)).digest('hex');
const uuid=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);

// Adapter for the existing CRM v1 contract. The model never sees its key or
// selects its account, staff identity, callback origin or trusted mappings.
export function createConversationControl(connection,workerCall,fetcher=fetch,now=Date.now){
 const c=connection.crm;if(!c||c.enabled===false)return undefined;
 const origin=new URL(c.callbackOrigin);
 if(origin.protocol!=='https:'||origin.origin!==c.callbackOrigin||origin.username||!c.keyId||!c.staffUserId||!Number.isSafeInteger(c.bindingVersion)||c.bindingVersion<1||(!c.secretFile&&!/^WORKBENCH_CRM_[A-Z0-9_]+$/.test(c.secretEnv||'')))throw Error('Invalid CRM control configuration');
 function target(value){let u;try{u=new URL(value)}catch{throw fail(400,'invalid_request')};if(u.origin!==origin.origin||u.username||u.password||u.search||u.hash||u.pathname==='/')throw fail(403,'forbidden');return u.href}
 target(c.callbackUrl);
 const scope=c.keyId+'/'+c.bindingVersion;
 const stateId='crm-callback-'+digest([connection.id,scope]).slice(0,48);
 async function secret(){
  if(c.secretFile){const st=await fs.lstat(c.secretFile);if(!c.secretFile.startsWith('/')||!st.isFile()||(st.mode&0o077)||st.size>4096)throw Error('CRM secret unavailable');const v=(await fs.readFile(c.secretFile,'utf8')).trim();if(v.length<16)throw Error('CRM secret unavailable');return v}
  const v=process.env[c.secretEnv];if(!v||v.length<16)throw Error('CRM secret unavailable');return v;
 }
 async function signature(direction,time,raw){return crypto.createHmac('sha256',await secret()).update('ar1\n'+direction+'\n'+c.keyId+'\n'+time+'\n').update(raw).digest('hex')}
 function allowed(actor){return !connection.allowedContacts?.length||connection.allowedContacts.includes(actor)}
 function mappedContact(external){const ids=Object.entries(c.contactMappings||{}).filter(([,value])=>value===external).map(([id])=>id);if(ids.length>1)throw fail(409,'ambiguous');return ids[0]||''}
 async function post(url,payload){
  const raw=Buffer.from(JSON.stringify(payload)),time=String(Math.floor(now()/1000));
  const r=await fetcher(target(url),{method:'POST',redirect:'error',signal:AbortSignal.timeout(12000),headers:{'content-type':'application/json','X-AR-Key':c.keyId,'X-AR-Time':time,'X-AR-Sig':await signature('wb-to-crm',time,raw)},body:raw});
  const body=await r.text();if(body.length>16384||!r.ok)throw fail(503,'unavailable');let value;try{value=JSON.parse(body)}catch{throw fail(503,'unavailable')}return value;
 }
 const state=()=>workerCall('/connector-state?id='+encodeURIComponent(stateId));
 const compare=(expected,value)=>workerCall('/connector-state/compare',{id:stateId,expected,value});
 async function configure(q){
  if(!uuid(q.requestId)||!Number.isSafeInteger(q.generation)||q.generation<1)throw fail(400,'invalid_request');
  const url=target(q.callbackUrl),hash=digest(q);let prepared;
  for(let attempt=0;attempt<5;attempt++){
   const old=await state(),receipt=old.receipts?.[q.requestId];
   if(receipt){if(receipt.hash!==hash)throw fail(409,'request_conflict');return receipt.result}
   if((old.generation||0)>q.generation||((old.generation||0)===q.generation&&old.pending?.requestId!==q.requestId))throw fail(409,'stale_version');
   if(old.pending?.requestId===q.requestId&&old.pending.hash!==hash)throw fail(409,'request_conflict');
   prepared={...old,generation:q.generation,pending:old.pending?.requestId===q.requestId?old.pending:{requestId:q.requestId,hash,url,generation:q.generation,nonce:crypto.randomUUID()}};
   try{await compare(old,prepared);break}catch(e){if(e.status!==409||attempt===4)throw e;prepared=undefined}
  }
  if(!prepared)throw fail(409,'stale_version');
  const probe=await post(url,{v:1,op:'probe',nonce:prepared.pending.nonce});if(probe.nonce!==prepared.pending.nonce)throw fail(503,'unavailable');
  const latest=await state();if(latest.pending?.hash!==hash||latest.pending?.requestId!==q.requestId||latest.generation!==q.generation)throw fail(409,'stale_version');
  const result={ok:true,callbackUrl:url};await compare(latest,{generation:q.generation,callbackUrl:url,receipts:{...latest.receipts,[q.requestId]:{hash,result}}});return result;
 }
 async function handle(raw,headers){
  if(raw.length>16384||headers['x-gateway-signature']||headers['x-gateway-event-id'])throw fail(400,'invalid_request');
  const time=headers['x-ar-time'],key=headers['x-ar-key'],sig=headers['x-ar-sig'];
  if(key!==c.keyId||typeof time!=='string'||!/^\d+$/.test(time)||Math.abs(now()/1000-Number(time))>300||typeof sig!=='string'||!(/^[a-f0-9]{64}$/.test(sig)))throw fail(401,'invalid_signature');
  const expected=Buffer.from(await signature('crm-to-wb',time,raw),'hex');if(!crypto.timingSafeEqual(expected,Buffer.from(sig,'hex')))throw fail(401,'invalid_signature');
  let q;try{q=JSON.parse(raw.toString('utf8'))}catch{throw fail(400,'invalid_request')}
  const keys=['v','op','staffUserId','externalUserId','actor','requestId','sessionId','version','bindingVersion','callbackUrl','generation'];
  if(!q||Array.isArray(q)||q.v!==1||Object.keys(q).some(k=>!keys.includes(k)))throw fail(400,'invalid_request');
  if(q.op==='configure')return configure(q);
  if(!['status','resume'].includes(q.op)||q.staffUserId!==c.staffUserId||typeof q.externalUserId!=='string'||!q.externalUserId||q.externalUserId.length>191||typeof q.actor!=='string'||!q.actor||q.actor.length>191)throw fail(403,'forbidden');
  const contactId=mappedContact(q.externalUserId);if(contactId&&!allowed(contactId))throw fail(403,'forbidden');
  const input={connectionId:connection.id,externalUserId:q.externalUserId,contactId,scope,actor:q.actor};
  const before=await workerCall('/conversation-control',{...input,op:'status'});
  if(!allowed(before.actorId))throw fail(403,'forbidden');
  let result=before;
  if(q.op==='resume'){
   if(q.bindingVersion!==c.bindingVersion)throw fail(404,'not_bound');
   if(!uuid(q.requestId)||typeof q.sessionId!=='string'||!Number.isSafeInteger(q.version)||q.version<1)throw fail(400,'invalid_request');
   result=await workerCall('/conversation-control',{...input,op:'resume',sessionId:q.sessionId,version:q.version,requestId:q.requestId});
  }
  return{mode:result.mode,version:result.version,bindingVersion:c.bindingVersion,sessionId:result.sessionId};
 }
 async function notify({target:destination,text}){
  let value;try{value=JSON.parse(text)}catch{return{state:'failed',error:'Invalid control event'}}
  if(destination.kind!=='conversation.mode.changed'||!destination.eventId||value.connectionId!==connection.id||!allowed(value.actorId)||!['auto','human'].includes(value.mode)||!Number.isSafeInteger(value.version)||value.version<1||!value.sessionId)return{state:'failed',error:'Invalid control event'};
  const mapped=c.contactMappings?.[value.actorId];if(mapped&&value.externalUserId&&mapped!==value.externalUserId)return{state:'failed',error:'Identity mapping conflict'};
  const external=value.externalUserId||mapped;if(!external)return{state:'failed',error:'Verified external identity unavailable'};
  const payload={v:1,op:'state.changed',eventId:destination.eventId,staffUserId:c.staffUserId,externalUserId:external,sessionId:value.sessionId,bindingVersion:c.bindingVersion,version:value.version,mode:value.mode};
  try{const current=await state();const ack=await post(current.callbackUrl||c.callbackUrl,payload);if(ack.ok!==true)throw fail(503,'unavailable');return{state:'sent',receipt:destination.eventId}}
  catch{return{state:'unknown',receipt:destination.eventId,error:'状态通知未确认；会话状态保持，按原事件编号核实。'}}
 }
 return{handle,notify};
}
