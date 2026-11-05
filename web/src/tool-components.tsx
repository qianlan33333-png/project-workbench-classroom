import { ComponentRunConfiguration } from "./component-run-config";
import React, { useEffect, useRef, useState } from "react";
import { api, type Project } from "./api";
import { useSurfaceGuard } from "./surface-ui";

type Action = { name: string; description: string; requiresOwner?:boolean; readOnly?:boolean };
type Spec = { key?:string; version: string; actions: Action[]; statusAction?: string; lookupAction?: string; cancelAction?: string };
type Plugin = { id: string; projectId: string; name: string; description: string; url: string; enabled: boolean; hasSecrets: boolean; component: Spec };
type Call = { id: string; pluginId: string; name: string; version: string; action: string; state: string; summary: string; error?: string; result?: unknown; jobRef?: string; arguments?:unknown };
const terminal = (s: string) => ["succeeded", "failed", "cancelled"].includes(s);
const labels: Record<string, string> = { awaiting_owner:"待本人确认", ready:"待提交", submitting: "提交中", unknown: "需查回确认", queued: "排队中", running: "运行中", succeeded: "已完成", failed: "失败", cancelled: "已取消" };

export function ToolComponents({ notify }: { notify: (s: string) => void }) {
  const [plugins, setPlugins] = useState<Plugin[]>([]), [projects, setProjects] = useState<Project[]>([]);
  const [draft, setDraft] = useState<any>(null), [error, setError] = useState(""), [busy, setBusy] = useState(false);
  useSurfaceGuard(!!draft, "放弃尚未保存的工具组件配置？");
  const load = async () => {
    const [ps, projects] = await Promise.all([api<Plugin[]>("/plugins"), api<Project[]>("/projects")]);
    setPlugins(ps.filter(p => p.component)); setProjects(projects.filter(p => !p.archived));
  };
  useEffect(() => { void load().catch(e => setError(e.message)); }, []);
  const edit = (p?: Plugin) => {
    setError("");
    setDraft({ key:p?.component.key||"", id: p?.id || "", projectId: p?.projectId || "", name: p?.name || "", description: p?.description || "", url: p?.url || "", version: p?.component.version || "1.0.0", actions: p?.component.actions.map(a => `${a.requiresOwner ? "!" : a.readOnly ? "?" : ""}${a.name} ${a.description}`).join("\n") || "", statusAction: p?.component.statusAction || "", lookupAction: p?.component.lookupAction || "", cancelAction: p?.component.cancelAction || "", headers: "", enabled: p?.enabled !== false });
  };
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setError("");
    try {
      const actions = draft.actions.split("\n").map((s: string) => s.trim()).filter(Boolean).map((s: string) => { const [name, ...rest] = s.split(/\s+/); return { name:name.replace(/^[!?]/,""), requiresOwner:name.startsWith("!"), readOnly:name.startsWith("?"), description: rest.join(" ") }; });
      await api("/plugins", { id: draft.id, name: draft.name, description: draft.description, projectId: draft.projectId, transport: "http", url: draft.url, enabled: draft.enabled, ...(draft.headers.trim() ? { headers: JSON.parse(draft.headers) } : {}), component: { key:draft.key||draft.name, version: draft.version, actions, statusAction: draft.statusAction, lookupAction: draft.lookupAction, cancelAction: draft.cancelAction } });
      setDraft(null); await load(); notify("工具组件已保存，尚未执行任何任务");
    } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };
  const change = async (p: Plugin, remove = false) => {
    if (remove && !window.confirm("移除此组件连接？插件自己的数据不会删除。在途任务须先核查。")) return;
    setBusy(true); setError("");
    try { if (remove) await api(`/plugins/${p.id}`, undefined, "DELETE"); else await api("/plugins", { ...p, enabled: p.enabled === false }); await load(); }
    catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };
  return <section className="settings-section tool-components">
    <div className="row"><h3>工具组件</h3><button disabled={busy} onClick={() => edit()}>接入组件</button></div>
    <p className="muted">选择一个项目并登记允许动作。组件负责自己的数据和后台任务。</p>
    {error && <p role="alert">{error}</p>}
    {draft && <form className="tool-form" onSubmit={save}>
      <label>所属项目<select required value={draft.projectId} disabled={!!draft.id} onChange={e => setDraft({ ...draft, projectId: e.target.value })}><option value="">请选择项目</option>{projects.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></label>
      {[["name", "组件名称"], ["key", "组件标识（专家引用）"], ["description", "用途说明"], ["url", "HTTPS MCP 地址"], ["version", "固定版本"]].map(([key, label]) => <label key={key}>{label}<input required={key !== "description"} value={draft[key]} onChange={e => setDraft({ ...draft, [key]: e.target.value })} /></label>)}
      <label>允许动作（每行：标识 用途；!表示本人确认，?表示纯读取）<textarea required rows={3} value={draft.actions} onChange={e => setDraft({ ...draft, actions: e.target.value })} /></label>
      <details><summary>后台任务与凭据</summary>
        <p className="muted">即时工具可留空。后台工具需要同时登记状态和查回动作。</p>
        {[["statusAction", "查询状态动作"], ["lookupAction", "按操作编号查回动作"], ["cancelAction", "取消动作（可选）"]].map(([key, label]) => <label key={key}>{label}<input value={draft[key]} onChange={e => setDraft({ ...draft, [key]: e.target.value })} /></label>)}
        <label>请求头（JSON；留空保留已有凭据）<textarea autoComplete="off" value={draft.headers} onChange={e => setDraft({ ...draft, headers: e.target.value })} /></label>
      </details>
      <label><input type="checkbox" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} />启用后允许本项目调用</label>
      <div className="row"><button disabled={busy} type="submit">保存组件</button><button disabled={busy} type="button" onClick={() => { if (window.confirm("放弃未保存配置？")) setDraft(null); }}>取消</button></div>
    </form>}
    {!plugins.length && !draft && <p className="muted">尚未接入工具组件。</p>}
    {plugins.map(p => <div className="setting-row" key={p.id}><div><strong>{p.name}</strong><p>{p.description} · {p.component.version} · {p.enabled === false ? "已停用" : "已启用"}</p><small>{projects.find(x => x.id === p.projectId)?.name || "所属项目"} · {p.component.actions.map(a => a.name).join("、")}{p.hasSecrets ? " · 已配置凭据" : ""}</small></div><div className="tool-actions"><button disabled={busy} onClick={() => edit(p)}>配置</button><button disabled={busy} onClick={() => void change(p)}>{p.enabled === false ? "启用" : "停用"}</button><button disabled={busy} onClick={() => void change(p, true)}>移除</button></div></div>)}
  </section>;
}

