import { z } from 'zod';

// A bounded projection of the original Session log, never another conversation.
// It records tool observations, not model thinking or generated chat summaries.
export const replyEvidenceProjection = {
  key:'workbenchReplyEvidence',stateVersion:1,
  stateSchema:z.object({active:z.string(),rounds:z.array(z.object({runId:z.string(),calls:z.array(z.object({id:z.string(),name:z.string(),input:z.string(),output:z.string(),failed:z.boolean()}))}))}),
  init:()=>({active:'',rounds:[]}),
  apply(state,event){
    if(event.type==='agent/inbox/spliced'){
      const incoming=event.data.inserted.find(m=>m.source?.kind==='workbench-incoming');
      if(!incoming)return{...state,active:''};
      const id=incoming.source.runId;
      return{active:id,rounds:state.rounds.some(r=>r.runId===id)?state.rounds:[...state.rounds,{runId:id,calls:[]}].slice(-100)};
    }
    if(event.type==='turn/end')return{...state,active:''};
    if(!state.active)return state;
    if(event.type==='tool/call')return{...state,rounds:state.rounds.map(r=>r.runId!==state.active?r:{...r,calls:[...r.calls,{id:event.data.callId,name:event.data.name,input:typeof event.data.arguments==='string'?event.data.arguments.slice(0,6000):JSON.stringify(event.data.arguments).slice(0,6000),output:'',failed:false}].slice(-64)})};
    if(event.type==='tool/result'){
      const message=event.data.message;
      const output=(message.content||[]).filter(c=>c.type==='text').map(c=>c.text).join('\n').slice(0,16000);
      return{...state,rounds:state.rounds.map(r=>r.runId!==state.active?r:{...r,calls:r.calls.map(c=>c.id!==message.toolCallId?c:{...c,output,failed:!!message.isError})})};
    }
    return state;
  },
};
