import React, { useEffect, useRef, useState } from "react";
import { X, RefreshCw, Search } from "lucide-react";
import { api, stamp } from "./api";
import { RichText } from "./file-ui";
type Ref = {
  origin?: string;
  id: string;
  version: number;
  quote: string;
  event?: string;
  at: number;
  expiresAt?: number;
};
type Paragraph = {
  id: string;
  text: string;
  status: string;
  scope?: string;
  refs: Ref[];
};
type Understanding = {
  version: number;
  paragraphs: Paragraph[];
  text: string;
  state: string;
  error?: string;
  updatedAt: number;
};
export function MemoryPanel({
  close,
  notify,
}: {
  close: () => void;
  notify: (s: string) => void;
}) {
  const [data, setData] = useState<Understanding | null>(null),
    [busy, setBusy] = useState(false),
    [active, setActive] = useState<Paragraph | null>(null),
    [evidence, setEvidence] = useState<{
      items: Ref[];
      hasMore: boolean;
      nextOffset: number;
    } | null>(null),
    [query, setQuery] = useState(""),
    [evidenceID, setEvidenceID] = useState(""),
    [evidenceOpen, setEvidenceOpen] = useState(false),
    [edit, setEdit] = useState<string | null>(null),
    [error, setError] = useState("");
  const request = useRef(0);
  const refresh = async () => setData(await api("/personal/understanding"));
  useEffect(() => {
    void refresh().catch((e) => setError(e.message));
    const t = setInterval(() => {
      void refresh().catch(() => {});
    }, 4000);
    return () => clearInterval(t);
  }, []);
  const act = async (f: () => Promise<void>) => {
    setBusy(true);
    setError("");
    try {
      await f();
      await refresh();
    } catch (e: any) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  };
  const sources = async (p: Paragraph | null, q = "", offset = 0, id = "") => {
    const n = ++request.current;
    setEvidenceOpen(true);
    setEvidenceID(id);
    setError("");
    try {
      const params = new URLSearchParams({ offset: String(offset) });
      if (p) params.set("paragraphId", p.id);
      if (q) params.set("query", q);
      if (id) params.set("id", id);
      const v = await api("/personal/understanding/evidence?" + params);
      if (n === request.current) setEvidence(v);
    } catch (e: any) {
      if (n === request.current) setError(e.message);
    }
  };
  const select = (p: Paragraph) => {
    setActive(p);
    setEdit(null);
    setEvidenceOpen(false);
    setEvidence(null);
    request.current++;
  };
  const forget = () => {
    if (!active || !data) return;
    if (
      !window.confirm(
        "忘记这段理解及相关依据？来自同一表达的派生理解也会停止使用，并清理个人助手原生上下文。可见聊天仍保留，后续不从旧聊天自动恢复。",
      )
    )
      return;
    void act(async () => {
      const only = active.id.startsWith("m:");
      const id = only ? active.id.slice(2) : active.id;
      await api(
        "/personal/memories/" +
          encodeURIComponent(id) +
          "?version=" +
          (only ? active.refs[0].version : data.version),
        undefined,
        "DELETE",
      );
      setActive(null);
      setEdit(null);
      setEvidenceOpen(false);
      notify("已忘记相关理解与依据，原生上下文已清理。");
    });
  };
  const save = () => {
    if (!active || edit === null) return;
    void act(async () => {
      await api("/personal/memories", {
        text: edit,
        kind: "fact",
        replaces: [active.id],
        baseRefs: Object.fromEntries(active.refs.map((r) => [r.id, r.version])),
      });
      setActive(null);
      setEdit(null);
      setEvidenceOpen(false);
      notify("纠正已生效，会话保持连续。");
    });
  };
  const stale =
    !!active && !!data && !data.paragraphs.some((p) => p.id === active.id);
  const dismiss = () => {
    if (
      edit !== null &&
      edit !== active?.text &&
      !window.confirm("放弃尚未保存的纠正？")
    )
      return;
    close();
  };
  return (
    <div className="today-modal-backdrop">
      <section
        className="today-modal understanding-modal"
        role="dialog"
        aria-modal="true"
        aria-label="你目前如何理解我"
      >
        <header>
          <h2>你目前如何理解我</h2>
          <button aria-label="关闭个人理解" onClick={dismiss}>
            <X size={18} />
          </button>
        </header>
        <p className="muted understanding-intro">
          留下对协作有用的理解，具体细节需要时再查。你可以随时纠正或忘记。
        </p>
        <div className="understanding-status">
          <span>
            {data?.updatedAt
              ? "更新于 " + stamp(data.updatedAt)
              : "尚未完成首次整理"}{" "}
            ·{" "}
            {data?.state === "failed"
              ? "整理未完成，保留有效内容"
              : data?.state === "pending"
                ? "等待整理"
                : "已整理"}
          </span>
          <button
            disabled={busy}
            onClick={() =>
              void act(async () => {
                await api("/personal/understanding/refresh", {});
                notify("已安排整理，有新内容时更新。");
              })
            }
          >
            <RefreshCw size={14} />
            重新整理
          </button>
        </div>
        {error && (
          <p role="alert" className="understanding-error">
            {error}
          </p>
        )}
        {!data ? (
          <p role="status">正在读取理解…</p>
        ) : (
          <>
            {!data.paragraphs.length && (
              <div className="understanding-empty">
                <h3>慢慢了解，从真实交流开始</h3>
                <p className="muted">
                  目前没有有效的个人理解。明确说“记住这件事”会立即保存；日常交流会在整理时归纳，不把一次选择当成长期偏好。
                </p>
              </div>
            )}
            <div className="understanding-prose">
              {data.paragraphs.map((p) => (
                <section
                  key={p.id}
                  className={
                    active?.id === p.id
                      ? "understanding-paragraph selected"
                      : "understanding-paragraph"
                  }
                >
                  {(p.status === "observation" ||
                    p.status === "unverified") && (
                    <small className="understanding-observation">
                      待核观察{p.scope ? " · " + p.scope : ""}
                    </small>
                  )}
                  <RichText text={p.text} />
                  <button
                    className="understanding-select"
                    aria-label={"查看这段理解：" + p.text.slice(0, 18)}
                    onClick={() => select(p)}
                  >
                    查看这段理解
                  </button>
                </section>
              ))}
            </div>
            <div className="understanding-actions">
              <button
                disabled={!active || busy || stale}
                onClick={() => {
                  setEdit(active!.text);
                  setEvidenceOpen(false);
                }}
              >
                纠正
              </button>
              <button disabled={busy} onClick={() => void sources(active)}>
                查看依据
              </button>
              <button disabled={!active || busy || stale} onClick={forget}>
                忘记
              </button>
              <span className="muted">
                {stale
                  ? "这段理解已更新，请重新选择；未保存的纠正仍保留。"
                  : active
                    ? "已选中一段理解"
                    : "选择一段后可纠正或忘记"}
              </span>
            </div>
            {edit !== null && (
              <div className="memory-editor">
                <label>
                  这段理解应当是
                  <textarea
                    autoFocus
                    aria-label="纠正这段理解"
                    value={edit}
                    onChange={(e) => setEdit(e.target.value)}
                  />
                </label>
                <div className="understanding-actions">
                  <button
                    className="primary"
                    disabled={busy || !edit.trim()}
                    onClick={save}
                  >
                    保存纠正
                  </button>
                  <button disabled={busy} onClick={() => setEdit(null)}>
                    取消
                  </button>
                </div>
              </div>
            )}
            {evidenceOpen && (
              <aside
                className="understanding-evidence"
                aria-label="个人理解依据"
              >
                <header>
                  <h3>{active ? "这段理解的依据" : "查找准确细节"}</h3>
                  <button
                    aria-label="收起依据"
                    onClick={() => {
                      setEvidenceOpen(false);
                      request.current++;
                    }}
                  >
                    <X size={16} />
                  </button>
                </header>
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    setActive(null);
                    void sources(null, query);
                  }}
                >
                  <label className="sr-only" htmlFor="memory-query">
                    查找记住的细节
                  </label>
                  <input
                    id="memory-query"
                    placeholder="查找记住的日期、约定或原话"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                  />
                  <button aria-label="搜索记忆">
                    <Search size={16} />
                  </button>
                </form>
                {evidence?.items.map((r, i) => (
                  <blockquote key={r.id + ":" + i}>
                    <small>
                      {r.origin === "legacy_unverified"
                        ? "旧记录（缺少原话，待核）"
                        : r.origin === "owner"
                          ? "本人填写或纠正"
                          : "个人对话原话"}{" "}
                      · {stamp(r.at)}
                      {r.expiresAt ? " · 有效至 " + stamp(r.expiresAt) : ""}
                    </small>
                    <p>{r.quote}</p>
                    <button onClick={() => void sources(null, "", 0, r.id)}>
                      读取完整依据
                    </button>
                  </blockquote>
                ))}
                {evidence && !evidence.items.length && (
                  <p className="muted">
                    没有找到有效依据。被遗忘、已失效或被纠正的内容不会用于当前理解。
                  </p>
                )}
                {evidence?.hasMore && (
                  <button
                    onClick={() =>
                      void sources(
                        active,
                        query,
                        evidence.nextOffset,
                        evidenceID,
                      )
                    }
                  >
                    查看更多依据
                  </button>
                )}
              </aside>
            )}
          </>
        )}
      </section>
    </div>
  );
}
