package engine

import (
	"context"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"time"
	"workbench-native/internal/core"
)

func Sync(ctx workflow.Context, id string) (err error) {
	defer reportFailure(ctx, id, &err)()
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{TaskQueue: BatchQueue, StartToCloseTimeout: 4 * time.Hour, HeartbeatTimeout: 45 * time.Second, WaitForCancellation: true, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 3, NonRetryableErrorTypes: []string{"Denied", "Invalid", "Conflict"}}})
	defer finishWrite(ctx, id, workflow.GetInfo(ctx).WorkflowExecution.RunID, &err)()
	var fence core.WriteFence
	if e := workflow.ExecuteActivity(activityContext(ctx, InteractiveQueue), "BeginWrite", id, workflow.GetInfo(ctx).WorkflowExecution.RunID).Get(ctx, &fence); e != nil {
		return e
	}
	return workflow.ExecuteActivity(ctx, "SyncResource", TaskInput{id, fence}).Get(ctx, nil)
}
func (a *Activities) SyncResource(ctx context.Context, in TaskInput) error {
	activity.RecordHeartbeat(ctx, in.RunID)
	id := in.RunID
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return failure(e)
	}
	fence, e := a.Store.BeginSyncAttempt(ctx, in.Fence, activity.GetInfo(ctx).Attempt)
	if e != nil {
		return failure(e)
	}
	ctx = core.WithFence(ctx, fence)
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
	_, e = a.Store.SyncRunSource(ctx, r, func() { activity.RecordHeartbeat(ctx, id) })
	return failure(e)
}
