import React, { useState, useEffect, useCallback, useRef } from "react";
import {
  Menu,
  Sun,
  PanelRightClose,
  PanelRightOpen,
  ArrowUp,
  RefreshCw,
  X,
  Plus,
  Square,
} from "lucide-react";
import { api, labels, stamp, type Project, type Run } from "./api";
import { MemoryPanel } from "./personal-understanding";
import { PersonalRulesPanel } from "./personal-rules";
import { RichText } from "./file-ui";
import { useMessageSubmissions } from "./message-submissions";
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
}: {
  projectId?: string;
  projects: Project[];
  begin?: (d: PreparedTask) => Promise<void>;
  notify: (s: string) => void;
  openTasks?: (id:string)=>void;
}) {
  const compact=!!openTasks;
  const [tasks, setTasks] = useState<Task[]>([]),
    [editing, setEditing] = useState<Partial<Task> | null>(null),
    [busy, setBusy] = useState(false);
  const refresh = useCallback(
    async () =>
      setTasks(
        await api(projectId ? `/projects/${projectId}/tasks` : "/tasks"),
      ),
    [projectId],
  );
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
      if(!begin) return;
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
  return (
    <div className={`tasks-panel ${compact ? "compact" : ""}`}>
      {!compact && (
        <button
          className="wide"
          onClick={() =>
            setEditing({
              projectId,
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
      {!sorted.length && (
        <p className="muted task-empty">
          {compact
            ? "目前没有安排。先聊聊你想关注什么。"
            : "还没有任务。点击“新增任务”记录要做的事情。"}
        </p>
      )}
      {sorted.map((t) => openTasks ? (
        <article className="task-card" key={t.id}>
          <small>{projects.find(p=>p.id===t.projectId)?.name} · {states[t.state]}</small>
          <details><summary>{t.title}</summary><RichText text={t.description}/></details>
          <p>{why(t)}</p><button onClick={()=>openTasks(t.projectId)}>进入项目任务 →</button>
        </article>
      ) : (
        <article className="task-card" key={t.id}>
          <small>
            {projects.find((p) => p.id === t.projectId)?.name} ·{" "}
            {states[t.state]}
          </small>
          <button className="task-title" onClick={() => setEditing(t)}>
            {t.title}
          </button>
          <p>{why(t)}</p>
          {(t.scheduledDate || t.dueDate) && (
            <small>
              {t.scheduledDate && `安排 ${t.scheduledDate} `}
              {t.dueDate && `截止 ${t.dueDate}`}
            </small>
          )}
          <div className="task-actions">
            {["pending", "in_progress"].includes(t.state) && (
              <button disabled={busy} onClick={() => start(t)}>
                {t.sessionId ? "继续处理" : "开始处理"} →
              </button>
            )}
            <button onClick={() => setEditing(t)}>详情</button>
          </div>
        </article>
      ))}
      {editing && (
        <div className="today-modal-backdrop">
          <section
            role="dialog"
            aria-modal="true"
            aria-label="任务详情"
            className="today-modal"
          >
            <header>
              <h2>{editing.id ? "任务详情" : "新增任务"}</h2>
              <button
                aria-label="关闭任务详情"
                onClick={() => setEditing(null)}
              >
                <X size={18} />
              </button>
            </header>
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
              做什么、为什么、背景与预期结果
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
                    setEditing(null);
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
                        setEditing(null);
                      })
                    }
                  >
                    {editing.state === "completed" ? "撤销完成" : "标记完成"}
                  </button>
                  <button
                    onClick={() => {
                      void start(editing as Task, true);
                      setEditing(null);
                    }}
                  >
                    另开对话
                  </button>
                  <button
                    className="danger"
                    onClick={() => {
                      if (
                        window.confirm("删除这项任务？关联对话和成果仍保留。")
                      )
                        void act(async () => {
                          await api(
                            `/tasks/${editing.id}?version=${editing.version}`,
                            undefined,
                            "DELETE",
                          );
                          setEditing(null);
                        });
                    }}
                  >
                    删除
                  </button>
                </>
              )}
            </footer>
          </section>
        </div>
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
      {d.state === "pending" && d.options.some((o:any)=>o.task) ? <p>此旧选项包含项目写操作。请进入项目任务页处理。</p> : d.state === "pending" ? (
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
  openNavigation:()=>void;
  projects: Project[];
  openTasks: (id:string)=>void;
  notify: (s: string) => void;
}) {
  const [data, setData] = useState<any>(null),
    [text, setText] = useState(
      () => localStorage.getItem("pwb.personalDraft") || "",
    ),
    [arrangements, setArrangements] = useState(
      () =>
        window.innerWidth >= 1280 &&
        localStorage.getItem("pwb.arrangements") !== "false",
    ),
    [memory, setMemory] = useState(false),
    [rules, setRules] = useState(false),
    [busy, setBusy] = useState(false),
    [refreshing, setRefreshing] = useState(false);
  const end = useRef<HTMLDivElement>(null);
  const refresh = useCallback(async () => setData(await api("/today")), []);
  useEffect(() => {
    void refresh().catch((e) => notify(e.message));
    const timer = setInterval(() => void refresh().catch(() => {}), 2500);
    return () => clearInterval(timer);
  }, [refresh]);
  useEffect(() => localStorage.setItem("pwb.personalDraft", text), [text]);
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
    if (!text.trim() || !data || busy) return;
    const body = text;
    setText("");
    setBusy(true);
    try {
      await submissions.submit({
        eventId: crypto.randomUUID(),
        sessionId: data.sessionId,
        text: body,
        attachments: [],
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
      <section className="today-main" aria-label="个人助手对话">
        <header className="today-header">
          <div>
            <span className="eyebrow">
              <Sun size={15} />{" "}
              {new Intl.DateTimeFormat("zh-CN", {
                month: "long",
                day: "numeric",
                weekday: "long",
                timeZone: "Asia/Shanghai",
              }).format(new Date())}
            </span>
            <h1>今天</h1><button className="mobile-only today-nav-button" aria-label="打开导航" onClick={openNavigation}><Menu size={18}/></button>
          </div>
          <div className="today-header-actions">
            <button onClick={() => setRules(true)}>助手规则</button>
            <button onClick={() => setMemory(true)}>你目前如何理解我</button>
            <button
              aria-label="更新今天"
              disabled={refreshing}
              onClick={async () => {
                setRefreshing(true);
                try {
                  await api("/today/refresh", {});
                  notify("已请求整理；用户任务优先，完成后会更新。");
                } catch (e: any) {
                  notify(e.message);
                } finally {
                  setRefreshing(false);
                }
              }}
            >
              <RefreshCw size={16} />
            </button>
            <button
              aria-label={arrangements ? "收起已有安排" : "打开已有安排"}
              onClick={() => {
                setArrangements(!arrangements);
                localStorage.setItem("pwb.arrangements", String(!arrangements));
              }}
            >
              {arrangements ? (
                <PanelRightClose size={18} />
              ) : (
                <PanelRightOpen size={18} />
              )}
            </button>
          </div>
        </header>
        <div className="today-subheading">
          <h2>个人助手</h2>
          <p>可查项目文件、对话和任务。微信、飞书与这里接续同一段对话。</p>
        </div>
        <div className="today-messages">
          {!data ? (
            <p>正在读取个人空间…</p>
          ) : (
            <>
              {data.channelNotes?.length > 0 && (
                <details className="context-status"><summary>机器人通知与回执</summary>{data.channelNotes.map((n:any)=><article key={n.id}><small>{n.kind==="notification"?"任务通知":"停止回执"} · {stamp(n.createdAt)} · {({sent:"已送达",accepted:"平台已接受",failed:"发送失败",unknown:"送达待核对",pending:"待发送",sending:"发送中"} as Record<string,string>)[n.state] || n.state}</small><RichText text={n.text}/></article>)}</details>
              )}
              {data.contextResetAt && (
                <p className="context-status">
                  原生上下文于 {new Date(data.contextResetAt).toLocaleString()}{" "}
                  清理过已失效资料；此后的交流从有效个人理解和项目说明继续，可见历史仍保留。
                </p>
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
                  <span className="assistant-icon">✦</span>
                  <h2>先关注真正值得的事</h2>
                  <p>
                    可以聊项目，也可以聊学习、方向，或最近的状态。
                    <br />
                    我会基于已有信息给出想法，和你一起做选择。
                  </p>
                  <button
                    onClick={() =>
                      setText("结合目前的情况，你建议我今天关注什么？")
                    }
                  >
                    一起想想今天
                  </button>
                </div>
              )}
              {[...runs]
                .sort((a, b) => a.createdAt - b.createdAt)
                .map((r) => (
                  <div className="personal-turn" key={r.id}>
                    <div className="personal-user">
                      {["wechat","feishu"].includes(r.source) && <small style={{display:"block"}}>{r.source==="wechat"?"微信":"飞书"} · {stamp(r.createdAt)}</small>}
                      {r.text.startsWith("[已提交的个人决策")
                        ? "已提交本次选择"
                        : r.text}
                    </div>
                    <div className="personal-answer">
                      <small>✦ 个人助手 · {labels[r.state] || r.state}</small>
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
                      ?.filter((d: any) => d.sourceRun === r.id)
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
                  <div className="personal-user">{v.input.text}</div>
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
        <div className="today-composer">
          <textarea
            aria-label="和个人助手聊聊"
            placeholder="说说你的想法，或今天想关注的事…"
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (
                e.key === "Enter" &&
                !e.shiftKey &&
                !e.nativeEvent.isComposing
              ) {
                e.preventDefault();
                void send();
              }
            }}
          />
          <footer>
            <small>Enter 发送 · Shift + Enter 换行</small>
            <button
              className="primary"
              aria-label="发送个人消息"
              disabled={busy || !text.trim() || !data}
              onClick={send}
            >
              <ArrowUp size={20} />
            </button>
          </footer>
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
      {rules && <PersonalRulesPanel close={() => setRules(false)} notify={notify} />}
      {memory && <MemoryPanel close={() => setMemory(false)} notify={notify} />}
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
    [busy, setBusy] = useState(false);
  useEffect(() => {
    void api(`/projects/${project.id}/context`)
      .then((c) => {
        setData(c);
        setText(c.description);
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
          rows={16}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
      </label>
      <p className="muted">
        一篇完整的项目说明，自动结合增量修订。你的纠正优先保留。
      </p>
      <p>
        {(
          {
            ready: "已整理",
            pending: "待更新",
            invalid: "正在重新核对",
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
              await onSaved();
              notify("说明已保存，人工纠正将作为后续整理依据");
            })
          }
        >
          保存说明
        </button>
        <button
          disabled={busy}
          onClick={() =>
            act(async () => {
              await api(`/projects/${project.id}/context/refresh`, {});
              notify("已进入后台整理队列");
            })
          }
        >
          更新说明
        </button>
        <button
          disabled={busy}
          onClick={() =>
            act(async () => {
              await api(`/projects/${project.id}/context/refresh`, {
                rebuild: true,
              });
              notify("已请求重新核对全部有效资料");
            })
          }
        >
          全量重建
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
