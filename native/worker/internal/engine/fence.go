package engine

import (
	"context"
	"encoding/json"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"workbench-native/internal/core"
)

func (a *Activities) BeginWrite(ctx context.Context, id, execution string) (core.WriteFence, error) {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return core.WriteFence{}, failure(e)
	}
	if r.Principal.Kind != "project" {
		return core.WriteFence{}, failure(core.ErrDenied)
	}
	f, e := a.Store.BeginWrite(ctx, r.Principal.ProjectID, execution)
	return f, failure(e)
}
func (a *Activities) EndWrite(ctx context.Context, id, execution string) error {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil { // Revocation must not prevent invalidating a writer.
		var raw []byte
		if e = a.Store.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, id).Scan(&raw); e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &r); e != nil {
			return e
		}
	}
	return a.Store.EndWrite(ctx, core.WriteFence{ProjectID: r.Principal.ProjectID, Execution: execution})
}
func finishWrite(ctx workflow.Context, id, execution string, err *error) func() {
	return func() {
		if workflow.IsContinueAsNewError(*err) {
			return
		}
		cleanup, _ := workflow.NewDisconnectedContext(ctx)
		cleanup = activityContext(cleanup, InteractiveQueue)
		options := workflow.GetActivityOptions(cleanup)
		options.RetryPolicy = &temporal.RetryPolicy{}
		cleanup = workflow.WithActivityOptions(cleanup, options)
		// Do not close the child while an old attempt can still commit. MySQL's row
		// lock makes invalidation wait for any already committing transaction.
		_ = workflow.ExecuteActivity(cleanup, "EndWrite", id, execution).Get(cleanup, nil)
	}
}
