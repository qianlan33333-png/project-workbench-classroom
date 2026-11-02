import test from "node:test";
import assert from "node:assert/strict";
import {
  mergeRuns,
  mergeEvents,
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

test("archive maintenance never becomes a user chat turn", () => {
  assert.equal(
    mergeRuns([], [run("completed", { kind: "archive" })]).length,
    0,
  );
});

const event = (seq, sessionId, type="harness.tool/call") => ({seq,sessionId,runId:"r-"+sessionId,type,data:{},createdAt:seq});
test("global event pressure never erases selected history or tool counts",()=>{
  const history=Array.from({length:2400},(_,i)=>event(i+1,"selected"));
  const flood=Array.from({length:4000},(_,i)=>event(i+2401,"another-project","run.updated"));
  let state=mergeEvents([],history,"selected");
  for (let i=0;i<flood.length;i+=100) state=mergeEvents(state,flood.slice(i,i+100),"selected");
  assert.equal(state.filter(e=>e.sessionId==="selected"&&e.type==="harness.tool/call").length,2400);
  assert.equal(state.filter(e=>e.sessionId!=="selected").length,1800);
  for(const replay of [history.slice().reverse(), flood.slice().reverse()]) state=mergeEvents(state,replay,"selected");
  assert.equal(state.filter(e=>e.sessionId==="selected").length,2400);
  assert.equal(new Set(state.map(e=>e.seq)).size,state.length);
});
test("history and realtime arrival order converge; changing focus bounds old history",()=>{
  const history=[event(1,"a"),event(2,"a"),event(3,"a")];
  const live=event(4,"a");
  assert.deepEqual(mergeEvents(mergeEvents([],history,"a"),[live],"a"),mergeEvents(mergeEvents([],[live],"a"),history,"a"));
  const other=Array.from({length:3000},(_,i)=>event(i+5,"b"));
  const changed=mergeEvents(mergeEvents([],history,"a"),other,"b");
  assert.equal(changed.filter(e=>e.sessionId==="b").length,3000);
  const cleared=mergeEvents(changed,[],"");assert.equal(cleared.length,1800);
});
