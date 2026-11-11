import test from 'node:test';
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import {createCRM} from './crm-cli.mjs';
function fixture({lostCreate=false,lostApprove=false,lostReserve='',status=0,queryError=0,abortAfterCreate,abortAfterSlot,state='provider_accepted',failure=''}={}){
 const states=new Map(),calls=[];let plan,row;
 const input={idempotencyKey:'original-delivery',target:{customerId:'10',staffId:'20',businessSlot:'original-business-slot'},text:'本人测试原文'};
 const worker=async(path,body)=>{
  if(!body)return structuredClone(states.get(new URL('http://test'+path).searchParams.get('id'))||{});
  assert.deepEqual(states.get(body.id)||{},body.expected);states.set(body.id,structuredClone(body.value));
  if(body.id.startsWith('crm-slot-')&&body.value.deliveryKey)abortAfterSlot?.abort();
  if(body.value.attempt&&Object.keys(body.expected).length===0&&lostReserve===(body.id.startsWith('crm-slot-')?'slot':'delivery'))throw Error('reservation response lost');
  return{ok:true};
 };
 const invoke=async(op,{body,params,key}={})=>{
  calls.push(op);
  if(op==='admin.createAIAssistantPlan'){plan={id:1,...body,state:'pending_review',version:1};row={id:2,customer_id:10,staff_id:20,content:body.recipients[0].content,review_state:'pending_review',version:1};if(abortAfterCreate)abortAfterCreate.abort();if(lostCreate)throw Error('response lost');return{plan}}
  if(op==='admin.listAIAssistantPlans')return{items:plan?[plan]:[],next_cursor:''};
  if(op==='admin.getAIAssistantPlan')return{plan};
  if(op==='admin.listAIAssistantRecipients'){const{content,...summary}=row;return{items:[summary],next_cursor:''}};
  if(op==='admin.getAIAssistantRecipient')return{recipient:row,content:{blocks:row.content}};
  if(op==='admin.reviewAIAssistantRecipient'){assert.equal(body.decision,'approved');row.review_state='approved';plan.version++;return{}};
  if(op==='admin.previewAIAssistantPlanApproval')return{eligible_count:1,plan_version:plan.version,preview_digest:'sha256:'+'a'.repeat(64)};
  if(op==='admin.approveAIAssistantPlan'){plan.state='executing';if(lostApprove)throw Error('response lost');return{}};
  if(op==='admin.listAIAssistantExecutionReceipts')return{items:[{customer_id:10,staff_id:20,recipient_id:2,local_execution_state:state,receipt:{receipt_found:true,msgid:'original-message',query_state:'observed',failure_reason:failure,query_errcode:queryError,provider_delivery_status:status,provider_sent_at:status===1?'2026-11-08T12:00:00Z':null}}],next_cursor:''};
  throw Error('unexpected operation');
 };
 const connector=allowedCustomerIds=>createCRM({id:'crm',allowedCustomerIds},worker,invoke);
 return{crm:connector([10]),connector,input,calls,states};
}
test('CRM saves the exact recipient and draft, approves only after the owner workflow, then reads receipt',async()=>{
 const f=fixture();assert.equal((await f.crm.send(f.input)).state,'accepted');assert.equal((await f.crm.receipt(f.input)).state,'pending_confirmation');
 await f.crm.send(f.input);assert.equal(f.calls.filter(x=>x==='admin.createAIAssistantPlan').length,1);assert.equal(f.calls.filter(x=>x==='admin.approveAIAssistantPlan').length,1);
});
test('individual business failures retain the original restricted reason',async()=>{
 const f=fixture({state:'failed',failure:'contact_no_longer_follows',queryError:84061});await f.crm.send(f.input);
 const result=await f.crm.receipt(f.input);assert.equal(result.state,'failed');assert.match(result.error,/contact_no_longer_follows/);assert.match(result.error,/84061/);
});
test('lost create response looks up original plan and never creates a duplicate',async()=>{
 const f=fixture({lostCreate:true});assert.equal((await f.crm.send(f.input)).state,'unknown');await f.crm.receipt(f.input);await f.crm.send(f.input);assert.equal(f.calls.filter(x=>x==='admin.createAIAssistantPlan').length,1);assert.equal(f.calls.filter(x=>x==='admin.approveAIAssistantPlan').length,0);
});
test('lost approval response reconciles original plan without approving again',async()=>{
 const f=fixture({lostApprove:true,status:1});assert.equal((await f.crm.send(f.input)).state,'unknown');assert.equal((await f.crm.receipt(f.input)).state,'sent');await f.crm.send(f.input);assert.equal(f.calls.filter(x=>x==='admin.approveAIAssistantPlan').length,1);
});
test('wrong customer, modified draft and untrusted receipt cannot become sent',async()=>{
 const f=fixture({status:1,queryError:503});assert.equal((await f.crm.send({...f.input,target:{customerId:'11',staffId:'20'}})).state,'failed');assert.equal(f.calls.length,0);
 await f.crm.send(f.input);assert.equal((await f.crm.receipt(f.input)).state,'unknown');assert.equal((await f.crm.send({...f.input,text:'changed'})).state,'unknown');assert.equal(f.calls.filter(x=>x==='admin.createAIAssistantPlan').length,1);
});

