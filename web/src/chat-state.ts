import type { Run } from "./api";

// Snapshot requests may finish after a newer SSE event. Never let them regress a run.
export function mergeRuns(
  existing: Run[],
  incoming: Run[],
  eventSequences?: Map<string, number>,
  requestedAt = Infinity,
): Run[] {
  const rows = new Map(existing.map((r) => [r.id, r]));
  for (const run of incoming) {
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
