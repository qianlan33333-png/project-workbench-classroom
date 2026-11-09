import crypto from 'node:crypto';
import {spawn} from 'node:child_process';

const canonical=value=>Array.isArray(value)?value.map(canonical):value&&typeof value==='object'?Object.fromEntries(Object.keys(value).sort().map(k=>[k,canonical(value[k])])):value;
const digest=value=>crypto.createHash('sha256').update(JSON.stringify(canonical(value))).digest('hex');
const uncertain=(receipt,reason='')=>({state:'unknown',receipt:receipt||'',error:'CRM结果未明确；只查询原计划，不重复提交。'+reason});
const businessReason=proof=>[...['failure_reason','query_failure'].map(k=>typeof proof[k]==='string'?proof[k].replace(/[\x00-\x1f\x7f]/g,'').slice(0,200):''),...['creation_errcode','query_errcode'].map(k=>Number.isSafeInteger(proof[k])&&proof[k]!==0?`${k}=${proof[k]}`:'')].filter(Boolean).join('；');
const positive=value=>/^[1-9][0-9]*$/.test(String(value))&&Number.isSafeInteger(Number(value));
const operations=new Set(['admin.createAIAssistantPlan','admin.getAIAssistantPlan','admin.getAIAssistantRecipient','admin.listAIAssistantPlans','admin.listAIAssistantRecipients','admin.reviewAIAssistantRecipient','admin.previewAIAssistantPlanApproval','admin.approveAIAssistantPlan','admin.listAIAssistantExecutionReceipts']);

// Only host configuration chooses the executable and credentials. Request
// content travels on stdin; no shell, model-controlled argv or token logging.
export function cliCaller(config){
 if(typeof config.cli!=='string'||!config.cli.startsWith('/'))throw Error('CRM CLI requires an installed absolute executable');
 return async(operation,{params={},query={},body,key,signal}={})=>{
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
   const timer=setTimeout(()=>{child.kill('SIGKILL');finish(Error('CRM CLI outcome requires readback'))},25000);
   child.stdout.on('data',part=>{output+=part;if(Buffer.byteLength(output)>4*1024*1024){child.kill('SIGKILL');finish(Error('CRM CLI response too large'))}});
   child.on('error',()=>finish(Error('CRM CLI unavailable')));
   child.on('close',code=>{if(code!==0)return finish(Error('CRM CLI rejected or did not complete operation'));try{const v=JSON.parse(output);if(v.error||v.ok===false||(v.http_status!==undefined&&(v.http_status<200||v.http_status>=300)))throw Error();finish(null,v.operation_id===operation&&v.data!==undefined?v.data:v)}catch{finish(Error('CRM CLI response invalid'))}});
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
 return{send,receipt};
}
