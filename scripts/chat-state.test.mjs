import test from "node:test";
import assert from "node:assert/strict";
import { mergeRuns } from "../web/src/chat-state.ts";
const run = (state, extra = {}) => ({
  id: "run-1",
  state,
  startedAt: 10,
  finishedAt: 0,
  ...extra,
});
test("a slow snapshot cannot overwrite a newer SSE state", () => {
  const waiting = run("waiting");
  const sequences = new Map([["run-1", 15]]);
  assert.equal(
    mergeRuns([waiting], [run("running")], sequences, 10)[0],
    waiting,
  );
  assert.equal(
    mergeRuns([waiting], [run("running")], sequences, 15)[0].state,
    "running",
  );
});
test("terminal results survive late snapshots and historical SSE replay", () => {
  const completed = run("completed", { finishedAt: 20, result: "done" });
  assert.equal(
    mergeRuns([completed], [run("queued", { startedAt: 0 })])[0],
    completed,
  );
  assert.equal(mergeRuns([completed], [run("running")])[0], completed);
  assert.equal(
    mergeRuns([run("queued", { startedAt: 0 })], [run("running")])[0].state,
    "running",
  );
});