export function SessionComponents({ sessionId, projectId, notify }: { sessionId: string; projectId: string; notify: (s: string) => void }) {
  const [plugins, setPlugins] = useState<Plugin[]>([]), [calls, setCalls] = useState<Call[]>([]), [open, setOpen] = useState(false);
  const [pluginId, setPluginId] = useState(""), [action, setAction] = useState(""), [input, setInput] = useState("{}"), [busy, setBusy] = useState(false), [error, setError] = useState("");
  const operation = useRef(crypto.randomUUID()), epoch = useRef(0);
  useSurfaceGuard(open && input !== "{}", "放弃未提交的工具参数？");
  useEffect(() => {
    const n = ++epoch.current; setCalls([]); setPlugins([]); setPluginId(""); setAction(""); setInput("{}"); setOpen(false); setError(""); operation.current = crypto.randomUUID();
    let live = true;
    const load = async () => { try { const [ps, cs] = await Promise.all([api<Plugin[]>(`/projects/${projectId}/components`), api<Call[]>(`/sessions/${sessionId}/component-calls`)]); if (live && epoch.current === n) { setPlugins(ps); setCalls(cs); } } catch (e) { if (live) setError((e as Error).message); } };
    void load(); const timer = setInterval(() => void load(), 5000);
    return () => { live = false; clearInterval(timer); };
  }, [sessionId, projectId]);
  const plugin = plugins.find(p => p.id === pluginId);
  const run = async () => {
    const n = epoch.current; setBusy(true); setError("");
    try {
      const args = JSON.parse(input); if (!args || Array.isArray(args) || typeof args !== "object") throw new Error("工具参数需要JSON对象");
      const required=plugin?.component.actions.find(a=>a.name===action)?.requiresOwner;const confirmed=required?window.confirm("确认执行此操作？它可能写回原系统或触发业务动作，请核对所选动作与参数。"):false;if(required&&!confirmed)return;
 const c = await api<Call>(`/sessions/${sessionId}/component-calls`, { pluginId, action, arguments: args, operationKey: operation.current,ownerConfirmed:confirmed });
      if (n !== epoch.current) return;
      setCalls(old => [c, ...old.filter(x => x.id !== c.id)]); operation.current = crypto.randomUUID(); setOpen(false); setInput("{}"); notify("组件结果已关联当前对话");
    } catch (e) { if (n === epoch.current) setError((e as Error).message); } finally { if (n === epoch.current) setBusy(false); }
  };
  const refresh = async (c: Call, cancel: boolean) => {
    const n = epoch.current; setBusy(true); setError("");
    try { const updated = await api<Call>(`/component-calls/${c.id}/${cancel ? "cancel" : "refresh"}`, {}); if (n === epoch.current) setCalls(old => old.map(x => x.id === c.id ? updated : x)); }
    catch (e) { if (n === epoch.current) setError((e as Error).message); } finally { if (n === epoch.current) setBusy(false); }
  };
  if (!plugins.length && !calls.length && !error) return null;
  return <section className="session-components">
    <ComponentRunConfiguration sessionId={sessionId} projectId={projectId} plugins={plugins} notify={notify}/>
    <button onClick={() => setOpen(!open)} aria-expanded={open}>工具组件{calls.filter(c => !terminal(c.state)).length ? ` · ${calls.filter(c => !terminal(c.state)).length}项待完成` : ""}</button>
    {open && <div className="tool-form"><label>组件<select value={pluginId} onChange={e => { setPluginId(e.target.value); setAction(""); }}><option value="">选择组件</option>{plugins.filter(p => p.enabled !== false).map(p => <option key={p.id} value={p.id}>{p.name} · {p.component.version}</option>)}</select></label><label>动作<select value={action} onChange={e => setAction(e.target.value)}><option value="">选择动作</option>{plugin?.component.actions.map(a => <option key={a.name} value={a.name}>{a.description || a.name}</option>)}</select></label><label>参数（JSON）<textarea rows={3} value={input} onChange={e => setInput(e.target.value)} /></label><button disabled={busy || !pluginId || !action} onClick={() => void run()}>执行组件</button><p className="muted">专家也可在当前对话中通过 list_components 和 call_component 调用。</p></div>}
    {error && <p role="alert">{error}</p>}
    {calls.map(c => <details key={c.id} className="component-result"><summary>{c.name} · {labels[c.state] || c.state} · {c.summary}</summary><p>{c.action} · {c.version}</p>{c.error && <p role="alert">{c.error}</p>}{c.state==='awaiting_owner'&&<><p>确认前请核对以下动作参数：</p><pre>{JSON.stringify(c.arguments,null,2)}</pre></>}{!terminal(c.state) && <div className="row">{c.state==='awaiting_owner'&&<button disabled={busy} onClick={()=>{if(window.confirm("确认按此任务保存的动作和参数执行？"))void api<Call>(`/component-calls/${c.id}/confirm`,{}).then(v=>setCalls(old=>old.map(x=>x.id===c.id?v:x))).catch(e=>setError(e.message))}}>本人确认执行</button>}<button disabled={busy} onClick={() => void refresh(c, false)}>刷新状态</button>{(c.state==='awaiting_owner'||c.state==='ready'||(c.jobRef&&plugins.find(p => p.id === c.pluginId)?.component.cancelAction)) && <button disabled={busy} onClick={() => { if (window.confirm("请求停止此组件任务？以组件返回结果为准。")) void refresh(c, true); }}>请求停止</button>}</div>}{c.result !== undefined && <pre>{JSON.stringify(c.result, null, 2)}</pre>}</details>)}
  </section>;
}
