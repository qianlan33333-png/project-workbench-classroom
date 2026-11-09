import test from 'node:test';
import assert from 'node:assert/strict';
import {createCRM} from './crm-cli.mjs';
function fixture({lostCreate=false,lostApprove=false,status=0,queryError=0,abortAfterCreate,state='provider_accepted',failure=''}={}){
 const states=new Map(),calls=[];let plan,row;
 const input={idempotencyKey:'original-delivery',target:{customerId:'10',staffId:'20',businessSlot:'original-business-slot'},text:'本人测试原文'};
 const worker=async(path,body)=>{
  if(!body)return structuredClone(states.get(new URL('http://test'+path).searchParams.get('id'))||{});
  assert.deepEqual(states.get(body.id)||{},body.expected);states.set(body.id,structuredClone(body.value));return{ok:true};
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
 return{crm:createCRM({id:'crm',allowedCustomerIds:[10]},worker,invoke),input,calls,states};
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
