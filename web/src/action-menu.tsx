import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { positionPopover } from "./popover-position";

// Native auto popovers escape the list's overflow and provide light dismiss,
// Escape and mutual exclusion. Keep placement and menu keyboard behavior here.
export function ActionMenu({
  label,
  context,
  children,
  triggerContent,
  triggerClassName,
  panelClassName,
  align = "end",
}: {
  label: string;
  context: string;
  children: ReactNode;
  triggerContent: ReactNode;
  triggerClassName: string;
  panelClassName: string;
  align?: "start" | "end";
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
  const anchorMoved = () => {
    const previous = placedAnchor.current;
    if (!previous || !trigger.current) return false;
    const current = trigger.current.getBoundingClientRect();
    return (["x", "y", "width", "height"] as const).some(
      (key) => Math.abs(current[key] - previous[key]) > 0.5,
    );
  };
  const place = () => {
    const panel = menu.current;
    const anchor = trigger.current;
    if (!panel || !anchor || !panel.matches(":popover-open")) return;
    placedAnchor.current = positionPopover(panel, anchor, { align });
  };
  useEffect(close, [context]);
  // React can move this row without a scroll/resize (for example an SSE pin or
  // a preceding title wrapping). Dismiss before painting a detached menu.
  useLayoutEffect(() => {
    if (menu.current?.matches(":popover-open") && anchorMoved()) close();
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
      // A delayed scroll event from revealing the trigger before it opened is
      // harmless when placement already used the trigger's new coordinates.
      if (
        event.target instanceof Node &&
        trigger.current &&
        event.target.contains(trigger.current) &&
        anchorMoved()
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
        className={triggerClassName}
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
          else
            (lastItem.current ? items().at(-1) : items()[0])?.focus({
              preventScroll: true,
            });
        }}
      >
        {triggerContent}
      </button>
      <div
        ref={menu}
        id={id}
        className={panelClassName}
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
            (lastItem.current ? items().at(-1) : items()[0])?.focus({
              preventScroll: true,
            });
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
              trigger.current?.focus({ preventScroll: true });
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
          enabled[next].focus({ preventScroll: true });
        }}
      >
        {children}
      </div>
    </>
  );
}
