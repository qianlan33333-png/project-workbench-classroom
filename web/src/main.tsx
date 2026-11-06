import { ToolComponents, SessionComponents } from "./tool-components";
import { CommunicationFeed, AgentOrigin } from "./session-communication";
import React, { useEffect, useState, useRef, useCallback } from "react";
import { createRoot } from "react-dom/client";
import {
  Bell,
  Folder,
  MoreHorizontal,
  SlidersHorizontal,
  Plus,
  ArrowUp,
  Settings,
  Library,
  FileText,
  ChevronRight,
  MessageSquare,
  Upload,
  Download,
  Square,
  Menu,
  X,
  History,
  Paperclip,
  Check,
  RefreshCw,
  ExternalLink,
  Terminal,
  ChevronDown,
  Archive,
  Pencil,
  Pin,
} from "lucide-react";
import {
  PageHeader,
  Overlay,
  EmptyState,
  plainPreview,
  confirmSurfaceNavigation,
  useSurfaceGuard,
  useNarrow,
} from "./surface-ui";
import { ActionMenu } from "./action-menu";
import { zipSync } from "fflate";
import {
  api,
  endpoint,
  labels,
  stamp,
  type Project,
  type Session,
  type Run,
  type Expert,
  type Message,
  type FileEntry,
  type WorkEvent,
  type Capability,
} from "./api";
import {
  NavigationRail,
  ProjectSidebar,
  ConversationPane,
  type ProjectTab,
} from "./workspace";
import { RichText, FileContent, useUnsavedChanges } from "./file-ui";
import { ProjectFiles } from "./project-files";
import { ExpertDetail } from "./expert-detail";
import { useMessageSubmissions } from "./message-submissions";
import {
  mergeRuns,
  mergeSessions,
  mergeEvents,
  initialSessionTitle,
} from "./chat-state";
import { RecycleBin, GlobalTools } from "./lifecycle-ui";
import { SessionMenu } from "./session-menu";
import { CapabilityComposer, CapabilityBadges } from "./capability-composer";
import { Today, TasksPanel, ContextSettings, type PreparedTask } from "./today";
import { GatewayProjectSettings, GatewayResume } from "./gateway-project";
import "./style.css";
import { CalendarPage, personal } from "./planner";
import { TaskCenter, NoticePage, ProjectSchedules } from "./planner-tasks";
import "./planner.css";
import "./suite.css";

