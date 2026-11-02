import { useCallback, useEffect, useRef, useState } from "react";
import { api, type Message, type Run, type Capability } from "./api";

export type MessageInput = {
  taskId?:string;
  capabilities?: Capability[];
  eventId: string;
  sessionId: string;
  text: string;
  attachments: string[];
  references: { projectId: string; path: string }[];
};
export type Submission = {
  input: MessageInput;
  createdAt: number;
  state: "sending" | "received" | "unknown" | "failed";
  messageId?: string;
  error?: string;
};
const key = "pwb.pendingMessages";
function restore(): Submission[] {
  try {
    return (JSON.parse(sessionStorage.getItem(key) || "[]") as Submission[])
      .filter(
        (v) =>
          v.input?.eventId &&
          v.input?.sessionId &&
          typeof v.input.text === "string",
      )
      .map((v) => ({
        ...v,
        state: v.state === "sending" ? "unknown" : v.state,
      }));
  } catch {
    return [];
  }
}

// Pending inputs are separate from server runs: receipt never pretends to be execution.
export function useMessageSubmissions(
  enabled: boolean,
  runs: Run[],
  onRun: (r: Run) => void,
) {
  const [items, setItems] = useState<Submission[]>(restore);
  const [sending, setSending] = useState(false);
  const [checkingIds, setCheckingIds] = useState(new Set<string>());
  const inFlight = useRef(false);
  const checking = useRef(new Set<string>());
  const current = useRef(items);
  current.current = items;
  const acceptRun = useRef(onRun);
  acceptRun.current = onRun;
  const patch = useCallback((id: string, fields: Partial<Submission>) => {
    setItems((old) =>
      old.map((v) => (v.input.eventId === id ? { ...v, ...fields } : v)),
    );
  }, []);
  const receipt = useCallback(
    (id: string, messageId: string) => {
      patch(id, { messageId, state: "received", error: undefined });
    },
    [patch],
  );
  const accept = useCallback(
    async (id: string, message: Message) => {
      receipt(id, message.id);
      if (message.state === "error" || message.state === "clarify")
        patch(id, {
          state: "failed",
          error: message.question || "消息需要补充信息，请查看待归属消息",
        });
      if (message.runId)
        acceptRun.current(await api<Run>(`/runs/${message.runId}`));
    },
    [receipt, patch],
  );
  const transmit = useCallback(
    async (input: MessageInput) => {
      if (inFlight.current) return;
      inFlight.current = true;
      setSending(true);
      patch(input.eventId, { state: "sending", error: undefined });
      try {
        const message = await api<Message>(
          "/messages",
          input,
          "POST",
          AbortSignal.timeout(15000),
        );
        await accept(input.eventId, message);
      } catch (e) {
        const error = e as Error & { status?: number };
        const rejected =
          !!error.status &&
          error.status >= 400 &&
          error.status < 500 &&
          ![408, 429].includes(error.status);
        // A transport timeout may happen after persistence. Preserve the ID and verify before retrying.
        setItems((old) =>
          old.map((v) =>
            v.input.eventId === input.eventId
              ? {
                  ...v,
                  state: v.messageId
                    ? "received"
                    : rejected
                      ? "failed"
                      : "unknown",
                  error: rejected
                    ? error.message
                    : "暂未确认发送结果，原消息已保留。",
                }
              : v,
          ),
        );
      } finally {
        inFlight.current = false;
        setSending(false);
      }
    },
    [accept, patch],
  );
  const submit = useCallback(
    (input: MessageInput) => {
      if (inFlight.current) return false;
      setItems((old) => [
        ...old,
        { input, createdAt: Date.now(), state: "sending" },
      ]);
      void transmit(input);
      return true;
    },
    [transmit],
  );
  const check = useCallback(
    async (input: MessageInput, retryIfMissing = false) => {
      if (checking.current.has(input.eventId)) return;
      checking.current.add(input.eventId);
      setCheckingIds(new Set(checking.current));
      try {
        const m = await api<Message>(
          `/messages/by-event/${encodeURIComponent(input.eventId)}`,
          undefined,
          undefined,
          AbortSignal.timeout(8000),
        );
        await accept(input.eventId, m);
      } catch (e) {
        if ((e as any).status === 404) {
          if (retryIfMissing) await transmit(input);
        } else if (retryIfMissing)
          patch(input.eventId, {
            state: "unknown",
            error: "暂时无法核对，请稍后重试；没有再次发送。",
          });
      } finally {
        checking.current.delete(input.eventId);
        setCheckingIds(new Set(checking.current));
      }
    },
    [accept, patch, transmit],
  );
  useEffect(() => {
    try {
      sessionStorage.setItem(key, JSON.stringify(items));
    } catch {
      /* UI remains usable if storage is unavailable. */
    }
  }, [items]);
  useEffect(() => {
    if (!items.length) return;
    const matched = (v: Submission) =>
      runs.some(
        (r) =>
          (v.messageId && r.messageId === v.messageId) ||
          (r.source === "web" &&
            r.sessionId === v.input.sessionId &&
            r.eventId === v.input.eventId),
      );
    if (items.some(matched)) setItems((old) => old.filter((v) => !matched(v)));
  }, [runs, items]);
  useEffect(() => {
    if (!enabled || !items.length) return;
    const tick = () => {
      for (const item of current.current)
        if (item.state !== "failed") void check(item.input);
    };
    tick();
    const timer = setInterval(tick, 1500);
    return () => clearInterval(timer);
  }, [enabled, items.length, check]);
  return {
    items,
    sending,
    checking: checkingIds,
    submit,
    receipt,
    retry: (v: Submission) => check(v.input, true),
    dismiss: (id: string) =>
      setItems((old) => old.filter((v) => v.input.eventId !== id)),
    clear: () => {
      setItems([]);
      try {
        sessionStorage.removeItem(key);
      } catch {
        /* Storage may be unavailable. */
      }
    },
  };
}
