import React,{useEffect,useState} from "react";
import {api,type Session,type Run} from "./api";
type Source={sourceId:string;name:string};
export function ProjectHistory({projectId}:{projectId:string}) {
 const [sources,setSources]=useState<Source[]>([]),[sessions,setSessions]=useState<Session[]>([]),[selected,setSelected]=useState(""),[runs,setRuns]=useState<Run[]>([]),[error,setError]=useState("");
 useEffect(()=>{let live=true;setSources([]);setSessions([]);setSelected("");setRuns([]);void Promise.all([api<Source[]>(`/projects/${projectId}/history-sources`),api<Session[]>(`/projects/${projectId}/history-sessions`)]).then(([a,b])=>{if(live){setSources(a);setSessions(b)}}).catch(e=>{if(live)setError(e.message)});return()=>{live=false}},[projectId]);
 useEffect(()=>{let live=true;setRuns([]);if(selected)void api<Run[]>(`/projects/${projectId}/history-runs?sessionId=${encodeURIComponent(selected)}`).then(v=>{if(live)setRuns(v)}).catch(e=>{if(live)setError(e.message)});return()=>{live=false}},[projectId,selected]);
 if(!sources.length&&!error)return null;
 return <details className="project-history"><summary>合并前的记录与配置</summary><p className="muted">保留原对话身份，只读查看。配置与文件快照在本项目文件的“历史来源”目录。</p>{error&&<p role="alert">{error}</p>}<label>历史对话<select value={selected} onChange={e=>setSelected(e.target.value)}><option value="">请选择原对话</option>{sessions.map(s=><option key={s.id} value={s.id}>{sources.find(x=>x.sourceId===s.projectId)?.name} · {s.title}</option>)}</select></label>{selected&&<div className="history-records">{runs.length===0&&<p>此对话暂无执行记录。</p>}{runs.map(r=><article key={r.id}><strong>{new Date(r.createdAt).toLocaleString()} · {r.state}</strong><p className="history-text">{r.text}</p><p className="history-text">{r.result||r.error||"尚无结果"}</p></article>)}</div>}</details>
}
