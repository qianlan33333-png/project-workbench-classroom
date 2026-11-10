import crypto from 'node:crypto';
import {spawn} from 'node:child_process';

const canonical=value=>Array.isArray(value)?value.map(canonical):value&&typeof value==='object'?Object.fromEntries(Object.keys(value).sort().map(k=>[k,canonical(value[k])])):value;
const digest=value=>crypto.createHash('sha256').update(JSON.stringify(canonical(value))).digest('hex');
const uncertain=(receipt,reason='')=>({state:'unknown',receipt:receipt||'',error:'CRM结果未明确；只查询原计划，不重复提交。'+reason});
const businessReason=proof=>[...['failure_reason','query_failure'].map(k=>typeof proof[k]==='string'?proof[k].replace(/[\x00-\x1f\x7f]/g,'').slice(0,200):''),...['creation_errcode','query_errcode'].map(k=>Number.isSafeInteger(proof[k])&&proof[k]!==0?`${k}=${proof[k]}`:'')].filter(Boolean).join('；');
const positive=value=>/^[1-9][0-9]*$/.test(String(value))&&Number.isSafeInteger(Number(value));
const operations=new Set(['admin.createAIAssistantPlan','admin.getAIAssistantPlan','admin.getAIAssistantRecipient','admin.listAIAssistantPlans','admin.listAIAssistantRecipients','admin.reviewAIAssistantRecipient','admin.previewAIAssistantPlanApproval','admin.approveAIAssistantPlan','admin.listAIAssistantExecutionReceipts','admin.getMediaMiniProgram','admin.getCustomerDirectoryDetail','admin.getMediaImage']);

// Only host configuration chooses the executable and credentials. Request
// content travels on stdin; no shell, model-controlled argv or token logging.
export function cliCaller(config){
 if(typeof config.cli!=='string'||!config.cli.startsWith('/'))throw Error('CRM CLI requires an installed absolute executable');
 return async(operation,{params={},query={},body,key,signal,timeout=25000}={})=>{
  if(!operations.has(operation))throw Error('Unregistered CRM operation');
  const args=['invoke',operation];
  for(const[k,v]of Object.entries(params))args.push('--param',`${k}=${v}`);
  for(const[k,v]of Object.entries(query))args.push('--query',`${k}=${v}`);
  if(key)args.push('--idempotency-key',key);
  if(body!==undefined)args.push('--input','-');
  const env=Object.fromEntries(Object.entries(process.env).filter(([name])=>!name.startsWith('AICRM_CLI_')));
  for(const[name,source]of Object.entries(config.cliEnv||{})){
   if(!['AICRM_CLI_TOKEN','AICRM_CLI_CREDENTIAL_ORIGIN','AICRM_CLI_CONFIG_DIR','AICRM_CLI_CREDENTIAL_FILE'].includes(name)||!/^WORKBENCH_CRM_[A-Z0-9_]+$/.test(source)||!process.env[source])throw Error('CRM CLI environment missing');
   env[name]=process.env[source];
  }
  return new Promise((resolve,reject)=>{
   const child=spawn(config.cli,args,{env,signal,stdio:['pipe','pipe','ignore']});let output='',settled=false;
   const finish=(err,value)=>{if(settled)return;settled=true;clearTimeout(timer);err?reject(err):resolve(value)};
   const timer=setTimeout(()=>{child.kill('SIGKILL');finish(Error('CRM CLI outcome requires readback'))},timeout);
   child.stdout.on('data',part=>{output+=part;if(Buffer.byteLength(output)>4*1024*1024){child.kill('SIGKILL');finish(Error('CRM CLI response too large'))}});
   child.on('error',()=>finish(Error('CRM CLI unavailable')));
   child.on('close',code=>{
    try {
     const v=JSON.parse(output);
     const status=v.error?.http_status??(v.operation_id===operation?v.http_status:undefined);
     if(Number.isInteger(status)&&status>=400)
      return finish(Object.assign(Error('CRM rejected the request'),{status,crmRejected:true}));
     if(code!==0||v.error||v.ok===false||(v.http_status!==undefined&&(v.http_status<200||v.http_status>=300)))throw Error();
     finish(null,v.operation_id===operation&&v.data!==undefined?v.data:v);
    } catch { finish(Error('CRM CLI rejected or did not complete operation')); }
   });
   child.stdin.on('error',()=>{});child.stdin.end(body===undefined?'':JSON.stringify(body));
  });
 };
}

