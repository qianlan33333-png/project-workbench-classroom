import test from "node:test";
import assert from "node:assert/strict";
import {
  mergeRuns,
  mergeSessions,
  initialSessionTitle,
} from "../web/src/chat-state.ts";
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

test("a delayed title snapshot cannot overwrite an owner rename", () => {
  const named = {
    id: "s",
    title: "人工标题",
    updatedAt: 20,
    titleSource: "manual",
  };
  assert.equal(
    mergeSessions([named], [{ ...named, title: "AI标题", updatedAt: 19 }])[0],
    named,
  );
  assert.equal(
    initialSessionTitle("请帮我整理客户的回访计划"),
    "请帮我整理客户的回访计划",
  );
  assert.equal(initialSessionTitle(""), "附件讨论");
  assert.equal(Array.from(initialSessionTitle("😀".repeat(30))).length, 25);
});
