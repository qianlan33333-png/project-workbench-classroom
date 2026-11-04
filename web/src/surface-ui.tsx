import { useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";

export function PageHeader({
  title,
  actions,
  children,
}: {
  title: string;
  actions?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <header className="surface-heading">
      <h1>{title}</h1>
      {actions && <div className="surface-actions">{actions}</div>}
      {children}
    </header>
  );
}
export function EmptyState({
  children,
  action,
}: {
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="surface-empty">
      <p>{children}</p>
      {action}
    </div>
  );
}
export function useNarrow(maxWidth = 767) {
  const [narrow, setNarrow] = useState(
    () => window.matchMedia(`(max-width: ${maxWidth}px)`).matches,
  );
  useEffect(() => {
    const q = window.matchMedia(`(max-width: ${maxWidth}px)`);
    const change = () => setNarrow(q.matches);
    q.addEventListener("change", change);
    change();
    return () => q.removeEventListener("change", change);
  }, [maxWidth]);
  return narrow;
}
let bodyLocks = 0,
  bodyOverflow = "";
const surfaces: HTMLElement[] = [];
function syncModalSurfaces() {
  const modalIndex = surfaces
    .map((n) => n.getAttribute("aria-modal") === "true")
    .lastIndexOf(true);
  const app = document.getElementById("root");
  if (app) app.inert = modalIndex >= 0;
  surfaces.forEach((n, i) => {
    n.inert = modalIndex >= 0 && i < modalIndex;
  });
}
export function Overlay({
  open = true,
  title,
  close,
  children,
  mode = "dialog",
  modal = true,
  busy = false,
  alert = false,
  className = "",
}: {
  open?: boolean;
  title: string;
  close: () => void;
  children: ReactNode;
  mode?: "dialog" | "drawer";
  modal?: boolean;
  busy?: boolean;
  alert?: boolean;
  className?: string;
}) {
  const root = useRef<HTMLElement>(null),
    closeRef = useRef(close),
    busyRef = useRef(busy);
  closeRef.current = close;
  busyRef.current = busy;
  useEffect(() => {
    if (!open) return;
    const previous = document.activeElement as HTMLElement | null;
    const panel = root.current!;
    surfaces.push(panel);
    syncModalSurfaces();
    const frame = requestAnimationFrame(() =>
      (
        root.current?.querySelector<HTMLElement>('[data-autofocus="true"]') ||
        root.current
      )?.focus({ preventScroll: true }),
    );
    if (modal) {
      if (bodyLocks++ === 0) {
        bodyOverflow = document.body.style.overflow;
        document.body.style.overflow = "hidden";
      }
    }
    const keys = (e: KeyboardEvent) => {
      if (e.defaultPrevented || document.querySelector(":popover-open")) return;
      if (surfaces.at(-1) !== panel) return;
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        if (!busyRef.current) closeRef.current();
      }
      if (e.key !== "Tab" || !modal) return;
      const nodes = [
        ...(root.current?.querySelectorAll<HTMLElement>(
          'button:not(:disabled),input:not(:disabled),textarea:not(:disabled),select:not(:disabled),a[href],summary,[tabindex]:not([tabindex="-1"])',
        ) || []),
      ].filter(
        (n) => !n.closest("[hidden],[inert]") && n.getClientRects().length,
      );
      const first = nodes[0],
        last = nodes.at(-1);
      if (!first) {
        e.preventDefault();
        return;
      }
      if (
        e.shiftKey &&
        (document.activeElement === first ||
          document.activeElement === root.current ||
          !root.current?.contains(document.activeElement))
      ) {
        e.preventDefault();
        last?.focus();
      } else if (
        !e.shiftKey &&
        (document.activeElement === last ||
          document.activeElement === root.current ||
          !root.current?.contains(document.activeElement))
      ) {
        e.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", keys);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("keydown", keys);
      surfaces.splice(surfaces.indexOf(panel), 1);
      syncModalSurfaces();
      if (modal && --bodyLocks === 0)
        document.body.style.overflow = bodyOverflow;
      requestAnimationFrame(() => {
        if (previous?.isConnected && !previous.closest("[hidden],[inert]"))
          previous.focus({ preventScroll: true });
      });
    };
  }, [open, modal]);
  return createPortal(
    <div
      hidden={!open}
      className={`surface-overlay ${mode} ${modal ? "modal-surface" : "nonmodal-surface"}`}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget && modal && !busy) close();
      }}
    >
      <section
        ref={root}
        tabIndex={-1}
        data-surface-open={open ? "true" : undefined}
        role={alert ? "alertdialog" : modal ? "dialog" : "complementary"}
        aria-modal={modal || undefined}
        aria-label={title}
        className={`surface-panel ${className}`}
      >
        <header className="surface-panel-heading">
          <h2>{title}</h2>
          <button
            type="button"
            className="icon"
            disabled={busy}
            aria-label={`关闭${title}`}
            onClick={close}
          >
            <X size={19} />
          </button>
        </header>
        <div className="surface-panel-content">{children}</div>
      </section>
    </div>,
    document.body,
  );
}
export function plainPreview(text: string, limit = 100) {
  return text
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/!?(?:\[([^\]]*)\])\([^)]*\)/g, "$1")
    .replace(/^[ \t]*#{1,6}\s*/gm, "")
    .replace(/[*_`>]/g, "")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, limit);
}

const surfaceGuards = new Set<() => boolean>();
export async function confirmSurfaceNavigation() {
  for (const guard of surfaceGuards) if (!guard()) return false;
  return true;
}
export function useSurfaceGuard(
  dirty: boolean,
  prompt = "放弃尚未保存的修改？",
) {
  const state = useRef({ dirty, prompt });
  state.current = { dirty, prompt };
  const check = () =>
    !state.current.dirty || window.confirm(state.current.prompt);
  useEffect(() => {
    const guard = () =>
      !state.current.dirty || window.confirm(state.current.prompt);
    const before = (e: BeforeUnloadEvent) => {
      if (state.current.dirty) {
        e.preventDefault();
        e.returnValue = "";
      }
    };
    surfaceGuards.add(guard);
    window.addEventListener("beforeunload", before);
    return () => {
      surfaceGuards.delete(guard);
      window.removeEventListener("beforeunload", before);
    };
  }, []);
  return check;
}
