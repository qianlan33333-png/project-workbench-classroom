import { CommunicationFeed, AgentOrigin } from "./session-communication";
import { Overlay, useNarrow, useSurfaceGuard } from "./surface-ui";
import React, { useState, useEffect, useCallback, useRef } from "react";
import {
  Menu,
  PanelRightClose,
  PanelRightOpen,
  ArrowUp,
  X,
  Plus,
  Square,
  SlidersHorizontal,
  Paperclip,
} from "lucide-react";
import {
  api,
  labels,
  stamp,
  type Project,
  type Run,
  type Capability,
} from "./api";
import { CapabilityComposer, CapabilityBadges } from "./capability-composer";
import { Personalization } from "./personalization";
import {
  usePersonalAttachments,
  PersonalAttachmentChips,
  attachmentName,
} from "./personal-attachments";
import { RichText } from "./file-ui";
import { useMessageSubmissions } from "./message-submissions";
import { FollowupsPanel } from "./followups";
export type Task = {
  id: string;
  projectId: string;
  title: string;
  description: string;
  state: string;
  version: number;
  scheduledDate?: string;
  dueDate?: string;
  sessionId?: string;
  draftVersion?: number;
  updatedAt: number;
};
export type PreparedTask = {
  task: Task;
  session: { id: string; draft: boolean };
  text: string;
  changed: boolean;
  refilled?: boolean;
};
const states: Record<string, string> = {
  pending: "待处理",
  in_progress: "处理中",
  completed: "已完成",
  cancelled: "已取消",
};
const why = (t: Task) =>
  t.description.match(/为什么[^\n]*\n+([^\n]+)/)?.[1] ||
  t.description
    .replace(/^#+\s*/gm, "")
    .split("\n")
    .filter(Boolean)[0] ||
  "尚未补充背景";
export function TasksPanel({
  projectId,
  projects,
  begin,
  notify,
  openTasks,
  selectedId,
  inlineDetail = false,
  clearSelected,
  editorChanged,
}: {
  inlineDetail?: boolean;
  clearSelected?: () => void;
  editorChanged?: (open: boolean) => void;
  selectedId?: string;
  projectId?: string;
  projects: Project[];
  begin?: (d: PreparedTask) => Promise<void>;
  notify: (s: string) => void;
  openTasks?: (id: string) => void;
}) {
  const compact = !!openTasks;
  const narrow = useNarrow(1099);
  const editBaseline = useRef("");
  const [tasks, setTasks] = useState<Task[]>([]),
    [editing, setEditing] = useState<Partial<Task> | null>(null),
    [viewing, setViewing] = useState<Task | null>(null),
    [busy, setBusy] = useState(false);
  const refresh = useCallback(async () => {
    const rows = await api<Task[]>(
      projectId ? `/projects/${projectId}/tasks` : "/tasks",
    );
    setTasks(rows);
    setViewing((old) =>
      old && rows.some((t) => t.id === old.id) ? old : null,
    );
  }, [projectId]);
  useEffect(() => {
    void refresh().catch((e) => notify(e.message));
    const t = setInterval(() => void refresh().catch(() => {}), 5000);
    return () => clearInterval(t);
  }, [refresh]);
  const act = async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await f();
      await refresh();
    } catch (e: any) {
      notify(e.message);
    } finally {
      setBusy(false);
    }
  };
  const start = (t: Task, another = false) =>
    act(async () => {
      if (!begin) return;
      let d: PreparedTask = await api(`/tasks/${t.id}/prepare`, { another });
      if (
        d.changed &&
        d.session.draft &&
        window.confirm(
          "任务已更新。是否用新任务说明替换草稿？取消将保留原草稿。",
        )
      )
        d = {
          ...(await api(`/tasks/${t.id}/prepare`, { refill: true })),
          refilled: true,
        };
      await begin(d);
    });
  useEffect(() => {
    let alive = true;
    if (selectedId)
      void api<Task>("/tasks/" + selectedId)
        .then((v) => {
          if (alive && (!projectId || v.projectId === projectId)) setViewing(v);
        })
        .catch((e) => notify(e.message));
    return () => {
      alive = false;
    };
  }, [selectedId, projectId]);
  const editTask = (task: Partial<Task>) => {
    editBaseline.current = JSON.stringify(task);
    setEditing(task);
    editorChanged?.(true);
  };
  const dirty = !!editing && JSON.stringify(editing) !== editBaseline.current;
  const editGuard = useSurfaceGuard(dirty);
  const finishEditing = () => {
    setEditing(null);
    editorChanged?.(false);
  };
  const closeEditing = () => {
    if (!busy && editGuard()) finishEditing();
  };
  const viewed = tasks.find((t) => t.id === viewing?.id) || viewing;

  const sorted = [...tasks]
    .filter((t) => !compact || ["pending", "in_progress"].includes(t.state))
    .sort((a, b) => {
      const ad = a.scheduledDate || a.dueDate,
        bd = b.scheduledDate || b.dueDate;
      return ad && bd
        ? ad.localeCompare(bd)
        : ad
          ? -1
          : bd
            ? 1
            : b.updatedAt - a.updatedAt;
    });
  const active = sorted.filter(
    (t) => !["completed", "cancelled"].includes(t.state),
  );
  const ended = sorted.filter((t) =>
    ["completed", "cancelled"].includes(t.state),
  );
  const row = (t: Task) => (
    <article
      className={`task-card ${viewed?.id === t.id ? "selected" : ""}`}
      key={t.id}
    >
      {!compact && (
        <label className="task-check">
          <input
            type="checkbox"
            aria-label={"完成任务：" + t.title}
            checked={t.state === "completed"}
            disabled={busy || t.state === "cancelled"}
            onChange={(e) =>
              void act(async () => {
                await api(
                  `/tasks/${t.id}`,
                  { ...t, state: e.target.checked ? "completed" : "pending" },
                  "PUT",
                );
              })
            }
          />
        </label>
      )}
      <button
        className="task-title"
        onClick={() => (openTasks ? openTasks(t.projectId) : setViewing(t))}
      >
        <strong>{t.title}</strong>
        <small>
          {projects.find((p) => p.id === t.projectId)?.name || "个人"}
          {(t.scheduledDate || t.dueDate) &&
            " · " + (t.scheduledDate || t.dueDate)}
        </small>
      </button>
      {t.state !== "pending" && (
        <span className={`state state-${t.state}`}>{states[t.state]}</span>
      )}
    </article>
  );
  const closeDetail = () => {
    setViewing(null);
    clearSelected?.();
  };
  const taskDetail = viewed && (
    <>
      <div className="task-detail-title">
        <h3>{viewed.title}</h3>
        <button onClick={() => editTask(viewed)}>编辑</button>
      </div>
      <p className="muted">
        {projects.find((p) => p.id === viewed.projectId)?.name || "个人"} ·{" "}
        {states[viewed.state]}
      </p>
      {(viewed.scheduledDate || viewed.dueDate) && (
        <p className="task-detail-time">
          {viewed.scheduledDate && "安排 " + viewed.scheduledDate}
          {viewed.dueDate && " · 截止 " + viewed.dueDate}
        </p>
      )}
      <RichText text={viewed.description || "暂无说明"} />
      {["pending", "in_progress"].includes(viewed.state) && (
        <button
          className="primary"
          disabled={busy}
          onClick={() => void start(viewed)}
        >
          {viewed.sessionId ? "继续处理" : "开始处理"}
        </button>
      )}
    </>
  );
  return (
    <div
      className={`tasks-panel ${compact ? "compact" : ""} ${viewed && !compact && !inlineDetail ? "has-task-detail" : ""}`}
    >
      {inlineDetail && viewed ? (
        <section className="project-record-detail" aria-label="任务详情">
          <button className="panel-action" onClick={closeDetail}>
            返回任务列表
          </button>
          {taskDetail}
        </section>
      ) : (
        <>
          {!compact && (
            <button
              className="task-add"
              onClick={() =>
                editTask({
                  projectId: projectId || "personal-assistant",
                  title: "",
                  description: "",
                  state: "pending",
                })
              }
            >
              <Plus size={15} />
              新增任务
            </button>
          )}
          {!active.length && (
            <p className="muted task-empty">
              {compact ? "暂无安排" : "暂无未完成任务"}
            </p>
          )}
          <div className="task-rows">{active.map(row)}</div>
          {!!ended.length && (
            <details className="tasks-ended">
              <summary>已结束 · {ended.length}</summary>
              {ended.map(row)}
            </details>
          )}
        </>
      )}
      {!compact && viewed && !inlineDetail && (
        <Overlay
          title="任务详情"
          close={closeDetail}
          mode="drawer"
          modal={narrow}
          className="task-detail-panel"
        >
          {taskDetail}
        </Overlay>
      )}
      {editing && (
        <Overlay
          title={editing.id ? "编辑任务" : "新增任务"}
          close={closeEditing}
          busy={busy}
          className="task-editor"
        >
          {!projectId && !editing.id && (
            <label>
              归属
              <select
                value={editing.projectId}
                onChange={(e) =>
                  setEditing({ ...editing, projectId: e.target.value })
                }
              >
                <option value="personal-assistant">个人</option>
                {projects.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
          )}
          <label>
            标题
            <input
              value={editing.title || ""}
              onChange={(e) =>
                setEditing({ ...editing, title: e.target.value })
              }
            />
          </label>
          <label>
            任务说明
            <textarea
              rows={9}
              value={editing.description || ""}
              onChange={(e) =>
                setEditing({ ...editing, description: e.target.value })
              }
            />
          </label>
          <div className="task-dates">
            <label>
              安排日期
              <input
                type="date"
                value={editing.scheduledDate || ""}
                onChange={(e) =>
                  setEditing({ ...editing, scheduledDate: e.target.value })
                }
              />
            </label>
            <label>
              截止日期
              <input
                type="date"
                value={editing.dueDate || ""}
                onChange={(e) =>
                  setEditing({ ...editing, dueDate: e.target.value })
                }
              />
            </label>
          </div>
          <label>
            状态
            <select
              value={editing.state}
              onChange={(e) =>
                setEditing({ ...editing, state: e.target.value })
              }
            >
              {Object.entries(states).map(([v, t]) => (
                <option key={v} value={v}>
                  {t}
                </option>
              ))}
            </select>
          </label>
          <footer>
            <button
              disabled={busy}
              className="primary"
              onClick={() =>
                act(async () => {
                  await api(
                    editing.id
                      ? `/tasks/${editing.id}`
                      : `/projects/${editing.projectId}/tasks`,
                    {
                      ...editing,
                      operation: editing.id || crypto.randomUUID(),
                    },
                    editing.id ? "PUT" : "POST",
                  );
                  finishEditing();
                })
              }
            >
              保存
            </button>
            {editing.id && (
              <>
                <button
                  disabled={busy}
                  onClick={() =>
                    act(async () => {
                      await api(
                        `/tasks/${editing.id}`,
                        {
                          ...editing,
                          state:
                            editing.state === "completed"
                              ? "pending"
                              : "completed",
                        },
                        "PUT",
                      );
                      finishEditing();
                    })
                  }
                >
                  {editing.state === "completed" ? "撤销完成" : "标记完成"}
                </button>
                <button
                  onClick={() => {
                    void start(editing as Task);
                    finishEditing();
                  }}
                >
                  执行待办
                </button>
                {editing.projectId !== "personal-assistant" &&
                  editing.sessionId && (
                    <button
                      onClick={() => {
                        void start(editing as Task, true);
                        finishEditing();
                      }}
                    >
                      另开对话
                    </button>
                  )}
                <button
                  className="danger"
                  onClick={() => {
                    if (window.confirm("删除这项任务？关联对话和成果仍保留。"))
                      void act(async () => {
                        await api(
                          `/tasks/${editing.id}?version=${editing.version}`,
                          undefined,
                          "DELETE",
                        );
                        finishEditing();
                        setViewing(null);
                      });
                  }}
                >
                  删除
                </button>
              </>
            )}
          </footer>
        </Overlay>
      )}
    </div>
  );
}
function DecisionCard({
  d,
  projects,
  refresh,
  notify,
}: {
  d: any;
  projects: Project[];
  refresh: () => Promise<void>;
  notify: (s: string) => void;
}) {
  const [selected, setSelected] = useState(""),
    [other, setOther] = useState(""),
    [busy, setBusy] = useState(false);
  const submit = async () => {
    setBusy(true);
    try {
      await api(`/personal/decisions/${d.id}/answer`, {
        version: d.version,
        answer: selected,
        other,
      });
      await refresh();
    } catch (e: any) {
      notify(e.message);
      await refresh();
    } finally {
      setBusy(false);
    }
  };
  return (
    <article className="decision-card">
      <small>一起做个选择</small>
      <h3>{d.question}</h3>
      <p>{d.reason}</p>
      {d.state === "pending" && d.options.some((o: any) => o.task) ? (
        <p>此旧选项包含项目写操作。请进入项目任务页处理。</p>
      ) : d.state === "pending" ? (
        <>
          <div role="radiogroup" aria-label={d.question}>
            {d.options.map((o: any) => (
              <label
                className={`decision-option ${selected === o.id ? "selected" : ""}`}
                key={o.id}
              >
                <input
                  type="radio"
                  name={d.id}
                  value={o.id}
                  checked={selected === o.id}
                  onChange={() => setSelected(o.id)}
                />
                <span>
                  <strong>
                    {o.label}
                    {o.recommended && <em>推荐</em>}
                  </strong>
                  <small>{o.tradeoff}</small>
                  {o.task && (
                    <small>
                      项目任务 ·{" "}
                      {projects.find((p) => p.id === o.task.projectId)?.name} ·{" "}
                      {o.task.title}
                    </small>
                  )}
                </span>
              </label>
            ))}
            <label className="decision-option">
              <input
                type="radio"
                name={d.id}
                checked={selected === "other"}
                onChange={() => setSelected("other")}
              />
              其他，我想补充
            </label>
            {selected === "other" && (
              <textarea
                aria-label="其他想法"
                value={other}
                onChange={(e) => setOther(e.target.value)}
              />
            )}
            <label className="decision-option">
              <input
                type="radio"
                name={d.id}
                checked={selected === "defer"}
                onChange={() => setSelected("defer")}
              />
              暂不决定
            </label>
          </div>
          <button
            className="primary"
            disabled={
              !selected || busy || (selected === "other" && !other.trim())
            }
            onClick={submit}
          >
            {busy ? "正在保存…" : "提交选择"}
          </button>
        </>
      ) : (
        <p>
          {d.state === "stale"
            ? "依据已变化，请重新讨论。"
            : d.state === "deferred"
              ? "已暂缓。"
              : `已记录本次选择 ${d.answer}${d.taskId ? " · 项目任务已创建" : ""}`}
        </p>
      )}
    </article>
  );
}
export function Today({
  projects,
  openTasks,
  notify,
  openNavigation,
}: {
  openNavigation: () => void;
  projects: Project[];
  openTasks: (id: string) => void;
  notify: (s: string) => void;
}) {
  const [data, setData] = useState<any>(null),
    [text, setText] = useState(() => {
      try {
        return (
          JSON.parse(localStorage.getItem("pwb.personalComposer") || "null")
            ?.text ??
          localStorage.getItem("pwb.personalDraft") ??
          ""
        );
      } catch {
        return "";
      }
    }),
    [capabilities, setCapabilities] = useState<Capability[]>(() => {
      try {
        const v = JSON.parse(
          localStorage.getItem("pwb.personalDraftTools") || "[]",
        );
        return Array.isArray(v) ? v : [];
      } catch {
        return [];
      }
    }),
    [arrangements, setArrangements] = useState(false),
    [personalization, setPersonalization] = useState(false),
    [busy, setBusy] = useState(false),
    [dragging, setDragging] = useState(false);
  const attachment = usePersonalAttachments(data?.sessionId || "", notify);
  const fileInput = useRef<HTMLInputElement>(null);
  const personalizationTrigger = useRef<HTMLButtonElement>(null);
  const attachmentBlocked = attachment.items.some((v) => v.state !== "ready");
  const canSend =
    !!data &&
    !busy &&
    !attachmentBlocked &&
    (!!text.trim() || attachment.items.some((v) => v.state === "ready"));
  const [draftTaskId, setDraftTaskId] = useState<string | undefined>(() => {
    try {
      return JSON.parse(localStorage.getItem("pwb.personalComposer") || "null")
        ?.taskId;
    } catch {
      return undefined;
    }
  });
  const editText = (value: string) => {
    setText(value);
    if (!value.trim()) setDraftTaskId(undefined);
  };
  const end = useRef<HTMLDivElement>(null);
  const refresh = useCallback(async () => setData(await api("/today")), []);
  useEffect(() => {
    void refresh().catch((e) => notify(e.message));
    const timer = setInterval(() => void refresh().catch(() => {}), 2500);
    return () => clearInterval(timer);
  }, [refresh]);
  useEffect(() => {
    localStorage.setItem("pwb.personalDraft", text);
    localStorage.setItem(
      "pwb.personalComposer",
      JSON.stringify({ text, taskId: draftTaskId }),
    );
  }, [text, draftTaskId]);
  useEffect(
    () =>
      localStorage.setItem(
        "pwb.personalDraftTools",
        JSON.stringify(capabilities),
      ),
    [capabilities],
  );
  const runs: Run[] = data?.runs || [];
  const applyRun = useCallback(
    (r: Run) =>
      setData((old: any) =>
        old
          ? { ...old, runs: [...old.runs.filter((v: Run) => v.id !== r.id), r] }
          : old,
      ),
    [],
  );
  const submissions = useMessageSubmissions(true, runs, applyRun);
  const pending = submissions.items.filter(
    (v) =>
      v.input.sessionId === data?.sessionId &&
      !runs.some((r) => r.eventId === v.input.eventId),
  );
  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [runs.length, pending.length, data?.decisions?.length]);
  const send = async () => {
    if (!canSend) return;
    const body = text;
    const attached = attachment.items.map((v) => v.path!);
    const taskId = draftTaskId;
    setDraftTaskId(undefined);
    sessionStorage.removeItem("today.taskId");
    setText("");
    setCapabilities([]);
    attachment.clear();
    setBusy(true);
    try {
      await submissions.submit({
        eventId: crypto.randomUUID(),
        sessionId: data.sessionId,
        text: body,
        taskId,
        capabilities,
        attachments: attached,
        references: [],
      });
      await refresh();
    } catch (e: any) {
      notify(e.message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className={`today-layout ${arrangements ? "with-arrangements" : ""}`}>
      <section
        className="today-main"
        aria-label="个人助手对话"
        inert={personalization || undefined}
        onDragOver={(e) => {
          if (e.dataTransfer.types.includes("Files")) {
            e.preventDefault();
            if (!busy) setDragging(true);
          }
        }}
        onDragLeave={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node | null))
            setDragging(false);
        }}
        onDrop={(e) => {
          if (e.dataTransfer.files.length) {
            e.preventDefault();
            setDragging(false);
            if (!busy) attachment.add(e.dataTransfer.files);
          }
        }}
      >
        <div className="today-messages">
          {!data ? (
            <p>正在读取个人空间…</p>
          ) : (
            <>
              {data.channelNotes?.length > 0 && (
                <details className="context-status">
                  <summary>机器人通知与回执</summary>
                  {data.channelNotes.map((n: any) => (
                    <article key={n.id}>
                      <small>
                        {n.kind === "notification" ? "任务通知" : "停止回执"} ·{" "}
                        {stamp(n.createdAt)} ·{" "}
                        {(
                          {
                            sent: "已送达",
                            accepted: "平台已接受",
                            failed: "发送失败",
                            unknown: "送达待核对",
                            pending: "待发送",
                            sending: "发送中",
                            cancelled: "已取消",
                          } as Record<string, string>
                        )[n.state] || n.state}
                      </small>
                      <RichText text={n.text} />
                    </article>
                  ))}
                </details>
              )}
              {data.daily?.text && (
                <article className="daily-card">
                  <small>今天可以想一想 · {stamp(data.daily.updatedAt)}</small>
                  <RichText text={data.daily.text} />
                  <button
                    onClick={async () => {
                      try {
                        await api("/today/continue", {
                          day: data.daily.day,
                          version: data.daily.version,
                          eventId: crypto.randomUUID(),
                        });
                        await refresh();
                      } catch (e: any) {
                        notify(e.message);
                      }
                    }}
                  >
                    沿着这个想法聊聊 →
                  </button>
                </article>
              )}
              {data.daily?.decisionId &&
                data.decisions
                  ?.filter((d: any) => d.id === data.daily.decisionId)
                  .map((d: any) => (
                    <DecisionCard
                      key={d.id}
                      d={d}
                      projects={projects}
                      refresh={refresh}
                      notify={notify}
                    />
                  ))}
              {data.daily?.state === "failed" && (
                <p role="status">
                  本次整理未完成，保留上次内容。{data.daily.error}
                </p>
              )}
              {data.contexts?.some((c: any) => c.state !== "ready") && (
                <p className="context-status">
                  部分项目资料正在整理或待更新，建议将标明这些限制。
                </p>
              )}
              {!runs.length && !data.daily?.text && (
                <div className="today-welcome">
                  <h2>想聊些什么？</h2>
                </div>
              )}
              {data?.contextNotice && <p role="status">{data.contextNotice}</p>}
              <CommunicationFeed
                sessionId={data?.sessionId || ""}
                notify={notify}
                prepare={(v) => {
                  if (text.trim()) {
                    notify("输入框已有草稿，请先发送或清空后再准备回复。");
                    return;
                  }
                  editText(v);
                }}
              />
              {[...runs]
                .sort((a, b) => a.createdAt - b.createdAt)
                .map((r) => (
                  <div className="personal-turn" key={r.id}>
                    <div
                      className={
                        r.agent ? "communication-input" : "personal-user"
                      }
                    >
                      <AgentOrigin agent={r.agent} />
                      {["wechat", "feishu", "automation"].includes(
                        r.source,
                      ) && (
                        <small style={{ display: "block" }}>
                          {labels[r.source] || r.source} · {stamp(r.createdAt)}
                        </small>
                      )}
                      {r.text.startsWith("[已提交的个人决策")
                        ? "已提交本次选择"
                        : r.text}
                      {!!r.attachments?.length && (
                        <div className="personal-message-files">
                          {r.attachments.map((path) => (
                            <span key={path}>
                              <Paperclip size={14} />
                              {attachmentName(path)}
                            </span>
                          ))}
                        </div>
                      )}
                      <CapabilityBadges values={r.capabilities || []} />
                    </div>
                    <div className="personal-answer">
                      {r.state !== "completed" && (
                        <small className="personal-run-state">
                          {labels[r.state] || r.state}
                        </small>
                      )}
                      {r.result ? (
                        <RichText text={r.result} />
                      ) : (
                        <p>
                          {r.error ||
                            (["queued"].includes(r.state)
                              ? "已收到，等待执行。"
                              : "正在思考…")}
                        </p>
                      )}
                      {["queued", "running", "waiting"].includes(r.state) && (
                        <button
                          onClick={async () => {
                            try {
                              await api(`/runs/${r.id}/stop`, {});
                              await refresh();
                            } catch (e: any) {
                              notify(e.message);
                            }
                          }}
                        >
                          <Square size={12} />
                          停止
                        </button>
                      )}
                    </div>
                    {data.decisions
                      ?.filter(
                        (d: any) => d.sourceRun === r.id && !d.followupId,
                      )
                      .map((d: any) => (
                        <DecisionCard
                          key={d.id}
                          d={d}
                          projects={projects}
                          refresh={refresh}
                          notify={notify}
                        />
                      ))}
                  </div>
                ))}
              {pending.map((v) => (
                <div key={v.input.eventId} className="personal-turn">
                  <div className="personal-user">
                    {v.input.text}
                    {!!v.input.attachments?.length && (
                      <div className="personal-message-files">
                        {v.input.attachments.map((path) => (
                          <span key={path}>
                            <Paperclip size={14} />
                            {attachmentName(path)}
                          </span>
                        ))}
                      </div>
                    )}
                    <CapabilityBadges values={v.input.capabilities || []} />
                  </div>
                  <small>
                    {v.state === "sending"
                      ? "正在发送…"
                      : v.state === "unknown"
                        ? "正在核对接收状态…"
                        : v.state === "failed"
                          ? "发送失败，请重试。"
                          : "已收到，正在排队…"}
                  </small>
                  {v.state === "failed" && (
                    <button onClick={() => void submissions.retry(v)}>
                      重试发送
                    </button>
                  )}
                </div>
              ))}
              <div ref={end} />
            </>
          )}
        </div>
        <div className="today-bottom">
          {data && (
            <FollowupsPanel
              compact
              items={data.followups || []}
              decisions={data.decisions || []}
              refresh={refresh}
              renderDecision={(d) => (
                <DecisionCard
                  key={d.id}
                  d={d}
                  projects={projects}
                  refresh={refresh}
                  notify={notify}
                />
              )}
            />
          )}
          <div className={`today-composer ${dragging ? "dragging" : ""}`}>
            {dragging && (
              <div className="personal-drop-label" role="status">
                松开添加文件
              </div>
            )}
            {!!attachment.items.length && (
              <PersonalAttachmentChips
                items={attachment.items}
                remove={attachment.remove}
                retry={attachment.retry}
              />
            )}
            <CapabilityComposer
              personal
              projectId="personal-assistant"
              sessionId={data?.sessionId || ""}
              value={text}
              onChange={editText}
              selected={capabilities}
              onSelected={setCapabilities}
              send={() => void send()}
              onPaste={(e) => {
                const files = Array.from(e.clipboardData.files);
                if (files.length && !busy) {
                  e.preventDefault();
                  attachment.add(files);
                }
              }}
            />
            <footer>
              <div className="today-composer-tools">
                <input
                  ref={fileInput}
                  hidden
                  type="file"
                  multiple
                  aria-label="选择本地附件"
                  onChange={(e) => {
                    attachment.add(e.currentTarget.files);
                    e.currentTarget.value = "";
                  }}
                />
                <button
                  className="today-attach-button"
                  aria-label="添加附件"
                  title="添加本地附件"
                  disabled={busy || !data}
                  onClick={() => fileInput.current?.click()}
                >
                  <Plus size={22} />
                </button>
                <span className="composer-divider" />
                <button
                  ref={personalizationTrigger}
                  onClick={() => setPersonalization(true)}
                >
                  <SlidersHorizontal size={18} />
                  <span>个性化</span>
                </button>
                <span className="composer-divider" />
                <button
                  aria-label={arrangements ? "收起已有安排" : "打开已有安排"}
                  aria-expanded={arrangements}
                  onClick={() => setArrangements(!arrangements)}
                >
                  {arrangements ? (
                    <PanelRightClose size={18} />
                  ) : (
                    <PanelRightOpen size={18} />
                  )}
                  <span>已有安排</span>
                </button>
              </div>
              <button
                className="primary today-send"
                aria-label="发送个人消息"
                disabled={!canSend}
                onClick={send}
              >
                <ArrowUp size={21} />
              </button>
            </footer>
          </div>
        </div>
      </section>
      {arrangements && (
        <aside className="today-arrangements" aria-label="已有安排">
          <header>
            <h2>已有安排</h2>
            <button
              aria-label="关闭已有安排"
              onClick={() => setArrangements(false)}
            >
              <X size={18} />
            </button>
          </header>
          <p className="muted">来自各项目的待处理与进行中任务</p>
          <TasksPanel
            projects={projects}
            openTasks={openTasks}
            notify={notify}
          />
        </aside>
      )}
      {personalization && (
        <Personalization
          close={() => setPersonalization(false)}
          notify={notify}
          returnFocus={personalizationTrigger.current}
        />
      )}
    </div>
  );
}
export function ContextSettings({
  project,
  notify,
  onSaved,
}: {
  project: Project;
  notify: (s: string) => void;
  onSaved: () => Promise<void>;
}) {
  const [data, setData] = useState<any>(null),
    [text, setText] = useState(project.description),
    [savedText, setSavedText] = useState(project.description),
    [busy, setBusy] = useState(false);
  useSurfaceGuard(text !== savedText);
  useEffect(() => {
    void api(`/projects/${project.id}/context`)
      .then((c) => {
        setData(c);
        setText(c.description);
        setSavedText(c.description);
      })
      .catch((e) => notify(e.message));
  }, [project.id]);
  const act = async (f: () => Promise<void>) => {
    setBusy(true);
    try {
      await f();
    } catch (e: any) {
      notify(e.message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="context-settings">
      <label>
        简短说明
        <textarea
          rows={6}
          aria-label="简短说明"
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
      </label>
      <p className="muted">
        可直接保存说明，也可在项目的「定时」中添加整理任务。
      </p>
      <p>
        {(
          {
            ready: "已整理",
            pending: "待更新",
            invalid: "来源已变化",
            failed: "整理失败",
          } as Record<string, string>
        )[data?.state] || "加载中"}{" "}
        · {data?.updatedAt ? stamp(data.updatedAt) : "尚未整理"} {data?.error}
      </p>
      <div className="task-actions">
        <button
          className="primary"
          disabled={busy || !data}
          onClick={() =>
            act(async () => {
              const c = await api(
                `/projects/${project.id}/context`,
                { description: text, version: data.version },
                "PUT",
              );
              setData(c);
              setSavedText(c.description);
              await onSaved();
              notify("说明已保存，人工纠正将作为后续整理依据");
            })
          }
        >
          保存说明
        </button>
      </div>
      <details>
        <summary>整理覆盖与来源（{data?.sources?.length || 0}）</summary>
        {data?.sources?.map((s: any) => (
          <p key={s.id}>
            {s.id} · {s.status} · {stamp(s.at)}
          </p>
        ))}
      </details>
    </div>
  );
}
