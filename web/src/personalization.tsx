import { useEffect, useRef, useState } from "react";
import { ArrowLeft, X } from "lucide-react";
import { PersonalRulesForm, personalRulesDraftKey } from "./personal-rules";
import { MemoryPanel } from "./personal-understanding";

export function Personalization({
  close,
  notify,
  returnFocus,
}: {
  close: () => void;
  notify: (s: string) => void;
  returnFocus: HTMLElement | null;
}) {
  const [page, setPage] = useState<"rules" | "memory">("rules");
  const [rulesDirty, setRulesDirty] = useState(false),
    [memoryDirty, setMemoryDirty] = useState(false);
  const [rulesBusy, setRulesBusy] = useState(false),
    [memoryBusy, setMemoryBusy] = useState(false);
  const dialog = useRef<HTMLElement>(null),
    memoryButton = useRef<HTMLButtonElement | null>(null);
  const busy = rulesBusy || memoryBusy;
  const dismiss = () => {
    if (
      busy ||
      ((rulesDirty || memoryDirty) &&
        !window.confirm("放弃尚未保存的修改并关闭？"))
    )
      return;
    sessionStorage.removeItem(personalRulesDraftKey);
    close();
  };
  const back = () => {
    if (busy || (memoryDirty && !window.confirm("放弃尚未保存的纠正？")))
      return;
    setPage("rules");
    setMemoryDirty(false);
    requestAnimationFrame(() => memoryButton.current?.focus());
  };
  useEffect(() => {
    const previous =
      returnFocus || (document.activeElement as HTMLElement | null);
    dialog.current?.focus();
    return () => {
      requestAnimationFrame(() => previous?.isConnected && previous.focus());
    };
  }, []);
  useEffect(() => {
    dialog.current?.focus();
  }, [page]);
  return (
    <div
      className="personalization-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) dismiss();
      }}
    >
      <section
        ref={dialog}
        tabIndex={-1}
        className="personalization-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={page === "rules" ? "个性化" : "我的记忆"}
        onKeyDown={(e) => {
          if (e.defaultPrevented) return;
          if (e.key === "Escape") {
            e.preventDefault();
            dismiss();
          }
          if (e.key === "Tab") {
            const nodes = [
              ...(dialog.current?.querySelectorAll<HTMLElement>(
                'button:not(:disabled),textarea:not(:disabled),input:not(:disabled),a[href],summary,[tabindex]:not([tabindex="-1"])',
              ) || []),
            ].filter(
              (n) => !n.closest("[hidden]") && n.getClientRects().length,
            );
            const first = nodes[0],
              last = nodes.at(-1);
            if (
              e.shiftKey &&
              (document.activeElement === first ||
                document.activeElement === dialog.current)
            ) {
              e.preventDefault();
              last?.focus();
            } else if (
              !e.shiftKey &&
              (document.activeElement === last ||
                document.activeElement === dialog.current)
            ) {
              e.preventDefault();
              first?.focus();
            }
          }
        }}
      >
        <header className="personalization-header">
          {page === "memory" ? (
            <button
              className="personalization-back"
              disabled={busy}
              onClick={back}
            >
              <ArrowLeft size={19} />
              个性化
            </button>
          ) : (
            <span />
          )}
          <h2>{page === "memory" ? "我的记忆" : "个性化"}</h2>
          <button
            className="personalization-close"
            aria-label="关闭个性化"
            disabled={busy}
            onClick={dismiss}
          >
            <X size={21} />
          </button>
        </header>
        <div
          className="personalization-body"
          hidden={page !== "rules"}
          ref={(el) => {
            memoryButton.current =
              el?.querySelector(".personalization-memory-link") || null;
          }}
        >
          <PersonalRulesForm
            openMemory={() => setPage("memory")}
            notify={notify}
            dirtyChanged={setRulesDirty}
            busyChanged={setRulesBusy}
          />
        </div>
        {page === "memory" && (
          <div className="personalization-body memory-page">
            <MemoryPanel
              notify={notify}
              dirtyChanged={setMemoryDirty}
              busyChanged={setMemoryBusy}
            />
          </div>
        )}
      </section>
    </div>
  );
}