test('two separately selected batches atomically share one business slot',async()=>{
 const f=fixture();const results=await Promise.all([f.crm.send(f.input),f.crm.send({...f.input,idempotencyKey:'another-cycle'})]);
 assert.equal(results.filter(x=>x.state==='accepted').length,1);assert.equal(results.filter(x=>x.state==='failed').length,1);
 assert.equal(f.calls.filter(x=>x==='admin.createAIAssistantPlan').length,1);
});
test('a multi-person submission keeps each draft and recovers only original plans after partial failure',async()=>{
 const states=new Map(),plans=new Map();let creates=0;
 const worker=async(path,body)=>{if(!body)return structuredClone(states.get(new URL('http://test'+path).searchParams.get('id'))||{});assert.deepEqual(states.get(body.id)||{},body.expected);states.set(body.id,structuredClone(body.value));return{ok:true}};
 const invoke=async(op,{params={},body}={})=>{
  let plan=plans.get(Number(params.plan_id));
  if(op==='admin.createAIAssistantPlan'){creates++;plan={...body,id:creates,version:1,state:'pending_review'};plan.row={id:100+creates,...body.recipients[0],review_state:'pending_review',version:1};plans.set(plan.id,plan);if(plan.row.customer_id===11)throw Error('creation response lost');return{plan}}
  if(op==='admin.listAIAssistantPlans')return{items:[...plans.values()],next_cursor:''};
  if(op==='admin.getAIAssistantPlan')return{plan};
  if(op==='admin.listAIAssistantRecipients'){const{content,...row}=plan.row;return{items:[row],next_cursor:''}};
  if(op==='admin.getAIAssistantRecipient')return{recipient:plan.row,content:{blocks:plan.row.content}};
  if(op==='admin.reviewAIAssistantRecipient'){plan.row.review_state='approved';plan.version++;return{}};
  if(op==='admin.previewAIAssistantPlanApproval')return{eligible_count:1,plan_version:plan.version,preview_digest:'sha256:'+'f'.repeat(64)};
  if(op==='admin.approveAIAssistantPlan'){plan.state='executing';return{}};
  if(op==='admin.listAIAssistantExecutionReceipts'){
   const row=plan.row,failed=row.customer_id===12,ready=plan.state==='executing';return{items:[{customer_id:row.customer_id,staff_id:row.staff_id,recipient_id:row.id,local_execution_state:failed?'final_failed':ready?'provider_accepted':'not_accepted',receipt:{receipt_found:ready,msgid:ready?'original-'+plan.id:'',query_state:ready?'observed':'not_requested',query_errcode:ready?0:null,provider_delivery_status:ready?(failed?2:1):null,provider_sent_at:ready&&!failed?'2026-11-08T12:00:00Z':null,failure_reason:failed?'contact_no_longer_follows':''}}],next_cursor:''};
  }
  throw Error('unexpected operation');
 };
 const crm=createCRM({id:'multi-person',allowedCustomerIds:[10,11,12]},worker,invoke);
 const inputs=[10,11,12].map(customer=>({idempotencyKey:'delivery-'+customer,target:{customerId:String(customer),staffId:'20',businessSlot:'original-slot-'+customer},text:'fixed-draft-'+customer}));
 const sent=await Promise.all(inputs.map(input=>crm.send(input)));assert.deepEqual(sent.map(x=>x.state),['accepted','unknown','accepted']);
 const receipts=await Promise.all(inputs.map(input=>crm.receipt(input)));assert.deepEqual(receipts.map(x=>x.state),['sent','unknown','failed']);
 for(const plan of plans.values())assert.equal(plan.row.content[0].text,'fixed-draft-'+plan.row.customer_id);
 await Promise.all(inputs.map(input=>crm.send(input)));assert.equal(creates,3);
});

