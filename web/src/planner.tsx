import React, { useEffect, useRef, useState } from "react";
import {
  Bell,
  CalendarDays,
  ChevronLeft,
  ChevronRight,
  Clock,
  Plus,
  RefreshCw,
  X,
  Bot,
} from "lucide-react";
import {
  api,
  labels,
  stamp,
  type Project,
  type Session,
  type Capability,
} from "./api";
import { RichText } from "./file-ui";

const day = 86400000,
  offset = 8 * 3600000;
export const personal = "personal-assistant";
const date = (n: number) => new Date(n + offset).toISOString().slice(0, 10);
const fieldTime = (n: number) =>
  new Date(n + offset).toISOString().slice(0, 16);
const time = (n: number) => fieldTime(n).slice(11);
const instant = (s: string) =>
  Date.parse(s + (s.length === 10 ? "T00:00" : "") + ":00+08:00");
const weekStart = (n: number) => n - new Date(n + offset).getUTCDay() * day;
const weekDays = ["日", "一", "二", "三", "四", "五", "六"];
type Item = {
  id: string;
  kind: string;
  title: string;
  start: number;
  end: number;
  description?: string;
  destination?: string;
  sync?: string;
  error?: string;
  pending?: string;
  allDay?: boolean;
  projectId?: string;
  version: number;
  original?: number;
  repeated?: boolean;
  state?: string;
  runId?: string;
  operationKey?: string;
  link?: string;
};
const projectName = (id: string | undefined, ps: Project[]) =>
  ps.find((p) => p.id === id)?.name || "个人";
const stateLabel = (s: string) =>
  ({
    active: "已有执行",
    skipped: "已跳过",
    ready: "已同步",
    pending: "待处理",
    unknown: "待核对",
    cancelled: "已取消",
  })[s] ||
  labels[s] ||
  s;
