import React, { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Overlay, useNarrow } from "./surface-ui";
import { positionPopover } from "./popover-position";
import {
  Sun,
  CalendarDays,
  ListChecks,
  Bell,
  Folder,
  Library,
  Settings,
  Plus,
  PanelLeftClose,
  PanelLeftOpen,
  LogOut,
  X,
} from "lucide-react";
import type { Project } from "./api";

export function NavigationRail({
  collapsed,
  toggle,
  mobileOpen,
  closeMobile,
  unread,
  page,
  projects,
  projectId,
  navigate,
  openProject,
  newProject,
  logout,
}: {
  collapsed: boolean;
  toggle: () => void;
  mobileOpen: boolean;
  closeMobile: () => void;
  unread: number;
  page: string;
  projects: Project[];
  projectId: string;
  navigate: (page: string) => void;
  openProject: (p: Project) => void;
  newProject: () => void;
  logout: () => void;
}) {
  const narrow = useNarrow();
  const [projectTip, setProjectTip] = useState<{
    id: string;
    anchor: HTMLButtonElement;
  } | null>(null);
  const tip = useRef<HTMLDivElement>(null);
  const tipName = projects.find((p) => p.id === projectTip?.id)?.name;
  useLayoutEffect(() => {
    if (!projectTip || !tip.current) return;
    if (!projectTip.anchor.isConnected || !tipName) {
      setProjectTip(null);
      return;
    }
    if (!tip.current.matches(":popover-open")) tip.current.showPopover();
    positionPopover(tip.current, projectTip.anchor, {
      side: "right",
      gap: 8,
      maxWidth: 320,
    });
  });
  useEffect(() => {
    if (!projectTip) return;
    const close = () => setProjectTip(null);
    const scroll = (event: Event) => {
      if (
        event.target instanceof Node &&
        event.target.contains(projectTip.anchor)
      )
        close();
    };
    document.addEventListener("scroll", scroll, true);
    window.addEventListener("resize", close);
    window.visualViewport?.addEventListener("resize", close);
    window.visualViewport?.addEventListener("scroll", close);
    return () => {
      document.removeEventListener("scroll", scroll, true);
      window.removeEventListener("resize", close);
      window.visualViewport?.removeEventListener("resize", close);
      window.visualViewport?.removeEventListener("scroll", close);
    };
  }, [projectTip]);
  const rail = (
    <aside
      className={`rail ${collapsed && !narrow ? "collapsed" : "expanded"} ${mobileOpen ? "open" : ""}`}
      aria-label="全局导航"
    >
      <div className="rail-brand">
        <button
          className="brand"
          onClick={() => navigate("projects")}
          aria-label="项目工作台首页"
        >
          <span className="brand-mark">工</span>
          <span className="brand-copy">项目工作台</span>
        </button>
        <button
          className="rail-toggle icon"
          onClick={toggle}
          title={collapsed ? "展开导航" : "收起导航"}
          aria-label={collapsed ? "展开导航" : "收起导航"}
          aria-expanded={!collapsed}
        >
          {collapsed ? (
            <PanelLeftOpen size={17} />
          ) : (
            <PanelLeftClose size={17} />
          )}
        </button>
      </div>
      <nav>
        {[
          { id: "today", text: "今天", Icon: Sun },
          { id: "calendar", text: "日历", Icon: CalendarDays },
          { id: "tasks", text: "任务", Icon: ListChecks },
          { id: "projects", text: "项目", Icon: Folder },
          { id: "experts", text: "专家库", Icon: Library },
        ].map(({ id, text, Icon }) => (
          <button
            key={id}
            title={text}
            className={
              (
                id === "projects"
                  ? page.startsWith("project")
                  : id === "experts"
                    ? page.startsWith("expert")
                    : page.startsWith(id)
              )
                ? "active"
                : ""
            }
            onClick={() => navigate(id)}
          >
            <Icon size={20} />
            <span>{text}</span>
          </button>
        ))}
      </nav>
      <details className="rail-projects">
        <summary>最近项目</summary>
        <div className="rail-title">
          <span>工作区</span>
          <button title="新建项目" aria-label="新建项目" onClick={newProject}>
            <Plus size={17} />
          </button>
        </div>
        <div className="project-links">
          {projects
            .filter((p) => !p.archived)
            .map((p) => (
              <button
                key={p.id}
                title={p.name}
                aria-label={p.name}
                onFocus={(e) => {
                  setProjectTip({ id: p.id, anchor: e.currentTarget });
                }}
                onKeyDown={(e) => {
                  if (e.key === "Escape") {
                    e.preventDefault();
                    setProjectTip(null);
                  }
                }}
                onBlur={() => setProjectTip(null)}
                aria-current={
                  page === "project" && p.id === projectId ? "page" : undefined
                }
                className={
                  page === "project" && p.id === projectId ? "chosen" : ""
                }
                onClick={() => openProject(p)}
              >
                <span>{p.name}</span>
              </button>
            ))}
        </div>
      </details>
      {projectTip && tipName && (
        <div
          ref={tip}
          className="project-name-tip"
          role="tooltip"
          popover="manual"
        >
          {tipName}
        </div>
      )}
      <div className="rail-bottom">
        <button
          className={`notice-bell ${page === "notices" ? "active" : ""}`}
          aria-label={`站内信，${unread}条未读`}
          title="站内信"
          onClick={() => navigate("notices")}
        >
          <Bell size={18} />
          <span>站内信</span>
          {unread > 0 && <b>{unread}</b>}
        </button>
        <button
          aria-label="设置"
          title="设置"
          className={page === "settings" ? "active" : ""}
          onClick={() => navigate("settings")}
        >
          <Settings size={18} />
          <span>设置</span>
        </button>
        <button aria-label="退出登录" title="退出登录" onClick={logout}>
          <LogOut size={17} />
          <span>退出</span>
        </button>
      </div>
    </aside>
  );
  return narrow ? (
    <Overlay
      open={mobileOpen}
      title="导航"
      close={closeMobile}
      mode="drawer"
      className="mobile-nav-panel"
    >
      {rail}
    </Overlay>
  ) : (
    rail
  );
}
export function ProjectSidebar({
  children,
  open,
  close,
  title = "资料",
}: {
  children: React.ReactNode;
  open: boolean;
  close: () => void;
  title?: string;
}) {
  const narrow = useNarrow(1099);
  return (
    <Overlay
      open={open}
      title={title}
      close={close}
      mode="drawer"
      modal={narrow}
      className="project-panel"
    >
      <div className="project-panel-body">{children}</div>
    </Overlay>
  );
}
export function ConversationPane({ children }: { children: React.ReactNode }) {
  return (
    <section className="chat" aria-label="项目对话">
      {children}
    </section>
  );
}
export function InspectorPane({
  children,
  close,
}: {
  children: React.ReactNode;
  close: () => void;
}) {
  const pane = useRef<HTMLElement>(null);
  const [width, setWidth] = useState<number | null>(() => {
    const n = Number(localStorage.getItem("pwb.inspectorWidth"));
    return n >= 360 ? n : null;
  });
  const [available, setAvailable] = useState(1100);
  useEffect(() => {
    const parent = pane.current?.parentElement;
    if (!parent) return;
    const update = () => setAvailable(parent.clientWidth);
    const observer = new ResizeObserver(update);
    observer.observe(parent);
    const sidebar = parent.querySelector(".project-panel");
    if (sidebar) observer.observe(sidebar);
    update();
    return () => observer.disconnect();
  }, []);
  const actual = Math.max(
    360,
    Math.min(width ?? available * 0.4, 640, available - 446),
  );
  const resize = (value: number) => {
    const n = Math.max(360, Math.min(value, 640, available - 446));
    setWidth(n);
    localStorage.setItem("pwb.inspectorWidth", String(n));
  };
  return (
    <section
      ref={pane}
      className="inspector editor-pane"
      style={{ "--inspector-width": `${actual}px` } as React.CSSProperties}
      aria-label="文档与详情"
    >
      <div
        className="inspector-resize"
        role="separator"
        aria-label="调整详情宽度"
        aria-orientation="vertical"
        aria-valuemin={360}
        aria-valuemax={Math.floor(
          Math.max(360, Math.min(640, available - 446)),
        )}
        aria-valuenow={Math.floor(actual)}
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === "ArrowLeft") {
            e.preventDefault();
            resize(actual + 20);
          }
          if (e.key === "ArrowRight") {
            e.preventDefault();
            resize(actual - 20);
          }
        }}
        onPointerDown={(e) => {
          const el = e.currentTarget;
          el.setPointerCapture(e.pointerId);
          const x = e.clientX;
          const initial = actual;
          const move = (event: PointerEvent) =>
            resize(initial + x - event.clientX);
          const stop = () => {
            el.removeEventListener("pointermove", move);
            el.removeEventListener("pointerup", stop);
            el.removeEventListener("pointercancel", stop);
          };
          el.addEventListener("pointermove", move);
          el.addEventListener("pointerup", stop);
          el.addEventListener("pointercancel", stop);
        }}
      />
      <button className="inspector-back" onClick={close}>
        <X size={16} />
        关闭文档
      </button>
      {children}
    </section>
  );
}
