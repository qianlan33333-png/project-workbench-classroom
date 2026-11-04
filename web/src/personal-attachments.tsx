import { useEffect, useRef, useState } from "react";
import { FileText, Loader2, RotateCcw, X } from "lucide-react";
import { api } from "./api";
export type PersonalAttachment = {
  id: string;
  name: string;
  size: number;
  path?: string;
  state: "uploading" | "ready" | "failed";
  error?: string;
};
const nameKey = "pwb.personalUploadNames";
export function attachmentName(path: string): string {
  if (path.startsWith("upload:")) {
    try {
      return JSON.parse(localStorage.getItem(nameKey) || "{}")[path] || "附件";
    } catch {
      return "附件";
    }
  }
  return (path.split("/").at(-1) || "附件").replace(/^[a-f0-9]{8}-/, "");
}
function restored(session: string): PersonalAttachment[] {
  try {
    const rows = JSON.parse(
      localStorage.getItem("pwb.personalAttachments:" + session) || "[]",
    );
    return Array.isArray(rows)
      ? rows
          .filter(
            (v) =>
              v &&
              typeof v.id === "string" &&
              typeof v.name === "string" &&
              typeof v.size === "number",
          )
          .slice(0, 8)
          .map((v) =>
            v.state === "ready" &&
            typeof v.path === "string" &&
            v.path.startsWith("upload:")
              ? v
              : {
                  ...v,
                  path: undefined,
                  state: "failed",
                  error: "请重新选择这个文件",
                },
          )
      : [];
  } catch {
    return [];
  }
}
export function usePersonalAttachments(
  sessionId: string,
  notify: (s: string) => void,
) {
  const [state, setState] = useState<{
    session: string;
    items: PersonalAttachment[];
  }>({ session: "", items: [] });
  const current = useRef(sessionId),
    files = useRef(new Map<string, File>()),
    controllers = useRef(new Map<string, AbortController>());
  const itemsRef = useRef<PersonalAttachment[]>([]);
  current.current = sessionId;
  const replace = (
    fn: (items: PersonalAttachment[]) => PersonalAttachment[],
  ) => {
    const next = fn(itemsRef.current);
    itemsRef.current = next;
    setState({ session: current.current, items: next });
  };
  useEffect(() => {
    for (const c of controllers.current.values()) c.abort();
    controllers.current.clear();
    files.current.clear();
    const items = sessionId ? restored(sessionId) : [];
    itemsRef.current = items;
    setState({ session: sessionId, items });
    return () => {
      for (const c of controllers.current.values()) c.abort();
      controllers.current.clear();
    };
  }, [sessionId]);
  useEffect(() => {
    if (sessionId && state.session === sessionId)
      localStorage.setItem(
        "pwb.personalAttachments:" + sessionId,
        JSON.stringify(state.items),
      );
  }, [state, sessionId]);
  const upload = async (item: PersonalAttachment) => {
    const file = files.current.get(item.id),
      session = sessionId;
    if (!file || controllers.current.has(item.id) || !session) return;
    if (file.size > 128 * 1024 * 1024) {
      replace((rows) =>
        rows.map((v) =>
          v.id === item.id
            ? { ...v, state: "failed", error: "文件超过128MB，请缩小后重试" }
            : v,
        ),
      );
      return;
    }
    const controller = new AbortController();
    controllers.current.set(item.id, controller);
    replace((rows) =>
      rows.map((v) =>
        v.id === item.id ? { ...v, state: "uploading", error: undefined } : v,
      ),
    );
    try {
      const form = new FormData();
      form.append("sessionId", session);
      form.append("file", file);
      const result = await api<{ path: string; name: string; size: number }>(
        "/personal/attachments",
        form,
        undefined,
        controller.signal,
      );
      if (controller.signal.aborted || current.current !== session) return;
      try {
        const names = JSON.parse(localStorage.getItem(nameKey) || "{}");
        names[result.path] = result.name;
        const keys = Object.keys(names);
        for (const k of keys.slice(0, Math.max(0, keys.length - 200)))
          delete names[k];
        localStorage.setItem(nameKey, JSON.stringify(names));
      } catch {
        /* Names are presentation only. */
      }
      replace((rows) =>
        rows.map((v) =>
          v.id === item.id
            ? { ...v, ...result, state: "ready", error: undefined }
            : v,
        ),
      );
    } catch (e: any) {
      if (!controller.signal.aborted && current.current === session)
        replace((rows) =>
          rows.map((v) =>
            v.id === item.id ? { ...v, state: "failed", error: e.message } : v,
          ),
        );
    } finally {
      if (controllers.current.get(item.id) === controller)
        controllers.current.delete(item.id);
    }
  };
  const add = (list: FileList | File[] | null) => {
    if (!list?.length) return;
    if (!sessionId) {
      notify("对话还在准备，请稍后添加附件。");
      return;
    }
    for (const file of Array.from(list)) {
      if (itemsRef.current.length >= 8) {
        notify("一条消息最多添加8个附件。");
        break;
      }
      const item: PersonalAttachment = {
        id: crypto.randomUUID(),
        name: file.name,
        size: file.size,
        state: "uploading",
      };
      files.current.set(item.id, file);
      replace((rows) => [...rows, item]);
      void upload(item);
    }
  };
  return {
    items: state.session === sessionId ? state.items : [],
    add,
    remove: (id: string) => {
      controllers.current.get(id)?.abort();
      controllers.current.delete(id);
      files.current.delete(id);
      replace((rows) => rows.filter((v) => v.id !== id));
    },
    retry: (id: string) => {
      const item = itemsRef.current.find((v) => v.id === id);
      if (item) {
        if (files.current.has(id)) void upload(item);
        else notify("请移除后重新选择这个文件。");
      }
    },
    clear: () => {
      files.current.clear();
      replace(() => []);
    },
  };
}
export function PersonalAttachmentChips({
  items,
  remove,
  retry,
}: {
  items: PersonalAttachment[];
  remove: (id: string) => void;
  retry: (id: string) => void;
}) {
  return (
    <div className="personal-attachment-list">
      {items.map((item) => (
        <div key={item.id} className={`personal-attachment ${item.state}`}>
          {item.state === "uploading" ? (
            <Loader2 size={17} className="spin" />
          ) : (
            <FileText size={17} />
          )}
          <div>
            <span className="personal-attachment-name">{item.name}</span>
            {item.state === "uploading" && <small role="status">上传中…</small>}
            {item.state === "failed" && (
              <small role="alert">{item.error || "上传失败"}</small>
            )}
          </div>
          {item.state === "failed" && (
            <button
              aria-label={"重试上传：" + item.name}
              onClick={() => retry(item.id)}
            >
              <RotateCcw size={15} />
            </button>
          )}
          <button
            aria-label={"移除附件：" + item.name}
            onClick={() => remove(item.id)}
          >
            <X size={15} />
          </button>
        </div>
      ))}
    </div>
  );
}
