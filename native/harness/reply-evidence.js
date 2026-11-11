import { z } from 'zod';

// A bounded projection of the original Session log, never another conversation.
// It records tool observations, not model thinking or generated chat summaries.
export const replyEvidenceProjection = {
  key:'workbenchReplyEvidence',stateVersion:2,
  stateSchema:z.object({active:z.string(),rounds:z.array(z.object({runId:z.string(),calls:z.array(z.object({id:z.string(),name:z.string(),input:z.string(),output:z.string(),failed:z.boolean()}))}))}),
  init:()=>({active:'',rounds:[]}),
  apply(state,event){
    if(event.type==='turn/start'||event.type==='turn/end')return{...state,active:''};
    // Inbox splices also record queue removals. Only the committed user
    // message identifies the input that the loop actually presents to tools.
    if(event.type==='user/message'){
      const source=event.data.source;
      if(source?.kind==='workbench-incoming'&&source.runId){
        const id=source.runId;
        return{active:id,rounds:state.rounds.some(r=>r.runId===id)?state.rounds:[...state.rounds,{runId:id,calls:[]}].slice(-100)};
      }
      if(['user','workbench-owner','workbench-delegation','workbench-result'].includes(source?.kind))return{...state,active:''};
    }
    if(!state.active)return state;
    if(event.type==='tool/call')return{...state,rounds:state.rounds.map(r=>r.runId!==state.active?r:{...r,calls:[...r.calls,{id:event.data.callId,name:event.data.name,input:typeof event.data.arguments==='string'?event.data.arguments.slice(0,6000):JSON.stringify(event.data.arguments).slice(0,6000),output:'',failed:false}].slice(-64)})};
    if(event.type==='tool/result'){
      // Context pruning replaces the model-facing copy of an existing result.
      if(event.surfaceOp?.op==='replace')return state;
      const message=event.data.message;
      const output=(message.content||[]).filter(c=>c.type==='text').map(c=>c.text).join('\n').slice(0,16000);
      return{...state,rounds:state.rounds.map(r=>r.runId!==state.active?r:{...r,calls:r.calls.map(c=>c.id!==message.toolCallId?c:{...c,output,failed:!!message.isError})})};
    }
    return state;
  },
};
