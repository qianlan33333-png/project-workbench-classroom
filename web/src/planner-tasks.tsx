import { PageHeader, Overlay, useNarrow, useSurfaceGuard } from "./surface-ui";
import React, { useState, useEffect } from "react";
import { Bot, Plus, Clock, X, Mail } from "lucide-react";
import {
  api,
  stamp,
  labels,
  type Project,
  type Expert,
  type Capability,
  type Session,
} from "./api";
import { TasksPanel, type PreparedTask } from "./today";
import { RichText } from "./file-ui";
import { personal } from "./planner";
const zoneTime = (t: number) =>
  new Date(t + 28800000).toISOString().slice(0, 16);
type Rule = {
  id: string;
  version: number;
  name: string;
  instruction: string;
  projectId: string;
  cron: string;
  timezone: string;
  onceAt: number;
  sessionId: string;
  enabled: boolean;
  expertId: string;
  output: string;
  condition: string;
  sourceScope: string;
  capabilities: Capability[];
  nextAt?: number;
  pausedReason?: string;
  notifyConnectionId?: string;
  notifyConversationId?: string;
  changes?: { from: number; cron: string }[];
};
const blank = (projectId: string): Rule => ({
  id: "",
  version: 0,
  name: "",
  instruction: "",
  projectId,
  cron: "0 9 * * *",
  onceAt: 0,
  timezone: "Asia/Shanghai",
  sessionId: "",
  enabled: true,
  expertId: "",
  output: "",
  condition: "",
  sourceScope: "",
  capabilities: [],
});
type Props = {
  projects: Project[];
  experts: Expert[];
  notify: (s: string) => void;
  begin: (v: PreparedTask) => Promise<void>;
  parameters: URLSearchParams;
};
export function TaskCenter({
  projects,
  experts,
  notify,
  begin,
  parameters,
}: Props) {
  const [tab, setTab] = useState(
      parameters.get("tab") === "ai" ? "ai" : "todo",
    ),
    [filter, setFilter] = useState(parameters.get("scope") || ""),
    [rules, setRules] = useState<Rule[]>([]),
    [edit, setEdit] = useState<Rule | null>(null),
    [recent, setRecent] = useState<any[]>([]),
    [active, setActive] = useState(""),
    [tick, setTick] = useState(0),
    [busy, setBusy] = useState(false);
  const target = parameters.get("rule") || "";
  const load = async () =>
    setRules(await api("/schedules?projectId=" + encodeURIComponent(filter)));
  useEffect(() => {
    let on = true;
    const fetch = async () => {
      const rows = await api<Rule[]>(
        "/schedules?projectId=" + encodeURIComponent(filter),
      );
      if (on) setRules(rows);
    };
    void fetch().catch((e) => notify(e.message));
    const t = setInterval(() => void fetch().catch(() => {}), 7000);
    return () => {
      on = false;
      clearInterval(t);
    };
  }, [filter, tick]);
  useEffect(() => {
    if (target) {
      setTab("ai");
      api<Rule[]>("/schedules")
        .then((v) => {
          const r = v.find((x) => x.id === target);
          if (r) setEdit(r);
          else notify("原任务已删除");
        })
        .catch((e) => notify(e.message));
    }
  }, [target]);
  const act = async (f: () => Promise<void>) => {
    if (busy) return;
    setBusy(true);
    try {
      await f();
      await load();
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const history = async (r: Rule) => {
    setActive(r.id);
    setRecent(await api("/schedules/" + r.id + "/checks"));
  };
  return (
    <section className="planner-screen">
      <div className="planner-main">
        <PageHeader
          title="任务"
          actions={
            <>
              {" "}
              {tab === "ai" && (
                <button
                  className="primary"
                  onClick={() => setEdit(blank(filter || personal))}
                >
                  <Plus size={16} />
                  新建 AI 任务
                </button>
              )}
            </>
          }
        />
        <div className="task-toolbar">
          <div className="plan-segment">
            {[
              ["todo", "我的待办"],
              ["ai", "AI 任务"],
            ].map(([id, name]) => (
              <button
                key={id}
                aria-pressed={tab === id}
                onClick={() => {
                  setTab(id);
                  setEdit(null);
                }}
              >
                {name}
              </button>
            ))}
          </div>
          <select
            aria-label="筛选归属"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          >
            <option value="">全部归属</option>
            <option value={personal}>个人</option>
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </div>
        <div className="planner-list">
          {tab === "todo" ? (
            <TasksPanel
              projectId={filter || undefined}
              projects={projects}
              notify={notify}
              begin={begin}
              selectedId={parameters.get("task") || ""}
            />
          ) : (
            <>
              {!rules.length && (
                <p className="planner-empty">
                  <Bot />
                  还没有 AI 任务。创建一条指令，安排助手何时执行。
                </p>
              )}
              {rules.map((r) => (
                <article className="rule-card" key={r.id}>
                  <Clock size={18} />
                  <div>
                    <button className="title-link" onClick={() => setEdit(r)}>
                      {r.name}
                    </button>
                    <p>
                      {projects.find((p) => p.id === r.projectId)?.name ||
                        "个人"}{" "}
                      ·{" "}
                      {r.enabled && r.nextAt
                        ? "下次 " + stamp(r.nextAt)
                        : "已暂停"}{" "}
                      · {r.sessionId ? "继续指定对话" : "每次新开对话"}
                    </p>
                    {r.pausedReason && <small>{r.pausedReason}</small>}
                  </div>
                  <div className="rule-actions">
                    <button
                      disabled={busy}
                      onClick={() =>
                        act(async () => {
                          await api("/schedules/" + r.id + "/enabled", {
                            enabled: !r.enabled,
                          });
                        })
                      }
                    >
                      {r.enabled ? "暂停" : "启用"}
                    </button>
                    <button
                      disabled={busy}
                      onClick={() =>
                        act(async () => {
                          const k = "pwb.schedule." + r.id;
                          const operation =
                            sessionStorage.getItem(k) || crypto.randomUUID();
                          sessionStorage.setItem(k, operation);
                          const ans = await api("/schedules/" + r.id + "/run", {
                            operation,
                          });
                          sessionStorage.removeItem(k);
                          notify(ans.detail || "已提交执行");
                          await history(r);
                        })
                      }
                    >
                      执行一次
                    </button>
                    <button onClick={() => act(() => history(r))}>记录</button>
                  </div>
                </article>
              ))}
              {active && (
                <div className="history-box">
                  <b>执行记录</b>
                  <button onClick={() => setActive("")}>收起</button>
                  {!recent.length && <p>尚无执行记录</p>}
                  {recent.map((v) => (
                    <Execution key={v.operation} check={v} />
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      </div>
      {edit && (
        <RuleForm
          key={edit.id || "new"}
          initial={edit}
          projects={projects}
          experts={experts}
          notify={notify}
          close={() => setEdit(null)}
          saved={() => {
            setEdit(null);
            setTick((t) => t + 1);
          }}
        />
      )}
    </section>
  );
}
function Execution({ check }: { check: any }) {
  const [text, setText] = useState("");
  return (
    <details
      onToggle={(e) => {
        if (e.currentTarget.open && check.runId && !text)
          api("/runs/" + check.runId)
            .then((r) => setText(r.result || r.error || "尚无输出"))
            .catch((e) => setText(e.message));
      }}
    >
      <summary>
        {stamp(check.at)} ·{" "}
        {labels[check.outcome] ||
          { skipped: "已跳过", active: "已有执行" }[check.outcome as string] ||
          check.outcome}
      </summary>
      <RichText text={text || check.detail || "尚无输出"} />
    </details>
  );
}
function Cadence({
  value,
  change,
}: {
  value: string;
  change: (s: string) => void;
}) {
  const [m, h, md, mo, wd] = value.split(" ");
  const simple =
    /^\d+ \d+ (\*|\d+) \* (\*|[0-6])$/.test(value) &&
    (md === "*" || wd === "*");
  const [advanced, setAdvanced] = useState(!simple);
  const type = advanced
    ? "custom"
    : md !== "*"
      ? "month"
      : wd !== "*"
        ? "week"
        : "day";
  const write = (
    kind: string,
    hour = h,
    min = m,
    monthDay = md,
    weekday = wd,
  ) => {
    if (kind === "custom") {
      setAdvanced(true);
      return;
    }
    setAdvanced(false);
    change(
      [
        Number(min) || 0,
        Number(hour) || 0,
        kind === "month" ? Number(monthDay) || 1 : "*",
        "*",
        kind === "week" ? (weekday === "*" ? 1 : Number(weekday) || 0) : "*",
      ].join(" "),
    );
  };
  return (
    <>
      <label>
        重复频率
        <select value={type} onChange={(e) => write(e.target.value)}>
          <option value="day">每天</option>
          <option value="week">每周</option>
          <option value="month">每月</option>
          <option value="custom">自定义 cron</option>
        </select>
      </label>
      {type === "custom" ? (
        <label>
          五段 cron
          <input value={value} onChange={(e) => change(e.target.value)} />
        </label>
      ) : (
        <>
          <label>
            执行时间
            <input
              type="time"
              value={
                String(h).padStart(2, "0") + ":" + String(m).padStart(2, "0")
              }
              onChange={(e) => {
                const [hr, min] = e.target.value.split(":");
                if (min) write(type, hr, min);
              }}
            />
          </label>
          {type === "week" && (
            <label>
              每周几
              <select
                value={wd}
                onChange={(e) => write(type, h, m, md, e.target.value)}
              >
                {["日", "一", "二", "三", "四", "五", "六"].map((v, i) => (
                  <option key={i} value={i}>
                    星期{v}
                  </option>
                ))}
              </select>
            </label>
          )}
          {type === "month" && (
            <label>
              每月几号
              <input
                type="number"
                min="1"
                max="31"
                value={md}
                onChange={(e) => write(type, h, m, e.target.value)}
              />
            </label>
          )}
        </>
      )}
    </>
  );
}
function RuleForm({
  initial,
  projects,
  experts,
  close,
  saved,
  notify,
}: {
  initial: Rule;
  projects: Project[];
  experts: Expert[];
  close: () => void;
  saved: () => void;
  notify: (s: string) => void;
}) {
  const [v, set] = useState(initial),
    [sessions, setSessions] = useState<Session[]>([]),
    [caps, setCaps] = useState<Capability[]>([]),
    [targets, setTargets] = useState<any[]>([]),
    [busy, setBusy] = useState(false);
  const [preview, setPreview] = useState("");
  const narrow = useNarrow(1099);
  const guard = useSurfaceGuard(JSON.stringify(v) !== JSON.stringify(initial));
  useEffect(() => {
    let live = true;
    const timer = setTimeout(() => {
      api<number[]>("/schedules/preview", v)
        .then(
          (times) =>
            live &&
            setPreview(
              times.length
                ? "接下来：" + times.map(stamp).join("、")
                : "没有未来的执行时间",
            ),
        )
        .catch((e) => live && setPreview(e.message));
    }, 200);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [v.cron, v.onceAt, v.timezone]);
  const field = (part: Partial<Rule>) => set((old) => ({ ...old, ...part }));
  useEffect(() => {
    let live = true;
    const load = async () => {
      if (v.projectId === personal) await api("/planner/personal-space");
      const [s, c, t] = await Promise.all([
        api<Session[]>("/projects/" + v.projectId + "/sessions"),
        api<Capability[]>("/projects/" + v.projectId + "/capabilities"),
        api<any[]>("/notification-targets"),
      ]);
      if (live) {
        setSessions(s);
        setCaps(c);
        setTargets(t);
      }
    };
    void load().catch((e) => notify(e.message));
    return () => {
      live = false;
    };
  }, [v.projectId]);
  const save = async () => {
    setBusy(true);
    try {
      await api(
        v.id ? "/schedules/" + v.id : "/schedules",
        v,
        v.id ? "PUT" : "POST",
      );
      saved();
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Overlay
      title={v.id ? "编辑 AI 任务" : "新建 AI 任务"}
      close={() => {
        if (!busy && guard()) close();
      }}
      busy={busy}
      mode="drawer"
      modal={narrow}
      className="plan-detail"
    >
      <label>
        任务名称
        <input
          value={v.name}
          onChange={(e) => field({ name: e.target.value })}
        />
      </label>
      <label>
        做什么
        <textarea
          rows={4}
          value={v.instruction}
          onChange={(e) => field({ instruction: e.target.value })}
        />
      </label>
      <label>
        在哪里工作
        <select
          value={v.projectId}
          disabled={!!v.id}
          onChange={(e) =>
            field({
              projectId: e.target.value,
              expertId: "",
              sessionId: "",
              capabilities: [],
            })
          }
        >
          <option value={personal}>个人</option>
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <label>
        什么时候
        <select
          value={v.onceAt ? "once" : "repeat"}
          onChange={(e) =>
            field({
              onceAt: e.target.value === "once" ? Date.now() + 3600000 : 0,
            })
          }
        >
          <option value="repeat">重复执行</option>
          <option value="once">只执行一次</option>
        </select>
      </label>
      {v.onceAt ? (
        <label>
          北京时间
          <input
            type="datetime-local"
            value={zoneTime(v.onceAt)}
            onChange={(e) =>
              e.target.value &&
              field({ onceAt: Date.parse(e.target.value + ":00+08:00") })
            }
          />
        </label>
      ) : (
        <>
          <Cadence value={v.cron} change={(cron) => field({ cron })} />
          {v.changes?.map((c) => (
            <small key={c.from}>
              {stamp(c.from)} 起 · {c.cron}
            </small>
          ))}
        </>
      )}
      <small>
        {v.timezone} · {preview}
      </small>
      <label>
        对话
        <select
          value={v.sessionId || ""}
          onChange={(e) => field({ sessionId: e.target.value })}
        >
          <option value="">每次新开对话</option>
          {sessions.map((s) => (
            <option key={s.id} value={s.id}>
              继续 · {s.title}
            </option>
          ))}
        </select>
      </label>
      <details>
        <summary>工具与执行条件</summary>
        <label>
          专家
          <select
            value={v.expertId || ""}
            onChange={(e) => field({ expertId: e.target.value })}
          >
            <option value="">通用助手</option>
            {experts
              .filter((x) =>
                projects
                  .find((p) => p.id === v.projectId)
                  ?.expertIds.includes(x.id),
              )
              .map((x) => (
                <option key={x.id} value={x.id}>
                  {x.name}
                </option>
              ))}
          </select>
        </label>
        {caps
          .filter((c) => c.kind !== "skill")
          .map((c) => (
            <label className="inline-check" key={c.kind + c.id}>
              <input
                type="checkbox"
                checked={
                  !!v.capabilities?.some(
                    (x) => x.id === c.id && x.kind === c.kind,
                  )
                }
                onChange={(e) =>
                  field({
                    capabilities: e.target.checked
                      ? [...(v.capabilities || []), c]
                      : (v.capabilities || []).filter(
                          (x) => x.kind !== c.kind || x.id !== c.id,
                        ),
                  })
                }
              />
              {c.name}
            </label>
          ))}
        <label>
          条件
          <select
            value={v.condition || ""}
            onChange={(e) => field({ condition: e.target.value })}
          >
            <option value="">每次到点执行</option>
            <option value="changed">资料有变化时</option>
          </select>
        </label>
        {v.condition === "changed" && (
          <label>
            资料范围
            <textarea
              value={v.sourceScope}
              onChange={(e) => field({ sourceScope: e.target.value })}
            />
          </label>
        )}
        {v.projectId !== personal && (
          <label>
            结果
            <select
              value={v.output || ""}
              onChange={(e) =>
                field({
                  output: e.target.value,
                  ...(e.target.value === "project_context"
                    ? { sessionId: "", ...(!v.id ? { enabled: false } : {}) }
                    : {}),
                })
              }
            >
              <option value="">对话结果</option>
              <option value="project_context">整理项目说明</option>
            </select>
          </label>
        )}
        <label>
          通知目标
          <select
            value={
              v.notifyConnectionId
                ? v.notifyConnectionId + "|" + v.notifyConversationId
                : ""
            }
            onChange={(e) => {
              const [a, b] = e.target.value.split("|");
              field({ notifyConnectionId: a, notifyConversationId: b });
            }}
          >
            <option value="">仅保存结果</option>
            {targets.map((t) => (
              <option
                key={t.connectionId + t.conversationId}
                value={t.connectionId + "|" + t.conversationId}
              >
                {t.label}
              </option>
            ))}
          </select>
        </label>
      </details>
      {!v.id && (
        <label className="inline-check">
          <input
            type="checkbox"
            checked={v.enabled}
            onChange={(e) => field({ enabled: e.target.checked })}
          />
          启用这条任务
        </label>
      )}
      <footer>
        <button
          disabled={busy || !v.name.trim() || !v.instruction.trim()}
          className="primary"
          onClick={save}
        >
          保存任务
        </button>
        {v.id && (
          <button
            disabled={busy}
            onClick={async () => {
              if (!confirm("删除这条 AI 规则？")) return;
              try {
                await api("/schedules/" + v.id, undefined, "DELETE");
                saved();
              } catch (e) {
                notify((e as Error).message);
              }
            }}
          >
            删除规则
          </button>
        )}
      </footer>
    </Overlay>
  );
}
export function NoticePage({
  failure,
  select,
  openRule,
  notify,
  refreshBadge,
}: {
  failure: string;
  select: (id: string) => void;
  openRule: (id: string) => void;
  notify: (s: string) => void;
  refreshBadge: () => void;
}) {
  const [list, setList] = useState<any[]>([]),
    [f, setF] = useState<any>(null),
    [checked, setChecked] = useState(false),
    [busy, setBusy] = useState(false),
    [trace, setTrace] = useState("");
  const narrow = useNarrow(1099);
  const load = async () => {
    setList(await api("/notices"));
    refreshBadge();
  };
  useEffect(() => {
    void load().catch((e) => notify(e.message));
    const timer = setInterval(() => void load().catch(() => {}), 10000);
    return () => clearInterval(timer);
  }, []);
  useEffect(() => {
    let current = true;
    setF(null);
    setChecked(false);
    setTrace("");
    if (failure)
      api("/failures/" + encodeURIComponent(failure))
        .then((v) => {
          if (current) setF(v);
        })
        .catch((e) => notify(e.message));
    return () => {
      current = false;
    };
  }, [failure]);
  return (
    <section className="planner-screen">
      <div className="planner-main">
        <PageHeader
          title="站内信"
          actions={
            <>
              {" "}
              {list.length > 0 && (
                <button
                  onClick={async () => {
                    try {
                      await api("/notices/clear", {
                        through: Math.max(...list.map((n) => n.created)),
                      });
                      await load();
                    } catch (e) {
                      notify((e as Error).message);
                    }
                  }}
                >
                  清空
                </button>
              )}{" "}
            </>
          }
        />

        <div className="planner-list">
          {list.length === 0 && (
            <p className="planner-empty">
              <Mail />
              暂无站内信
            </p>
          )}
          {list.map((n) => (
            <button
              key={n.id}
              className="notice-row"
              onClick={async () => {
                try {
                  await api("/notices/" + n.id + "/read", {});
                  await load();
                  if (n.link.startsWith("#")) location.hash = n.link.slice(1);
                } catch (e) {
                  notify((e as Error).message);
                }
              }}
            >
              <strong>
                {!n.seen && "● "}
                {n.title}
              </strong>
              <span>{n.summary}</span>
              <small>{stamp(n.created)}</small>
            </button>
          ))}
        </div>
      </div>
      {f && (
        <Overlay
          title="执行详情"
          close={() => select("")}
          mode="drawer"
          modal={narrow}
          className="plan-detail"
        >
          <h2>{f.name}</h2>
          <p>{stamp(f.at)} · 失败</p>
          <RichText text={f.detail || "未获得完成回执"} />
          <button onClick={() => openRule(f.scheduleId)}>打开任务规则</button>
          {f.runId && (
            <button
              onClick={async () => {
                try {
                  const events = await api<any[]>(
                    "/failures/" + f.id + "/tools",
                  );
                  setTrace(
                    events
                      .filter(
                        (x) =>
                          x.runId === f.runId &&
                          x.type.startsWith("harness.tool/"),
                      )
                      .map(
                        (x) =>
                          x.type + " " + JSON.stringify(x.data).slice(0, 900),
                      )
                      .join("\n") || "没有工具记录",
                  );
                } catch (e) {
                  notify((e as Error).message);
                }
              }}
            >
              核对执行记录
            </button>
          )}
          {trace && <pre>{trace}</pre>}
          {f.checkExternal && (
            <label className="inline-check">
              <input
                type="checkbox"
                checked={checked}
                onChange={(e) => setChecked(e.target.checked)}
              />
              已核对外部操作回执
            </label>
          )}
          {f.retry ? (
            <Execution check={f.retry} />
          ) : (
            <button
              disabled={busy || (f.checkExternal && !checked)}
              onClick={async () => {
                setBusy(true);
                try {
                  await api("/failures/" + f.id + "/retry", { checked });
                  setF(await api("/failures/" + f.id));
                } catch (e) {
                  notify((e as Error).message);
                } finally {
                  setBusy(false);
                }
              }}
            >
              按当前配置重跑
            </button>
          )}
        </Overlay>
      )}
    </section>
  );
}