test('cancellation after plan creation stops later review and approval calls',async()=>{
 const controller=new AbortController();const f=fixture({abortAfterCreate:controller});
 assert.equal((await f.crm.send(f.input,{signal:controller.signal})).state,'unknown');
 assert.equal(f.calls.filter(x=>x==='admin.createAIAssistantPlan').length,1);
 assert.equal(f.calls.filter(x=>x==='admin.reviewAIAssistantRecipient'||x==='admin.approveAIAssistantPlan').length,0);
});

test('pre-create cancellation and invalid UTF-8 content leave the slot available',async()=>{
 for(const abortWhileClaiming of [false,true]){
  const controller=new AbortController();
  const f=fixture({abortAfterSlot:abortWhileClaiming?controller:undefined});
  for(const text of ['   ','中'.repeat(3000)])assert.equal((await f.crm.send({...f.input,text})).state,'failed');
  if(!abortWhileClaiming)controller.abort();
  assert.equal((await f.crm.send(f.input,{signal:controller.signal})).state,'failed');
  assert.equal(f.calls.length,0);
  assert.ok([...f.states.values()].every(v=>Object.keys(v).length===0));
  assert.equal((await f.crm.send({...f.input,idempotencyKey:'next-owner-approved-delivery'})).state,'accepted');
 }
});

test('verified provider failures are terminal even while local CRM remains accepted',async()=>{
 for(const status of [2,3,4]){
  const f=fixture({status});
  await f.crm.send(f.input);
  const result=await f.crm.receipt(f.input);
  assert.equal(result.state,'failed');
  assert.match(result.error,new RegExp(String(status)));
 }
});

test('narrowing the send allowlist still permits reading the exact existing plan',async()=>{
 const f=fixture({status:1});
 await f.crm.send(f.input);
 const restricted=f.connector([11]);
 assert.equal((await restricted.receipt(f.input)).state,'sent');
 assert.equal((await restricted.send({...f.input,idempotencyKey:'new-delivery'})).state,'failed');
 assert.equal(f.calls.filter(x=>x==='admin.createAIAssistantPlan').length,1);
});

test('a lost reservation response reads back only the caller’s own attempt',async()=>{
 for(const lostReserve of ['delivery','slot']){
  const f=fixture({lostReserve});
  const results=await Promise.all([f.crm.send(f.input),f.crm.send(f.input)]);
  assert.equal(results.filter(result=>result.state==='accepted').length,1);
  assert.equal(f.calls.filter(x=>x==='admin.createAIAssistantPlan').length,1);
 }
});

