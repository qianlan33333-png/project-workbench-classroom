import type { Run, Session, WorkEvent } from "./api";

// Snapshot requests may finish after a newer SSE event. Never let them regress a run.
export function mergeRuns(
  existing: Run[],
  incoming: Run[],
  eventSequences?: Map<string, number>,
  requestedAt = Infinity,
): Run[] {
  const rows = new Map(existing.map((r) => [r.id, r]));
  for (const run of incoming) {
    if (run.kind === "archive") continue;
    if ((eventSequences?.get(run.id) || 0) > requestedAt) continue;
    const old = rows.get(run.id);
    if (
      old &&
      ((old.finishedAt || 0) > (run.finishedAt || 0) ||
        (old.startedAt || 0) > (run.startedAt || 0))
    )
      continue;
    rows.set(run.id, run);
  }
  return [...rows.values()];
}

// Titles are metadata, never part of the session identity. Slow snapshots must
// not undo an automatic title or an owner's newer rename.
export function mergeSessions(
  existing: Session[],
  incoming: Session[],
): Session[] {
  const rows = new Map(existing.map((s) => [s.id, s]));
  for (const session of incoming) {
    const old = rows.get(session.id);
    if (!old || (session.updatedAt || 0) >= (old.updatedAt || 0))
      rows.set(session.id, session);
  }
  return [...rows.values()].sort(
    (a, b) =>
      Number(!!b.pinnedAt) - Number(!!a.pinnedAt) ||
      (b.pinnedAt || 0) - (a.pinnedAt || 0) ||
      (b.updatedAt || 0) - (a.updatedAt || 0),
  );
}
export function initialSessionTitle(text: string): string {
  const words = text.trim().split(/\s+/).slice(0, 8).join(" ");
  const chars = Array.from(words);
  return chars.length > 24
    ? chars.slice(0, 24).join("") + "…"
    : words || "附件讨论";
}

// The live stream is global. A bounded background cache must not evict the
// selected conversation's historical tool calls when any new event arrives.
export function mergeEvents(existing: WorkEvent[], incoming: WorkEvent[], sessionId: string): WorkEvent[] {
  const rows = new Map([...existing, ...incoming].map(event => [event.seq, event]));
  const ordered = [...rows.values()].sort((a,b) => a.seq-b.seq);
  const selected = sessionId ? ordered.filter(event => event.sessionId === sessionId) : [];
  const background = ordered.filter(event => !sessionId || event.sessionId !== sessionId).slice(-1800);
  return [...selected, ...background].sort((a,b) => a.seq-b.seq);
}
