import React, { useEffect, useState } from 'react';
import { api, type Expert, type Session, type Project } from './api';
import { useSurfaceGuard } from './surface-ui';
type Plugin={id:string;name:string;enabled:boolean;component:{key?:string;version:string;actions:{name:string;description:string;requiresOwner?:boolean;readOnly?:boolean}[]}};
type Config={mode?:string;goal?:string;sessionId:string;revision:number;pluginId:string;pluginVersion:string;action:string;arguments:Record<string,unknown>;expertId:string;expertVersion:string;ruleVersion:string};
export function ComponentRunConfiguration({sessionId,projectId,plugins,notify}:{sessionId:string;projectId:string;plugins:Plugin[];notify:(s:string)=>void}){
 const [mode,setMode]=useState('program'),[goal,setGoal]=useState(''),[cfg,setCfg]=useState<Config|null>(null),[pluginId,setPluginId]=useState(''),[action,setAction]=useState(''),[expertId,setExpertId]=useState(''),[experts,setExperts]=useState<Expert[]>([]),[input,setInput]=useState('{}'),[base,setBase]=useState(''),[rule,setRule]=useState(''),[cron,setCron]=useState('0 2 * * *'),[schedule,setSchedule]=useState<any>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false);
 const catalogKey=plugins.map(p=>p.id).sort().join(',');
 const state=JSON.stringify({pluginId,action,expertId,input,rule,mode,goal});useSurfaceGuard(!!base&&base!==state,'放弃尚未保存的运行配置？');
 useEffect(()=>{let live=true;void(async()=>{try{
  const [ss,p,all,scs]=await Promise.all([api<Session>(`/sessions/${sessionId}`),api<Project>(`/projects/${projectId}`),api<Expert[]>('/experts'),api<any[]>(`/schedules?projectId=${projectId}`)]);
  let saved:Config|null=null;try{saved=await api<Config>(`/sessions/${sessionId}/component-config`)}catch(e){if(!(e as Error).message.includes('找不到'))throw e}
  if(!live)return;const available=all.filter(e=>p.expertIds.includes(e.id));setExperts(available);const ex=available.find(e=>e.id===(saved?.expertId||ss.expertId||p.defaultExpertId));
  const selected=plugins.find(x=>x.id===saved?.pluginId)||plugins.find(x=>ex?.componentTools?.includes(x.component.key||x.id));
  const a=saved?.action||selected?.component.actions.find(a=>!a.requiresOwner)?.name||'';const text=JSON.stringify(saved?.arguments||{},null,2);const ei=ex?.id||'';const rv=saved?.ruleVersion||'';
  setCfg(saved);setMode(saved?.mode||"program");setGoal(saved?.goal||"");setPluginId(saved?.pluginId||selected?.id||'');setAction(a);setExpertId(ei);setInput(text);setRule(rv);setBase(JSON.stringify({pluginId:saved?.pluginId||selected?.id||'',action:a,expertId:ei,input:text,rule:rv,mode:saved?.mode||"program",goal:saved?.goal||""}));const sc=scs.find(x=>x.component&&x.sessionId===sessionId);setSchedule(sc||null);if(sc)setCron(sc.cron);
 }catch(e){if(live)setError((e as Error).message)}})();return()=>{live=false}},[sessionId,projectId,catalogKey]);
 const plugin=plugins.find(p=>p.id===pluginId);
 const save=async(upgrade=false)=>{if(!plugin)throw new Error('请选择可用组件');const expert=experts.find(e=>e.id===expertId);if(cfg&&cfg.pluginId===pluginId&&cfg.pluginVersion!==plugin.component.version&&!upgrade)throw new Error("组件版本已变化，请明确更新版本后保存");const parameters=JSON.parse(input);if(!parameters||Array.isArray(parameters)||typeof parameters!=='object')throw new Error('参数需要JSON对象');const v=await api<Config>(`/sessions/${sessionId}/component-config`,{sessionId,mode,goal,revision:cfg?.revision||0,pluginId,pluginVersion:plugin.component.version,action,arguments:parameters,expertId,expertVersion:!upgrade&&cfg?.expertId===expertId?cfg.expertVersion:(expert?.digest||''),ruleVersion:rule},'PUT');setCfg(v);setBase(state);return v};
 const perform=async(kind:'save'|'run'|'schedule')=>{setBusy(true);setError('');try{
  let v=cfg;if(kind==='save')v=await save();else if(!cfg||state!==base)throw new Error('请先保存配置变更，再执行或启用定时');if(!v)throw new Error('请先保存运行配置');
  if(kind==='run')await api(`/sessions/${sessionId}/component-config/run`,{operationKey:crypto.randomUUID()});
  if(kind==='schedule'){const sc=await api<any>('/schedules',{...(schedule||{}),projectId,sessionId,expertId:v.expertId,component:v,name:'组件任务：'+(plugin?.name||''),instruction:'按固定组件配置执行，结果返回原会话',timezone:'Asia/Shanghai',cron,enabled:true});setSchedule(sc)}
  notify(kind==='save'?'运行配置已保存，未执行':kind==='run'?'已按固定配置提交':'定时已启用；下一次运行将使用此固定版本');
 }catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 const pause=async()=>{if(!schedule)return;setBusy(true);try{const v=await api<any>(`/schedules/${schedule.id}/enabled`,{enabled:false});setSchedule(v);notify('已暂停后续运行，当前后台任务仍可在结果中管理')}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 return <details className="component-run-config"><summary>运行配置{cfg?` · 第${cfg.revision}版`:' · 尚未保存'}</summary><div className="tool-form">
 <label>执行方式<select value={mode} onChange={e=>setMode(e.target.value)}><option value="program">固定程序执行</option><option value="expert">由专家组织查询与解释</option></select></label>{mode==='expert'&&<label>任务目标<textarea value={goal} onChange={e=>setGoal(e.target.value)}/></label>}
 <label>工作专家<select value={expertId} onChange={e=>setExpertId(e.target.value)}><option value="">沿用固定程序，无需专家判断</option>{experts.map(e=><option key={e.id} value={e.id}>{e.name}</option>)}</select></label>
 <label>默认组件<select value={pluginId} onChange={e=>{setPluginId(e.target.value);setAction('')}}><option value="">请选择</option>{plugins.filter(p=>p.enabled!==false).map(p=><option key={p.id} value={p.id}>{p.name} · {p.component.version}</option>)}</select></label>
 <label>执行动作<select value={action} onChange={e=>setAction(e.target.value)}><option value="">请选择</option>{plugin?.component.actions.filter(a=>!a.requiresOwner&&(mode!=="expert"||(a as any).readOnly)).map(a=><option key={a.name} value={a.name}>{a.description||a.name}</option>)}</select></label>
 <details><summary>参数与规则版本</summary><label>固定参数<textarea rows={5} value={input} onChange={e=>setInput(e.target.value)}/></label><button type="button" disabled={busy} onClick={()=>{if(window.confirm("确认使用当前组件和专家版本保存新修订？在途任务不改变。"))void save(true).then(()=>notify("已明确更新版本")).catch(e=>setError(e.message))}}>使用当前版本保存新修订</button><label>规则版本说明<input value={rule} onChange={e=>setRule(e.target.value)}/></label></details>
 <div className="row"><button disabled={busy||!action} onClick={()=>void perform('save')}>保存运行配置</button><button disabled={busy||!action} onClick={()=>void perform('run')}>按配置执行一次</button></div>
 <details><summary>定时运行{schedule?.enabled?' · 已启用':' · 未启用'}</summary><label>运行时间（五段Cron，上海时间）<input value={cron} onChange={e=>setCron(e.target.value)}/></label><p className="muted">例如 0 2 * * * 为每天02:00。保存配置不会自动启用定时。</p><button disabled={busy||!action} onClick={()=>void perform('schedule')}>保存并启用定时</button>{schedule?.enabled&&<button disabled={busy} onClick={()=>void pause()}>暂停定时</button>}</details>
 {error&&<p role="alert">{error}</p>}</div></details>;
}