function operationsBatchFixture(count,{lostApproval=false,pendingApproval=false,reportedFailure=false,invalid=false,merged=false,edited=false,materialDrift=false,lostCreate=false,onCheck,onClaim,onApproving}={}){
 const states=new Map(),calls=[];
 const runId='12345678-1234-4abc-8123-123456789abc';
 const cover=crypto.createHash('sha256').update(JSON.stringify([303,'cover-version'])).digest('hex');
 const rows=Array.from({length:count},(_,i)=>({key:'item-'+i,customer:String(i+1),message:'逐人正文 '+i,materials:i===0?[{id:'1070',title:'小程序',appId:'wx-demo',page:'pages/main',version:1,cover}]:[]}));
 if(invalid)rows.push({key:'missing',customer:'',message:'需要精确客户身份',materials:[]});
 let saved,plan,approved=false,materialReads=0;
 const invoke=async(operation,options={})=>{
  calls.push({operation,options});
  switch(operation){
   case 'admin.getCustomerDirectoryDetail': return {canonical_customer_id:merged&&options.params.customer_id==='2'?'20':options.params.customer_id,
    customer:{customer_id:options.params.customer_id,status:'active'}};
   case 'admin.getMediaImage': return {item:{id:303,enabled:true,updated_at:'cover-version'}};
   case 'admin.getMediaMiniProgram': return {item:{id:1070,title:'小程序',app_id:'wx-demo',page_path:'pages/main',version:materialDrift?++materialReads:1,enabled:true,thumbnail_status:'ready',thumb_image_id:303}};
   case 'admin.createAIAssistantPlan':
    saved=options.body;
    plan={id:42,source_kind:saved.source_kind,source_digest:saved.source_digest,target_count:saved.recipients.length,version:1,state:'pending_review'};
    if(lostCreate)throw Error('lost create response');
    return {plan};
   case 'admin.listAIAssistantPlans': return {items:plan?[plan]:[]};
   case 'admin.getAIAssistantPlan': return {plan};
   case 'admin.previewAIAssistantPlanApproval':
    assert.equal(options.body.expected_version,1);
    return {eligible_count:saved.recipients.length,plan_version:1,preview_digest:'preview'};
   case 'admin.approveAIAssistantPlan':
    if(pendingApproval)return {plan};
    approved=true;plan={...plan,state:reportedFailure?'completed_with_failures':'dispatching',version:2,
     approved_count:saved.recipients.length,updated_at:'2026-11-11T00:00:00Z'};
    if(lostApproval)throw Error('lost response');
    return {plan};
   case 'admin.listAIAssistantRecipients':
    return {items:saved.recipients.map((row,i)=>({id:10000+i,customer_id:row.customer_id,staff_id:row.staff_id,version:edited&&i===0?2:1,content_version_id:20000+i,
     review_state:approved?'approved':'pending_review',effect_id:approved?'eer_'+(i+1):undefined,
     execution_state:approved?((lostApproval||reportedFailure)&&i===1?'final_failed':'queued'):'not_accepted'}))};
   default: throw Error(operation);
  }
 };
 const workerCall=async(path,input)=>{
  if(path.startsWith('/operations/batch-manifest?')){
   const enabled=path.includes('check=write')&&onCheck?await onCheck():true;
   return {staff:'20',rows,sendEnabled:enabled};
  }
  if(path.startsWith('/connector-state?'))return states.get(new URL(path,'http://local').searchParams.get('id'))||{};
  assert.equal(path,'/connector-state/compare');
  assert.deepEqual(states.get(input.id)||{},input.expected);
  states.set(input.id,input.value);
  if(input.value.phase==='creating'&&onClaim)await onClaim();
  if(input.value.phase==='approving'&&onApproving)await onApproving();
  return {ok:true};
 };
 const crm=createCRM({id:'batch-test',allowedCustomerIds:Array.from({length:count},(_,i)=>i+1)},workerCall,invoke);
 return {crm,runId,calls,rows};
}

test('one confirmed work package creates and approves one CRM plan of 5000 personalized recipients',async()=>{
 const f=operationsBatchFixture(5000);
 const out=await f.crm.batch({runId:f.runId},true);
 const created=f.calls.filter(x=>x.operation==='admin.createAIAssistantPlan');
 assert.equal(created.length,1);
 assert.equal(created[0].options.body.recipients.length,5000);
 assert.deepEqual(created[0].options.body.recipients[0].content,[{kind:'text',text:'逐人正文 0'},{kind:'mini_program',material_id:1070}]);
 assert.equal(f.calls.filter(x=>x.operation==='admin.approveAIAssistantPlan').length,1);
 assert.deepEqual(out.entries['item-4999'],{state:'accepted',at:'2026-11-11T00:00:00Z'});
 assert.equal(Object.keys(out.entries).length,5000);
 const callsAfterApproval=f.calls.length;
 const receipts=await f.crm.batch({runId:f.runId},false);
 assert.deepEqual(receipts,out);
 assert.equal(f.calls.length,callsAfterApproval);
});