export function createCRM(config,workerCall,invoke=cliCaller(config)){
 if(!Array.isArray(config.allowedCustomerIds)||!config.allowedCustomerIds.length||config.allowedCustomerIds.some(x=>!positive(x)))throw Error('CRM requires explicit verified customer allowlist');
 const permitted=new Set(config.allowedCustomerIds.map(String));
 const load=id=>workerCall('/connector-state?id='+encodeURIComponent(id));
 const compare=(id,expected,value)=>workerCall('/connector-state/compare',{id,expected,value});
 async function reserve(id,claim){
  try{await compare(id,{},claim);return true}
  catch{return digest(await load(id))===digest(claim)}
 }
 const pages=async(operation,params={},query={})=>{
  const rows=[];let cursor='';const seen=new Set();
  for(;;){const data=await invoke(operation,{params,query:{...query,...(cursor?{cursor}:{})}});if(!Array.isArray(data.items))throw Error('CRM page invalid');rows.push(...data.items);if(!data.next_cursor)return rows;if(seen.has(data.next_cursor))throw Error('CRM cursor repeated');seen.add(data.next_cursor);cursor=data.next_cursor}
 };
 function request(input){
  const customer=String(input.target?.customerId||''),staff=String(input.target?.staffId||'');
  const slot=input.target?.businessSlot;
  if(typeof input.idempotencyKey!=='string'||!input.idempotencyKey||!positive(customer)||!positive(staff)||typeof slot!=='string'||!/^[A-Za-z0-9_.:-]{1,160}$/.test(slot)||typeof input.text!=='string'||/^\p{White_Space}*$/u.test(input.text)||Buffer.byteLength(input.text,'utf8')>8000)throw Error('CRM recipient or content not allowed');
  const content=[{kind:'text',text:input.text}];
  const recipients=[{customer_id:Number(customer),staff_id:Number(staff),content}];
  const sourceDigest='sha256:'+digest({key:input.idempotencyKey,recipients,slot});
  return {customer,staff,content,recipients,sourceDigest,slotId:'crm-slot-'+digest([customer,slot]).slice(0,48),stateId:'crm-'+digest([config.id,input.idempotencyKey]).slice(0,48)};
 }
 async function originalPlan(req,state){
  let plan;
  if(state.planId){const data=await invoke('admin.getAIAssistantPlan',{params:{plan_id:state.planId}});plan=data.plan}
  else {const found=(await pages('admin.listAIAssistantPlans')).filter(x=>x.source_kind==='workbench_operations'&&x.source_digest===req.sourceDigest);if(found.length!==1)return null;plan=found[0]}
  if(!plan||plan.source_kind!=='workbench_operations'||plan.source_digest!==req.sourceDigest||!positive(plan.id))throw Error('CRM plan identity mismatch');
  return plan;
 }
 async function recipients(req,plan){
  const rows=await pages('admin.listAIAssistantRecipients',{plan_id:plan.id});
  if(rows.length!==1||String(rows[0].customer_id)!==req.customer||String(rows[0].staff_id)!==req.staff)throw Error('CRM saved recipient changed');
  const detail=await invoke('admin.getAIAssistantRecipient',{params:{plan_id:plan.id,recipient_id:rows[0].id}});
  if(digest(detail.content?.blocks)!==digest(req.content))throw Error('CRM saved draft changed');
  return rows;
 }
 async function receipt(input){
  let req;try{req=request(input)}catch{return uncertain(input.receipt)}
  const state=await load(req.stateId);
  if(state.digest!==req.sourceDigest)return uncertain();
  try{
   const plan=await originalPlan(req,state);if(!plan)return uncertain();const saved=await recipients(req,plan);
   const rows=await pages('admin.listAIAssistantExecutionReceipts',{plan_id:plan.id},{live:true});
   if(rows.length!==1||String(rows[0].customer_id)!==req.customer||String(rows[0].staff_id)!==req.staff||rows[0].recipient_id!==saved[0].id)return uncertain(String(plan.id));
   const row=rows[0],proof=row.receipt||{};
   const observed=proof.receipt_found===true&&proof.msgid&&proof.query_state==='observed'&&proof.query_errcode===0;
   if(observed&&proof.provider_delivery_status===1&&proof.provider_sent_at)return{state:'sent',receipt:String(plan.id)};
   if(observed&&[2,3,4].includes(proof.provider_delivery_status))return{state:'failed',receipt:String(plan.id),error:businessReason(proof)||`企微执行失败（状态 ${proof.provider_delivery_status}）。`};
   if(['final_failed','terminal_failed','permanent_failed','failed','rejected','ineligible','cancelled','stopped'].includes(row.local_execution_state))return{state:'failed',receipt:String(plan.id),error:businessReason(proof)||'CRM执行已终止或失败。'};
   if(observed&&proof.provider_delivery_status===0)return{state:'pending_confirmation',receipt:String(plan.id)};
   return uncertain(String(plan.id),businessReason(proof));
  }catch{return uncertain(state.planId?String(state.planId):'')}
 }
 async function send(input,{signal}={}){
  let req;try{req=request(input)}catch{return{state:'failed',error:'CRM目标或内容不在允许范围。'}}
  let state=await load(req.stateId);
  if(Object.keys(state).length)return receipt(input);
  if(!permitted.has(req.customer))return{state:'failed',error:'CRM目标不在允许范围。'};
  const canceled={state:'failed',error:'CRM提交前已取消。'};
  if(signal?.aborted)return canceled;
  state={digest:req.sourceDigest,phase:'creating',attempt:crypto.randomUUID()};
  try{if(!await reserve(req.stateId,state))return uncertain()}catch{return uncertain()}
  // Reuse the existing durable CAS for the original business slot. Two
  // independently selected batches cannot both create a plan for that slot.
  const claim={deliveryKey:input.idempotencyKey,digest:req.sourceDigest,attempt:state.attempt};
  let slotClaimed;
  try{slotClaimed=await reserve(req.slotId,claim)}catch{return uncertain()}
  if(!slotClaimed){
   try{await compare(req.stateId,state,{})}catch{return uncertain()}
   return{state:'failed',error:'CRM业务槽未取得，未提交计划。'};
  }
  if(signal?.aborted){
   try{
    await compare(req.slotId,claim,{});
    await compare(req.stateId,state,{});
    return canceled;
   }catch{return uncertain()}
  }
  try{
   const data=await invoke('admin.createAIAssistantPlan',{signal,body:{name:'工作台运营 '+input.idempotencyKey,source_kind:'workbench_operations',source_digest:req.sourceDigest,recipients:req.recipients},key:'wb-create-'+digest([config.id,input.idempotencyKey])});
   const plan=data.plan;
   if(!plan||!positive(plan.id)||plan.source_digest!==req.sourceDigest)throw Error('CRM create result mismatch');
   const saved={...state,planId:plan.id,phase:'created'};await compare(req.stateId,state,saved);state=saved;
   const rows=await recipients(req,plan),row=rows[0];
   if(!['pending_review','pending','approved'].includes(row.review_state))throw Error('CRM recipient is not reviewable');
   signal?.throwIfAborted();
   if(row.review_state!=='approved')await invoke('admin.reviewAIAssistantRecipient',{signal,params:{plan_id:plan.id,recipient_id:row.id},body:{expected_version:row.version,decision:'approved',reason:'工作台本人已确认该名单和正文'},key:'wb-review-'+digest([config.id,input.idempotencyKey])});
   const current=(await invoke('admin.getAIAssistantPlan',{params:{plan_id:plan.id}})).plan;
   const preview=await invoke('admin.previewAIAssistantPlanApproval',{params:{plan_id:plan.id},body:{expected_version:current.version}});
   if(preview.eligible_count!==1||!preview.preview_digest)throw Error('CRM plan eligibility changed');
   await recipients(req,current);
   signal?.throwIfAborted();
   await invoke('admin.approveAIAssistantPlan',{signal,params:{plan_id:plan.id},body:{expected_version:preview.plan_version,preview_digest:preview.preview_digest},key:'wb-approve-'+digest([config.id,input.idempotencyKey])});
   await compare(req.stateId,state,{...state,phase:'submitted'});
   return{state:'accepted',receipt:String(plan.id)};
  }catch{return uncertain(state.planId?String(state.planId):'')}
 }
 async function materialVersions({ids},{signal}={}){
  const cards=[];
  for(let i=0;i<ids.length;i+=8){
   const page=await Promise.all(ids.slice(i,i+8).map(async id=>{
    try{
     const item=await readMiniProgram(invoke,id,signal);
     if(item?.enabled&&String(item.id)===id&&positive(item.version))
      return {id,title:item.title,appId:item.app_id,page:item.page_path,version:item.version,cover:item.cover};
    }catch{/* A missing version leaves the card visible but not authorized for sending. */}
    return null;
   }));
   cards.push(...page.filter(Boolean));
  }
  return {cards};
 }
 return{send,receipt,materialVersions,batch:createOperationsBatch(config,workerCall,invoke,permitted,load,reserve,compare,pages)};
}

