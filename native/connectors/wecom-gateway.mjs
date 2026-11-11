import crypto from 'node:crypto';

const ambiguous=()=>({state:'unknown',error:'发送结果未明确；只核对原回执，不重发。'});
export function verifyGateway(c,raw,headers,now=Date.now()) {
  const secret=process.env[c.webhookSecretEnv];
  if(!secret||raw.length>1048576)throw Error('Invalid webhook');
  const fields=new Map();for(const part of String(headers['x-gateway-signature']||'').split(',')){
    const [key,value,...rest]=part.trim().split('=');if(!key||!value||rest.length||fields.has(key))throw Error('Invalid signature');fields.set(key,value);
  }
  const time=fields.get('t');if(!/^\d+$/.test(time||'')||Math.abs(now/1000-Number(time))>300)throw Error('Expired signature');
  const expected=crypto.createHmac('sha256',secret).update(time+'.').update(raw).digest();
  const signature=fields.get('v1');if(!/^[a-fA-F0-9]{64}$/.test(signature||''))throw Error('Invalid signature');
  if(!crypto.timingSafeEqual(expected,Buffer.from(signature,'hex')))throw Error('Invalid signature');
  const event=JSON.parse(raw.toString('utf8'));
  if(!event.eventId||event.eventId!==headers['x-gateway-event-id']||event.accountId!==c.accountId||event.tenantId!==c.tenantId||!Number.isFinite(Date.parse(event.occurredAt)))throw Error('Wrong event identity');
  return event;
}
export function createGateway(c,workerCall,fetcher=fetch){
  const base=new URL(c.baseUrl);
  if(base.username||base.password||base.search||base.hash||(base.protocol!=='https:'&&!(base.protocol==='http:'&&base.hostname==='127.0.0.1'))||!c.accountId||!c.tenantId||!/^WORKBENCH_GATEWAY_[A-Z0-9_]+$/.test(c.apiKeyEnv||'')||!/^WORKBENCH_GATEWAY_[A-Z0-9_]+$/.test(c.webhookSecretEnv||''))throw Error('Invalid gateway configuration');
  async function request(method,path,data,key,signal){
    const token=process.env[c.apiKeyEnv];if(!token)throw Error('Gateway credentials unavailable');
    const response=await fetcher(new URL(base.href.replace(/\/$/,'')+path),{method,redirect:'error',signal:AbortSignal.any([AbortSignal.timeout(15000),...(signal?[signal]:[])]),headers:{authorization:'Bearer '+token,'content-type':'application/json',...(key?{'Idempotency-Key':key}:{})},body:data===undefined?undefined:JSON.stringify(data)});
    if(!response.ok){const error=new Error('Gateway request rejected');error.definite=[400,401,403,404,409,422,429].includes(response.status);let data;try{data=await response.json()}catch{};error.code=/^[A-Z0-9_]{1,64}$/.test(data?.code||'')?data.code:String(response.status);throw error}return response.json();
  }
  function receipt(v){
    if(!v.messageId)return ambiguous();
    const states={queued:'accepted',pending:'accepted',accepted:'accepted',sending:'accepted',sent:'sent',delivered:'delivered',failed:'failed'};
    return{state:states[v.status]||'unknown',receipt:v.messageId,...(v.status==='failed'?{error:'网关明确返回发送失败。'}:{})};
  }
  async function ingestEvent(event){
      if(event.accountId!==c.accountId||event.tenantId!==c.tenantId)throw Error('Wrong event identity');
      const m=event.data?.message,contactId=m?.contactId||m?.sender?.contactId;
      if(m&&c.allowedContacts?.length&&!c.allowedContacts.includes(contactId))return;
      if(event.type==='message.status'){
        await workerCall('/messages/receipt-event',{connectionId:c.id,eventId:event.eventId,messageId:event.data.messageId,state:event.data.status});return;
      }
      if(!['message.received','message.self_sent'].includes(event.type)||!m||m.roomId||m.historical)return;
      if(event.type==='message.received'&&(m.type!=='text'||m.content?.type!=='text'||!m.content?.text?.trim()))return;
      if(!m.messageId||!contactId||m.accountId&&m.accountId!==c.accountId)throw Error('Invalid message identity');
      // Different event IDs carrying the same platform message remain one input.
      await workerCall('/messages',{connectionId:c.id,eventId:'msg-'+m.messageId,actorId:contactId,conversationId:contactId,externalUserId:m.externalUserId||m.sender?.externalUserId||'',displayName:m.sender?.name||m.contactId,text:m.content?.text||'',occurredAt:m.sentAt||event.occurredAt,...(event.type==='message.self_sent'?{kind:'human'}:{})});
  }
  return{
    async identity({contactId}, {signal}={}){
      if(!contactId||(c.allowedContacts?.length&&!c.allowedContacts.includes(contactId)))throw Error('Contact unavailable');
      const result=await request('GET','/contacts/resolve?'+new URLSearchParams({contactId}),undefined,undefined,signal);
      const matches=(result.items||[]).filter(x=>x.accountId===c.accountId&&x.contactId===contactId);
      if(matches.length!==1||matches[0].idMapping?.status!=='ready'||!matches[0].externalUserId)throw Error('Contact identity unavailable');
      return {externalUserId:matches[0].externalUserId};
    },
    async describe(){
      let cursor='',found;const seen=new Set();
      for(let page=0;page<100;page++){
        const result=await request('GET','/accounts?'+new URLSearchParams({limit:'200',...(cursor?{cursor}:{})}));
        if(!Array.isArray(result.items))throw Error('Invalid account directory');
        found=result.items.find(a=>(a.accountId||a.id)===c.accountId);if(found)break;
        if(!result.nextCursor)break;
        if(seen.has(result.nextCursor))throw Error('Account cursor did not advance');seen.add(result.nextCursor);cursor=result.nextCursor;
      }
      if(!found)throw Error('Bound account is not in the gateway directory');
      const account=await request('GET','/accounts/'+encodeURIComponent(c.accountId));
      if((account.accountId||account.id)!==c.accountId||account.tenantId&&account.tenantId!==c.tenantId)throw Error('Wrong account identity');
      return{accountId:c.accountId,name:account.name||account.displayName||account.remark||found.name||found.displayName||found.remark||c.accountId,status:account.status,sendEnabled:account.sendEnabled===true};
    },
    async ingest(raw,headers){return ingestEvent(verifyGateway(c,raw,headers))},
    async poll(signal){
      const stateKey='gateway-'+c.id;
      while(!signal.aborted){
        try{
          const state=await workerCall('/connector-state?id='+encodeURIComponent(stateKey));
          const head=await request('GET','/events?'+new URLSearchParams({accountId:c.accountId,limit:'200'}));
          if(!state.head){const newest=head.items?.[0]?.event?.eventId;await workerCall('/connector-state',{id:stateKey,value:{head:newest||'__empty__'}})}
          else{
            let batch=head,cursor='',found=false;const fresh=[];const newest=head.items?.[0]?.event?.eventId;
            for(let page=0;page<30;page++){
              for(const item of batch.items||[]){if(item.event?.eventId===state.head){found=true;break}if(item.event)fresh.push(item.event)}
              if(found||!batch.nextCursor){if(!batch.nextCursor&&state.head==='__empty__')found=true;break;}
              if(batch.nextCursor===cursor)throw Error('Event cursor did not advance');cursor=batch.nextCursor;
              batch=await request('GET','/events?'+new URLSearchParams({accountId:c.accountId,limit:'200',cursor}));
            }
            if(!found)throw Error('Event checkpoint unavailable; keeping original watermark');
            for(const event of fresh.reverse()){if(event.tenantId!==c.tenantId)throw Error('Wrong event tenant');if(event.accountId===c.accountId)await ingestEvent(event)}
            if(newest&&newest!==state.head)await workerCall('/connector-state',{id:stateKey,value:{head:newest}});
          }
        }catch{if(!signal.aborted)console.error('Gateway event read deferred; original cursor retained')}
        if(!signal.aborted)await new Promise(resolve=>{const timer=setTimeout(done,2000);function done(){clearTimeout(timer);signal.removeEventListener('abort',done);resolve()}signal.addEventListener('abort',done,{once:true})});
      }
    },
    async send({idempotencyKey,target,text}){
      if(!target.actorId||target.actorId!==target.conversationId||!target.replyTo||!text||(c.allowedContacts?.length&&!c.allowedContacts.includes(target.actorId)))return{state:'failed',error:'即时应答必须绑定原私聊消息。'};
      try{return receipt(await request('POST','/messages',{accountId:c.accountId,to:{contactId:target.actorId},type:'text',content:{text},mode:'immediate'},idempotencyKey))}catch(error){return error.definite?{state:'failed',error:'网关明确拒绝发送：'+error.code}:ambiguous()}
    },
    async receipt({receipt:id}){
      if(!id)return ambiguous();
      try{const v=await request('GET','/messages/'+encodeURIComponent(id));if(v.accountId!==c.accountId)return ambiguous();return receipt(v)}catch{return{...ambiguous(),receipt:id}}
    }
  };
}