test('batch excludes a missing identity, recovers a lost approval and never creates or approves again',async()=>{
 const f=operationsBatchFixture(3,{lostApproval:true,invalid:true});
 const first=await f.crm.batch({runId:f.runId},true);
 assert.equal(first.entries.missing.state,'failed');
 assert.equal(first.entries['item-0'].state,'unknown');
 const read=await f.crm.batch({runId:f.runId},true);
 assert.equal(read.plan,'42');
 assert.equal(read.entries['item-0'].state,'accepted');
 assert.equal(read.entries['item-1'].state,'failed');
 assert.equal(read.entries['item-2'].state,'accepted');
 assert.equal(f.calls.filter(x=>x.operation==='admin.createAIAssistantPlan').length,1);
 assert.equal(f.calls.filter(x=>x.operation==='admin.approveAIAssistantPlan').length,1);
 assert.equal(f.calls.some(x=>x.operation==='admin.listAIAssistantExecutionReceipts'),false);
});

test('a created plan with an unconfirmed approval response remains unknown',async()=>{
 const f=operationsBatchFixture(2,{pendingApproval:true});
 assert.equal((await f.crm.batch({runId:f.runId},true)).entries['item-0'].state,'unknown');
 assert.equal((await f.crm.batch({runId:f.runId},false)).entries['item-0'].state,'unknown');
 assert.equal(f.calls.filter(c=>c.operation==='admin.approveAIAssistantPlan').length,1);
});

test('a reported plan failure preserves the affected recipient error',async()=>{
 const f=operationsBatchFixture(2,{reportedFailure:true});
 const {entries}=await f.crm.batch({runId:f.runId},true);
 assert.equal(entries['item-0'].state,'accepted');
 assert.equal(entries['item-1'].state,'failed');
 assert.equal(f.calls.some(x=>x.operation==='admin.listAIAssistantExecutionReceipts'),false);
});

test('a batch exceeding the CRM body limit is reported without automatic splitting',async()=>{
 const f=operationsBatchFixture(700);
 for(const row of f.rows)row.message='x'.repeat(6500);
 const out=await f.crm.batch({runId:f.runId},true);
 assert.match(out.entries['item-0'].error,/请求大小/);
 assert.equal(f.calls.filter(x=>x.operation==='admin.createAIAssistantPlan').length,0);
});

test('a merged CRM customer does not poison the one submitted plan',async()=>{
 const f=operationsBatchFixture(3,{merged:true});
 const result=await f.crm.batch({runId:f.runId},true);
 assert.equal(result.entries['item-1'].state,'failed');
 const writes=f.calls.filter(c=>c.operation==='admin.createAIAssistantPlan');
 assert.equal(writes.length,1);
 assert.deepEqual(writes[0].options.body.recipients.map(row=>row.customer_id),[1,3]);
});

test('plan approval stops if CRM content or a card changes after create',async()=>{
 for(const options of [{edited:true},{materialDrift:true}]){
  const f=operationsBatchFixture(2,options);
  const result=await f.crm.batch({runId:f.runId},true);
  assert.equal(result.entries['item-0'].state,'failed');
  assert.equal(f.calls.filter(c=>c.operation==='admin.approveAIAssistantPlan').length,0);
 }
});

test('a verified pending original plan can continue after losing the create response',async()=>{
 const f=operationsBatchFixture(2,{lostCreate:true});
 assert.equal((await f.crm.batch({runId:f.runId},true)).entries['item-0'].state,'unknown');
 assert.equal((await f.crm.batch({runId:f.runId},true)).entries['item-0'].state,'accepted');
 assert.equal(f.calls.filter(c=>c.operation==='admin.createAIAssistantPlan').length,1);
 assert.equal(f.calls.filter(c=>c.operation==='admin.approveAIAssistantPlan').length,1);
});