type Field = {
  key: string;
  label: string;
  value?: string;
  type?: string;
  hint?: string;
  required?: boolean;
  options?: { value: string; label: string }[];
};
type Dialog = {
  title: string;
  description?: string;
  fields?: Field[];
  confirm?: string;
  action?: (data: Record<string, string>) => Promise<void>;
  content?: React.ReactNode;
};
const initialLocation = new URLSearchParams(location.hash.slice(1));
function App() {
  const [auth, setAuth] = useState<boolean | null>(null),
    [page, setPage] = useState(
      initialLocation.get("expert")
        ? "expert-detail"
        : initialLocation.get("project")
          ? "project"
          : ["today", "tasks", "calendar", "notices", "settings"].includes(
                initialLocation.get("page") || "",
              )
            ? initialLocation.get("page")!
            : "projects",
    ),
    [projects, setProjects] = useState<Project[]>([]),
    [experts, setExperts] = useState<Expert[]>([]),
    [expertId, setExpertId] = useState(initialLocation.get("expert") || ""),
    [projectId, setProjectId] = useState(initialLocation.get("project") || ""),
    [sessionId, setSessionId] = useState(initialLocation.get("session") || ""),
    [sessions, setSessions] = useState<Session[]>([]),
    [runs, setRuns] = useState<Run[]>([]),
    [inbox, setInbox] = useState<Message[]>([]),
    [projectFiles, setProjectFiles] = useState<{
      id: string;
      files: FileEntry[];
    }>({ id: "", files: [] }),
    [events, setEvents] = useState<WorkEvent[]>([]),
    [interactions, setInteractions] = useState<any[]>([]);
  const files = projectFiles.id === projectId ? projectFiles.files : [];
  const viewportNarrow = useNarrow(1099);
  const [taskEditorLayout, setTaskEditorLayout] = useState<boolean | null>(
    null,
  );
  const projectNarrow = taskEditorLayout ?? viewportNarrow;
  const historyGuard = useRef<() => Promise<boolean>>(async () => true);
  const renderedLocation = useRef(location.href);
  const [pageParams, setPageParams] = useState(initialLocation),
    [unread, setUnread] = useState(0);
  const badge = useCallback(() => {
    void api("/notices/count")
      .then((v) => setUnread(v.unread))
      .catch(() => {});
  }, []);
  useEffect(() => {
    if (!auth) return;
    badge();
    const timer = setInterval(badge, 10000);
    return () => clearInterval(timer);
  }, [auth, badge]);
  useEffect(() => {
    const pop = async () => {
      if (location.href === renderedLocation.current) return;
      if (!(await historyGuard.current())) {
        history.forward();
        return;
      }
      const q = new URLSearchParams(location.hash.slice(1));
      setPageParams(q);
      setTaskEditorLayout(null);
      const p = q.get("page");
      if (
        p &&
        ["tasks", "notices", "calendar", "today", "settings"].includes(p)
      )
        setPage(p);
      else if (q.has("project")) {
        const id = q.get("project") || "";
        fileRequest.current++;
        setFilePath("");
        setContent("");
        setSaved("");
        setDetailView(null);
        setRunDetails("");
        setDialog(null);
        setLinkedFile(q.get("file") || "");
        setLinkedTask(q.get("task") || "");
        setTab(q.has("file") ? "files" : q.has("task") ? "tasks" : "sessions");
        setDesktopPanelOpen(
          q.has("file") ||
            q.has("task") ||
            localStorage.getItem("pwb.projectPanelClosed." + id) !== "true",
        );
        setMobileProjectPanelOpen(q.has("file") || q.has("task"));
        setProjectId(id);
        restoreComposer(q.get("session") || "");
        setPage("project");
      } else if (q.has("expert")) {
        setExpertId(q.get("expert") || "");
        setPage("expert-detail");
      } else setPage("projects");
    };
    window.addEventListener("popstate", pop);
    return () => {
      window.removeEventListener("popstate", pop);
    };
  }, []);
  const plannerNav = (
    name: string,
    params: Record<string, string> = {},
    guarded = true,
  ) => {
    void (async () => {
      try {
        if (guarded && !(await historyGuard.current())) return;
        const q = new URLSearchParams({ page: name, ...params });
        history.pushState(null, "", "#" + q);
        setPageParams(q);
        setPage(name);
        setMobileNav(false);
      } catch (e) {
        notify((e as Error).message);
      }
    })();
  };
  const expertDirectoryInput = useRef<HTMLInputElement>(null),
    expertZipInput = useRef<HTMLInputElement>(null);
  const [expertSearch, setExpertSearch] = useState("");
  const [toast, setToast] = useState(""),
    [dialog, setDialog] = useState<Dialog | null>(null),
    [busy, setBusy] = useState(false),
    [creatingSession, setCreatingSession] = useState(false),
    [text, setText] = useState(""),
    [filePath, setFilePath] = useState(""),
    [content, setContent] = useState(""),
    [saved, setSaved] = useState(""),
    [hash, setHash] = useState(""),
    [binary, setBinary] = useState(false),
    [tooLarge, setTooLarge] = useState(false),
    [detailView, setDetailView] = useState<Dialog | null>(null),
    [runDetails, setRunDetails] = useState(""),
    [preview, setPreview] = useState(false),
    [tab, setTab] = useState<ProjectTab>(
      initialLocation.has("file")
        ? "files"
        : initialLocation.has("task")
          ? "tasks"
          : "sessions",
    ),
    [linkedFile, setLinkedFile] = useState(initialLocation.get("file") || ""),
    [linkedTask, setLinkedTask] = useState(initialLocation.get("task") || ""),
    [railCollapsed, setRailCollapsed] = useState(
      () => localStorage.getItem("pwb.railCollapsed") === "true",
    ),
    [desktopPanelOpen, setDesktopPanelOpen] = useState(
      () =>
        initialLocation.has("task") ||
        initialLocation.has("file") ||
        localStorage.getItem(
          "pwb.projectPanelClosed." + (initialLocation.get("project") || ""),
        ) !== "true",
    ),
    [mobileProjectPanelOpen, setMobileProjectPanelOpen] = useState(
      initialLocation.has("file") || initialLocation.has("task"),
    ),
    [mobileNav, setMobileNav] = useState(false),
    [attachments, setAttachments] = useState<string[]>([]),
    [capabilities, setCapabilities] = useState<Capability[]>([]),
    [references, setReferences] = useState<
      { projectId: string; path: string }[]
    >([]);
  const projectPanelOpen = projectNarrow
    ? mobileProjectPanelOpen || !!filePath
    : desktopPanelOpen;
  const showProjectPanel = () => {
    if (projectNarrow) setMobileProjectPanelOpen(true);
    else {
      setDesktopPanelOpen(true);
      localStorage.removeItem("pwb.projectPanelClosed." + projectId);
    }
  };
  const creatingSessionRef = useRef(false);
  const composerDrafts = useRef(
    new Map<
      string,
      {
        text: string;
        attachments: string[];
        references: { projectId: string; path: string }[];
        capabilities: Capability[];
      }
    >(),
  );
  const composerCurrent = useRef({
    text,
    attachments,
    references,
    capabilities,
  });
  composerCurrent.current = { text, attachments, references, capabilities };
  const latestEvent = useRef(0);
  const runEventSequences = useRef(new Map<string, number>());
  const applyRun = useCallback(
    (run: Run) => setRuns((old) => mergeRuns(old, [run])),
    [],
  );
  const submissions = useMessageSubmissions(auth === true, runs, applyRun);
  const submissionReceipt = submissions.receipt;
  const fileRequest = useRef(0);
  const expertGuard = useRef<(() => Promise<boolean>) | null>(null);
  const historyCursors = useRef<Record<string, string>>({});
  const chatEnd = useRef<HTMLDivElement>(null),
    projectRef = useRef(projectId),
    sessionRef = useRef(sessionId),
    refreshTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  projectRef.current = projectId;
  sessionRef.current = sessionId;
  const switchSession = async (id: string) => {
    if (id === sessionRef.current) return;
    try {
      await flushTaskDraft();
    } catch (e: any) {
      notify(e.message);
      return;
    }
    restoreComposer(id);
  };
  const restoreComposer = (id: string) => {
    if (id === sessionRef.current) return;
    composerDrafts.current.set(sessionRef.current, composerCurrent.current);
    const draft = composerDrafts.current.get(id);
    setText(draft?.text || "");
    setAttachments(draft?.attachments || []);
    setReferences(draft?.references || []);
    setCapabilities(draft?.capabilities || []);
    setSessionId(id);
  };
  const visibleSessions = sessions
    .filter((s) => s.projectId === projectId && !s.recycledAt)
    .flatMap((s) => {
      if (!s.draft) return [s];
      const first =
        submissions.items.find((v) => v.input.sessionId === s.id)?.input.text ??
        runs
          .filter((r) => r.sessionId === s.id)
          .sort((a, b) => a.createdAt - b.createdAt)[0]?.text;
      return first !== undefined
        ? [{ ...s, title: initialSessionTitle(first) }]
        : [];
    });
  const project = projects.find((p) => p.id === projectId),
    session = sessions.find((s) => s.id === sessionId),
    locked =
      runs.some(
        (r) =>
          r.projectId === projectId &&
          ["running", "waiting", "stopping"].includes(r.state),
      ) ||
      sessions.some(
        (s) => s.projectId === projectId && s.lifecycle === "archiving",
      );
  const notify = useCallback((message: string) => {
    setToast(message);
  }, []);
  const act = useCallback(
    async (fn: () => Promise<any>) => {
      try {
        return await fn();
      } catch (e) {
        notify((e as Error).message);
      }
    },
    [notify],
  );
  const refresh = useCallback(async () => {
    const requestedAt = latestEvent.current;
    const [ps, es, rs, ib, ints] = await Promise.all([
      api<Project[]>("/projects"),
      api<Expert[]>("/experts"),
      api<Run[]>("/runs"),
      api<Message[]>("/inbox"),
      api<any[]>("/interactions"),
    ]);
    setProjects(ps);
    setExperts(es);
    setRuns((old) =>
      mergeRuns(old, rs, runEventSequences.current, requestedAt),
    );
    setInbox(ib);
    setInteractions(ints);
    const targetProject = projectRef.current;
    const targetSession = sessionRef.current;
    if (targetProject) {
      const [ss, fs, current] = await Promise.all([
        api<Session[]>(`/projects/${targetProject}/sessions`),
        api<FileEntry[]>(`/projects/${targetProject}/files`),
        targetSession
          ? api<Session>(`/sessions/${targetSession}`).catch((e) => {
              if ([400, 404].includes(e.status)) return null;
              throw e;
            })
          : Promise.resolve(null),
      ]);
      if (targetProject !== projectRef.current) return;
      setSessions((old) =>
        mergeSessions(old, [
          ...ss,
          ...(current?.projectId === targetProject ? [current] : []),
        ]),
      );
      setProjectFiles({ id: targetProject, files: fs });
      if (
        !current &&
        sessionRef.current === targetSession &&
        requestedAt === latestEvent.current
      ) {
        const p = ps.find((p) => p.id === targetProject);
        if (p) {
          restoreComposer(p.defaultSessionId);
          return;
        }
      }
      if (sessionRef.current) {
        const history = await api<Run[]>(
          `/runs?sessionId=${sessionRef.current}`,
        );
        historyCursors.current[sessionRef.current] ??= history.at(-1)?.id || "";
        setRuns((old) =>
          mergeRuns(old, history, runEventSequences.current, requestedAt),
        );
      }
    }
  }, []);
  useEffect(() => {
    act(async () => {
      const a = await api("/auth");
      setAuth(a.authenticated);
    });
    const fn = () => setAuth(false);
    window.addEventListener("auth-expired", fn);
    return () => window.removeEventListener("auth-expired", fn);
  }, [act]);
  useEffect(() => {
    if (!auth) return;
    act(refresh);
    const stream = new EventSource(endpoint("/events"));
    stream.onmessage = (e) => {
      const event = JSON.parse(e.data) as WorkEvent;
      latestEvent.current = Math.max(latestEvent.current, event.seq);
      if (
        event.type === "message.received" &&
        event.data.source === "web" &&
        event.data.eventId
      )
        submissionReceipt(event.data.eventId, event.data.id);
      if (
        event.type === "run.updated" &&
        event.data?.id &&
        event.seq > (runEventSequences.current.get(event.data.id) || 0)
      ) {
        runEventSequences.current.set(event.data.id, event.seq);
        applyRun(event.data as Run);
      }
      if (
        (event.type === "session.updated" ||
          event.type === "session.created") &&
        event.data?.id
      ) {
        setSessions((old) => mergeSessions(old, [event.data as Session]));
      }
      if (
        event.type === "session.recycled" ||
        event.type === "session.purged"
      ) {
        const id = event.data.id;
        // Historical SSE replay must not remove or change focus for a session
        // that has since been restored. Reconcile against current server state.
        void api<Session>(`/sessions/${id}`).catch((e) => {
          if (![400, 404].includes(e.status)) return;
          setSessions((old) => old.filter((s) => s.id !== id));
          setRuns((old) => old.filter((r) => r.sessionId !== id));
          setEvents((old) => old.filter((e) => e.sessionId !== id));
        });
      }
      setEvents((old) => mergeEvents(old, [event], sessionRef.current));
      if (
        !event.type.startsWith("harness.") ||
        event.type === "harness.turn/end"
      ) {
        if (refreshTimer.current) clearTimeout(refreshTimer.current);
        refreshTimer.current = setTimeout(() => act(refresh), 200);
      }
    };
    return () => {
      stream.close();
      if (refreshTimer.current) clearTimeout(refreshTimer.current);
    };
  }, [auth, refresh, act, applyRun, submissionReceipt]);
  useEffect(() => {
    if (auth && projectId) act(refresh);
  }, [projectId, auth, refresh, act]);
  useEffect(() => {
    if (page === "project" && project && !sessionId)
      restoreComposer(project.defaultSessionId);
  }, [page, project, sessionId]);
  useEffect(() => {
    if (!sessionId || !auth) return;
    act(async () => {
      const requestedAt = latestEvent.current;
      const history = await api<Run[]>(`/runs?sessionId=${sessionId}`);
      if (sessionRef.current !== sessionId) return;
      historyCursors.current[sessionId] ??= history.at(-1)?.id || "";
      setRuns((old) =>
        mergeRuns(old, history, runEventSequences.current, requestedAt),
      );
      let after = 0;
      const all: WorkEvent[] = [];
      for (;;) {
        const batch = await api<WorkEvent[]>(
          `/events?json=1&sessionId=${sessionId}&after=${after}`,
        );
        if (sessionRef.current !== sessionId) return;
        all.push(...batch);
        if (batch.length < 500) break;
        after = batch.at(-1)!.seq;
      }
      setEvents((old) => mergeEvents(old, all, sessionRef.current));
    });
  }, [sessionId, auth, act]);
  useEffect(() => {
    chatEnd.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [sessionId, runs.length, events.length, submissions.items.length]);
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(""), 7000);
    return () => clearTimeout(t);
  }, [toast]);
  useEffect(() => {
    const hash =
      page === "project"
        ? new URLSearchParams({
            project: projectId,
            session: sessionId,
            ...(filePath || linkedFile ? { file: filePath || linkedFile } : {}),
            ...(linkedTask ? { task: linkedTask } : {}),
          }).toString()
        : page === "expert-detail"
          ? new URLSearchParams({ expert: expertId }).toString()
          : ["tasks", "notices", "calendar", "today", "settings"].includes(page)
            ? pageParams.get("page") === page
              ? pageParams.toString()
              : "page=" + page
            : "";
    history.replaceState(
      null,
      "",
      location.pathname + location.search + (hash ? "#" + hash : ""),
    );
    renderedLocation.current = location.href;
  }, [
    page,
    projectId,
    sessionId,
    expertId,
    pageParams,
    filePath,
    linkedFile,
    linkedTask,
  ]);
  const saveDocument = async () => {
    if (!filePath || content === saved) return;
    if (locked) throw new Error("任务正在写入，请等待任务结束或停止后再保存。");
    const v = await api(
      `/projects/${projectId}/file`,
      { path: filePath, content, baseHash: hash },
      "PUT",
    );
    setHash(v.after);
    setSaved(content);
    await refresh();
    notify("已保存");
  };
  const { guard: documentGuard, dialog: unsavedDialog } = useUnsavedChanges(
    !!filePath && content !== saved,
    saveDocument,
  );
  const canLeave = async () => {
    if (!(await confirmSurfaceNavigation())) return false;
    return page === "expert-detail"
      ? await (expertGuard.current?.() ?? Promise.resolve(true))
      : await documentGuard();
  };
  historyGuard.current = async () => {
    if (!(await canLeave())) return false;
    await flushTaskDraft();
    return true;
  };
  const navigate = async (next: string) => {
    if (!(await canLeave())) return;
    setTaskEditorLayout(null);
    fileRequest.current++;
    await flushTaskDraft();
    composerDrafts.current.set(sessionRef.current, composerCurrent.current);
    if (["tasks", "notices", "calendar", "today", "settings"].includes(next))
      plannerNav(next, {}, false);
    else setPage(next);
    setMobileNav(false);
    if (next !== "project") {
      setLinkedFile("");
      setLinkedTask("");
      setFilePath("");
      setContent("");
      setSaved("");
      setDetailView(null);
      setRunDetails("");
    }
  };
  const openExpert = async (id: string) => {
    if (!(await canLeave())) return;
    setTaskEditorLayout(null);
    fileRequest.current++;
    setExpertId(id);
    setPage("expert-detail");
    setFilePath("");
    setContent("");
    setSaved("");
  };
  const openProject = async (
    p: Project,
    targetSession?: string,
    initialTab: ProjectTab = "sessions",
  ) => {
    if (!(await canLeave())) return;
    setTaskEditorLayout(null);
    await flushTaskDraft();
    setProjectId(p.id);
    restoreComposer(targetSession || p.defaultSessionId);
    fileRequest.current++;
    setFilePath("");
    setDetailView(null);
    setRunDetails("");
    setContent("");
    setSaved("");
    setPage("project");
    setTab(initialTab);
    setLinkedFile("");
    setLinkedTask("");
    setDialog(null);
    setDesktopPanelOpen(
      initialTab !== "sessions" ||
        localStorage.getItem("pwb.projectPanelClosed." + p.id) !== "true",
    );
    setMobileProjectPanelOpen(initialTab !== "sessions");
    setMobileNav(false);
  };
  const closeDocument = async () => {
    if (!(await documentGuard())) return;
    fileRequest.current++;
    setFilePath("");
    setContent("");
    setSaved("");
    setDetailView(null);
    setRunDetails("");
  };
  const collapseProjectPanel = async () => {
    if (!(await canLeave())) return;
    setTaskEditorLayout(null);
    fileRequest.current++;
    setLinkedFile("");
    setLinkedTask("");
    setFilePath("");
    setContent("");
    setSaved("");
    setDetailView(null);
    setRunDetails("");
    if (projectNarrow) setMobileProjectPanelOpen(false);
    else {
      setDesktopPanelOpen(false);
      localStorage.setItem("pwb.projectPanelClosed." + projectId, "true");
    }
  };
  const openContextPanel = async (next: ProjectTab) => {
    if (
      projectPanelOpen &&
      tab === next &&
      !filePath &&
      !detailView &&
      !runDetails &&
      !linkedTask
    )
      return;
    if (!(await canLeave())) return;
    setTaskEditorLayout(null);
    fileRequest.current++;
    setFilePath("");
    setContent("");
    setSaved("");
    setDetailView(null);
    setRunDetails("");
    setLinkedFile("");
    setLinkedTask("");
    setTab(next);
    showProjectPanel();
  };
  const openFile = async (path: string) => {
    if (!(await documentGuard())) return;
    const request = ++fileRequest.current;
    setDetailView(null);
    setRunDetails("");
    const v = await api(
      `/projects/${projectId}/file?view=1&path=${encodeURIComponent(path)}`,
    );
    if (request !== fileRequest.current || projectId !== projectRef.current)
      return;
    setFilePath(path);
    setLinkedFile("");
    setContent(v.content);
    setSaved(v.content);
    setHash(v.hash);
    setBinary(!!v.binary);
    setTooLarge(!!v.tooLarge);
    setTab("files");
    setLinkedTask("");
    showProjectPanel();
    setPreview(
      !!v.binary ||
        !!v.tooLarge ||
        /\.(md|markdown|png|jpe?g|gif|webp|pdf|mp4|webm)$/i.test(path),
    );
  };
  useEffect(() => {
    if (page !== "project" || !project || !linkedFile) return;
    let active = true;
    void openFile(linkedFile).catch((e) => {
      if (active) {
        setLinkedFile("");
        notify(e.message);
      }
    });
    return () => {
      active = false;
    };
  }, [page, project?.id, linkedFile]);
  const openLinkedFile = async (link: string) => {
    const target = link.split("#")[0];
    const parts = (
      filePath.split("/").slice(0, -1).join("/") +
      "/" +
      target
    ).split("/");
    const normalized: string[] = [];
    for (const part of parts) {
      if (part === "..") normalized.pop();
      else if (part && part !== ".") normalized.push(part);
    }
    const relative = normalized.join("/");
    const candidates = files.filter(
      (f) =>
        !f.directory &&
        (f.path === relative ||
          f.path === target ||
          f.path.endsWith("/" + target)),
    );
    const local =
      candidates.find((f) => f.path === relative) ||
      candidates.find((f) => f.path === target);
    if (local) return openFile(local.path);
    if (candidates.length === 1) return openFile(candidates[0].path);
    if (candidates.length > 1) {
      setDialog({
        title: "选择链接资料",
        content: (
          <div>
            {candidates.map((f) => (
              <button
                key={f.path}
                onClick={() => {
                  setDialog(null);
                  act(() => openFile(f.path));
                }}
              >
                {f.path}
              </button>
            ))}
          </div>
        ),
      });
      return;
    }
    throw new Error("找不到链接资料：" + target);
  };
  const newProject = () =>
    setDialog({
      title: "新建项目",

      fields: [
        { key: "name", label: "项目名称", required: true },
        {
          key: "description",
          label: "简短说明",
          type: "textarea",
          hint: "帮助入口判断归属，可稍后填写",
        },
        {
          key: "aliases",
          label: "项目别名",
          hint: "用逗号分隔，例如：品牌A、A公司",
        },
      ],
      action: async (v) => {
        const p = await api<Project>("/projects", {
          ...v,
          aliases: v.aliases
            .split(/[,，]/)
            .map((x) => x.trim())
            .filter(Boolean),
        });
        await refresh();
        openProject(p);
      },
    });
  const newSession = () => {
    if (creatingSessionRef.current) return;
    if (
      session?.draft &&
      !text &&
      !attachments.length &&
      !references.length &&
      !capabilities.length
    ) {
      document
        .querySelector<HTMLTextAreaElement>('[aria-label="发送消息"]')
        ?.focus();
      return;
    }
    const targetProject = projectId;
    const priorSession = sessionId;
    creatingSessionRef.current = true;
    setCreatingSession(true);
    void act(async () => {
      try {
        const ss = await api<Session>(
          `/projects/${targetProject}/sessions`,
          {},
        );
        setSessions((old) => mergeSessions(old, [ss]));
        if (
          projectRef.current === targetProject &&
          sessionRef.current === priorSession
        ) {
          switchSession(ss.id);
          if (projectNarrow && tab === "sessions")
            setMobileProjectPanelOpen(false);
          requestAnimationFrame(() =>
            document
              .querySelector<HTMLTextAreaElement>('[aria-label="发送消息"]')
              ?.focus(),
          );
        }
      } finally {
        creatingSessionRef.current = false;
        setCreatingSession(false);
      }
    });
  };
  const renameSession = (selected?: Session) => {
    if (!selected && !session) return;
    const target = selected || session!;
    setDialog({
      title: "重命名对话",
      fields: [
        {
          key: "title",
          label: "对话标题",
          value: target.title,
          required: true,
        },
      ],
      confirm: "保存",
      action: async (v) => {
        const ss = await api<Session>(
          `/sessions/${target.id}`,
          { title: v.title },
          "PUT",
        );
        setSessions((old) => mergeSessions(old, [ss]));
      },
    });
  };
  const importFiles = async (
    list: FileList | null,
    archive = false,
    expert = false,
  ) => {
    if (!list?.length) return;
    setBusy(true);
    try {
      const form = new FormData();
      if (
        (archive || expert) &&
        list.length === 1 &&
        /\.zip$/i.test(list[0].name)
      )
        form.append("file", list[0]);
      else if (archive || expert) {
        const input: Record<string, Uint8Array> = {};
        const root = list[0].webkitRelativePath.split("/")[0];
        for (const f of Array.from(list)) {
          let path = f.webkitRelativePath || f.name;
          if (f.webkitRelativePath) path = path.slice(root.length + 1);
          input[path] = new Uint8Array(await f.arrayBuffer());
        }
        form.append(
          "file",
          new Blob([zipSync(input) as BlobPart]),
          "import.zip",
        );
      } else {
        form.append("file", list[0]);
        form.append("conflict", "rename");
      }
      const path = expert
        ? "/experts/import"
        : `/projects/${projectId}/${archive ? "import" : "upload"}`;
      const result = await api(path, form);
      if (!archive && !expert) setAttachments((old) => [...old, result.path]);
      await refresh();
      notify(
        expert
          ? "专家已导入"
          : archive
            ? "资料已导入"
            : "附件已保存，可随下一条消息引用",
      );
    } catch (e) {
      notify((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const send = () => {
    if (
      (!text.trim() && !attachments.length && !references.length) ||
      !sessionId ||
      session?.lifecycle ||
      busy ||
      submissions.sending
    )
      return;
    if (
      submissions.submit({
        eventId: crypto.randomUUID(),
        sessionId,
        text,
        attachments,
        references,
        capabilities,
      })
    ) {
      setText("");
      setAttachments([]);
      setReferences([]);
      setCapabilities([]);
    }
  };
  const versions = async () => {
    if (!(await documentGuard())) return;
    setContent("");
    setSaved("");
    setFilePath("");
    setRunDetails("");
    const request = ++fileRequest.current;
    const revisions = await api<any[]>(
      `/projects/${projectId}/revisions?path=${encodeURIComponent(filePath)}`,
    );
    if (request !== fileRequest.current || projectRef.current !== projectId)
      return;
    setTab("files");
    showProjectPanel();
    setDetailView({
      title: filePath ? "文件历史" : "项目文件变化",
      content: (
        <div className="revision-list">
          {revisions.length === 0 ? (
            <p>还没有文件变化记录。</p>
          ) : (
            revisions.map((v) => (
              <div className="revision" key={v.id}>
                <div>
                  <strong>{v.path}</strong>
                  <small>
                    {stamp(v.createdAt)} ·{" "}
                    {v.runId ? `#T${v.runId.slice(0, 8)}` : "手动编辑"}
                  </small>
                </div>
                <button
                  onClick={() =>
                    act(async () => {
                      const read = async (h: string) =>
                        h
                          ? fetch(endpoint(`/versions/${h}`)).then((r) =>
                              r.text(),
                            )
                          : "";
                      const [before, after] = await Promise.all([
                        read(v.before),
                        read(v.after),
                      ]);
                      if (
                        request !== fileRequest.current ||
                        projectRef.current !== projectId
                      )
                        return;
                      setDetailView({
                        title: v.path,
                        description: "左侧为修改前，右侧为修改后。",
                        content: (
                          <div className="diff">
                            <pre>{before || "（不存在）"}</pre>
                            <pre>{after || "（已删除）"}</pre>
                          </div>
                        ),
                      });
                    })
                  }
                >
                  查看差异
                </button>
                <button
                  disabled={locked}
                  onClick={() =>
                    act(async () => {
                      await api(
                        `/revisions/${encodeURIComponent(v.id)}/undo`,
                        {},
                      );
                      setDetailView(null);
                      await refresh();
                      if (filePath) await openFile(filePath);
                      notify("已撤销；后续修改受到版本校验保护");
                    })
                  }
                >
                  撤销
                </button>
              </div>
            ))
          )}
        </div>
      ),
    });
  };
  const openLinkedSettings = useRef(
    new URLSearchParams(location.search).get("settings") === "auto-reply",
  );
  const configureProject = (showGateway = false) =>
    project &&
    setDialog({
      title: "项目设置",
      content: (
        <>
          <ProjectMetadata project={project} onSave={refresh} notify={notify} />
          <GatewayProjectSettings
            project={project}
            experts={experts}
            expanded={showGateway}
          />
          <ContextSettings
            project={project}
            notify={notify}
            onSaved={refresh}
          />
        </>
      ),
    });
  useEffect(() => {
    if (
      auth &&
      project &&
      openLinkedSettings.current &&
      project.id === initialLocation.get("project")
    ) {
      openLinkedSettings.current = false;
      configureProject(true);
    }
  }, [auth, project]);
  const taskDraft = useRef<{
    id: string;
    version: number;
    sessionId: string;
  } | null>(null);
  const draftWrites = useRef<Promise<unknown>>(Promise.resolve());
  const flushTaskDraft = async () => {
    const d = taskDraft.current;
    if (!d || d.sessionId !== sessionRef.current) return;
    const latest = composerCurrent.current.text;
    draftWrites.current = draftWrites.current
      .catch(() => {})
      .then(() =>
        api(
          `/tasks/${d.id}/draft`,
          { text: latest, version: d.version },
          "PUT",
        ),
      );
    await draftWrites.current;
  };
  useEffect(() => {
    if (!projectId || !sessionId) return;
    const id = sessionId;
    void api(`/projects/${projectId}/tasks`)
      .then(async (ts: any[]) => {
        const t = ts.find((t) => t.sessionId === id);
        if (!t) return;
        const ss = await api(`/sessions/${id}`);
        if (sessionRef.current !== id || !ss.draft) return;
        taskDraft.current = {
          id: t.id,
          version: t.draftVersion,
          sessionId: id,
        };
        if (!composerCurrent.current.text && !composerDrafts.current.has(id))
          setText(t.draftText || "");
      })
      .catch(() => {});
  }, [projectId, sessionId]);
  const beginTask = async (d: PreparedTask) => {
    if (d.task.projectId === personal) {
      let existing: { text: string; taskId?: string } = {
        text: localStorage.getItem("pwb.personalDraft") || "",
      };
      try {
        existing =
          JSON.parse(localStorage.getItem("pwb.personalComposer") || "null") ||
          existing;
      } catch {}
      const draft = existing.text;
      let next = d.text;
      if (draft.trim()) {
        if (existing.taskId && existing.taskId !== d.task.id) {
          if (
            !window.confirm(
              "已有另一条待办草稿。替换为本次待办？取消将保留原草稿。",
            )
          )
            return;
        } else {
          if (!window.confirm("今天已有草稿。将待办追加到草稿末尾？")) return;
          next = draft + "\n\n" + d.text;
        }
      }
      localStorage.setItem("pwb.personalDraft", next);
      localStorage.setItem(
        "pwb.personalComposer",
        JSON.stringify({ text: next, taskId: d.task.id }),
      );
      await navigate("today");
      return;
    }
    const p = projects.find((p) => p.id === d.task.projectId);
    if (!p) return;
    if (!(await canLeave())) return;
    const existing = composerDrafts.current.get(d.session.id);
    if (d.session.draft && (!existing || d.refilled))
      composerDrafts.current.set(d.session.id, {
        text: d.text,
        attachments: [],
        references: [],
        capabilities: [],
      });
    await openProject(p, d.session.id);
    taskDraft.current = d.session.draft
      ? {
          id: d.task.id,
          version: d.task.draftVersion || 0,
          sessionId: d.session.id,
        }
      : null;
  };
  useEffect(() => {
    const d = taskDraft.current;
    if (!d || d.sessionId !== sessionId) return;
    const timer = setTimeout(() => {
      void flushTaskDraft().catch((e) => notify(e.message));
    }, 500);
    return () => clearTimeout(timer);
  }, [text, sessionId]);
  const sessionRuns = runs
      .filter((r) => r.sessionId === sessionId)
      .sort((a, b) => a.createdAt - b.createdAt),
    activity = events.filter((e) => e.sessionId === sessionId);
  const pendingSubmissions = submissions.items.filter(
    (v) =>
      v.input.sessionId === sessionId &&
      !runs.some(
        (r) =>
          (v.messageId && r.messageId === v.messageId) ||
          (r.source === "web" &&
            r.sessionId === v.input.sessionId &&
            r.eventId === v.input.eventId),
      ),
  );
  if (auth === null) return <div className="loading">正在打开工作台…</div>;
  if (!auth)
    return (
      <>
        <Login onLogin={() => setAuth(true)} notify={notify} />
        {toast && (
          <div role="alert" className="toast">
            {toast}
          </div>
        )}
      </>
    );
  return (
    <div
      className={`app suite-app ${page === "today" ? "today-app" : ""} ${page === "project" ? "project-app" : ""}`}
    >
      <NavigationRail
        collapsed={railCollapsed}
        toggle={() => {
          const next = !railCollapsed;
          setRailCollapsed(next);
          localStorage.setItem("pwb.railCollapsed", String(next));
        }}
        mobileOpen={mobileNav}
        closeMobile={() => setMobileNav(false)}
        unread={unread}
        page={page}
        projects={projects}
        projectId={projectId}
        navigate={(next) => {
          void navigate(next);
        }}
        openProject={openProject}
        newProject={newProject}
        logout={() =>
          act(async () => {
            if (!(await canLeave())) return;
            await api("/logout", {});
            submissions.clear();
            setAuth(false);
          })
        }
      />
      <main>
        <header className="topbar">
          <button
            className="mobile-only icon"
            aria-label="打开导航"
            onClick={() => setMobileNav(!mobileNav)}
          >
            <Menu size={20} />
          </button>
        </header>
        {page === "calendar" && (
          <CalendarPage
            projects={projects}
            notify={notify}
            openProject={(id) => {
              const p = projects.find((v) => v.id === id);
              if (p) {
                history.pushState(
                  null,
                  "",
                  "#project=" + encodeURIComponent(id),
                );
                void openProject(p);
              }
            }}
            openTask={(id) => plannerNav("tasks", { task: id })}
            openRule={(id) => plannerNav("tasks", { tab: "ai", rule: id })}
          />
        )}
        {page === "tasks" && (
          <TaskCenter
            key={pageParams.toString()}
            projects={projects}
            experts={experts}
            begin={beginTask}
            notify={notify}
            parameters={pageParams}
          />
        )}
        {page === "notices" && (
          <NoticePage
            failure={pageParams.get("failure") || ""}
            select={(id) => plannerNav("notices", id ? { failure: id } : {})}
            openRule={(id) => plannerNav("tasks", { tab: "ai", rule: id })}
            notify={notify}
            refreshBadge={badge}
          />
        )}
        {page === "today" && (
          <Today
            projects={projects}
            openTasks={(id) => {
              if (id === personal) {
                plannerNav("tasks", { scope: personal });
                return;
              }
              const p = projects.find((v) => v.id === id);
              if (p) act(() => openProject(p, undefined, "tasks"));
            }}
            notify={notify}
            openNavigation={() => setMobileNav(true)}
          />
        )}
        {page === "projects" && (
          <div className="home page">
            <PageHeader
              title="项目"
              actions={
                <button className="primary" onClick={newProject}>
                  <Plus size={17} />
                  新建项目
                </button>
              }
            />
            {inbox.length > 0 && (
              <section className="inbox">
                <h3>
                  待归属消息 <span>{inbox.length}</span>
                </h3>
                {inbox.map((m) => (
                  <div className="inbox-row" key={m.id}>
                    <div>
                      <small>
                        {labels[m.incoming.source] ?? m.incoming.source}
                      </small>
                      <p>{m.incoming.text}</p>
                      <span>{m.question || "正在判断归属…"}</span>
                    </div>
                    {["wechat", "feishu"].includes(m.incoming.source) ? (
                      <button
                        disabled={m.state === "unrouted"}
                        onClick={() =>
                          act(async () => {
                            await api(`/messages/${m.id}/resolve`, {});
                            await refresh();
                            await navigate("today");
                          })
                        }
                      >
                        {m.state === "unrouted"
                          ? "正在转到今天…"
                          : "转到今天并回复"}
                      </button>
                    ) : (
                      <select
                        aria-label="为消息选择项目"
                        value=""
                        onChange={(e) =>
                          act(async () => {
                            await api(`/messages/${m.id}/resolve`, {
                              sessionId: e.target.value,
                            });
                            await refresh();
                          })
                        }
                      >
                        <option value="">选择项目</option>
                        {projects
                          .filter((p) => !p.archived)
                          .map((p) => (
                            <option key={p.id} value={p.defaultSessionId}>
                              {p.name}
                            </option>
                          ))}
                      </select>
                    )}
                  </div>
                ))}
              </section>
            )}
            {projects.filter((p) => !p.archived).length === 0 ? (
              <div className="empty-workspace">
                <div className="empty-symbol">
                  <Folder size={34} />
                </div>
                <h2>从你的第一个项目开始</h2>

                <button className="primary" onClick={newProject}>
                  <Plus size={16} />
                  新建项目
                </button>
              </div>
            ) : (
              <div className="project-grid">
                {projects
                  .filter((p) => !p.archived)
                  .map((p, i) => (
                    <button
                      className="project-card"
                      key={p.id}
                      onClick={() => openProject(p)}
                    >
                      <Folder className="project-row-icon" size={19} />
                      <h2>
                        {p.name}
                        <ChevronRight size={17} />
                      </h2>
                      <p>{plainPreview(p.description) || ""}</p>
                    </button>
                  ))}
              </div>
            )}
            {runs.length > 0 && (
              <section className="recent">
                <h3>最近工作</h3>
                {runs.slice(0, 8).map((r) => (
                  <button
                    key={r.id}
                    className="recent-row"
                    onClick={() => {
                      if (r.projectId === "personal-assistant") {
                        void navigate("today");
                        return;
                      }
                      const p = projects.find((p) => p.id === r.projectId);
                      if (p) {
                        void openProject(p, r.sessionId);
                      }
                    }}
                  >
                    <MessageSquare size={17} />
                    <div>
                      <strong>{r.text}</strong>
                      <small>
                        {projects.find((p) => p.id === r.projectId)?.name ||
                          "今天"}{" "}
                        · {stamp(r.createdAt)}
                      </small>
                    </div>
                    <State state={r.state} />
                  </button>
                ))}
              </section>
            )}
            {projects.some((p) => p.archived) && (
              <details className="archived">
                <summary>已归档项目</summary>
                {projects
                  .filter((p) => p.archived)
                  .map((p) => (
                    <div key={p.id}>
                      {p.name}
                      <button
                        onClick={() =>
                          act(async () => {
                            await api(
                              `/projects/${p.id}`,
                              { ...p, archived: false },
                              "PUT",
                            );
                            await refresh();
                          })
                        }
                      >
                        恢复
                      </button>
                    </div>
                  ))}
              </details>
            )}
          </div>
        )}
        {page === "project" && project && (
          <div className="workspace project-workspace">
            <ConversationPane>
              <header className="chat-header">
                <div className="project-chat-location">
                  <strong>{project.name}</strong>
                  <span className="location-divider">/</span>
                  <select
                    aria-label="当前对话"
                    value={sessionId}
                    onChange={(e) => void switchSession(e.target.value)}
                  >
                    {!visibleSessions.some((s) => s.id === sessionId) && (
                      <option value={sessionId}>新对话</option>
                    )}
                    {visibleSessions.map((ss) => (
                      <option key={ss.id} value={ss.id}>
                        {ss.title}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="project-context-tools">
                  {session && !session.draft && (
                    <GatewayResume
                      key={session.id}
                      id={session.id}
                      notify={notify}
                    />
                  )}
                  <ActionMenu
                    label="项目操作"
                    context={project.id + ":" + sessionId}
                    triggerContent={<MoreHorizontal size={19} />}
                    triggerClassName="icon"
                    panelClassName="suite-action-menu"
                  >
                    <button
                      role="menuitem"
                      tabIndex={-1}
                      onClick={() => configureProject()}
                    >
                      项目设置
                    </button>
                    <button
                      role="menuitem"
                      tabIndex={-1}
                      disabled={!session || session.draft}
                      onClick={() => renameSession()}
                    >
                      重命名对话
                    </button>
                    <button
                      role="menuitem"
                      tabIndex={-1}
                      disabled={creatingSession}
                      onClick={newSession}
                    >
                      另开对话
                    </button>
                  </ActionMenu>
                </div>
              </header>
              <div className="chat-scroll">
                {sessionRuns.length === 0 &&
                  pendingSubmissions.length === 0 && (
                    <div className="chat-welcome">
                      <h2>想从哪里开始？</h2>
                      <div>
                        <button
                          onClick={() =>
                            setText("看看项目里有哪些资料，帮我梳理下一步。")
                          }
                        >
                          梳理项目资料
                        </button>
                        <button onClick={() => setText("帮我完成一个任务：")}>
                          开始一项工作
                        </button>
                      </div>
                    </div>
                  )}
                {sessionRuns.length >= 100 && (
                  <button
                    className="panel-action"
                    onClick={() =>
                      act(async () => {
                        const older = await api<Run[]>(
                          `/runs?sessionId=${sessionId}&before=${historyCursors.current[sessionId] || sessionRuns[0].id}`,
                        );
                        if (!older.length) notify("已到最早的记录");
                        else
                          historyCursors.current[sessionId] = older.at(-1)!.id;
                        setRuns((old) => [
                          ...new Map(
                            [...old, ...older].map((r) => [r.id, r]),
                          ).values(),
                        ]);
                      })
                    }
                  >
                    加载更早的工作记录
                  </button>
                )}
                {session?.contextNotice && (
                  <p role="status">{session.contextNotice}</p>
                )}
                <CommunicationFeed
                  sessionId={sessionId}
                  notify={notify}
                  prepare={(v) => {
                    if (text.trim()) {
                      notify("输入框已有草稿，请先发送或清空后再准备回复。");
                      return;
                    }
                    setText(v);
                  }}
                />
                <SessionComponents
                  key={sessionId}
                  sessionId={sessionId}
                  projectId={project!.id}
                  notify={notify}
                />
                {sessionRuns
                  .filter((r) => r.kind !== "component_notice")
                  .map((r) => (
                    <article
                      className="turn"
                      key={r.id}
                      data-message-id={r.messageId}
                    >
                      <div
                        className={
                          r.agent ? "communication-input" : "user-message"
                        }
                      >
                        <AgentOrigin agent={r.agent} />
                        <span>
                          {r.source !== "web" && r.source !== "agent"
                            ? labels[r.source] + " · "
                            : ""}
                          {stamp(r.createdAt)}
                        </span>
                        <p>{r.text}</p>
                        <CapabilityBadges values={r.capabilities || []} />
                      </div>
                      <div className="assistant-message">
                        <div
                          className={`assistant-line ${r.state === "completed" ? "completed-line" : ""}`}
                        >
                          <span className="assistant-icon">✦</span>
                          <strong>
                            {r.expertName ||
                              experts.find((e) => e.id === r.expertId)?.name ||
                              "项目助手"}
                          </strong>
                          <State state={r.state} />
                          <small>#T{r.id.slice(0, 8)}</small>
                          {["queued", "running", "waiting"].includes(
                            r.state,
                          ) && (
                            <button
                              className="stop"
                              onClick={() =>
                                act(async () => {
                                  await api(`/runs/${r.id}/stop`, {});
                                  await refresh();
                                })
                              }
                            >
                              <Square size={12} />
                              停止
                            </button>
                          )}
                        </div>
                        {(r.state !== "completed" ||
                          !r.result ||
                          activity.some(
                            (e) =>
                              e.runId === r.id &&
                              e.type === "harness.tool/call",
                          )) && (
                          <button
                            className="activity-summary"
                            onClick={() =>
                              act(async () => {
                                if (!(await documentGuard())) return;
                                setFilePath("");
                                setContent("");
                                setSaved("");
                                setDetailView(null);
                                fileRequest.current++;
                                setRunDetails(r.id);
                                showProjectPanel();
                              })
                            }
                          >
                            <Terminal size={14} />
                            执行过程 ·{" "}
                            {
                              activity.filter(
                                (e) =>
                                  e.runId === r.id &&
                                  e.type === "harness.tool/call",
                              ).length
                            }{" "}
                            次工具调用
                            <ChevronRight size={13} />
                          </button>
                        )}
                        {r.result && (
                          <RichText
                            text={r.result}
                            onFile={(p) => act(() => openLinkedFile(p))}
                          />
                        )}{" "}
                        {!r.result && r.state === "running" && (
                          <p className="thinking">
                            {activity.some(
                              (e) =>
                                e.runId === r.id &&
                                e.type === "harness.turn/start",
                            )
                              ? "正在处理你的任务"
                              : "正在启动项目助手"}
                            <span>…</span>
                          </p>
                        )}
                        {r.state === "queued" && (
                          <p className="thinking">
                            {runs.some((x) =>
                              ["running", "waiting", "stopping"].includes(
                                x.state,
                              ),
                            )
                              ? "消息已收到，前面的任务结束后会按顺序执行。"
                              : "消息已收到，正在等待执行。"}
                          </p>
                        )}
                        {r.error && (
                          <div
                            className={`run-error ${r.state === "cancelled" ? "neutral" : ""}`}
                          >
                            {r.error}
                          </div>
                        )}
                        {["failed", "interrupted", "cancelled"].includes(
                          r.state,
                        ) && (
                          <button
                            className="continue"
                            onClick={() => {
                              setText(
                                "请检查上次任务已经产生的结果，从中断处继续完成，不重复已经成功的外部操作。",
                              );
                            }}
                          >
                            <RefreshCw size={13} />
                            继续这项工作
                          </button>
                        )}
                        {interactions
                          .filter((i) => i.runId === r.id)
                          .map((i) => (
                            <Question
                              key={i.id}
                              interaction={i}
                              onAnswer={async (answer) => {
                                await api(
                                  `/interactions/${i.id}/answer`,
                                  answer,
                                );
                                await refresh();
                              }}
                              notify={notify}
                            />
                          ))}
                      </div>
                    </article>
                  ))}
                {pendingSubmissions.map((v) => (
                  <article
                    className="turn pending-turn"
                    key={v.input.eventId}
                    data-event-id={v.input.eventId}
                  >
                    <div className="user-message">
                      <span>网页 · {stamp(v.createdAt)}</span>
                      <p>{v.input.text}</p>
                      <CapabilityBadges values={v.input.capabilities || []} />
                    </div>
                    <div className="submission-status" role="status">
                      <span className={`state ${v.state}`}>
                        {
                          {
                            sending: "发送中",
                            received: "已接收",
                            unknown: "待确认",
                            failed: "未能提交",
                          }[v.state]
                        }
                      </span>
                      <span>
                        {v.state === "sending"
                          ? "正在送达当前对话…"
                          : v.state === "received"
                            ? "已送达当前对话，正在准备执行。"
                            : v.error}
                      </span>
                      {v.state === "unknown" && (
                        <button
                          disabled={
                            submissions.sending ||
                            submissions.checking.has(v.input.eventId)
                          }
                          onClick={() => void submissions.retry(v)}
                        >
                          核对并重试
                        </button>
                      )}
                      {v.state === "failed" && (
                        <button
                          onClick={() => {
                            setText((old) =>
                              old ? old + "\n\n" + v.input.text : v.input.text,
                            );
                            setAttachments((old) => [
                              ...new Set([...old, ...v.input.attachments]),
                            ]);
                            setReferences((old) => [
                              ...old,
                              ...v.input.references,
                            ]);
                            setCapabilities((old) => [
                              ...new Map(
                                [...old, ...(v.input.capabilities || [])].map(
                                  (c) => [c.kind + ":" + c.id, c],
                                ),
                              ).values(),
                            ]);
                            submissions.dismiss(v.input.eventId);
                          }}
                        >
                          恢复到输入框
                        </button>
                      )}
                    </div>
                  </article>
                ))}
                <div ref={chatEnd} />
              </div>
              <div className="composer-wrap">
                <div className="composer">
                  {references.length > 0 && (
                    <div className="attachment-chips">
                      {references.map((r, i) => (
                        <button
                          key={i}
                          onClick={() =>
                            setReferences(references.filter((_, n) => n !== i))
                          }
                        >
                          引用{" "}
                          {projects.find((p) => p.id === r.projectId)?.name}/
                          {r.path}
                          <X size={12} />
                        </button>
                      ))}
                    </div>
                  )}
                  {attachments.length > 0 && (
                    <div className="attachment-chips">
                      {attachments.map((p) => (
                        <button
                          key={p}
                          onClick={() =>
                            setAttachments((a) => a.filter((x) => x !== p))
                          }
                        >
                          <Paperclip size={12} />
                          {p.split("/").at(-1)}
                          <X size={12} />
                        </button>
                      ))}
                    </div>
                  )}
                  <CapabilityComposer
                    projectId={projectId}
                    sessionId={sessionId}
                    value={text}
                    onChange={setText}
                    selected={capabilities}
                    onSelected={setCapabilities}
                    send={send}
                  />
                  <div className="composer-actions">
                    <select
                      aria-label="当前专家"
                      value={
                        session?.expertId ||
                        project.defaultExpertId ||
                        "general"
                      }
                      onChange={(e) =>
                        act(async () => {
                          const ss = await api<Session>(
                            `/sessions/${sessionId}`,
                            {
                              expertId:
                                e.target.value ===
                                (project.defaultExpertId || "general")
                                  ? ""
                                  : e.target.value,
                            },
                            "PUT",
                          );
                          setSessions((old) => mergeSessions(old, [ss]));
                        })
                      }
                    >
                      <option value="general">通用助手</option>
                      {experts
                        .filter((e) => project.expertIds?.includes(e.id))
                        .map((e) => (
                          <option key={e.id} value={e.id}>
                            {e.name}
                          </option>
                        ))}
                    </select>
                    <label title={locked ? "任务结束后可添加文件" : "添加附件"}>
                      <Paperclip size={18} />
                      <input
                        type="file"
                        aria-label="添加项目附件"
                        disabled={locked || busy}
                        onChange={(e) => {
                          importFiles(e.target.files);
                          e.target.value = "";
                        }}
                      />
                    </label>
                    <button
                      type="button"
                      title="引用其他项目的指定资料"
                      onClick={() =>
                        setDialog({
                          title: "引用项目资料",
                          description: "仅授权读取本次指定的文件，并保留来源。",
                          fields: [
                            {
                              key: "projectId",
                              label: "来源项目",
                              required: true,
                              options: projects
                                .filter(
                                  (p) => !p.archived && p.id !== projectId,
                                )
                                .map((p) => ({ value: p.id, label: p.name })),
                            },
                            {
                              key: "path",
                              label: "文件相对路径",
                              required: true,
                              hint: "例如：品牌/定位.md",
                            },
                          ],
                          action: async (v) => {
                            await api(
                              `/projects/${v.projectId}/file?path=${encodeURIComponent(v.path)}`,
                            );
                            setReferences((old) => [
                              ...old,
                              { projectId: v.projectId, path: v.path },
                            ]);
                          },
                        })
                      }
                    >
                      <FileText size={16} />
                    </button>
                    <button
                      className="send"
                      aria-label="发送"
                      disabled={
                        (!text.trim() &&
                          !attachments.length &&
                          !references.length) ||
                        busy ||
                        submissions.sending
                      }
                      onClick={send}
                    >
                      <ArrowUp size={18} />
                    </button>
                  </div>
                </div>
              </div>
            </ConversationPane>
            <ProjectSidebar
              open={projectPanelOpen}
              narrow={projectNarrow}
              tab={tab}
              select={(next) => void openContextPanel(next)}
              close={() => void collapseProjectPanel()}
            >
              {filePath || detailView || runDetails ? (
                <div className="editor-pane project-inline-detail">
                  {filePath ? (
                    <>
                      <header>
                        <div>
                          <FileText size={15} />
                          <span title={filePath}>{filePath}</span>
                          {content !== saved && <span className="unsaved" />}
                        </div>
                        <div>
                          <button
                            className={preview ? "selected" : ""}
                            disabled={binary || tooLarge}
                            onClick={() => setPreview(!preview)}
                          >
                            {preview ? "编辑" : "预览"}
                          </button>
                          <button
                            title="文件历史"
                            onClick={() => act(versions)}
                          >
                            <History size={15} />
                          </button>
                          <button
                            className="primary small"
                            disabled={
                              binary || tooLarge || locked || content === saved
                            }
                            onClick={() => act(saveDocument)}
                          >
                            保存
                          </button>
                          <button
                            aria-label="返回资料列表"
                            onClick={() => {
                              void closeDocument();
                            }}
                          >
                            返回列表
                          </button>
                        </div>
                      </header>
                      {locked && (
                        <div className="lock-note">
                          任务正在写入项目，可以查看和编辑草稿；任务结束或停止后再保存。
                        </div>
                      )}
                      <FileContent
                        file={{
                          path: filePath,
                          content,
                          hash,
                          binary,
                          tooLarge,
                        }}
                        preview={preview}
                        onChange={setContent}
                        onAsk={() => {
                          setAttachments((old) => [
                            ...new Set([...old, filePath]),
                          ]);
                          setText(
                            (old) =>
                              old ||
                              `请按需读取并处理项目文件「${filePath}」。`,
                          );
                          void closeDocument();
                        }}
                        url={endpoint(
                          `/projects/${projectId}/file?raw=1&path=${encodeURIComponent(filePath)}`,
                        )}
                        onFile={(p) => act(() => openLinkedFile(p))}
                      />
                      <footer>
                        <span>
                          {tooLarge
                            ? "大文件 · 原件已保存"
                            : binary
                              ? "附件只读预览"
                              : `${content.length} 字符`}
                        </span>
                        <a
                          href={endpoint(
                            `/projects/${projectId}/file?raw=1&download=1&path=${encodeURIComponent(filePath)}`,
                          )}
                        >
                          <Download size={13} />
                          下载
                        </a>
                        <button
                          disabled={locked}
                          onClick={() =>
                            act(async () => {
                              if (
                                !window.confirm(
                                  `删除 ${filePath}？可从文件历史撤销。`,
                                )
                              )
                                return;
                              await api(
                                `/projects/${projectId}/file?path=${encodeURIComponent(filePath)}&baseHash=${hash}`,
                                undefined,
                                "DELETE",
                              );
                              setFilePath("");
                              setContent("");
                              setSaved("");
                              await refresh();
                            })
                          }
                        >
                          删除文件
                        </button>
                      </footer>
                    </>
                  ) : (
                    <>
                      <header>
                        <strong>
                          {runDetails
                            ? `执行详情 #T${runDetails.slice(0, 8)}`
                            : detailView?.title}
                        </strong>
                        <button
                          aria-label="返回列表"
                          onClick={() => {
                            void closeDocument();
                          }}
                        >
                          返回列表
                        </button>
                      </header>
                      <div className="inspector-detail">
                        {runDetails ? (
                          <RunActivity
                            events={activity.filter(
                              (e) => e.runId === runDetails,
                            )}
                            running={true}
                          />
                        ) : (
                          <>
                            {detailView?.description && (
                              <p>{detailView.description}</p>
                            )}
                            {detailView?.content}
                          </>
                        )}
                      </div>
                    </>
                  )}
                </div>
              ) : (
                <div
                  className="project-context-view"
                  key={projectId + ":" + tab}
                >
                  {tab === "files" && (
                    <ProjectFiles
                      projectId={projectId}
                      files={files}
                      selected={filePath}
                      locked={locked}
                      refresh={refresh}
                      openFile={openFile}
                      notify={notify}
                      versions={() => act(versions)}
                    />
                  )}
                  {tab === "sessions" && (
                    <>
                      <button
                        className="panel-action"
                        onClick={newSession}
                        disabled={creatingSession}
                      >
                        <Plus size={15} />
                        另开对话
                      </button>
                      <div className="session-list">
                        {visibleSessions.map((ss) => (
                          <div
                            data-session-id={ss.id}
                            className={
                              "session-row " +
                              (sessionId === ss.id ? "selected" : "")
                            }
                            key={ss.id}
                          >
                            <button
                              className="session-select"
                              onClick={() => {
                                switchSession(ss.id);
                                if (projectNarrow)
                                  setMobileProjectPanelOpen(false);
                              }}
                            >
                              {ss.pinnedAt ? (
                                <Pin size={15} />
                              ) : (
                                <MessageSquare size={15} />
                              )}
                              <span>
                                {ss.title}
                                <small>
                                  {ss.lifecycle === "archiving"
                                    ? "正在提炼归档…"
                                    : ss.kind === "assistant"
                                      ? "默认项目助手"
                                      : "专题对话"}
                                </small>
                                {ss.lifecycleError && (
                                  <small className="error">
                                    {ss.lifecycleError}
                                  </small>
                                )}
                              </span>
                            </button>
                            <SessionMenu
                              label={`对话操作：${ss.title}`}
                              context={`${projectId}:${sessionId}:${projectPanelOpen}:${ss.lifecycle || ""}`}
                            >
                              {ss.lifecycle === "archiving" && (
                                <button
                                  role="menuitem"
                                  tabIndex={-1}
                                  onClick={() =>
                                    void act(async () => {
                                      await api(
                                        `/sessions/${ss.id}/archive/stop`,
                                        {},
                                      );
                                      await refresh();
                                    })
                                  }
                                >
                                  停止归档
                                </button>
                              )}
                              <button
                                role="menuitem"
                                tabIndex={-1}
                                disabled={!!ss.lifecycle}
                                onClick={() =>
                                  void act(async () => {
                                    const updated = await api<Session>(
                                      `/sessions/${ss.id}/pin`,
                                      { pinned: !ss.pinnedAt },
                                      "PUT",
                                    );
                                    setSessions((old) =>
                                      mergeSessions(old, [updated]),
                                    );
                                  })
                                }
                              >
                                {ss.pinnedAt ? "取消置顶" : "置顶"}
                              </button>
                              <button
                                role="menuitem"
                                tabIndex={-1}
                                disabled={!!ss.lifecycle}
                                onClick={() => renameSession(ss)}
                              >
                                重命名
                              </button>
                              <button
                                role="menuitem"
                                tabIndex={-1}
                                disabled={!!ss.lifecycle}
                                onClick={() =>
                                  setDialog({
                                    title: "归档对话",
                                    description:
                                      "自动提炼核心信息到项目 MD，原始对话进入回收站保留 15 天。",
                                    confirm: "开始归档",
                                    action: async () => {
                                      await api(
                                        `/sessions/${ss.id}/archive`,
                                        {},
                                      );
                                      await refresh();
                                    },
                                  })
                                }
                              >
                                归档
                              </button>
                              <button
                                role="menuitem"
                                tabIndex={-1}
                                disabled={!!ss.lifecycle}
                                onClick={() =>
                                  setDialog({
                                    title: "删除对话",
                                    description:
                                      "原始对话进入回收站，15 天内可找回。项目文件继续保留。",
                                    confirm: "移入回收站",
                                    action: async () => {
                                      await api(
                                        `/sessions/${ss.id}`,
                                        undefined,
                                        "DELETE",
                                      );
                                      await refresh();
                                    },
                                  })
                                }
                              >
                                删除
                              </button>
                            </SessionMenu>
                          </div>
                        ))}
                      </div>
                    </>
                  )}
                  {tab === "tasks" && (
                    <TasksPanel
                      projectId={projectId}
                      projects={projects}
                      inlineDetail
                      editorChanged={(open) =>
                        setTaskEditorLayout(open ? projectNarrow : null)
                      }
                      selectedId={linkedTask}
                      clearSelected={() => setLinkedTask("")}
                      begin={beginTask}
                      notify={notify}
                    />
                  )}
                  {tab === "schedules" && (
                    <ProjectSchedules
                      projectId={projectId}
                      notify={notify}
                      manage={(rule) =>
                        plannerNav("tasks", {
                          tab: "ai",
                          scope: projectId,
                          ...(rule ? { rule } : {}),
                        })
                      }
                    />
                  )}
                  <div className="project-panel-footer">
                    <button
                      onClick={() =>
                        setDialog({
                          title: "项目能力",
                          content: (
                            <ProjectCapabilities
                              project={project}
                              experts={experts}
                              onSave={async (p) => {
                                await api(`/projects/${p.id}`, p, "PUT");
                                await refresh();
                                setDialog(null);
                              }}
                            />
                          ),
                        })
                      }
                    >
                      <Library size={15} />
                      项目专家
                      <ChevronRight size={14} />
                    </button>
                    <button
                      onClick={() =>
                        act(async () => {
                          if (!(await documentGuard())) return;
                          if (
                            !window.confirm(
                              "归档后项目仍保留，停止接收新任务。是否归档？",
                            )
                          )
                            return;
                          await api(
                            `/projects/${projectId}`,
                            { ...project, archived: true },
                            "PUT",
                          );
                          setPage("projects");
                          setProjectId("");
                          setFilePath("");
                          setContent("");
                          setSaved("");
                          await refresh();
                        })
                      }
                    >
                      <Archive size={15} />
                      归档项目
                    </button>
                  </div>
                </div>
              )}
            </ProjectSidebar>
          </div>
        )}
        {(page === "experts" || page === "expert-detail") && (
          <div
            className={`expert-library ${page === "expert-detail" ? "has-detail" : ""}`}
          >
            <section
              className="expert-library-index page"
              aria-label="专家列表"
            >
              <PageHeader
                title="专家库"
                actions={
                  <>
                    <ActionMenu
                      label="导入专家"
                      context={page}
                      triggerContent={
                        <>
                          导入
                          <ChevronDown size={14} />
                        </>
                      }
                      triggerClassName="expert-import-trigger"
                      panelClassName="suite-action-menu"
                    >
                      <button
                        role="menuitem"
                        tabIndex={-1}
                        onClick={() => expertDirectoryInput.current?.click()}
                      >
                        选择目录
                      </button>
                      <button
                        role="menuitem"
                        tabIndex={-1}
                        onClick={() => expertZipInput.current?.click()}
                      >
                        ZIP 文件
                      </button>
                      <button
                        role="menuitem"
                        tabIndex={-1}
                        onClick={() =>
                          setDialog({
                            title: "从 Git 导入专家",
                            fields: [
                              {
                                key: "url",
                                label: "HTTPS 仓库地址",
                                required: true,
                              },
                              {
                                key: "commit",
                                label: "固定提交（40 位 SHA）",
                                required: true,
                              },
                              {
                                key: "subdir",
                                label: "能力包目录",
                                hint: "SKILL.md 所在的相对目录",
                              },
                            ],
                            action: async (v) => {
                              await api("/experts/git", v);
                              await refresh();
                            },
                          })
                        }
                      >
                        Git 仓库
                      </button>
                    </ActionMenu>
                    <button
                      className="primary"
                      onClick={() =>
                        setDialog({
                          title: "创建专家",
                          fields: [
                            { key: "name", label: "名称", required: true },
                            {
                              key: "description",
                              label: "能力简介",
                              required: true,
                            },
                            {
                              key: "instructions",
                              label: "方法与工作准则",
                              type: "textarea",
                              required: true,
                            },
                          ],
                          action: async (v) => {
                            await api("/experts", v);
                            await refresh();
                          },
                        })
                      }
                    >
                      <Plus size={17} />
                      创建专家
                    </button>
                  </>
                }
              />
              <input
                ref={expertDirectoryInput}
                hidden
                type="file"
                multiple
                {...({ webkitdirectory: "" } as any)}
                aria-label="导入专家目录"
                onChange={(e) => {
                  void importFiles(e.target.files, false, true);
                  e.target.value = "";
                }}
              />
              <input
                ref={expertZipInput}
                hidden
                type="file"
                accept=".zip"
                aria-label="导入专家ZIP"
                onChange={(e) => {
                  void importFiles(e.target.files, false, true);
                  e.target.value = "";
                }}
              />
              <input
                className="expert-search"
                type="search"
                aria-label="搜索专家"
                placeholder="搜索专家"
                value={expertSearch}
                onChange={(e) => setExpertSearch(e.target.value)}
              />
              <div className="expert-grid">
                {experts
                  .filter((e) =>
                    (e.name + e.description)
                      .toLocaleLowerCase()
                      .includes(expertSearch.toLocaleLowerCase()),
                  )
                  .map((e) => (
                    <button
                      key={e.id}
                      aria-current={
                        page === "expert-detail" && expertId === e.id
                          ? "true"
                          : undefined
                      }
                      className="expert-card"
                      onClick={() => void openExpert(e.id)}
                    >
                      <Library size={19} />
                      <div>
                        <h2>{e.name}</h2>
                        <p>{plainPreview(e.description, 120)}</p>
                      </div>
                      <ChevronRight size={17} />
                    </button>
                  ))}
              </div>
              {!experts.length && (
                <EmptyState>暂无专家。可以创建或导入。</EmptyState>
              )}
              {!!experts.length &&
                !experts.some((e) =>
                  (e.name + e.description)
                    .toLocaleLowerCase()
                    .includes(expertSearch.toLocaleLowerCase()),
                ) && <EmptyState>没有找到匹配的专家。</EmptyState>}
            </section>
            {page === "expert-detail" && expertId && (
              <ExpertDetail
                key={expertId}
                id={expertId}
                projects={projects}
                refresh={refresh}
                notify={notify}
                onBack={() => {
                  void navigate("experts");
                }}
                onOpenProject={(p) => {
                  void openProject(p);
                }}
                onCopy={(id) => {
                  fileRequest.current++;
                  setExpertId(id);
                }}
                onGuard={(guard) => {
                  expertGuard.current = guard;
                }}
              />
            )}
          </div>
        )}
        {page === "settings" && (
          <SettingsPage act={act} setDialog={setDialog} notify={notify} />
        )}
      </main>
      {unsavedDialog}
      {dialog && (
        <Modal dialog={dialog} close={() => setDialog(null)} notify={notify} />
      )}{" "}
      {toast && (
        <div role="alert" className="toast">
          <span>{toast}</span>
          <button aria-label="关闭提示" onClick={() => setToast("")}>
            <X size={15} />
          </button>
        </div>
      )}
      {busy && <div className="global-busy">正在处理…</div>}
    </div>
  );
}

function Login({
  onLogin,
  notify,
}: {
  onLogin: () => void;
  notify: (s: string) => void;
}) {
  const [password, setPassword] = useState(""),
    [busy, setBusy] = useState(false);
  return (
    <div className="login">
      <div className="login-art">
        <span className="brand-mark">工</span>
        <h1>
          一处工作区，
          <br />
          持续向前。
        </h1>
        <p>你的项目、方法和每一步进展。</p>
        <div className="orbit orbit-one" />
        <div className="orbit orbit-two" />
      </div>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          try {
            await api("/login", { password });
            onLogin();
          } catch (e) {
            notify((e as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <span className="eyebrow">WELCOME BACK</span>
        <h2>登录工作台</h2>
        <p>继续你正在进行的工作。</p>
        <label>
          密码
          <input
            autoFocus
            required
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <button className="primary" disabled={busy}>
          {busy ? "正在登录…" : "进入工作台"}
          <ChevronRight size={16} />
        </button>
      </form>
    </div>
  );
}
function State({ state }: { state: string }) {
  return (
    <span className={`state state-${state}`}>{labels[state] ?? state}</span>
  );
}
function RunActivity({
  events,
  running,
}: {
  events: WorkEvent[];
  running: boolean;
}) {
  const tools = events.filter((e) => e.type === "harness.tool/call"),
    results = events.filter((e) => e.type === "harness.tool/result");
  if (!tools.length) return null;
  return (
    <details className="activity" open={running}>
      <summary>
        <Terminal size={13} />
        {running ? "正在执行" : "执行过程"} · {tools.length} 次工具调用
        <ChevronDown size={13} />
      </summary>
      {tools.map((e) => {
        const result = results.find((r) => r.data.callId === e.data.callId);
        return (
          <details key={e.seq} className="tool">
            <summary>
              {result ? <Check size={12} /> : <span className="pulse" />}
              <strong>{e.data.name}</strong>
              <span>{stamp(e.createdAt)}</span>
            </summary>
            <pre>
              {typeof e.data.arguments === "string"
                ? e.data.arguments
                : JSON.stringify(e.data.arguments, null, 2)}
            </pre>
            {result && (
              <pre>
                {JSON.stringify(
                  result.data.result ?? result.data,
                  null,
                  2,
                ).slice(0, 30000)}
              </pre>
            )}
          </details>
        );
      })}
    </details>
  );
}
function Modal({
  dialog,
  close,
  notify,
}: {
  dialog: Dialog;
  close: () => void;
  notify: (s: string) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>(() =>
      Object.fromEntries(
        (dialog.fields ?? []).map((f) => [f.key, f.value ?? ""]),
      ),
    ),
    [busy, setBusy] = useState(false);
  useEffect(() => {
    setValues(
      Object.fromEntries(
        (dialog.fields ?? []).map((f) => [f.key, f.value ?? ""]),
      ),
    );
  }, [dialog]);
  const base = Object.fromEntries(
    (dialog.fields ?? []).map((f) => [f.key, f.value ?? ""]),
  );
  const fieldGuard = useSurfaceGuard(
    !busy && JSON.stringify(values) !== JSON.stringify(base),
  );
  const dismiss = async () => {
    if (busy) return;
    if (dialog.content ? await confirmSurfaceNavigation() : fieldGuard())
      close();
  };
  return (
    <Overlay
      title={dialog.title}
      close={() => void dismiss()}
      busy={busy}
      className={`modal ${dialog.content ? "wide" : ""}`}
    >
      {dialog.description && (
        <p className="modal-description">{dialog.description}</p>
      )}
      {dialog.content ?? (
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            try {
              await dialog.action?.(values);
              close();
            } catch (e) {
              notify((e as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          {dialog.fields?.map((f, i) => (
            <label key={f.key}>
              {f.label}
              {f.options ? (
                <select
                  required={f.required}
                  value={values[f.key] ?? ""}
                  onChange={(e) =>
                    setValues({ ...values, [f.key]: e.target.value })
                  }
                >
                  <option value="">请选择</option>
                  {f.options.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
              ) : f.type === "textarea" ? (
                <textarea
                  rows={5}
                  required={f.required}
                  value={values[f.key] ?? ""}
                  onChange={(e) =>
                    setValues({ ...values, [f.key]: e.target.value })
                  }
                />
              ) : (
                <input
                  data-autofocus={i === 0}
                  required={f.required}
                  type={f.type ?? "text"}
                  value={values[f.key] ?? ""}
                  onChange={(e) =>
                    setValues({ ...values, [f.key]: e.target.value })
                  }
                />
              )}{" "}
              {f.hint && <small>{f.hint}</small>}
            </label>
          ))}
          <footer>
            <button
              type="button"
              disabled={busy}
              onClick={() => void dismiss()}
            >
              取消
            </button>
            <button className="primary" disabled={busy}>
              {busy ? "正在处理…" : (dialog.confirm ?? "保存")}
            </button>
          </footer>
        </form>
      )}
    </Overlay>
  );
}
function Question({
  interaction: i,
  onAnswer,
  notify,
}: {
  interaction: any;
  onAnswer: (a: any) => Promise<void>;
  notify: (s: string) => void;
}) {
  const [answers, setAnswers] = useState<
      Record<string, { selected: string[]; custom: string }>
    >({}),
    [busy, setBusy] = useState(false);
  const send = async (a: any) => {
    setBusy(true);
    try {
      await onAnswer(a);
    } catch (e) {
      notify((e as Error).message);
      setBusy(false);
    }
  };
  const questions = i.payload.questions ?? [];
  return (
    <div className="question">
      <h4>
        {i.kind === "approval" ? "需要单次授权" : "需要你的补充"}{" "}
        <small>#Q{i.id.slice(0, 8)}</small>
      </h4>
      {i.kind === "approval" ? (
        <>
          <p>
            {i.payload.toolName} · {i.payload.reason}
          </p>
          <button
            disabled={busy}
            onClick={() => send({ outcome: "allowed-once" })}
          >
            允许这一次
          </button>
          <button disabled={busy} onClick={() => send({ outcome: "rejected" })}>
            拒绝
          </button>
        </>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            send({
              answers: questions.map((q: any) => ({
                id: q.id,
                selected: answers[q.id]?.selected ?? [],
                ...(answers[q.id]?.custom
                  ? { custom: answers[q.id].custom }
                  : {}),
              })),
            });
          }}
        >
          {questions.map((q: any) => (
            <label key={q.id}>
              {q.question ?? q.title ?? q.prompt}
              {q.detail && <RichText text={q.detail} />}
              <div className="options">
                {q.options?.map((o: any) => (
                  <button
                    type="button"
                    key={o.id ?? o.label}
                    className={
                      answers[q.id]?.selected.includes(o.label) ? "primary" : ""
                    }
                    onClick={() => {
                      const selected = answers[q.id]?.selected ?? [];
                      setAnswers({
                        ...answers,
                        [q.id]: {
                          selected: q.multiSelect
                            ? selected.includes(o.label)
                              ? selected.filter((v) => v !== o.label)
                              : [...selected, o.label]
                            : [o.label],
                          custom: "",
                        },
                      });
                    }}
                  >
                    {o.label ?? o.title}
                  </button>
                ))}
              </div>
              {q.intent?.kind !== "approval" && (
                <input
                  placeholder="也可以填写补充说明"
                  required={!answers[q.id]?.selected.length}
                  value={answers[q.id]?.custom ?? ""}
                  onChange={(e) =>
                    setAnswers({
                      ...answers,
                      [q.id]: {
                        selected: q.multiSelect
                          ? (answers[q.id]?.selected ?? [])
                          : [],
                        custom: e.target.value,
                      },
                    })
                  }
                />
              )}
            </label>
          ))}
          <button className="primary small" disabled={busy}>
            提交回答
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => send({ cancelled: true })}
          >
            暂不回答
          </button>
        </form>
      )}
    </div>
  );
}
function ProjectCapabilities({
  project,
  experts,
  onSave,
}: {
  project: Project;
  experts: Expert[];
  onSave: (p: Project) => Promise<void>;
}) {
  const [p, setP] = useState(project);
  useSurfaceGuard(JSON.stringify(p) !== JSON.stringify(project));
  return (
    <div className="capabilities">
      <h3>专家方法</h3>
      {experts.length === 0 && <p>先到专家库导入或创建一个能力包。</p>}
      {experts.map((e) => (
        <label key={e.id}>
          <input
            type="checkbox"
            checked={p.expertIds?.includes(e.id) ?? false}
            onChange={(v) =>
              setP({
                ...p,
                expertIds: v.target.checked
                  ? [...(p.expertIds ?? []), e.id]
                  : (p.expertIds ?? []).filter((id) => id !== e.id),
                defaultExpertId:
                  p.defaultExpertId === e.id && !v.target.checked
                    ? ""
                    : p.defaultExpertId,
              })
            }
          />
          <span>
            {e.name}
            <small>{e.description}</small>
          </span>
        </label>
      ))}
    </div>
  );
}
function SettingsPage({
  act,
  setDialog,
  notify,
}: {
  act: (f: () => Promise<any>) => Promise<any>;
  setDialog: (d: Dialog | null) => void;
  notify: (s: string) => void;
}) {
  const [settings, setSettings] = useState<any>({}),
    [connections, setConnections] = useState<any[]>([]),
    [deliveries, setDeliveries] = useState<any[]>([]),
    [plugins, setPlugins] = useState<any[]>([]);
  const [focus, setFocus] = useState<
    "channels" | "mcp" | "cli" | "components" | "account" | null
  >(null);
  const [connectionId, setConnectionId] = useState("");
  const narrow = useNarrow(1099);
  const load = async () => {
    const [s, c, d, p] = await Promise.all([
      api("/settings"),
      api<any[]>("/connections"),
      api<any[]>("/deliveries"),
      api<any[]>("/plugins"),
    ]);
    setSettings(s);
    setConnections(c);
    setDeliveries(d);
    setPlugins(p);
  };
  useEffect(() => {
    act(load);
  }, []);
  return (
    <div className={`page settings-page ${focus ? "with-detail" : ""}`}>
      <PageHeader title="设置" />
      <div className="settings-main">
        <section className="settings-section connections-overview">
          <header>
            <h2>连接与工具</h2>{" "}
            <button
              onClick={() =>
                setDialog({
                  title: "绑定本人私聊",
                  fields: [
                    {
                      key: "kind",
                      label: "类型",
                      value: "wechat",
                      hint: "wechat 或 feishu",
                      required: true,
                    },
                    { key: "label", label: "连接名称", required: true },
                    {
                      key: "ownerId",
                      label: "本人渠道账号 ID",
                      hint: "飞书可留空，启用后通过一次性绑定码确认本人。",
                    },
                    {
                      key: "secrets",
                      label: "渠道配置（JSON）",
                      type: "textarea",
                      value: "{}",
                      hint: "飞书：appId、appSecret；微信：baseUrl、token、accountId",
                    },
                  ],
                  action: async (v) => {
                    await api("/connections", {
                      connection: {
                        kind: v.kind,
                        label: v.label,
                        ownerId: v.ownerId,
                        enabled: false,
                      },
                      secrets: JSON.parse(v.secrets),
                    });
                    await load();
                  },
                })
              }
            >
              <Plus size={15} />
              添加连接
            </button>
          </header>
          {connections.map((c) => (
            <button
              key={c.id}
              className="settings-entry"
              aria-expanded={focus === "channels" && connectionId === c.id}
              onClick={() => {
                setConnectionId(c.id);
                setFocus("channels");
              }}
            >
              <MessageSquare size={18} />
              <strong>{c.label || labels[c.kind]}</strong>
              <span>
                {!c.enabled
                  ? "已停用"
                  : (
                      {
                        connected: "已连接",
                        disconnected: "未连接",
                        connecting: "连接中",
                        error: "连接异常",
                      } as Record<string, string>
                    )[c.status] || c.status}
              </span>
              <ChevronRight size={16} />
            </button>
          ))}
          {!connections.length && (
            <p className="empty-text">暂无微信或飞书连接。</p>
          )}
          <button
            className="settings-entry"
            aria-expanded={focus === "mcp"}
            onClick={() => setFocus("mcp")}
          >
            <Library size={18} />
            <strong>MCP</strong>
            <span>
              {
                plugins.filter((p) => !p.component && p.enabled !== false)
                  .length
              }{" "}
              个全局启用
            </span>
            <ChevronRight size={16} />
          </button>
          <button
            className="settings-entry"
            aria-expanded={focus === "components"}
            onClick={() => setFocus("components")}
          >
            <Library size={18} />
            <strong>工具组件</strong>
            <span>按项目接入</span>
            <ChevronRight size={16} />
          </button>
          <button
            className="settings-entry"
            aria-expanded={focus === "cli"}
            onClick={() => setFocus("cli")}
          >
            <Terminal size={18} />
            <strong>CLI</strong>
            <span>管理</span>
            <ChevronRight size={16} />
          </button>
          {settings.browserUrl && (
            <div className="setting-row">
              <div>
                <strong>浏览器接管</strong>
                <p>登录或验证时，打开当前执行环境的浏览器。</p>
              </div>
              <a
                className="button"
                href={settings.browserUrl}
                target="_blank"
                rel="noreferrer"
              >
                打开 <ExternalLink size={14} />
              </a>
            </div>
          )}
        </section>
        <button
          className="settings-entry"
          aria-expanded={focus === "account"}
          onClick={() => setFocus("account")}
        >
          <Settings size={18} />
          <strong>账号</strong>
          <ChevronRight size={16} />
        </button>
        <details className="settings-advanced">
          <summary>
            <SlidersHorizontal size={18} />
            高级
          </summary>
          <section className="settings-section">
            <h3>运行状态</h3>
            <div className="setting-row">
              <div>
                <strong>DeepSeek Harness</strong>
                <p>
                  {settings.model || "尚未配置"} · 同一个执行核心服务所有入口
                </p>
              </div>
              <span className="state state-completed">
                {settings.runtimeMode || "未配置"}
              </span>
            </div>
            <div className="setting-row">
              <div>
                <strong>全工作台并发上限</strong>
                <p>
                  个人助手、项目任务和客服应答共享名额。同一客户的消息按顺序处理，同一项目一次只运行一个写入任务。
                </p>
              </div>
              <span>{settings.maxConcurrentRuns}</span>
            </div>
          </section>
          <section className="settings-section">
            <h3>发送回执</h3>
            {deliveries.length ? (
              deliveries.slice(0, 15).map((d) => (
                <div key={d.id} className="setting-row">
                  <div>
                    <strong>{d.text.slice(0, 75)}</strong>
                    <p>
                      {stamp(d.createdAt)} {d.error}
                    </p>
                  </div>
                  <State state={d.state} />
                </div>
              ))
            ) : (
              <p className="empty-text">尚无渠道发送记录。</p>
            )}
          </section>
          <RecycleBin notify={notify} />
        </details>
      </div>
      <Overlay
        open={focus === "channels"}
        title={
          connections.find((c) => c.id === connectionId)?.label || "连接详情"
        }
        close={() => setFocus(null)}
        mode="drawer"
        modal={narrow}
        className="settings-detail"
      >
        <p className="muted">接续本人个人对话，回复发回本次消息来源。</p>
        {connections
          .filter((c) => c.id === connectionId)
          .map((c) => (
            <div className="setting-row" key={c.id}>
              <div>
                <strong>{c.label || labels[c.kind]}</strong>
                {c.ownerId.startsWith("pairing:") && (
                  <p>
                    启用后，在飞书私聊机器人发送：
                    <code>绑定 {c.ownerId.slice(8)}</code>
                  </p>
                )}
                <p>
                  {(
                    {
                      connected: "已连接",
                      disconnected: "未连接",
                      connecting: "连接中",
                      error: "连接异常",
                    } as Record<string, string>
                  )[c.status] || c.status}{" "}
                  ·{" "}
                  {c.lastHeartbeat
                    ? `最近连接 ${stamp(c.lastHeartbeat)}`
                    : "尚未连接"}
                </p>
              </div>
              <button
                onClick={() =>
                  act(async () => {
                    await api("/connections", {
                      connection: { ...c, enabled: !c.enabled },
                    });
                    await load();
                  })
                }
              >
                {c.enabled ? "停用" : "启用"}
              </button>
            </div>
          ))}
      </Overlay>
      <Overlay
        open={focus === "mcp"}
        title="MCP 插件"
        close={() => setFocus(null)}
        mode="drawer"
        modal={narrow}
        className="settings-detail"
      >
        <section className="settings-section">
          <div className="surface-inline-actions">
            <button
              onClick={() =>
                setDialog({
                  title: "添加 MCP 插件",
                  fields: [
                    { key: "name", label: "插件名称", required: true },
                    { key: "description", label: "用途说明" },
                    {
                      key: "transport",
                      label: "传输方式",
                      value: "http",
                      hint: "http 或 stdio",
                    },
                    { key: "url", label: "HTTPS 地址（http）" },
                    { key: "command", label: "容器内命令（stdio）" },
                    {
                      key: "args",
                      label: "命令参数（JSON 数组）",
                      value: "[]",
                    },
                    {
                      key: "headers",
                      label: "请求头（JSON，独立保存）",
                      type: "textarea",
                      value: "{}",
                    },
                  ],
                  action: async (v) => {
                    await api("/plugins", {
                      ...v,
                      args: JSON.parse(v.args),
                      headers: JSON.parse(v.headers),
                    });
                    await load();
                  },
                })
              }
            >
              <Plus size={15} />
              添加插件
            </button>
          </div>
          <p>全局启用的 MCP 可供所有项目调用。</p>
          {plugins
            .filter((p) => !p.component)
            .map((p) => (
              <div key={p.id} className="setting-row">
                <div>
                  <strong>{p.name}</strong>
                  <p>
                    {p.description || p.transport}
                    {p.hasSecrets ? " · 已配置凭证" : ""}
                    {p.enabled === false ? " · 已停用" : " · 全局启用"}
                  </p>
                </div>
                <button
                  onClick={() =>
                    act(async () => {
                      await api("/plugins", {
                        ...p,
                        enabled: p.enabled === false,
                      });
                      await load();
                    })
                  }
                >
                  {p.enabled === false ? "启用" : "停用"}
                </button>
                <button
                  onClick={() =>
                    act(async () => {
                      await api(`/plugins/${p.id}`, undefined, "DELETE");
                      await load();
                    })
                  }
                >
                  移除
                </button>
              </div>
            ))}
        </section>
      </Overlay>
      <Overlay
        open={focus === "cli"}
        title="CLI 工具"
        close={() => setFocus(null)}
        mode="drawer"
        modal={narrow}
        className="settings-detail"
      >
        <GlobalTools notify={notify} />
      </Overlay>
      <Overlay
        open={focus === "components"}
        title="工具组件"
        close={() => setFocus(null)}
        mode="drawer"
        modal={narrow}
        className="settings-detail"
      >
        <ToolComponents notify={notify} />
      </Overlay>
      <Overlay
        open={focus === "account"}
        title="账号"
        close={() => setFocus(null)}
        mode="drawer"
        modal={narrow}
        className="settings-detail"
      >
        <section className="settings-section">
          <div className="setting-row">
            <div>
              <strong>工作台密码</strong>
              <p>用于网页登录。</p>
            </div>
            <button
              onClick={() =>
                setDialog({
                  title: "修改密码",
                  fields: [
                    {
                      key: "password",
                      label: "新密码",
                      type: "password",
                      required: true,
                      hint: "至少 8 个字符",
                    },
                  ],
                  action: async (v) => {
                    await api("/settings/password", v);
                    notify("密码已更新");
                  },
                })
              }
            >
              修改密码
            </button>
          </div>
        </section>
      </Overlay>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(<App />);

function ProjectMetadata({
  project,
  onSave,
  notify,
}: {
  project: Project;
  onSave: () => Promise<void>;
  notify: (s: string) => void;
}) {
  const [name, setName] = useState(project.name),
    [aliases, setAliases] = useState((project.aliases || []).join("，"));
  const [savedFields, setSavedFields] = useState({
    name: project.name,
    aliases: (project.aliases || []).join("，"),
  });
  useSurfaceGuard(name !== savedFields.name || aliases !== savedFields.aliases);
  return (
    <div className="project-metadata">
      <label>
        名称
        <input value={name} onChange={(e) => setName(e.target.value)} />
      </label>
      <label>
        别名
        <input value={aliases} onChange={(e) => setAliases(e.target.value)} />
      </label>
      <button
        onClick={async () => {
          try {
            await api(
              `/projects/${project.id}`,
              {
                name,
                aliases: aliases
                  .split(/[,，]/)
                  .map((s) => s.trim())
                  .filter(Boolean),
              },
              "PATCH",
            );
            setSavedFields({ name, aliases });
            await onSave();
            notify("名称与别名已保存");
          } catch (e: any) {
            notify(e.message);
          }
        }}
      >
        保存名称与别名
      </button>
    </div>
  );
}
