export const base = new URL(".", document.baseURI).pathname.replace(/\/$/, "");
export const endpoint = (path: string) => `${base}/api/v1${path}`;
export async function api<T = any>(
  path: string,
  body?: unknown,
  method?: string,
  signal?: AbortSignal,
): Promise<T> {
  const isForm = body instanceof FormData;
  const res = await fetch(endpoint(path), {
    signal,
    method: method ?? (body === undefined ? "GET" : "POST"),
    headers:
      body !== undefined && !isForm
        ? { "Content-Type": "application/json" }
        : undefined,
    body: body === undefined ? undefined : isForm ? body : JSON.stringify(body),
  });
  if (res.status === 401 && path != "/login")
    window.dispatchEvent(new Event("auth-expired"));
  const data = await res.json();
  if (!res.ok)
    throw Object.assign(new Error(data.error ?? `请求失败（${res.status}）`), {
      status: res.status,
    });
  return data;
}
export type Project = {
  id: string;
  name: string;
  description: string;
  aliases: string[];
  defaultSessionId: string;
  expertIds: string[];
  defaultExpertId: string;
  pluginIds: string[];
  archived: boolean;
};
export type Session = {
  pinnedAt?: number;
  recycledAt?: number;
  expiresAt?: number;
  recycleReason?: string;
  lifecycle?: string;
  lifecycleError?: string;
  archivePath?: string;
  draft?: boolean;
  titleSource?: string;
  updatedAt?: number;
  id: string;
  projectId: string;
  title: string;
  kind: string;
  expertId: string;
  harnessId: string;
};
export type Run = {
  kind?: string;
  id: string;
  sessionId: string;
  projectId: string;
  messageId: string;
  eventId?: string;
  text: string;
  result: string;
  state: string;
  error: string;
  source: string;
  expertId: string;
  expertDigest: string;
  expertName?: string;
  createdAt: number;
  startedAt: number;
  finishedAt: number;
  scheduleId: string;
};
export type Expert = {
  skillName?: string;
  updatedAt?: number;
  createdAt?: number;
  id: string;
  name: string;
  description: string;
  digest: string;
  tools: string[];
};
export type Message = {
  id: string;
  state: string;
  incoming: {
    text: string;
    source: string;
    eventId?: string;
    sessionId?: string;
  };
  createdAt?: number;
  sessionId: string;
  runId: string;
  question: string;
  candidates: { projectId: string; sessionId: string; label: string }[];
};
export type FileEntry = {
  path: string;
  directory: boolean;
  size: number;
  modifiedAt: number;
};
export type WorkEvent = {
  seq: number;
  sessionId: string;
  runId: string;
  type: string;
  data: any;
  createdAt: number;
};
export const labels: Record<string, string> = {
  queued: "排队中",
  running: "执行中",
  waiting: "等待回答",
  completed: "已完成",
  failed: "失败",
  cancelled: "已停止",
  interrupted: "已中断",
  stopping: "等待清理",
  pending: "待发送",
  sending: "发送中",
  sent: "平台已发送",
  accepted: "平台已接受",
  unknown: "待核对",
  web: "网页",
  wechat: "微信",
  feishu: "飞书",
  automation: "定时任务",
};
export const stamp = (n: number) =>
  n
    ? new Date(n).toLocaleString("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "";