test('source-owned report card uses only CRM CLI and recovers a lost material response without recreating',async()=>{
 const states=new Map(),calls=[];let created,visible=false;
 const worker=async(path,body)=>{
  if(!body)return structuredClone(states.get(new URL('http://test'+path).searchParams.get('id'))||{});
  assert.deepEqual(states.get(body.id)||{},body.expected);states.set(body.id,structuredClone(body.value));return{};
 };
 const base={enabled:true,thumbnail_status:'ready',thumb_image_id:303,version:1,app_id:'wx-owned',title:'Existing cover',page_path:'pages/home/home'};
 const invoke=async(op,args)=>{
  calls.push(op);
  if(op==='admin.getMediaMiniProgram')return{item:String(args.params.id)==='20'?{...base,id:20}:{...created}};
  if(op==='admin.getMediaImage')return{item:{id:303,enabled:true,updated_at:'2026-11-11T08:00:00Z'}};
  if(op==='admin.listMediaMiniPrograms')return{items:visible&&created?[created]:[],has_more:false};
  if(op==='admin.createMediaMiniProgram'){
   assert.match(args.key,/^crm-material-/);
   created={...base,id:21,...args.body,app_id:args.body.appid,page_path:args.body.pagepath};
   throw Error('response lost');
  }
  throw Error('Unexpected operation '+op);
 };
 const crm=createCRM({id:'crm',allowedCustomerIds:[10],materialTemplates:{'wx-owned':20}},worker,invoke);
 const request={ids:[],drafts:[{title:'报告',appId:'wx-owned',page:'pages/outcome/outcome?id=original-report'}]};
 await assert.rejects(crm.materialVersions(request),/original readback/);
 await assert.rejects(crm.materialVersions(request),/original readback/);
 visible=true;
 const first=await crm.materialVersions(request),again=await crm.materialVersions(request);
 assert.equal(first.cards[0].id,'21');assert.deepEqual(first,again);
 assert.equal(calls.filter(x=>x==='admin.createMediaMiniProgram').length,1);
 assert.ok(calls.every(x=>/Media/.test(x)));
 await assert.rejects(crm.materialVersions({ids:[],drafts:[{...request.drafts[0],appId:'unregistered'}]}),/template missing/);
 await assert.rejects(crm.materialVersions({ids:[],drafts:[{...request.drafts[0],page:request.drafts[0].page+'&dt=borrowed'}]}),/Invalid source material/);
});

test('material preparation uses one concurrent claim and releases only proven pre-create failures',async()=>{
 for(const scenario of ['concurrent','canceled','rejected','lost_claim']){
  const states=new Map(),controller=new AbortController();let creates=0,item,first=true;
  const template={id:20,name:'template',title:'template',app_id:'wx-owned',page_path:'pages/home/home',version:1,enabled:true,thumbnail_status:'ready',thumb_image_id:30};
  const worker=async(path,body)=>{
   if(!body)return structuredClone(states.get(new URL('http://test'+path).searchParams.get('id'))||{});
   assert.deepEqual(states.get(body.id)||{},body.expected);states.set(body.id,structuredClone(body.value));
   if(body.value.phase==='creating'&&first){first=false;if(scenario==='canceled')controller.abort();if(scenario==='lost_claim')throw Error('lost CAS response')}
   return{ok:true};
  };
  const invoke=async(op,args)=>{
   if(op==='admin.getMediaMiniProgram')return{item:String(args.params.id)==='20'?template:item};
   if(op==='admin.getMediaImage')return{item:{id:30,enabled:true,updated_at:'2026-11-11T00:00:00Z'}};
   if(op==='admin.listMediaMiniPrograms')return{items:item?[item]:[],has_more:false};
   if(op==='admin.createMediaMiniProgram'){
    creates++;if(scenario==='rejected'&&creates===1)throw Object.assign(Error('denied'),{crmRejected:true,status:403});
    item={...template,...args.body,id:21,app_id:args.body.appid,page_path:args.body.pagepath};return{item};
   }
   throw Error('unexpected '+op);
  };
  const crm=createCRM({id:'crm',allowedCustomerIds:[10],materialTemplates:{'wx-owned':20}},worker,invoke);
  const request={ids:[],drafts:[{title:'报告',appId:'wx-owned',page:'pages/outcome/outcome?id=original-report'}]};
  if(scenario==='concurrent'){
   const result=await Promise.allSettled([crm.materialVersions(request),crm.materialVersions(request)]);
   assert.ok(result.some(x=>x.status==='fulfilled'));assert.equal(creates,1);
  }else if(scenario==='canceled'){
   await assert.rejects(crm.materialVersions(request,{signal:controller.signal}));assert.equal(creates,0);
  }else if(scenario==='rejected')await assert.rejects(crm.materialVersions(request),/denied/);
  const result=await crm.materialVersions(request);assert.equal(result.cards[0].id,'21');
  assert.equal(creates,scenario==='rejected'?2:1);
 }
});