async function readMiniProgram(invoke,id,signal){
 const result=await invoke('admin.getMediaMiniProgram',{params:{id},signal});
 const card=result.item||result.miniprogram;
 if(!card?.enabled||card.thumbnail_status!=='ready'||!positive(card.thumb_image_id))return null;
 const coverResult=await invoke('admin.getMediaImage',{params:{id:card.thumb_image_id},signal});
 const cover=coverResult.item||coverResult.image;
 if(!cover?.enabled||cover.id!==card.thumb_image_id||!cover.updated_at)return null;
 return {...card,cover:digest([cover.id,cover.updated_at])};
}

const batchKind='workbench_operations_batch';
const fail=error=>({state:'failed',error});
const ambiguous=()=>({state:'unknown'});
const rejected=error=>[400,401,403,404,409,413,422].includes(error.status);

function providerResult(row,plan) {
 const proof=row.receipt||{};
 const observed=proof.receipt_found===true&&proof.msgid&&proof.query_state==='observed'&&proof.query_errcode===0;
 if(observed&&proof.provider_delivery_status===1&&proof.provider_sent_at)
  return {state:'sent',at:proof.provider_sent_at};
 if(observed&&[2,3,4].includes(proof.provider_delivery_status))
  return fail(businessReason(proof)||'企微返回发送失败。');
 if(['final_failed','terminal_failed','permanent_failed','failed','rejected','ineligible','cancelled','stopped'].includes(row.local_execution_state))
  return fail(businessReason(proof)||'CRM 执行已终止或失败。');
 if(observed&&proof.provider_delivery_status===0)
  return {state:'pending_confirmation'};
 if(['submitting','unknown','outcome_unknown','needs_reconcile','attempted','retryable_failed'].includes(row.local_execution_state))
  return ambiguous();
 return ['accepted','queued','provider_accepted','reconciled'].includes(row.local_execution_state)||
  ['dispatching','completed','completed_with_failures'].includes(plan.state)?{state:'accepted'}:ambiguous();
}

