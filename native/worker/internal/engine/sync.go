package engine

import (
	"context"
	"fmt"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"time"
	"workbench-native/internal/core"
)

func Sync(ctx workflow.Context, id string) (err error) {
	defer reportFailure(ctx, id, &err)()
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{TaskQueue: BatchQueue, StartToCloseTimeout: 2 * time.Hour, HeartbeatTimeout: 45 * time.Second, WaitForCancellation: true, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 3, NonRetryableErrorTypes: []string{"Denied", "Invalid", "Conflict"}}})
	defer finishWrite(ctx, id, workflow.GetInfo(ctx).WorkflowExecution.RunID, &err)()
	var fence core.WriteFence
	if e := workflow.ExecuteActivity(activityContext(ctx, InteractiveQueue), "BeginWrite", id, workflow.GetInfo(ctx).WorkflowExecution.RunID).Get(ctx, &fence); e != nil {
		return e
	}
	return workflow.ExecuteActivity(ctx, "SyncResource", TaskInput{id, fence}).Get(ctx, nil)
}
func (a *Activities) SyncResource(ctx context.Context, in TaskInput) error {
	ctx = core.WithFence(ctx, in.Fence)
	id := in.RunID
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return failure(e)
	}
	done := make(chan struct{})
	defer close(done)
	activity.RecordHeartbeat(ctx, id)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				activity.RecordHeartbeat(ctx, id)
			}
		}
	}()
	v, e := a.Store.SyncSource(ctx, r, func() { activity.RecordHeartbeat(ctx, id) })
	if e != nil {
		return failure(e)
	}
	refs := []core.Ref{}
	for _, section := range v.Sections {
		refs = append(refs, core.Ref{ResourceID: v.ResourceID, Version: v.Version, Section: section.ID})
	}
	if e = a.Store.PinRunSources(ctx, r.Principal, id, refs); e != nil {
		return failure(e)
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: id + "-published", SessionID: r.Principal.SessionID, RunID: id, Kind: "resource.published", Text: fmt.Sprintf("资料已发布，共 %d 条。", v.Count), Details: v})
}
