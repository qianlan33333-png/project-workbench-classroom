package engine

import (
	"context"
	"errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"time"
	"workbench-native/internal/core"
)

type OwnerRequest struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type AgentHost interface {
	OwnerRequest(context.Context, core.Principal) (OwnerRequest, error)
	Dispatch(context.Context, core.Run) error
}

// Dispatch completion means the target Session durably accepted the request;
// subsequent business jobs expose their own execution and receipt states.
func Dispatch(ctx workflow.Context, id string) (err error) {
	defer reportFailure(ctx, id, &err)()
	// The channel already durably accepted the input. Host restarts must not
	// discard that input after three short attempts; delivery is idempotent by
	// run ID, and Temporal retains retries without keeping a Worker occupied.
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue: InteractiveQueue, StartToCloseTimeout: 2 * time.Minute,
		WaitForCancellation: true,
		RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 2,
			MaximumInterval: time.Minute, MaximumAttempts: 0,
			NonRetryableErrorTypes: []string{"Denied", "Invalid", "Conflict"}},
	})
	return workflow.ExecuteActivity(ctx, "DispatchProject", id).Get(ctx, nil)
}
func (a *Activities) DispatchProject(ctx context.Context, id string) error {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return failure(e)
	}
	if e = a.Store.RememberRead(ctx, r.Principal, core.UnionRefs(r.Sources, r.Lineage)); e != nil {
		return failure(e)
	}
	if e = a.Host.Dispatch(ctx, r); e != nil {
		if errors.Is(e, core.ErrProjectExpertRequired) && r.Operation == "dispatch" && r.NotifySession != "" {
			event := core.RunEvent{ID: id + "-expert-required", SessionID: r.NotifySession, RunID: id,
				Kind: "project.expert-required", Text: "目标项目有多个可用专家。请设置项目默认专家后再派发。", Details: map[string]any{"projectId": r.Principal.ProjectID}}
			if err := a.Store.Emit(ctx, event); err != nil {
				return failure(err)
			}
			return failure(core.ErrConflict)
		}
		return failure(e)
	}
	if r.Operation == "incoming" {
		return nil
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: id + "-accepted", SessionID: r.NotifySession, RunID: id, Kind: "project.accepted", Text: "目标项目已接收要求，将在项目对话中执行。已接收不代表业务完成。", Details: map[string]any{"projectId": r.Principal.ProjectID, "sessionId": r.Principal.SessionID}})
}
