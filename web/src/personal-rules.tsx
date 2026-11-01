import { useEffect, useRef, useState } from "react";
import { X } from "lucide-react";
import { api, stamp } from "./api";
import { RichText } from "./file-ui";

type Rules = { version: number; soul: string; agent: string; updatedAt: number };
const draftKey = "pwb.personalRulesDraft";

export function PersonalRulesPanel({ close, notify }: { close: () => void; notify: (s: string) => void }) {
  const [saved, setSaved] = useState<Rules | null>(null);
  const [draft, setDraft] = useState<Rules | null>(null);
  const [tab, setTab] = useState<"soul" | "agent">("soul");
  const [preview, setPreview] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<"close" | "reload" | null>(null);
  const dialog = useRef<HTMLElement>(null);
  const dirty = !!draft && !!saved && (draft.soul !== saved.soul || draft.agent !== saved.agent);
  const conflict = !!draft && !!saved && draft.version !== saved.version;
  const load = async (restore: boolean) => {
    try {
      const r = await api<Rules>("/personal/rules");
      setSaved(r);
      let d = r;
      if (restore) {
        try {
          const v = JSON.parse(sessionStorage.getItem(draftKey) || "null");
          if (v && typeof v.soul === "string" && typeof v.agent === "string" && typeof v.version === "number") d = v;
        } catch { /* A corrupt browser draft must not hide the saved documents. */ }
      }
      setDraft(d); setError(""); setConfirm(null);
    } catch (e: any) { setError(e.message); }
  };
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    dialog.current?.focus();
    void load(true);
    return () => previous?.focus();
  }, []);
  useEffect(() => {
    if (dirty) sessionStorage.setItem(draftKey, JSON.stringify(draft));
    else if (saved) sessionStorage.removeItem(draftKey);
  }, [dirty, draft, saved]);
  useEffect(() => {
    const protect = (e: BeforeUnloadEvent) => { if (dirty) { e.preventDefault(); e.returnValue = ""; } };
    window.addEventListener("beforeunload", protect);
    return () => window.removeEventListener("beforeunload", protect);
  }, [dirty]);
  const save = async (finish = false) => {
    if (!draft) return;
    setBusy(true); setError("");
    try {
      const r = await api<Rules>("/personal/rules", draft, "PUT");
      setSaved(r); setDraft(r); setConfirm(null); sessionStorage.removeItem(draftKey);
      notify("规则已保存，从下一轮执行起生效；原有对话继续保留。");
      if (finish) close();
    } catch (e: any) {
      setError(e.status === 409 ? "其他页面已保存新版本。当前草稿已保留，请先复制或下载，再读取最新版本合并。" : e.message);
    } finally { setBusy(false); }
  };
  const requestClose = () => { if (!busy) dirty ? setConfirm("close") : close(); };
  const name = tab === "soul" ? "SOUL.md" : "AGENT.md";
  return <div className="today-modal-backdrop">
    <section ref={dialog} tabIndex={-1} className="today-modal personal-rules" role="dialog" aria-modal="true" aria-label="个人助手规则" onKeyDown={e => {
      if (e.key === "Escape") { e.preventDefault(); requestClose(); }
      if (e.key === "Tab") {
        const nodes = [...(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), textarea, a[href]') || [])];
        const first = nodes[0], last = nodes.at(-1);
        if (e.shiftKey && (document.activeElement === first || document.activeElement === dialog.current)) { e.preventDefault(); last?.focus(); }
        else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first?.focus(); }
      }
    }}>
      <header><h2>个人助手规则</h2><button aria-label="关闭个人助手规则" disabled={busy} onClick={requestClose}><X size={18}/></button></header>
      <p className="muted">每日建议、今天对话、微信和飞书共用。你决定规则，助手只能提出修改建议。项目保持只读。</p>
      {!draft ? <p role="status">{error || "正在读取规则…"}<button onClick={() => void load(true)}>重新读取</button></p> : <>
        <nav className="rules-tabs" aria-label="规则文档">
          <button aria-pressed={tab === "soul"} onClick={() => setTab("soul")}>SOUL.md · 怎样陪我</button>
          <button aria-pressed={tab === "agent"} onClick={() => setTab("agent")}>AGENT.md · 怎样合作</button>
        </nav>
        <div className="rules-toolbar"><small>v{draft.version} · {stamp(draft.updatedAt)}{dirty ? " · 有未保存修改" : " · 已保存"}</small>
          <button aria-pressed={preview} onClick={() => setPreview(!preview)}>{preview ? "编辑正文" : "预览正文"}</button>
          <button onClick={() => {
            const u = URL.createObjectURL(new Blob([draft[tab]], { type: "text/markdown;charset=utf-8" }));
            const a = document.createElement("a"); a.href = u; a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(u), 1000);
          }}>下载 {name}</button>
        </div>
        {preview ? <div className="rules-preview"><RichText text={draft[tab]}/></div> : <textarea disabled={busy} aria-label={`${name} 正文`} spellCheck={false} value={draft[tab]} onChange={e => setDraft({ ...draft, [tab]: e.target.value })}/>}
        {(error || conflict) && <p role="alert">{error || "已保存版本发生变化，浏览器草稿已保留。请下载或复制草稿，再读取最新版本合并。"}</p>}
        <footer><button className="primary" disabled={busy || !dirty || conflict} onClick={() => void save()}>{busy ? "保存中…" : "保存两份规则"}</button>
          <button disabled={busy} onClick={() => dirty ? setConfirm("reload") : void load(false)}>读取最新版本</button>
          <small>下一轮生效，进行中的回答保持原版本。</small>
        </footer>
        {confirm && <div role="alert" className="rules-confirm"><p>还有未保存修改，如何处理？</p>
          <button disabled={busy || conflict} onClick={() => void save(confirm === "close")}>保存{confirm === "close" ? "并关闭" : ""}</button>
          <button disabled={busy} onClick={() => { sessionStorage.removeItem(draftKey); if (confirm === "close") close(); else void load(false); }}>放弃修改{confirm === "close" ? "并关闭" : "并读取"}</button>
          <button disabled={busy} onClick={() => setConfirm(null)}>继续编辑</button>
        </div>}
      </>}
    </section>
  </div>;
}
