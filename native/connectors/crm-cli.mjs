import crypto from 'node:crypto';
import {spawn} from 'node:child_process';

const canonical=value=>Array.isArray(value)?value.map(canonical):value&&typeof value==='object'?Object.fromEntries(Object.keys(value).sort().map(k=>[k,canonical(value[k])])):value;
const digest=value=>crypto.createHash('sha256').update(JSON.stringify(canonical(value))).digest('hex');
const uncertain=(receipt,reason='')=>({state:'unknown',receipt:receipt||'',error:'CRM结果未明确；只查询原计划，不重复提交。'+reason});
const businessReason=proof=>[...['failure_reason','query_failure'].map(k=>typeof proof[k]==='string'?proof[k].replace(/[\x00-\x1f\x7f]/g,'').slice(0,200):''),...['creation_errcode','query_errcode'].map(k=>Number.isSafeInteger(proof[k])&&proof[k]!==0?`${k}=${proof[k]}`:'')].filter(Boolean).join('；');
const positive=value=>/^[1-9][0-9]*$/.test(String(value))&&Number.isSafeInteger(Number(value));
const operations=new Set(['admin.createAIAssistantPlan','admin.getAIAssistantPlan','admin.getAIAssistantRecipient','admin.listAIAssistantPlans','admin.listAIAssistantRecipients','admin.reviewAIAssistantRecipient','admin.previewAIAssistantPlanApproval','admin.approveAIAssistantPlan','admin.listAIAssistantExecutionReceipts','admin.getMediaMiniProgram','admin.getMediaImage']);