function Close({ close }: { close: () => void }) {
  return (
    <button className="icon" onClick={close} aria-label="关闭详情">
      <X size={17} />
    </button>
  );
}
function Frame({
  title,
  close,
  children,
}: {
  title: string;
  close: () => void;
  children: React.ReactNode;
}) {
  return (
    <aside className="plan-detail" aria-label={title}>
      <header>
        <b>{title}</b>
        <Close close={close} />
      </header>
      {children}
    </aside>
  );
}
function useAction(notify: (s: string) => void) {
  const guard = useRef(false);
  const [busy, setBusy] = useState(false);
  return {
    busy,
    act: async (f: () => Promise<void>) => {
      if (guard.current) return;
      guard.current = true;
      setBusy(true);
      try {
        await f();
      } catch (e) {
        notify((e as Error).message);
      } finally {
        guard.current = false;
        setBusy(false);
      }
    },
  };
}
function EventEditor({
  item,
  projects,
  close,
  saved,
  openProject,
  notify,
}: {
  item: Item;
  projects: Project[];
  close: () => void;
  saved: () => void;
  openProject: (id: string) => void;
  notify: (s: string) => void;
}) {
  const [v, set] = useState({ ...item }),
    [key] = useState(() => crypto.randomUUID()),
    { act, busy } = useAction(notify);
  const field = (s: Partial<Item>) => set((o) => ({ ...o, ...s }));
  const commit = (action: string) =>
    act(async () => {
      const reply = await api<Item>("/calendar/" + action, {
        ...v,
        operation: key,
      });
      if (reply.pending || reply.sync === "failed") {
        set({ ...reply, kind: "event" });
        notify(reply.error || "飞书操作待核对");
      } else {
        saved();
      }
    });
  return (
    <Frame title={v.id ? "日程详情" : "新建日程"} close={close}>
      <label>
        日程名称
        <input
          value={v.title}
          onChange={(e) => field({ title: e.target.value })}
          placeholder="例如 产品评审"
        />
      </label>
      <label>
        保存到
        <select
          disabled={!!v.id}
          value={v.destination}
          onChange={(e) => field({ destination: e.target.value })}
        >
          <option value="">选择目的地</option>
          <option value="cloud">工作台云端</option>
          <option value="feishu">飞书日历</option>
          <option value="both">云端和飞书</option>
        </select>
      </label>
      <label>
        归属
        <select
          disabled={!!v.id}
          value={v.projectId || ""}
          onChange={(e) => field({ projectId: e.target.value })}
        >
          <option value="">个人</option>
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <label className="inline-check">
        <input
          type="checkbox"
          checked={!!v.allDay}
          onChange={(e) => {
            const start = instant(date(v.start));
            field(
              e.target.checked
                ? {
                    allDay: true,
                    start,
                    end: Math.max(start + day, instant(date(v.end - 1)) + day),
                  }
                : { allDay: false },
            );
          }}
        />
        全天
      </label>
      {(["start", "end"] as const).map((k) => (
        <label key={k}>
          {k === "start" ? "开始" : "结束"}（北京时间）
          <input
            type={v.allDay ? "date" : "datetime-local"}
            value={
              v.allDay ? date(v[k] - (k === "end" ? 1 : 0)) : fieldTime(v[k])
            }
            onChange={(e) => {
              if (e.target.value) {
                const n =
                  instant(e.target.value) + (v.allDay && k === "end" ? day : 0);
                field({ [k]: n });
              }
            }}
          />
        </label>
      ))}
      <label>
        说明与资料
        <textarea
          value={v.description || ""}
          onChange={(e) => field({ description: e.target.value })}
        />
      </label>
      {v.projectId && (
        <button onClick={() => openProject(v.projectId!)}>
          打开所属项目 →
        </button>
      )}
      {v.description && (
        <details>
          <summary>查看关联资料</summary>
          <RichText text={v.description} />
        </details>
      )}
      {v.link && /^https?:\/\//i.test(v.link) && (
        <a href={v.link} target="_blank" rel="noreferrer">
          打开飞书日程 ↗
        </a>
      )}
      {v.error && <p role="status">{v.error}</p>}
      <footer>
        <button
          className="primary"
          disabled={
            busy || !v.title.trim() || !v.destination || v.end <= v.start
          }
          onClick={() =>
            commit(v.pending ? "reconcile" : v.id ? "edit" : "create")
          }
        >
          {v.pending ? "核对飞书操作" : "保存日程"}
        </button>
        {v.pending && v.id && <button disabled={busy} onClick={()=>window.confirm("采用飞书上的当前日程并结束本次待核对操作？")&&commit("accept_remote")}>采用飞书现状</button>}
        {v.id && !v.pending && (
          <button
            disabled={busy}
            onClick={() => window.confirm("取消这条日程？") && commit("delete")}
          >
            取消日程
          </button>
        )}
      </footer>
    </Frame>
  );
}
export function CalendarPage({
  projects,
  notify,
  openTask,
  openRule,
  openProject,
}: {
  projects: Project[];
  notify: (s: string) => void;
  openTask: (id: string) => void;
  openProject: (id: string) => void;
  openRule: (id: string) => void;
}) {
  const [mode, setMode] = useState(sessionStorage.getItem("cal.view") || "周"),
    [filter, setFilter] = useState(
      sessionStorage.getItem("cal.filter") || "全部",
    ),
    [selected, setSelected] = useState(
      sessionStorage.getItem("cal.date") || date(Date.now()),
    ),
    [items, setItems] = useState<Item[]>([]),
    [detail, setDetail] = useState<Item | null>(null),
    [warning, setWarning] = useState(""),
    [rev, bump] = useState(0),
    [loading, setLoading] = useState(false),
    [editAt, setEditAt] = useState(""),
    [cron, setCron] = useState(""),
    [result, setResult] = useState(""),
    { act, busy } = useAction(notify);
  const scroll = useRef<HTMLDivElement>(null),
    drag = useRef<Item | null>(null);
  const ts = instant(selected),
    month = selected.slice(0, 7),
    first = instant(month + "-01"),
    lo = mode === "月" ? weekStart(first) : mode === "周" ? weekStart(ts) : ts;
  const endMonth = new Date(first + offset);
  endMonth.setUTCMonth(endMonth.getUTCMonth() + 1);
  const days =
      mode === "月"
        ? Math.ceil((endMonth.getTime() - offset - lo) / (7 * day)) * 7
        : mode === "周"
          ? 7
          : 1,
    hi = lo + days * day;
  const cols = Array.from({ length: days }, (_, i) => lo + i * day);
  useEffect(() => {
    sessionStorage.setItem("cal.view", mode);
    sessionStorage.setItem("cal.date", selected);
    sessionStorage.setItem("cal.filter", filter);
  }, [mode, selected, filter]);
  useEffect(() => {
    let ok = true;
    const abort = new AbortController();
    setLoading(true);
    setWarning("");
    setItems([]);
    const read = async () => {
      const path = "/calendar?start=" + lo + "&end=" + hi;
      const local = await api<{ items: Item[] }>(
        path,
        undefined,
        undefined,
        abort.signal,
      );
      if (!ok) return;
      setItems(local.items);
      if (filter !== "AI 执行") {
        const full = await api<{ items: Item[]; providerError?: string }>(
          path + "&remote=1",
          undefined,
          undefined,
          abort.signal,
        );
        if (ok) {
          setItems(full.items);
          setWarning(full.providerError || "");
        }
      }
    };
    void read()
      .catch((e) => ok && setWarning(e.message))
      .finally(() => ok && setLoading(false));
    return () => {
      ok = false;
      abort.abort();
    };
  }, [lo, hi, rev, filter === "AI 执行"]);
  useEffect(() => {
    if (scroll.current)
      scroll.current.scrollTop = Number(
        sessionStorage.getItem("cal.scroll." + mode) ||
          (mode === "月" ? 0 : 340),
      );
  }, [mode]);
  const selection = useRef(0);
  const choose = (v: Item) => {
    const request = ++selection.current;
    setDetail(v);
    setResult("");
    setEditAt(fieldTime(v.start));
    setCron("");
    if (v.runId)
      void api("/runs/" + v.runId)
        .then((r) => {
          if (request === selection.current)
            setResult(r.result || r.error || "等待执行");
        })
        .catch((e) => notify(e.message));
  };
  const fresh = () => {
    setDetail(null);
    bump((i) => i + 1);
  };
  const add = (start = ts + 9 * 3600000) =>
    choose({
      kind: "event",
      id: "",
      version: 0,
      title: "",
      destination: "",
      start,
      end: start + 3600000,
    });
  const step = (d: number) => {
    if (mode === "月") {
      const m = new Date(first + offset);
      m.setUTCMonth(m.getUTCMonth() + d);
      setSelected(date(m.getTime() - offset));
    } else setSelected(date(ts + d * days * day));
  };
  const editAI = (v: Item, op: string, to: number, replaceExceptions = false) =>
    act(async () => {
      const args = {
        original: v.original,
        at: to,
        skip: op === "skip",
        version: v.version,
        cron,
        replaceExceptions,
      };
      const url =
        "/schedules/" +
        v.id +
        "/" +
        (op === "series" ? "series" : "occurrence");
      try {
        await api(url, args);
      } catch (e) {
        if (
          op !== "series" ||
          !(e as Error).message.includes("例外") ||
          !confirm((e as Error).message + "。确认移除这些例外？")
        )
          throw e;
        await api(url, { ...args, replaceExceptions: true });
      }
      fresh();
    });
  const drop = (e: React.DragEvent, d: number, grid: boolean) => {
    e.preventDefault();
    const v = drag.current;
    drag.current = null;
    if (!v) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const at =
      d +
      (grid
        ? Math.min(
            95,
            Math.max(
              0,
              Math.floor(((e.clientY - rect.top) / rect.height) * 96),
            ),
          ) * 900000
        : v.start - instant(date(v.start)));
    if (v.kind === "ai") {
      choose(v);
      setEditAt(fieldTime(at));
      return;
    }
    choose({ ...v, start: at, end: at + v.end - v.start });
  };
  const visible = items.filter(
    (v) => filter === "全部" || (filter === "AI 执行") === (v.kind === "ai"),
  );
  const item = (v: Item, style?: React.CSSProperties) => (
    <button
      key={v.kind + v.id + v.start + (v.operationKey || "")}
      title={v.title}
      className={"cal-item " + v.kind}
      style={style}
      draggable={v.kind === "event" || (v.kind === "ai" && !v.state)}
      onDragStart={() => {
        drag.current = v;
      }}
      onClick={() => choose(v)}
    >
      <span>
        {v.kind === "ai" ? "AI · " : ""}
        {mode === "月" && !v.allDay && time(v.start) + " "}
        {v.title}
      </span>
      <small>
        {v.allDay ? "全天" : time(v.start)}
        {v.state
          ? " · " + stateLabel(v.state)
          : v.kind === "event" && !v.allDay
            ? "–" + time(v.end)
            : ""}
      </small>
    </button>
  );
  return (
    <section className="planner-screen">
      <div className="planner-main">
        <header className="planner-title">
          <div>
            <small>时间安排</small>
            <h1>日历</h1>
          </div>
          <button className="primary" onClick={() => add()}>
            <Plus size={15} />
            新建日程
          </button>
        </header>
        <div className="cal-controls">
          <button onClick={() => setSelected(date(Date.now()))}>今天</button>
          <button aria-label="上一期" onClick={() => step(-1)}>
            <ChevronLeft size={16} />
          </button>
          <button aria-label="下一期" onClick={() => step(1)}>
            <ChevronRight size={16} />
          </button>
          <input
            type="date"
            aria-label="选择日期"
            value={selected}
            onChange={(e) => e.target.value && setSelected(e.target.value)}
          />
          <div className="plan-segment">
            {["日", "周", "月"].map((v) => (
              <button
                aria-pressed={mode === v}
                key={v}
                onClick={() => setMode(v)}
              >
                {v}
              </button>
            ))}
          </div>
        </div>
        <div className="cal-filters">
          {["全部", "人的安排", "AI 执行"].map((v) => (
            <button
              aria-pressed={filter === v}
              key={v}
              onClick={() => setFilter(v)}
            >
              {v}
            </button>
          ))}
          <small>{loading ? "读取中…" : warning || "北京时间 GMT+8"}</small>
          <button aria-label="刷新日历" onClick={() => bump((n) => n + 1)}>
            <RefreshCw size={14} />
          </button>
        </div>
        <div
          className="cal-scroll"
          ref={scroll}
          onScroll={(e) =>
            sessionStorage.setItem(
              "cal.scroll." + mode,
              String(e.currentTarget.scrollTop),
            )
          }
        >
          {mode === "月" ? (
            <div className="month-grid">
              {weekDays.map((d) => (
                <b key={d}>周{d}</b>
              ))}
              {cols.map((d) => (
                <div
                  className={date(d) === date(Date.now()) ? "is-today" : ""}
                  key={d}
                  onDragOver={(e) => e.preventDefault()}
                  onDrop={(e) => drop(e, d, false)}
                >
                  <button
                    className="date-num"
                    onClick={() => {
                      setSelected(date(d));
                      setMode("日");
                    }}
                  >
                    {Number(date(d).slice(-2))}
                  </button>
                  {visible
                    .filter(
                      (v) => v.start < d + day && (v.end || v.start + 1) > d,
                    )
                    .slice(0, 3)
                    .map((v) => item(v))}
                  {visible.filter(
                    (v) => v.start < d + day && (v.end || v.start + 1) > d,
                  ).length > 3 && (
                    <button
                      onClick={() => {
                        setSelected(date(d));
                        setMode("日");
                      }}
                    >
                      另有{" "}
                      {visible.filter(
                        (v) => v.start < d + day && (v.end || v.start + 1) > d,
                      ).length - 3}{" "}
                      项
                    </button>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <div
              className="cal-grid"
              style={{
                gridTemplateColumns: "48px repeat(" + days + ", minmax(0,1fr))",
              }}
            >
              <div className="cal-head">GMT+8</div>
              {cols.map((d) => (
                <div
                  key={d}
                  className={
                    "cal-head " +
                    (date(d) === date(Date.now()) ? "is-today" : "")
                  }
                >
                  周{weekDays[new Date(d + offset).getUTCDay()]}{" "}
                  <b>{Number(date(d).slice(-2))}</b>
                </div>
              ))}
              <small>全天</small>
              {cols.map((d) => (
                <div className="all-day" key={d}>
                  {visible
                    .filter((v) => v.allDay && v.start < d + day && v.end > d)
                    .map((v) => item(v))}
                </div>
              ))}
              <div className="hour-label">
                {Array.from({ length: 24 }, (_, h) => (
                  <small key={h}>{h}:00</small>
                ))}
              </div>
              {cols.map((d) => {
                const lanes: number[] = [];
                const vs = visible
                  .filter(
                    (v) =>
                      !v.allDay &&
                      v.start < d + day &&
                      (v.end || v.start + 1) > d,
                  )
                  .sort((a, b) => a.start - b.start);
                const placed: { v: Item; lane: number; width: number }[] = [];
                let groupStart = 0,
                  groupEnd = 0;
                const finishGroup = () => {
                  for (let i = groupStart; i < placed.length; i++)
                    placed[i].width = lanes.length;
                  groupStart = placed.length;
                  lanes.length = 0;
                };
                for (const v of vs) {
                  if (v.start >= groupEnd) finishGroup();
                  let lane = lanes.findIndex((end) => end <= v.start);
                  if (lane < 0) lane = lanes.length;
                  lanes[lane] = Math.max(v.start + 1800000, v.end || 0);
                  groupEnd = Math.max(...lanes);
                  placed.push({ v, lane, width: 1 });
                }
                finishGroup();
                return (
                  <div
                    className="hour-col"
                    key={d}
                    onDragOver={(e) => e.preventDefault()}
                    onDrop={(e) => drop(e, d, true)}
                    onDoubleClick={(e) => {
                      if (e.target === e.currentTarget)
                        add(
                          d +
                            Math.floor((e.nativeEvent.offsetY / 48) * 4) *
                              900000,
                        );
                    }}
                  >
                    {placed.map(({ v, lane, width }) =>
                      item(v, {
                        top: (Math.max(0, v.start - d) / day) * 100 + "%",
                        height: Math.max(
                          23,
                          ((Math.min(v.end || v.start, d + day) -
                            Math.max(d, v.start)) /
                            day) *
                            1152,
                        ),
                        left: (100 * lane) / width + "%",
                        width: 100 / width + "%",
                      }),
                    )}
                    {date(d) === date(Date.now()) && (
                      <div
                        className="now-line"
                        style={{ top: ((Date.now() - d) / day) * 100 + "%" }}
                      />
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>
      {detail?.kind === "event" ? (
        <EventEditor
          key={detail.id + detail.start}
          item={detail}
          openProject={openProject}
          projects={projects}
          close={() => setDetail(null)}
          saved={fresh}
          notify={notify}
        />
      ) : (
        detail && (
          <Frame
            title={detail.kind === "ai" ? "AI 执行详情" : "待办详情"}
            close={() => setDetail(null)}
          >
            <h2>{detail.title}</h2>
            <p>
              {projectName(detail.projectId, projects)} · {stamp(detail.start)}
            </p>
            <RichText text={result || detail.description || ""} />
            {detail.kind === "todo" ? (
              <button onClick={() => openTask(detail.id)}>打开待办</button>
            ) : (
              <>
                {detail.state && <p>{stateLabel(detail.state)}</p>}
                {!detail.state && (
                  <>
                    <label>
                      仅此次的执行时间
                      <input
                        type="datetime-local"
                        value={editAt}
                        onChange={(e) => setEditAt(e.target.value)}
                      />
                    </label>
                    <button
                      disabled={busy}
                      onClick={() => editAI(detail, "once", instant(editAt))}
                    >
                      保存这次时间
                    </button>
                    <button
                      disabled={busy}
                      onClick={() => editAI(detail, "skip", 0)}
                    >
                      跳过这一次
                    </button>
                    {detail.repeated && (
                      <details>
                        <summary>此次及以后</summary>
                        <label>
                          新周期（五段 cron）
                          <input
                            value={cron}
                            onChange={(e) => setCron(e.target.value)}
                            placeholder="0 20 * * 2"
                          />
                        </label>
                        <button
                          disabled={busy || !cron}
                          onClick={() => editAI(detail, "series", 0)}
                        >
                          保存新周期
                        </button>
                      </details>
                    )}
                  </>
                )}
                <button onClick={() => openRule(detail.id)}>
                  打开任务规则
                </button>
              </>
            )}
          </Frame>
        )
      )}
    </section>
  );
}
