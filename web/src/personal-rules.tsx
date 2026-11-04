import { useEffect, useState } from "react";
import { api } from "./api";
import { ChevronRight } from "lucide-react";

type Rules = {
  version: number;
  soul: string;
  agent: string;
  updatedAt: number;
};
export const personalRulesDraftKey = "pwb.personalRulesDraft";
export function PersonalRulesForm({
  openMemory,
  notify,
  dirtyChanged,
  busyChanged,
}: {
  openMemory: () => void;
  notify: (s: string) => void;
  dirtyChanged: (v: boolean) => void;
  busyChanged: (v: boolean) => void;
}) {
  const [saved, setSaved] = useState<Rules | null>(null),
    [draft, setDraft] = useState<Rules | null>(null);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [conflict, setConflict] = useState(false);
  const dirty =
    !!draft &&
    !!saved &&
    (draft.soul !== saved.soul || draft.agent !== saved.agent);
  const load = async (restore: boolean) => {
    setBusy(true);
    try {
      const r = await api<Rules>("/personal/rules");
      let d = r;
      if (restore) {
        try {
          const v = JSON.parse(
            sessionStorage.getItem(personalRulesDraftKey) || "null",
          );
          if (
            v &&
            typeof v.soul === "string" &&
            typeof v.agent === "string" &&
            typeof v.version === "number"
          )
            d = v;
        } catch {
          /* Ignore corrupt local drafts. */
        }
      }
      setSaved(r);
      setDraft(d);
      setConflict(d.version !== r.version);
      setError("");
    } catch (e: any) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    void load(true);
  }, []);
  useEffect(() => {
    dirtyChanged(dirty);
    if (dirty)
      sessionStorage.setItem(personalRulesDraftKey, JSON.stringify(draft));
    else if (saved) sessionStorage.removeItem(personalRulesDraftKey);
  }, [dirty, draft, saved, dirtyChanged]);
  useEffect(() => {
    busyChanged(busy);
    return () => busyChanged(false);
  }, [busy, busyChanged]);
  useEffect(() => {
    const protect = (e: BeforeUnloadEvent) => {
      if (dirty) {
        e.preventDefault();
        e.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", protect);
    return () => window.removeEventListener("beforeunload", protect);
  }, [dirty]);
  const save = async () => {
    if (!draft || busy) return;
    setBusy(true);
    setError("");
    try {
      const r = await api<Rules>("/personal/rules", draft, "PUT");
      setSaved(r);
      setDraft(r);
      setConflict(false);
      sessionStorage.removeItem(personalRulesDraftKey);
      notify("已保存，下一轮生效。");
    } catch (e: any) {
      setConflict(e.status === 409);
      setError(
        e.status === 409
          ? "其他页面已更新。当前修改已保留，请复制后重新读取并合并。"
          : e.message,
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="personalization-rules">
      {!draft ? (
        <p role="status">
          {error || "正在读取…"}
          <button onClick={() => void load(true)}>重试</button>
        </p>
      ) : (
        <>
          <label>
            怎么合作
            <textarea
              aria-label="怎么合作"
              spellCheck={false}
              disabled={busy}
              value={draft.agent}
              onChange={(e) => setDraft({ ...draft, agent: e.target.value })}
            />
          </label>
          <label>
            怎么陪我
            <textarea
              aria-label="怎么陪我"
              spellCheck={false}
              disabled={busy}
              value={draft.soul}
              onChange={(e) => setDraft({ ...draft, soul: e.target.value })}
            />
          </label>
          <button
            className="personalization-memory-link"
            disabled={busy}
            onClick={openMemory}
          >
            <span>我的记忆</span>
            <ChevronRight size={18} />
          </button>
          {(error || conflict) && (
            <div className="personalization-error" role="alert">
              <p>{error || "已有新内容，当前修改仍保留。"}</p>
              <button
                disabled={busy}
                onClick={() => {
                  if (
                    !dirty ||
                    window.confirm("放弃当前修改并读取最新内容？")
                  ) {
                    sessionStorage.removeItem(personalRulesDraftKey);
                    void load(false);
                  }
                }}
              >
                重新读取
              </button>
            </div>
          )}
          <footer className="personalization-save">
            <span role="status">{dirty ? "有未保存修改" : ""}</span>
            <button
              className="primary"
              disabled={busy || !dirty || conflict}
              onClick={() => void save()}
            >
              {busy ? "保存中…" : "保存"}
            </button>
          </footer>
        </>
      )}
    </div>
  );
}