// Only host configuration chooses the executable and credentials. Request
// content travels on stdin; no shell, model-controlled argv or token logging.
export function cliCaller(config){
 if(typeof config.cli!=='string'||!config.cli.startsWith('/'))throw Error('CRM CLI requires an installed absolute executable');
 return async(operation,{params={},query={},body,key,signal,timeout=25000}={})=>{
  if(!operations.has(operation)&&!['admin.createMediaMiniProgram','admin.listMediaMiniPrograms'].includes(operation))throw Error('Unregistered CRM operation');
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
 async function prepareMaterial(card,signal){
  if(!card||typeof card.appId!=='string'||typeof card.title!=='string'||!card.title.trim()||Buffer.byteLength(card.title)>64||
     typeof card.page!=='string'||!/^pages\/[A-Za-z0-9_/-]+\?[^#\x00-\x20]{1,450}$/.test(card.page)||card.page.includes('dt='))throw Error('Invalid source material');
  const templateID=config.materialTemplates?.[card.appId];
  if(!positive(templateID))throw Error('Approved CRM material template missing');
  const template=await readMiniProgram(invoke,String(templateID),signal);
  if(!template||template.app_id!==card.appId)throw Error('CRM material template unavailable');
  const body={name:'workbench-'+digest([card.appId,card.title,card.page,template.thumb_image_id]),title:card.title,
   appid:card.appId,pagepath:card.page,thumb_image_id:template.thumb_image_id,enabled:true};
  const stateID='crm-material-'+digest([config.id,body]).slice(0,48);
  let state=await load(stateID);
  const matching=item=>item?.name===body.name&&item.title===body.title&&item.app_id===body.appid&&item.page_path===body.pagepath&&item.thumb_image_id===body.thumb_image_id&&positive(item.id);
  const find=async()=>{
   let match=null;
   for(let offset=0;;offset+=100){
    const page=await invoke('admin.listMediaMiniPrograms',{query:{q:body.name,enabled_only:false,limit:100,offset},signal});
    if(!Array.isArray(page.items)||page.items.length>100||typeof page.has_more!=='boolean')throw Error('CRM material readback incomplete');
    for(const item of page.items)if(matching(item)){if(match&&match.id!==item.id)throw Error('CRM material ambiguous');match=item;}
    if(!page.has_more)return match;
    if(page.items.length===0||offset>=5000)throw Error('CRM material readback incomplete');
   }
  };
  let item=state.id?(await invoke('admin.getMediaMiniProgram',{params:{id:state.id},signal})).item:await find();
  if(item&&!matching(item))throw Error('CRM material changed');
  if(!item&&!Object.keys(state).length){
   signal?.throwIfAborted();
   const claim={body,phase:'creating',attempt:crypto.randomUUID()};
   if(!await reserve(stateID,claim))throw Error('CRM material preparation in progress');
   state=claim;
   if(signal?.aborted){
    // This attempt has made no external request. Release only its own claim;
    // an ambiguous create below remains query-only across all later attempts.
    await compare(stateID,claim,{});
    signal.throwIfAborted();
   }
   try{const created=await invoke('admin.createMediaMiniProgram',{body,key:stateID,signal});item=created.item;}
   catch(error){
    if(error.crmRejected&&[401,403,422].includes(error.status)){
     await compare(stateID,claim,{});
     throw error;
    }
    item=await find();
   }
  }
  // An ambiguous create is reconciled by the same exact name/body. Never
  // mint another key or re-create when the response was lost.
  if(!matching(item)||!item.enabled)throw Error('CRM material outcome requires original readback');
  if(!state.id)await compare(stateID,state,{body,phase:'ready',id:String(item.id)});
  return item;
 }
 async function materialVersions({ids,drafts=[]},{signal}={}){
  const covers=new Map();
  // The previous release needs these stored fields to read an existing plan.
  // The current submission uses only the reviewed card destination.
  const describe=async item=>{
   if(!item?.enabled||!positive(item.id)||!positive(item.version)||!positive(item.thumb_image_id))return null;
   const imageId=item.thumb_image_id;
   if(!covers.has(imageId))covers.set(imageId,invoke('admin.getMediaImage',{params:{id:imageId},signal}).then(response=>{
    const image=response.item||response.image;
    return image?.id===imageId&&image.enabled&&image.updated_at?digest([image.id,image.updated_at]):'';
   }));
   const cover=await covers.get(imageId);
   return cover?{id:String(item.id),title:item.title,appId:item.app_id,page:item.page_path,version:item.version,cover}:null;
  };
  const cards=[];
  for(let i=0;i<ids.length;i+=8){
   const page=await Promise.all(ids.slice(i,i+8).map(async id=>{
    try{
     const item=await readMiniProgram(invoke,id,signal);
     if(String(item?.id)===id)return await describe(item);
    }catch{/* Missing or unusable card. */}
    return null;
   }));
   cards.push(...page.filter(Boolean));
  }
  for(const draft of drafts){
   const card=await describe(await prepareMaterial(draft,signal));
   if(!card)throw Error('CRM material not ready');
   cards.push(card);
  }
  return {cards};
 }
 return{send,receipt,materialVersions,batch:createOperationsBatch(config,workerCall,invoke,permitted,load,reserve,compare,pages)};
}

async function readMiniProgram(invoke,id,signal){
 const result=await invoke('admin.getMediaMiniProgram',{params:{id},signal});
 const card=result.item||result.miniprogram;
 if(!card?.enabled||card.thumbnail_status!=='ready'||!positive(card.thumb_image_id))return null;
 return card;
}

const batchKind='workbench_operations_batch';
const fail=error=>({state:'failed',error});
const ambiguous=()=>({state:'unknown'});
const rejected=error=>[400,401,403,404,409,413,422].includes(error.status);

const approvedPlanStates=new Set(['approved','dispatching','needs_attention','completed','completed_with_failures']);

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
  const recipients=[];
  const itemKey=new Map();
  const cards=new Map();
  for(const row of manifest.rows){
   if(!/^[a-zA-Z0-9_-]{1,96}$/.test(row.key)||entries[row.key])throw Error('Invalid batch item');
   if(row.blocked)entries[row.key]=fail(row.blocked);
   else if(!positive(row.customer)||!allowed.has(row.customer)||duplicateCustomers.has(row.customer))
    entries[row.key]=fail('CRM 客户身份缺失、重复或超出发送授权。');
   else if(typeof row.message!=='string'||!row.message.trim()||Buffer.byteLength(row.message)>8000)
    entries[row.key]=fail('正文不符合 CRM 内容合同。');
   else if(!Array.isArray(row.materials)||row.materials.length>19||
           row.materials.some(card=>!positive(card?.id)||!card.title||!card.appId||!card.page))
    entries[row.key]=fail('随附卡片缺少已验证的 CRM 素材。');
   else {
    for(const card of row.materials){
     const expected=cards.get(card.id)||[];
     expected.push(card);
     cards.set(card.id,expected);
    }
    recipients.push({customer_id:Number(row.customer),staff_id:Number(manifest.staff),
     content:[{kind:'text',text:row.message},...row.materials.map(card=>({kind:'mini_program',material_id:Number(card.id)}))]});
    itemKey.set(row.customer,row.key);
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
  const acceptedAt=plan=>plan.updated_at||new Date().toISOString();
  async function readPlan(id) {
   if(id)return (await invoke('admin.getAIAssistantPlan',{params:{plan_id:id}})).plan;
   const found=(await pages('admin.listAIAssistantPlans',{}, {keyword:runId})).filter(validPlan);
   return found.length===1?found[0]:null;
  }
  const finish=plan=>({...(positive(plan)?{plan:String(plan)}:{}),entries});
  const validRecipients=saved=>saved.length===itemKey.size&&new Set(saved.map(row=>String(row.customer_id))).size===saved.length&&
   saved.every(row=>itemKey.has(String(row.customer_id))&&String(row.staff_id)===String(manifest.staff)&&positive(row.id));
  const contentStamp=saved=>digest(saved.map(row=>[String(row.customer_id),String(row.id),String(row.content_version_id)]).sort((a,b)=>a[0].localeCompare(b[0])));
  async function read() {
   if(state.digest!==sourceDigest)return finish();
   if(state.phase==='failed'){
    setAll(fail(state.error));
    return finish(state.planId);
   }
   if(state.phase==='submitted'&&state.submittedAt){
    setAll({state:'accepted',at:state.submittedAt});
    return finish(state.planId);
   }
   try{
    const plan=await readPlan(state.planId);
    if(!validPlan(plan))return finish();
    if(!approvedPlanStates.has(plan.state)||!state.contents)return finish(plan.id);
    const saved=await pages('admin.listAIAssistantRecipients',{plan_id:plan.id});
    if(!validRecipients(saved)||state.contents!==contentStamp(saved))return finish(plan.id);
    const at=acceptedAt(plan);
    const failedRecipients=new Map();
    for(const row of saved){
     const customer=String(row.customer_id),key=itemKey.get(customer);
     if(row.review_state==='rejected')entries[key]=fail('CRM 拒绝了该客户的发送申请。');
     else if(row.review_state==='ineligible')entries[key]=fail('CRM 判定该客户不符合发送资格。');
     else if(row.execution_state==='final_failed'){
      entries[key]=fail('CRM 返回该客户执行失败。');
      failedRecipients.set(customer,String(row.id));
     }
     else if(row.review_state==='approved'&&row.effect_id)
      entries[key]={state:'accepted',at};
    }
    if(failedRecipients.size){
     try {
      const budget=AbortSignal.timeout(5000);
      const lookupSignal=signal?AbortSignal.any([budget,signal]):budget;
      let cursor='';
      for(let page=0;page<4&&failedRecipients.size;page++){
       lookupSignal.throwIfAborted();
       const receipts=await invoke('admin.listAIAssistantExecutionReceipts',{
        params:{plan_id:plan.id},query:{live:false,limit:50,...(cursor?{cursor}:{})},signal:lookupSignal,
       });
       for(const row of receipts.items){
        const customer=String(row.customer_id);
        if(failedRecipients.get(customer)!==String(row.recipient_id)||String(row.staff_id)!==String(manifest.staff))continue;
        const reason=businessReason(row.receipt||{});
        if(reason)entries[itemKey.get(customer)]=fail(reason);
        failedRecipients.delete(customer);
       }
       if(!receipts.next_cursor||receipts.next_cursor===cursor)break;
       cursor=receipts.next_cursor;
      }
     }catch{/* Keep the known CRM failure when its reason is unavailable. */}
    }
    return finish(plan.id);
   }catch{return finish(state.planId);}
  }
  const remember=async patch=>{
   const next={...state,...patch};
   try { await compare(stateID,state,next); }
   catch(error){
    // A lost CAS response is recoverable only when the exact attempt persisted.
    if(digest(await load(stateID))!==digest(next))throw error;
   }
   state=next;
  };
  if(!send||!manifest.sendEnabled)return read();
  if(previous&&state.phase!=='creating'&&state.phase!=='created')return read();
  signal?.throwIfAborted();
  if(!previous){
   if(!await writeAllowed()){
    setAll(fail('CRM 提交权限已关闭或任务已取消。'));
    return finish();
   }
   signal?.throwIfAborted();
   const cardVersions=Object.fromEntries([...cards].map(([id,[card]])=>[id,[card.version,card.cover]]));
   state={digest:sourceDigest,admitted:recipients.map(row=>String(row.customer_id)),cardVersions:digest(cardVersions),
    attempt:crypto.randomUUID(),phase:'creating'};
   if(!await reserve(stateID,state))return finish();
   if(signal?.aborted){
    // No CLI write has started; release only this exact attempt.
    await compare(stateID,state,{});
    signal.throwIfAborted();
   }
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
   if(plan.version!==state.planVersion||plan.state!=='pending_review')return read();
   const params={plan_id:plan.id};
   const preview=await invoke('admin.previewAIAssistantPlanApproval',{params,body:{expected_version:plan.version},signal,timeout:120000});
   if(preview.eligible_count!==recipients.length||preview.plan_version!==plan.version||!preview.preview_digest)
    return recordFailure('CRM 批量计划校验不通过，未批准发送。');
   // Initial recipient version 1 binds the exact create payload. A CRM
   // content edit increments that version even if the plan version stays 1.
   // Reading after preview closes the gap; later edits invalidate the digest.
   const saved=await pages('admin.listAIAssistantRecipients',params);
   if(!validRecipients(saved)||saved.some(row=>row.version!==1||!positive(row.content_version_id)||
     row.review_state!=='pending_review'||row.execution_state!=='not_accepted'))
    return recordFailure('CRM 计划的收件人或稿件版本已经改变，未批准发送。');
   // An editable card can change away from the reviewed page and back again.
   // Its revision binds this single read to the card the user saw.
   for(const [id,expected] of cards){
    const response=await invoke('admin.getMediaMiniProgram',{params:{id},signal});
    const current=response.item||response.miniprogram;
    if(String(current?.id)!==id||!expected.every(card=>current.version===card.version&&current.title===card.title&&current.app_id===card.appId&&current.page_path===card.page))
     return recordFailure('CRM 卡片已被修改，请核对原稿件与卡片。');
   }
   if(!await writeAllowed())
    return recordFailure('CRM 提交权限已关闭或任务已取消。');
   signal?.throwIfAborted();
   const beforeApproval=state;
   await remember({phase:'approving',approvalAttempt:crypto.randomUUID(),contents:contentStamp(saved)});
   if(signal?.aborted){
    // Approval has not started; retain the exact created plan for recovery.
    await compare(stateID,state,beforeApproval);state=beforeApproval;
    signal.throwIfAborted();
   }
   const {plan:approved}=await invoke('admin.approveAIAssistantPlan',{params,body:{expected_version:preview.plan_version,preview_digest:preview.preview_digest},
    key:key('approve'),signal,timeout:120000});
   if(!validPlan(approved)||approved.id!==plan.id||!approvedPlanStates.has(approved.state)||
      approved.approved_count!==recipients.length)return finish(plan.id);
   if(approved.needs_attention_count>0||approved.state==='completed_with_failures')return read();
   const at=acceptedAt(approved);
   await remember({phase:'submitted',submittedAt:at});
   setAll({state:'accepted',at});
   return finish(plan.id);
  }catch(error){
   if(rejected(error)&&error.crmRejected===true)return recordFailure('CRM 明确拒绝本次批量计划。');
   return finish(state.planId);
  }
 };
}
