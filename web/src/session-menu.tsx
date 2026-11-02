import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { MoreHorizontal } from "lucide-react";

// Native auto popovers escape the list's overflow and provide light dismiss,
// Escape and mutual exclusion. Keep placement and menu keyboard behavior here.
export function SessionMenu({
  label,
  context,
  children,
}: {
  label: string;
  context: string;
  children: ReactNode;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const lastItem = useRef(false);
  const openingFrame = useRef<number | undefined>(undefined);
  const placedAnchor = useRef<DOMRect | null>(null);
  const [open, setOpen] = useState(false);
  const items = () =>
    Array.from(
      menu.current?.querySelectorAll<HTMLButtonElement>(
        'button[role="menuitem"]:not(:disabled)',
      ) ?? [],
    );
  const close = () => {
    if (menu.current?.matches(":popover-open")) menu.current.hidePopover();
  };
  const place = () => {
    const panel = menu.current;
    const anchor = trigger.current;
    if (!panel || !anchor || !panel.matches(":popover-open")) return;
    const viewport = window.visualViewport;
    const leftEdge = (viewport?.offsetLeft ?? 0) + 8;
    const topEdge = (viewport?.offsetTop ?? 0) + 8;
    const rightEdge = leftEdge + (viewport?.width ?? window.innerWidth) - 16;
    const bottomEdge = topEdge + (viewport?.height ?? window.innerHeight) - 16;
    const rect = anchor.getBoundingClientRect();
    placedAnchor.current = rect;
    panel.style.maxHeight = `${bottomEdge - topEdge}px`;
    const size = panel.getBoundingClientRect();
    const below = Math.max(0, bottomEdge - rect.bottom - 4);
    const above = Math.max(0, rect.top - topEdge - 4);
    const upwards = below < size.height && above > below;
    const height = Math.min(size.height, upwards ? above : below);
    panel.style.maxHeight = `${height}px`;
    panel.style.left = `${Math.max(leftEdge, Math.min(rect.right - size.width, rightEdge - size.width))}px`;
    panel.style.top = `${Math.max(topEdge, Math.min(upwards ? rect.top - height - 4 : rect.bottom + 4, bottomEdge - height))}px`;
  };
  useEffect(close, [context]);
  // React can move this row without a scroll/resize (for example an SSE pin or
  // a preceding title wrapping). Dismiss before painting a detached menu.
  useLayoutEffect(() => {
    const previous = placedAnchor.current;
    if (
      !previous ||
      !trigger.current ||
      !menu.current?.matches(":popover-open")
    )
      return;
    const current = trigger.current.getBoundingClientRect();
    if (
      (["x", "y", "width", "height"] as const).some(
        (key) => Math.abs(current[key] - previous[key]) > 0.5,
      )
    )
      close();
  });
  useEffect(
    () => () => {
      if (openingFrame.current !== undefined)
        cancelAnimationFrame(openingFrame.current);
    },
    [],
  );
  useEffect(() => {
    if (!open) return;
    const onScroll = (event: Event) => {
      // Only ancestor scrolling can move the anchor. Chat auto-scroll and the
      // menu's own scrolling must not interrupt an unrelated sidebar action.
      if (
        event.target instanceof Node &&
        trigger.current &&
        event.target.contains(trigger.current)
      )
        close();
    };
    document.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", close);
    window.visualViewport?.addEventListener("resize", close);
    window.visualViewport?.addEventListener("scroll", close);
    return () => {
      document.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", close);
      window.visualViewport?.removeEventListener("resize", close);
      window.visualViewport?.removeEventListener("scroll", close);
    };
  }, [open]);
  return (
    <>
      <button
        ref={trigger}
        id={`${id}-trigger`}
        type="button"
        className="session-menu-trigger"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={id}
        popoverTarget={id}
        onKeyDown={(event) => {
          // The first animation frame may not yet have moved focus into the
          // menu when the user quickly presses Tab again.
          if (event.key === "Tab") {
            close();
            return;
          }
          if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
          event.preventDefault();
          lastItem.current = event.key === "ArrowUp";
          if (!menu.current?.matches(":popover-open")) trigger.current?.click();
          else (lastItem.current ? items().at(-1) : items()[0])?.focus();
        }}
      >
        <MoreHorizontal size={15} />
      </button>
      <div
        ref={menu}
        id={id}
        className="session-menu-popover"
        popover="auto"
        role="menu"
        aria-labelledby={`${id}-trigger`}
        onBeforeToggle={(event) => {
          if (openingFrame.current !== undefined)
            cancelAnimationFrame(openingFrame.current);
          openingFrame.current = undefined;
          placedAnchor.current = null;
          setOpen(event.newState === "open");
          if (event.newState !== "open") return;
          openingFrame.current = requestAnimationFrame(() => {
            openingFrame.current = undefined;
            if (!menu.current?.matches(":popover-open")) return;
            place();
            (lastItem.current ? items().at(-1) : items()[0])?.focus();
            lastItem.current = false;
          });
        }}
        onClickCapture={(event) => {
          const button = (event.target as Element).closest("button");
          // Close before the existing action opens a dialog or starts a request.
          if (button && !button.disabled) close();
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape" || event.key === "Tab") {
            close();
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              trigger.current?.focus();
            }
            return;
          }
          const enabled = items();
          if (!enabled.length) return;
          const current = enabled.indexOf(
            document.activeElement as HTMLButtonElement,
          );
          let next: number;
          if (event.key === "ArrowDown") next = (current + 1) % enabled.length;
          else if (event.key === "ArrowUp")
            next = (current - 1 + enabled.length) % enabled.length;
          else if (event.key === "Home") next = 0;
          else if (event.key === "End") next = enabled.length - 1;
          else return;
          event.preventDefault();
          enabled[next].focus();
        }}
      >
        {children}
      </div>
    </>
  );
}
