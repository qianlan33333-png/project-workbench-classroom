import { useEffect, useRef, useState } from "react";
import { MoreHorizontal, Search, X } from "lucide-react";
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
type Evidence = { items: Ref[]; hasMore: boolean; nextOffset: number };
export function MemoryPanel({
  notify,
  dirtyChanged,
  busyChanged,
}: {
  notify: (s: string) => void;
  dirtyChanged: (v: boolean) => void;
  busyChanged: (v: boolean) => void;
}) {
  const [data, setData] = useState<Understanding | null>(null),
    [busy, setBusy] = useState(false),
    [active, setActive] = useState<Paragraph | null>(null);
  const [edit, setEdit] = useState<string | null>(null),
    [error, setError] = useState("");
  const [evidence, setEvidence] = useState<Evidence | null>(null),
    [evidenceOpen, setEvidenceOpen] = useState(false),
    [query, setQuery] = useState("");
  const [evidenceID, setEvidenceID] = useState(""),
    [evidenceParagraph, setEvidenceParagraph] = useState<Paragraph | null>(
      null,
    );
  const requests = useRef(0),
    sourceRequest = useRef(0),
    alive = useRef(true),
    isBusy = useRef(false);
  const dirty = edit !== null && edit !== active?.text;
  const stale =
    !!active && !!data && !data.paragraphs.some((p) => p.id === active.id);
  const refresh = async () => {
    const n = ++requests.current,
      v = await api<Understanding>("/personal/understanding");
    if (alive.current && n === requests.current) setData(v);
  };
  useEffect(() => {
    alive.current = true;
    void refresh().catch((e) => {
      if (alive.current) setError(e.message);
    });
    const t = setInterval(() => {
      if (!isBusy.current) void refresh().catch(() => {});
    }, 4000);
    return () => {
      alive.current = false;
      requests.current++;
      sourceRequest.current++;
      clearInterval(t);
      dirtyChanged(false);
      busyChanged(false);
    };
  }, []);
  useEffect(() => {
    dirtyChanged(dirty);
  }, [dirty, dirtyChanged]);
  const act = async (f: () => Promise<void>) => {
    if (isBusy.current) return;
    isBusy.current = true;
    requests.current++;
    setBusy(true);
    busyChanged(true);
    setError("");
    try {
      await f();
      if (alive.current) await refresh();
    } catch (e: any) {
      if (alive.current)
        setError(
          e.status === 409
            ? "这段记忆已更新，当前修改仍保留。请取消后重新选择。"
            : e.message,
        );
    } finally {
      isBusy.current = false;
      if (alive.current) {
        setBusy(false);
        busyChanged(false);
      }
    }
  };
  const choose = (p: Paragraph) => {
    if (busy || (dirty && !window.confirm("放弃尚未保存的纠正？"))) return;
    setActive(active?.id === p.id ? null : p);
    setEdit(null);
    setEvidenceOpen(false);
    setEvidence(null);
    sourceRequest.current++;
    setError("");
  };
  const sources = async (p: Paragraph | null, q = "", offset = 0, id = "") => {
    const n = ++sourceRequest.current;
    setEvidenceOpen(true);
    setEvidenceID(id);
    setEvidenceParagraph(p);
    setError("");
    if (!offset) setEvidence(null);
    try {
      const params = new URLSearchParams({ offset: String(offset) });
      if (p) params.set("paragraphId", p.id);
      if (q) params.set("query", q);
      if (id) params.set("id", id);
      const v = await api<Evidence>(
        "/personal/understanding/evidence?" + params,
      );
      if (alive.current && n === sourceRequest.current)
        setEvidence(
          offset
            ? (previous) => ({
                ...v,
                items: [...(previous?.items || []), ...v.items],
              })
            : v,
        );
    } catch (e: any) {
      if (alive.current && n === sourceRequest.current) setError(e.message);
    }
  };
  const forget = (p: Paragraph) => {
    if (!data || !window.confirm("忘记这段记忆及相关依据？聊天记录会保留。"))
      return;
    void act(async () => {
      const only = p.id.startsWith("m:"),
        id = only ? p.id.slice(2) : p.id;
      await api(
        "/personal/memories/" +
          encodeURIComponent(id) +
          "?version=" +
          (only ? p.refs[0].version : data.version),
        undefined,
        "DELETE",
      );
      setActive(null);
      setEdit(null);
      setEvidenceOpen(false);
      sourceRequest.current++;
      notify("已忘记这段记忆。");
    });
  };
  const save = () => {
    if (!active || edit === null || !edit.trim()) return;
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
      sourceRequest.current++;
      notify("已保存纠正。");
    });
  };
  return (
    <div className="memory-content">
      {error && (
        <p className="personalization-error" role="alert">
          {error}
        </p>
      )}
      {!data ? (
        <p role="status">正在读取记忆…</p>
      ) : (
        <>
          {!data.paragraphs.length && (
            <p className="memory-empty">
              暂无记忆。你可以在对话中说“记住这件事”。
            </p>
          )}
          <div className="memory-rows">
            {data.paragraphs.map((p) => (
              <article
                key={p.id}
                className={`memory-row ${active?.id === p.id ? "selected" : ""}`}
              >
                <div className="memory-row-main">
                  <div>
                    {(p.status === "observation" ||
                      p.status === "unverified") && (
                      <small className="memory-observation">
                        待核观察{p.scope ? " · " + p.scope : ""}
                      </small>
                    )}
                    <RichText text={p.text} />
                  </div>
                  <button
                    className="memory-row-menu"
                    aria-label={"记忆操作：" + p.text.slice(0, 18)}
                    aria-expanded={active?.id === p.id}
                    disabled={busy}
                    onClick={() => choose(p)}
                  >
                    <MoreHorizontal size={20} />
                  </button>
                </div>
                {active?.id === p.id && (
                  <>
                    {edit === null ? (
                      <div className="memory-actions">
                        <button
                          disabled={busy || stale}
                          onClick={() => setEdit(p.text)}
                        >
                          纠正
                        </button>
                        <button
                          disabled={busy || stale}
                          onClick={() => forget(p)}
                        >
                          忘记
                        </button>
                        <button
                          disabled={busy || stale}
                          onClick={() => void sources(p)}
                        >
                          查看依据
                        </button>
                      </div>
                    ) : (
                      <div className="memory-editor">
                        <textarea
                          autoFocus
                          aria-label="纠正这段记忆"
                          value={edit}
                          disabled={busy}
                          onChange={(e) => setEdit(e.target.value)}
                        />
                        <div className="memory-edit-actions">
                          <button
                            className="primary"
                            disabled={busy || !edit.trim() || stale}
                            onClick={save}
                          >
                            保存纠正
                          </button>
                          <button
                            disabled={busy}
                            onClick={() => {
                              if (
                                !dirty ||
                                window.confirm("放弃尚未保存的纠正？")
                              )
                                setEdit(null);
                            }}
                          >
                            取消
                          </button>
                        </div>
                      </div>
                    )}
                  </>
                )}
              </article>
            ))}
          </div>
          {stale && (
            <p role="status" className="muted">
              这段记忆已更新，未保存的纠正仍保留。
            </p>
          )}
          {edit !== null && stale && (
            <div className="memory-editor">
              <textarea
                aria-label="已保留的纠正草稿"
                value={edit}
                onChange={(e) => setEdit(e.target.value)}
              />
              <button
                onClick={() => {
                  setEdit(null);
                  setActive(null);
                }}
              >
                取消纠正
              </button>
            </div>
          )}
          <details className="memory-more">
            <summary>更多</summary>
            <div>
              <button disabled={busy} onClick={() => void sources(null)}>
                查找记忆依据
              </button>
              <button
                disabled={busy}
                onClick={() =>
                  void act(async () => {
                    await api("/personal/understanding/refresh", {});
                    notify("已安排整理。");
                  })
                }
              >
                重新整理
              </button>
            </div>
          </details>
        </>
      )}
      {evidenceOpen && (
        <aside className="memory-evidence" aria-label="记忆依据">
          <header>
            <h3>{evidenceParagraph ? "这段记忆的依据" : "查找记忆依据"}</h3>
            <button
              aria-label="收起依据"
              onClick={() => {
                setEvidenceOpen(false);
                sourceRequest.current++;
              }}
            >
              <X size={18} />
            </button>
          </header>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void sources(null, query);
            }}
          >
            <input
              aria-label="查找记忆依据"
              placeholder="查找原话或细节"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            <button aria-label="搜索记忆">
              <Search size={17} />
            </button>
          </form>
          {!evidence ? (
            <p role="status">正在读取…</p>
          ) : (
            <>
              {evidence.items.map((r, i) => (
                <blockquote key={r.id + ":" + i}>
                  <small>
                    {r.origin === "legacy_unverified"
                      ? "旧记录，待核"
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
              {!evidence.items.length && (
                <p className="muted">没有找到有效依据。</p>
              )}
              {evidence.hasMore && (
                <button
                  onClick={() =>
                    void sources(
                      evidenceParagraph,
                      query,
                      evidence.nextOffset,
                      evidenceID,
                    )
                  }
                >
                  读取更多
                </button>
              )}
            </>
          )}
        </aside>
      )}
    </div>
  );
}