// The connector fetches the frozen manifest from the Worker. The browser
// cannot supply identities, message bodies, or a new plan key.
function createOperationsBatch(config,workerCall,invoke,permitted,load,reserve,compare,pages) {
 return async function batch({runId},send,{signal}={}) {
  if(!/^[a-f0-9-]{36}$/.test(runId))throw Error('Invalid batch reference');
  const manifestPath='/operations/batch-manifest?'+new URLSearchParams({id:runId,connection:config.id});
  const manifest=await workerCall(manifestPath);
  const writeAllowed=async()=> (await workerCall(manifestPath+'&check=write')).sendEnabled===true;
  if(!positive(manifest.staff)||!Array.isArray(manifest.rows)||!manifest.rows.length||manifest.rows.length>5000)throw Error('Invalid batch manifest');
  const stateID='crm-batch-'+digest([config.id,runId]).slice(0,48);
  let state=await load(stateID);
  const previous=Object.keys(state).length!==0;
  const allowed=previous?new Set(state.admitted):permitted;
  const entries=Object.create(null),keysByCustomer=new Map(),duplicateCustomers=new Set();
  for(const row of manifest.rows){
   if(keysByCustomer.has(row.customer))duplicateCustomers.add(row.customer);
   keysByCustomer.set(row.customer,row.key);
  }
  const activeCustomers=new Set();
  if(!previous&&send&&manifest.sendEnabled){
   // Read only the frozen, explicitly allowed targets. A customer can have
   // merged after source publication, but the CRM create is all-or-nothing.
   const ids=[...keysByCustomer.keys()].filter(id=>positive(id)&&allowed.has(id)&&!duplicateCustomers.has(id));
   for(let i=0;i<ids.length;i+=12){
    await Promise.all(ids.slice(i,i+12).map(async id=>{
     try{
      const found=await invoke('admin.getCustomerDirectoryDetail',{params:{customer_id:id},signal});
      if(String(found.canonical_customer_id)===id&&String(found.customer?.customer_id)===id&&found.customer?.status==='active')
       activeCustomers.add(id);
     }catch(error){
      if(error.status!==404)throw error;
     }
    }));
   }
  }
  const recipients=[];
  const itemKey=new Map(),deadlines=[],materials=new Map(),versions={};
  const validCard=card=>card&&positive(card.id)&&positive(card.version)&&/^[a-f0-9]{64}$/.test(card.cover||"")&&card.title&&card.appId&&card.page;
  const readMaterial=async id=>{
   try{
    return await readMiniProgram(invoke,id,signal);
   }catch(error){
    if(error.status===404)return null;
    throw error;
   }
  };
  for(const row of manifest.rows){
   if(!/^[a-zA-Z0-9_-]{1,96}$/.test(row.key)||entries[row.key])throw Error('Invalid batch item');
   if(row.blocked)entries[row.key]=fail(row.blocked);
   else if(!previous&&row.expiresAt&&!(Date.parse(row.expiresAt)>Date.now()))
    entries[row.key]=fail('原服务动作已过期。');
   else if(!positive(row.customer)||!allowed.has(row.customer)||duplicateCustomers.has(row.customer))
    entries[row.key]=fail('CRM 客户身份缺失、重复或超出发送授权。');
   else if(!previous&&send&&manifest.sendEnabled&&!activeCustomers.has(row.customer))
    entries[row.key]=fail('CRM 客户身份已失效或已合并。');
   else if(typeof row.message!=='string'||!row.message.trim()||Buffer.byteLength(row.message)>8000)
    entries[row.key]=fail('正文不符合 CRM 内容合同。');
   else if(!Array.isArray(row.materials)||row.materials.length>19||row.materials.some(card=>!validCard(card)))
    entries[row.key]=fail('随附卡片缺少已验证的 CRM 素材。');
   else {
    if(!previous&&send&&manifest.sendEnabled){
     for(const card of row.materials){
      if(!materials.has(card.id))materials.set(card.id,await readMaterial(card.id));
      const current=materials.get(card.id);
      if(!current?.enabled||String(current.id)!==card.id||current.title!==card.title||
         current.app_id!==card.appId||current.page_path!==card.page||current.version!==card.version||current.cover!==card.cover){
       entries[row.key]=fail('CRM 小程序素材已变化或不可用。');
       break;
      }
      versions[card.id]=[current.version,current.cover];
     }
     if(entries[row.key])continue;
    }
    recipients.push({customer_id:Number(row.customer),staff_id:Number(manifest.staff),
     content:[{kind:'text',text:row.message},...row.materials.map(card=>({kind:'mini_program',material_id:Number(card.id)}))]});
    itemKey.set(row.customer,row.key);
    if(row.expiresAt)deadlines.push(Date.parse(row.expiresAt));
    entries[row.key]=ambiguous();
   }
  }
  const setAll=value=>{for(const key of itemKey.values())entries[key]=value;};
  if(!recipients.length)return {entries};
  const sourceDigest='sha256:'+digest({runId,recipients});
  const planBody={name:'工作台运营 '+runId,source_kind:batchKind,source_digest:sourceDigest,recipients};
  if(Buffer.byteLength(JSON.stringify(planBody))>4*1024*1024){
   setAll(fail('整包超过 CRM 允许的请求大小，未提交。'));
   return {entries};
  }
  const validPlan=plan=>positive(plan?.id)&&plan.source_kind===batchKind&&plan.source_digest===sourceDigest&&plan.target_count===recipients.length;
  async function readPlan(id) {
   if(id)return (await invoke('admin.getAIAssistantPlan',{params:{plan_id:id}})).plan;
   const found=(await pages('admin.listAIAssistantPlans',{}, {keyword:runId})).filter(validPlan);
   return found.length===1?found[0]:null;
  }
  const finish=plan=>({...(positive(plan)?{plan:String(plan)}:{}),entries});
  const validRecipients=saved=>{
   const ids=new Map(saved.map(row=>[String(row.customer_id),String(row.id)]));
   return saved.length===itemKey.size&&ids.size===saved.length&&
    saved.every(row=>itemKey.has(String(row.customer_id))&&String(row.staff_id)===String(manifest.staff)&&positive(row.id))?ids:null;
  };
  const contentStamp=saved=>digest(saved.map(row=>[String(row.customer_id),String(row.id),String(row.content_version_id)]).sort((a,b)=>a[0].localeCompare(b[0])));
  async function read() {
   if(state.digest!==sourceDigest)return finish();
   if(state.phase==='failed'){
    setAll(fail(state.error));
    return finish(state.planId);
   }
   try{
    const plan=await readPlan(state.planId);
    if(!validPlan(plan))return finish();
    const params={plan_id:plan.id};
    const saved=await pages('admin.listAIAssistantRecipients',params);
    const recipientIds=validRecipients(saved);
    if(!recipientIds||!state.contents||state.contents!==contentStamp(saved))return finish(plan.id);
    let cursor=state.receiptCursor||'',limit=state.receiptLimit||20;
    for(let page=0;page<12;page++){
     let data;
     try{data=await invoke('admin.listAIAssistantExecutionReceipts',{params,query:{live:true,limit,...(cursor?{cursor}:{})}});}
     catch{break;}
     if(!Array.isArray(data.items))break;
     const verified=new Map();
     for(const row of data.items){
      const customer=String(row.customer_id),key=itemKey.get(customer);
      if(!key||verified.has(key)||String(row.staff_id)!==String(manifest.staff)||
         recipientIds.get(customer)!==String(row.recipient_id))return finish(plan.id);
      verified.set(key,providerResult(row,plan));
     }
     for(const [key,receipt] of verified)entries[key]=receipt;
     if(limit>1&&data.items.some(row=>row.receipt?.query_failure==='refresh_budget_exhausted')){
      limit=1;
      continue;
     }
     const next=data.next_cursor||'';
     if(next&&next===cursor)break;
     cursor=next;
     limit=20;
     if(!cursor)break;
    }
    if(cursor!==(state.receiptCursor||'')||limit!==(state.receiptLimit||20)){
     try{await remember({receiptCursor:cursor,receiptLimit:limit});}catch{/* Another read can repeat the same page safely. */}
    }
    return finish(plan.id);
   }catch{return finish(state.planId);}
  }
  const remember=async patch=>{
   const next={...state,...patch};
   await compare(stateID,state,next);
   state=next;
  };
  if(!send||!manifest.sendEnabled)return read();
  if(previous&&state.phase!=='creating'&&state.phase!=='created')return read();
  signal?.throwIfAborted();
  if(!previous){
   if(!await writeAllowed()){
    setAll(fail('发送账号、原稿或来源授权已变化，未提交。'));
    return finish();
   }
   const used=new Set(recipients.flatMap(row=>row.content.filter(block=>block.kind==='mini_program').map(block=>String(block.material_id))));
   const cardVersions=Object.fromEntries(Object.entries(versions).filter(([id])=>used.has(id)));
   state={digest:sourceDigest,admitted:recipients.map(row=>String(row.customer_id)),cardVersions:digest(cardVersions),
    attempt:crypto.randomUUID(),phase:'creating'};
   if(!await reserve(stateID,state))return finish();
  }else if(state.digest!==sourceDigest)return finish();
  const recordFailure=async error=>{
   await remember({phase:'failed',error});
   setAll(fail(error));
   return finish(state.planId);
  };
  const key=operation=>'wb-batch-'+operation+'-'+digest([config.id,runId]);
  try {
   const plan=previous
    ? await readPlan(state.planId)
    : (await invoke('admin.createAIAssistantPlan',{body:planBody,key:key('create'),signal,timeout:120000})).plan;
   if(!validPlan(plan)||!positive(plan.version))return finish();
   if(state.phase==='creating'){
    // A read-back of the exact, still unedited plan proves create settled.
    // No later invocation issues create again.
    if(previous&&(plan.version!==1||plan.state!=='pending_review'))return read();
    await remember({planId:plan.id,planVersion:plan.version,phase:'created'});
   }
   const current=previous?plan:await readPlan(plan.id);
   if(!validPlan(current)||current.version!==state.planVersion||current.state!=='pending_review')return read();
   if(deadlines.some(deadline=>!(deadline>Date.now())))
    return recordFailure('计划中的原服务动作已经过期，未批准发送。');
   const params={plan_id:plan.id};
   const preview=await invoke('admin.previewAIAssistantPlanApproval',{params,body:{expected_version:plan.version},signal,timeout:120000});
   if(preview.eligible_count!==recipients.length||preview.plan_version!==plan.version||!preview.preview_digest)
    return recordFailure('CRM 批量计划校验不通过，未批准发送。');
   // Initial recipient version 1 binds the exact create payload. A CRM
   // content edit increments that version even if the plan version stays 1.
   // Reading after preview closes the gap; later edits invalidate the digest.
   const saved=await pages('admin.listAIAssistantRecipients',params,{limit:200});
   if(!validRecipients(saved)||saved.some(row=>row.version!==1||!positive(row.content_version_id)||
     row.review_state!=='pending_review'||row.execution_state!=='not_accepted'))
    return recordFailure('CRM 计划的收件人或稿件版本已经改变，未批准发送。');
   if(deadlines.some(deadline=>!(deadline>Date.now())))
    return recordFailure('计划中的原服务动作已经过期，未批准发送。');
   const currentVersions={};
   for(const id of new Set(recipients.flatMap(row=>row.content.filter(block=>block.kind==='mini_program').map(block=>String(block.material_id))))){
    const material=await readMaterial(id);
    if(!material?.enabled||String(material.id)!==id||!positive(material.version))
     return recordFailure('CRM 小程序素材已经失效，未批准发送。');
    currentVersions[id]=[material.version,material.cover];
   }
   if(state.cardVersions!==digest(currentVersions))
    return recordFailure('CRM 小程序素材版本已经改变，未批准发送。');
   if(!await writeAllowed())
    return recordFailure('发送账号、原稿或来源授权已变化，未批准发送。');
   if(deadlines.some(deadline=>!(deadline>Date.now())))
    return recordFailure('计划中的原服务动作已经过期，未批准发送。');
   signal?.throwIfAborted();
   await remember({phase:'approving',contents:contentStamp(saved)});
   await invoke('admin.approveAIAssistantPlan',{params,body:{expected_version:preview.plan_version,preview_digest:preview.preview_digest},
    key:key('approve'),signal,timeout:120000});
   await remember({phase:'submitted'});
   setAll({state:'accepted'});
   return finish(plan.id);
  }catch(error){
   if(rejected(error)&&error.crmRejected===true)return recordFailure('CRM 明确拒绝本次批量计划。');
   return finish(state.planId);
  }
 };
}