test('one confirmed batch survives temporary source failure without another confirmation or plan',async()=>{
 for(const phase of [1,2]){
  let checks=0;
  const f=operationsBatchFixture(2,{onCheck:()=>{if(++checks===phase)throw Error('temporary read failure');return true}});
  if(phase===1)await assert.rejects(f.crm.batch({runId:f.runId},true),/temporary read/);
  else assert.equal((await f.crm.batch({runId:f.runId},true)).entries['item-0'].state,'unknown');
  await f.crm.batch({runId:f.runId},false);
  assert.equal(f.calls.filter(c=>c.operation==='admin.approveAIAssistantPlan').length,0);
  assert.equal((await f.crm.batch({runId:f.runId},true)).entries['item-0'].state,'accepted');
  assert.equal(f.calls.filter(c=>c.operation==='admin.createAIAssistantPlan').length,1);
  assert.equal(f.calls.filter(c=>c.operation==='admin.approveAIAssistantPlan').length,1);
 }
});

test('cancellation during live source read or reservation leaves no false unknown CRM plan',async()=>{
 for(const phase of ['source','claim']){
  const controller=new AbortController();let first=true;
  const stop=()=>{if(first){first=false;controller.abort()}return true};
  const f=operationsBatchFixture(2,phase==='source'?{onCheck:stop}:{onClaim:stop});
  await assert.rejects(f.crm.batch({runId:f.runId},true,{signal:controller.signal}),{name:'AbortError'});
  assert.equal(f.calls.filter(c=>c.operation==='admin.createAIAssistantPlan').length,0);
  assert.equal((await f.crm.batch({runId:f.runId},true)).entries['item-0'].state,'accepted');
  assert.equal(f.calls.filter(c=>c.operation==='admin.createAIAssistantPlan').length,1);
 }
});


test('cancellation before approval invocation preserves the original created plan',async()=>{
 const controller=new AbortController();let first=true;
 const f=operationsBatchFixture(2,{onApproving:()=>{if(first){first=false;controller.abort()}}});
 assert.equal((await f.crm.batch({runId:f.runId},true,{signal:controller.signal})).entries['item-0'].state,'unknown');
 assert.equal(f.calls.filter(c=>c.operation==='admin.approveAIAssistantPlan').length,0);
 assert.equal((await f.crm.batch({runId:f.runId},true)).entries['item-0'].state,'accepted');
 assert.equal(f.calls.filter(c=>c.operation==='admin.createAIAssistantPlan').length,1);
 assert.equal(f.calls.filter(c=>c.operation==='admin.approveAIAssistantPlan').length,1);
});

test('an explicit source change stops the original pending plan permanently',async()=>{
 let checks=0;const f=operationsBatchFixture(2,{onCheck:()=>++checks!==2});
 assert.equal((await f.crm.batch({runId:f.runId},true)).entries['item-0'].state,'failed');
 assert.equal((await f.crm.batch({runId:f.runId},true)).entries['item-0'].state,'failed');
 assert.equal(f.calls.filter(c=>c.operation==='admin.createAIAssistantPlan').length,1);
 assert.equal(f.calls.filter(c=>c.operation==='admin.approveAIAssistantPlan').length,0);
});


test('a lost approval reservation response reads back this attempt before approving once',async()=>{
 let first=true;const f=operationsBatchFixture(2,{onApproving:()=>{if(first){first=false;throw Error('lost committed CAS response')}}});
 assert.equal((await f.crm.batch({runId:f.runId},true)).entries['item-0'].state,'accepted');
 await f.crm.batch({runId:f.runId},true);
 assert.equal(f.calls.filter(c=>c.operation==='admin.createAIAssistantPlan').length,1);
 assert.equal(f.calls.filter(c=>c.operation==='admin.approveAIAssistantPlan').length,1);
});
