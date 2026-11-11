package engine

import (
	"context"
	"encoding/json"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"workbench-native/internal/core"
)

type Terminal struct{ RunID, AttemptID, State string }

func reportFailure(ctx workflow.Context, id string, err *error) func() {
	return func() {
		if *err == nil || workflow.IsContinueAsNewError(*err) {
			return
		}
		state := "failed"
		if temporal.IsCanceledError(*err) {
			state = "canceled"
		}
		cleanup, _ := workflow.NewDisconnectedContext(ctx)
		cleanup = activityContext(cleanup, InteractiveQueue)
		// Terminal recording is an idempotent side effect; it does not rerun work.
		_ = workflow.ExecuteActivity(cleanup, "RecordTerminal", Terminal{id, workflow.GetInfo(ctx).WorkflowExecution.RunID, state}).Get(cleanup, nil)
	}
}
func (a *Activities) RecordTerminal(ctx context.Context, in Terminal) error {
	var raw []byte
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, in.RunID).Scan(&raw); e != nil {
		return e
	}
	var r core.Run
	if e := json.Unmarshal(raw, &r); e != nil {
		return e
	}
	if in.State == "canceled" {
		// A prepared delivery has not crossed the connector boundary. Persist
		// its cancellation even if execution stopped before creating a decision.
		// Submitting/unknown requests retain their original receipt lookup.
		if _, e := a.Store.DB.ExecContext(ctx, `UPDATE wb_deliveries SET state='failed',body=JSON_SET(body,'$.state','failed','$.error','run_canceled_before_submission') WHERE run_id=? AND state='prepared'`, r.ID); e != nil {
			return e
		}
		if _, e := a.Store.DB.ExecContext(ctx, `UPDATE wb_decisions SET state='canceled',body=JSON_SET(body,'$.state','canceled') WHERE run_id=? AND state='waiting'`, r.ID); e != nil {
			return e
		}
	}
	text := "本轮执行失败，已保存成功步骤；请查看原任务，再决定是否继续。"
	if in.State == "canceled" {
		text = "本轮已停止，已完成结果保留，后续复查已取消。"
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: in.AttemptID + "-terminal", SessionID: r.Principal.SessionID, RunID: r.ID, Kind: "run." + in.State, Text: text})
}
