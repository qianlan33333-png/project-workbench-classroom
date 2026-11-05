import { useEffect, useState } from "react";
import { api, base } from "./api";
import { MessageSquare } from "lucide-react";
export type AgentMessage = {
  fromSessionId: string;
  fromProjectId: string;
  fromRunId: string;
  fromLabel: string;
  rootId: string;
  delivery: string;
  execution: string;
  authorizedAt?: number;
  ownerSent?: boolean;
};
type Receipt = {
  id: string;
  sessionId: string;
  targetProjectId: string;
  targetLabel: string;
  agent: AgentMessage;
  text: string;
  state: string;
  runId?: string;
  error?: string;
  createdAt: number;
};
const states: Record<string, string> = {
  unrouted: "已投递 · 等待入队",
  context: "已投递 · 下轮读取",
  authorization: "已投递 · 等待本人授权",
  queued: "已投递 · 排队中",
  running: "已投递 · 处理中",
  stopping: "正在停止",
  communication_frozen: "兼容保护已冻结",
  waiting: "已投递 · 等待处理",
  completed: "已处理",
  consumed: "已读取",
  failed: "处理失败",
  interrupted: "待核实",
  unknown: "待核实",
  cancelled: "已取消",
  error: "处理失败",
};
export function CommunicationFeed({
  sessionId,
  prepare,
  notify,
}: {
  sessionId: string;
  prepare: (s: string) => void;
  notify: (s: string) => void;
}) {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [rows, setRows] = useState<Receipt[]>([]),
    [error, setError] = useState("");
  useEffect(() => {
    if (!sessionId) return;
    let live = true,
      seq = 0;
    const load = async () => {
      const turn = ++seq;
      try {
        const r = await api<Receipt[]>(
          "/session-messages?sessionId=" + encodeURIComponent(sessionId),
        );
        if (live && turn === seq) {
          setRows(r);
          setError("");
        }
      } catch (e) {
        if (live) setError((e as Error).message);
      }
    };
    void load();
    const t = setInterval(() => void load(), 2500);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [sessionId]);
  const perform = async (id: string, action: string, body: unknown = {}) => {
    try {
      await api(`/session-messages/${id}/${action}`, body);
      setRows(
        await api(
          "/session-messages?sessionId=" + encodeURIComponent(sessionId),
        ),
      );
    } catch (e) {
      notify((e as Error).message);
    }
  };
  const visible = rows.filter(
    (r) =>
      (r.sessionId === sessionId || r.agent.fromSessionId === sessionId) &&
      (r.agent.fromSessionId === sessionId ||
        !r.runId ||
        r.agent.delivery === "context"),
  );
  if (!visible.length && !error) return null;
  return (
    <section className="communication-feed" aria-label="会话通信">
      {error && <small role="status">会话通信暂未更新：{error}</small>}
      {[...visible].reverse().map((r) => {
        const outgoing = r.agent.fromSessionId === sessionId;
        const url =
          base +
          "/#project=" +
          encodeURIComponent(r.agent.fromProjectId) +
          "&session=" +
          encodeURIComponent(r.agent.fromSessionId);
        return (
          <details
            className="communication-card"
            key={r.id}
            open={!!expanded[r.id]}
          >
            <summary
              onClick={(e) => {
                e.preventDefault();
                setExpanded((v) => ({ ...v, [r.id]: !v[r.id] }));
              }}
            >
              <MessageSquare size={15} />
              <span>
                {outgoing
                  ? "发送至 " + r.targetLabel
                  : "来自 " + r.agent.fromLabel}
              </span>
              <small>{states[r.state] || r.state}</small>
            </summary>
            <small>
              {r.agent.ownerSent ? "本人通过接口发送" : "Agent消息"} ·{" "}
              {new Date(r.createdAt).toLocaleString("zh-CN")} · #
              {r.id.slice(0, 8)}
            </small>
            <p className="communication-body">{r.text}</p>
            {r.error && <p role="status">{r.error}</p>}
            <div className="communication-actions">
              <a href={url}>来源会话</a>
              {outgoing && (
                <a
                  href={
                    base +
                    "/#project=" +
                    encodeURIComponent(r.targetProjectId) +
                    "&session=" +
                    encodeURIComponent(r.sessionId)
                  }
                >
                  目标会话
                </a>
              )}
              {!outgoing && (
                <button
                  onClick={() =>
                    prepare(
                      `请向会话 ${r.agent.fromSessionId} 发送回复，replyTo=${r.id}。先根据以下内容编辑我的答复，不要自动确认收到：\n`,
                    )
                  }
                >
                  准备回复
                </button>
              )}
              {r.state === "authorization" && (
                <button
                  onClick={() => {
                    if (
                      window.confirm(
                        "允许目标会话按这份不可变指令执行？这会使用目标原有工具与项目写入能力。",
                      )
                    )
                      void perform(r.id, "authorize");
                  }}
                >
                  授权这次执行
                </button>
              )}
              {[
                "authorization",
                "context",
                "unrouted",
                "queued",
                "running",
                "waiting",
              ].includes(r.state) && (
                <button onClick={() => void perform(r.id, "cancel")}>
                  取消消息
                </button>
              )}
              <button
                onClick={() => void perform(r.id, "cancel", { chain: true })}
              >
                停止此轮协作
              </button>
            </div>
          </details>
        );
      })}
    </section>
  );
}
export function AgentOrigin({ agent }: { agent?: AgentMessage }) {
  return agent ? (
    <small className="agent-origin">
      {agent.ownerSent ? "本人经接口发送" : "Agent消息"} · 来自{" "}
      {agent.fromLabel}
    </small>
  ) : null;
}
