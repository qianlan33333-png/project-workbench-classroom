import React, { useState } from "react";
import { ChevronDown, ChevronRight, Clock3, ListChecks } from "lucide-react";
import { api, stamp } from "./api";
import { RichText } from "./file-ui";

type Followup = {
  id: string;
  title: string;
  goal: string;
  about: string;
  state: string;
  version: number;
  nextAt?: number;
  decisionId?: string;
  evidence?: string;
  error?: string;
  reviewCount: number;
};
const states: Record<string, string> = {
  pending: "待跟进", waiting_decision: "等你决定", scheduled: "待复查",
  queued: "已排队", running: "核对中", needs_review: "待核实",
  completed: "已完成", cancelled: "已取消",
};
const closed = (f: Followup) => ["completed", "cancelled"].includes(f.state);

export function FollowupsPanel({ items, decisions, refresh, renderDecision, compact = false }: {
  compact?: boolean;
  items: Followup[];
  decisions: any[];
  refresh: () => Promise<void>;
  renderDecision: (decision: any) => React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [confirmCancel, setConfirmCancel] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState("");
  const active = items.filter((f) => !closed(f));
  const history = items.filter(closed);
  const waiting = active.filter((f) => f.state === "waiting_decision").length;
  const priority = (f: Followup) => ({ waiting_decision: 0, needs_review: 1, running: 2, queued: 3, scheduled: 4 }[f.state] ?? 5);
  const act = async (f: Followup, action: "continue" | "cancel") => {
    setBusy(f.id); setError("");
    try {
      await api(`/personal/followups/${f.id}/${action}`, { version: f.version });
      setConfirmCancel(null);
      await refresh();
    } catch (e: any) {
      setError(e.status === 409 ? "跟进状态已有更新，已为你刷新，请核对后再操作。" : e.status === 423 ? "关联任务仍在执行，请结束后再继续。" : e.message);
      await refresh().catch(() => {});
    } finally { setBusy(null); }
  };
  const row = (f: Followup) => {
    const detail = expanded === f.id;
    const decision = decisions.find((d) => d.id === f.decisionId);
    const progress = <>
      <small>完成条件</small><p>{f.goal}</p>
      {f.about && <><small>目前进展</small><RichText text={f.about} /></>}
      {f.evidence && <><small>核实依据</small><RichText text={f.evidence} /></>}
    </>;
    return <article className="followup-item" aria-label={f.title} key={f.id}>
      <button className="followup-heading" aria-expanded={detail} aria-controls={`followup-${f.id}`}
        onClick={() => { setExpanded(detail ? null : f.id); setConfirmCancel(null); }}>
        {detail ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
        <span className="followup-title">{f.title}</span>
        <span className={`followup-state state-${f.state}`}>{states[f.state] || f.state}</span>
      </button>
      {f.nextAt && f.state === "scheduled" ? <small className="followup-time"><Clock3 size={13} />下次核对 · {stamp(f.nextAt)}（北京时间）</small> : null}
      {detail && <div className="followup-detail" id={`followup-${f.id}`}>
        {f.state === "waiting_decision" && decision ? <>
          {renderDecision(decision)}
          <details className="followup-progress"><summary>进展与完成条件</summary>{progress}</details>
        </> : <>{progress}{decision && renderDecision(decision)}</>}
        {f.error && <p className="followup-error" role="status">{f.error}</p>}
        {!closed(f) && <div className="followup-actions">
          {["pending", "needs_review", "scheduled"].includes(f.state) && <button disabled={busy === f.id} onClick={() => void act(f, "continue")}>
            {busy === f.id ? "正在提交…" : f.state === "scheduled" ? "立即核实" : "继续核实"}
          </button>}
          {confirmCancel !== f.id ? <button className="followup-cancel" disabled={busy === f.id} onClick={() => setConfirmCancel(f.id)}>取消跟进</button> :
            <div className="followup-confirm" role="group" aria-label="确认取消跟进">
              <p>取消后不再自动复查，已完成的操作会保留。</p>
              <button disabled={busy === f.id} onClick={() => void act(f, "cancel")}>确认取消</button>
              <button disabled={busy === f.id} onClick={() => setConfirmCancel(null)}>保留跟进</button>
            </div>}
        </div>}
      </div>}
    </article>;
  };
  if (compact && !active.length && !history.length && !open) return null;
  return <section className={`followup-panel ${compact ? "compact" : ""}`} aria-label="持续跟进">
    <button className="followup-toggle" aria-expanded={open} aria-controls="today-followups" onClick={() => setOpen(!open)}>
      <span><ListChecks size={17} /><strong>持续跟进</strong><small>{active.length ? `${active.length} 项进行中` : "暂无进行中事项"}</small></span>
      <span>{waiting > 0 && <em>{waiting} 项等你决定</em>}{open ? <ChevronDown size={16} /> : <ChevronRight size={16} />}</span>
    </button>
    {open && <div id="today-followups" className="followup-list">
      {error && <p className="followup-error" role="alert">{error}</p>}
      {!active.length && <p className="followup-empty">可以在对话里说“帮我持续跟进这件事”或“下午再核对一次”。</p>}
      {[...active].sort((a, b) => priority(a) - priority(b)).map(row)}
      {history.length > 0 && <details className="followup-history"><summary>已结束 · {history.length} 项</summary>{history.map(row)}</details>}
    </div>}
  </section>;
}
