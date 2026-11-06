import React, { useCallback, useEffect, useRef, useState } from "react";
import { api, type Run } from "./api";
import type { TimelineCall } from "./tool-components";

type Step = { name: string; state: string; callId?: string; attempts?: number; at?: number; detail?: string };
export type Workflow = {
  id: string; name: string; source: string; state: string; at: number;
  feedbackAt?: number; summary?: string; summaryFrom?: string; steps: Step[]; canResume?: boolean;
  canRetrySummary?: boolean;
  prior?: {at: number; text: string; from: string}[];
};
const labels: Record<string, string> = {
  pending: "待执行", running: "执行中", waiting: "等待回执",
  needs_check: "待核实", succeeded: "已完成", failed: "失败", cancelled: "已取消", skipped: "已跳过",
};
const date = new Intl.DateTimeFormat("zh-CN", {
  timeZone: "Asia/Shanghai", month: "long", day: "numeric", hour: "2-digit", minute: "2-digit",
  second: "2-digit", hourCycle: "h23",
});

function Time({ at }: { at: number }) {
  return <time title="北京时间" dateTime={new Date(at).toISOString()}>{date.format(at)}</time>;
}

function ArchivedReceipt({ sessionId, workflowId, index }: { sessionId: string; workflowId: string; index: number }) {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const open = async () => {
    setError("");
    try {
      const call = await api<{ summary: string; result?: unknown }>(`/sessions/${sessionId}/workflows/${workflowId}/steps/${index}/receipt`);
      setText(call.result === undefined ? call.summary : JSON.stringify(call.result, null, 2));
    } catch {
      setError("原始回执暂不可读");
    }
  };
  return <details className="component-result" onToggle={e => { if (e.currentTarget.open && !text) void open(); }}>
    <summary>原始回执</summary>
    {text && <pre>{text}</pre>}
    {error && <p role="status">{error}</p>}
  </details>;
}

export function WorkflowConversation({ sessionId, runs, cards, renderRun, quote, notify, followTail }: {
  sessionId: string; runs: Run[]; cards: TimelineCall[];
  renderRun: (run: Run, tools: React.ReactNode[]) => React.ReactNode;
  quote: (w: Workflow) => void; notify: (message: string) => void; followTail: () => void;
}) {
  const [items, setItems] = useState<Workflow[]>([]);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const request = useRef(0);
  const refresh = useCallback(async (before?: number) => {
    const n = ++request.current;
    try {
      const rows = await api<Workflow[]>(`/sessions/${sessionId}/workflows${before ? "?before="+before : ""}`);
      if (n !== request.current) return;
      setError("");
      setItems(old => [...new Map([...old, ...rows].map(v => [v.id, v])).values()]);
    } catch {
      if (n === request.current) setError("工作轮次暂时无法读取");
    }
  }, [sessionId]);
  useEffect(() => {
    if (!sessionId) return;
    void refresh();
    const timer = setInterval(() => void refresh(), 5000);
    return () => { clearInterval(timer); request.current++; };
  }, [refresh, sessionId]);
  const updateWork = async (w: Workflow, action: "resume" | "retry-summary") => {
    setBusy(w.id);
    try {
      request.current++;
      const next = await api<Workflow>(`/sessions/${sessionId}/workflows/${w.id}/${action}`, {});
      request.current++;
      setItems(old => old.map(item => item.id === w.id ? next : item));
      notify(action === "resume" ? "正在接续原轮次" : "正在重新生成反馈");
    } catch (e) {
      notify((e as Error).message);
    } finally { setBusy(""); }
  };
  const visibleRuns = runs.filter(r => !["component_notice", "workflow_summary"].includes(r.kind || ""));
  const runIDs = new Set(visibleRuns.map(r => r.id));
  const workflowIDs = new Set(items.map(w => w.id));
  const byRun = new Map<string, TimelineCall[]>();
  const byCall = new Map(cards.map(c => [c.call.id, c.card]));
  const entries: {key: string; at: number; node: React.ReactNode}[] = [];
  for (const item of cards) {
    const {call, card} = item;
    if (call.workflowId && workflowIDs.has(call.workflowId)) continue;
    const origin = call.runId || call.operationKey?.split(":", 1)[0] || "";
    if (runIDs.has(origin)) {
      const list = byRun.get(origin) || [];
      list.push(item);
      byRun.set(origin, list);
    } else {
      entries.push({key: "component:"+call.id, at: call.createdAt || 0, node: card});
    }
  }
  for (const r of visibleRuns) {
    const tools = (byRun.get(r.id) || []).sort((a,b) => (a.call.createdAt || 0) - (b.call.createdAt || 0));
    entries.push({key:r.id, at:r.createdAt, node:renderRun(r, tools.map(c => c.card))});
  }
  for (const w of items) {
    const anchor = "workflow-" + w.id;
    const done = w.steps.filter(s => s.state === "succeeded").length;
    entries.push({key:anchor, at:w.at, node:
      <article className="workflow-entry" id={anchor} data-workflow-id={w.id}>
        <details>
          <summary><strong>{w.name}</strong> · {labels[w.state] || w.state}
            <span className="workflow-meta"><Time at={w.at}/> · {w.source === "schedule" ? "定时" : "手动"} · {done}/{w.steps.length} 步完成</span>
          </summary>
          <ol className="workflow-steps">
            {w.steps.map((s,i) => <li key={i}>
              <strong>{s.name}</strong> · {labels[s.state] || s.state}
              {s.at && <span className="workflow-meta"><Time at={s.at}/></span>}
              {s.detail && <p>{s.detail}</p>}
              {s.callId && (byCall.get(s.callId) || <ArchivedReceipt key={s.callId} sessionId={sessionId} workflowId={w.id} index={i}/>)}
            </li>)}
          </ol>
        </details>
        <div className="workflow-actions">
          <button type="button" onClick={() => quote(w)}>引用本轮</button>
          {w.canResume && <button type="button" disabled={busy !== ""} onClick={() => void updateWork(w,"resume")}>
            {w.state === "needs_check" ? "继续查回" : "继续失败步骤"}
          </button>}
          {w.canRetrySummary && <button type="button" disabled={busy !== ""} onClick={() => void updateWork(w,"retry-summary")}>重试助手反馈</button>}
        </div>
      </article>});
    const feedback = [...(w.prior || [])];
    if (w.feedbackAt && w.summary) feedback.push({at:w.feedbackAt, text:w.summary, from:w.summaryFrom || "system"});
    for (const result of feedback) {
      entries.push({key:anchor+":feedback:"+result.at, at:result.at, node:
        <article className="workflow-entry workflow-feedback" data-workflow-feedback={w.id}>
          <div><strong>{result.from === "assistant" ? "助手 · 工作反馈" : "工作结果"}</strong> · <Time at={result.at}/></div>
          <p>{result.text}</p>
          <button type="button" onClick={() => document.getElementById(anchor)?.scrollIntoView({block:"center"})}>查看本轮步骤</button>
        </article>});
    }
  }
  const oldest = items.length ? Math.min(...items.map(w => w.at)) : 0;
  entries.sort((a,b) => a.at-b.at || a.key.localeCompare(b.key));
  const last = entries.at(-1)?.key;
  const tail = useRef(followTail);
  tail.current = followTail;
  useEffect(() => { if (last) tail.current(); }, [last]);
  return <>
    {error && <p role="status">{error}</p>}
    {items.length >= 50 && <button className="panel-action" onClick={() => void refresh(oldest)}>加载更早的工作轮次</button>}
    {entries.map(e => <React.Fragment key={e.key}>{e.node}</React.Fragment>)}
  </>;
}
