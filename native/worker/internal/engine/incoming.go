package engine

import (
	"context"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"time"
)

// The existing incoming Run stays open until its message has a disposition.
// Re-delivery reloads the same native inbox after Host restarts; it never
// constructs a second Session or a second provider send.
func Incoming(ctx workflow.Context, id string) (err error) {
	defer reportFailure(ctx, id, &err)()
	defer func() {
		if err != nil {
			cleanup, _ := workflow.NewDisconnectedContext(ctx)
			cleanup = activityContext(cleanup, InteractiveQueue)
			_ = workflow.ExecuteActivity(cleanup, "HandoffIncoming", id).Get(cleanup, nil)
		}
	}()
	ctx = activityContext(ctx, InteractiveQueue)
	dispatchCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{TaskQueue: InteractiveQueue, StartToCloseTimeout: 2 * time.Minute, WaitForCancellation: true, RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 30 * time.Second, NonRetryableErrorTypes: []string{"Denied", "Invalid", "Conflict"}}})
	for checks := 0; checks < 150; checks++ {
		var done bool
		if err = workflow.ExecuteActivity(ctx, "IncomingFinished", id).Get(ctx, &done); err != nil {
			return err
		}
		if done {
			return nil
		}
		if err = workflow.ExecuteActivity(dispatchCtx, "DispatchProject", id).Get(ctx, nil); err != nil {
			break
		}
		if err = workflow.Sleep(ctx, 2*time.Second); err != nil {
			return err
		}
	}
	return workflow.ExecuteActivity(ctx, "HandoffIncoming", id).Get(ctx, nil)
}
func (a *Activities) IncomingFinished(ctx context.Context, id string) (bool, error) {
	m, e := a.Store.IncomingForRun(ctx, id)
	if e != nil {
		return false, failure(e)
	}
	return m.State != "received", nil
}
func (a *Activities) HandoffIncoming(ctx context.Context, id string) error {
	m, e := a.Store.IncomingForRun(ctx, id)
	if e != nil {
		return failure(e)
	}
	if m.State != "received" {
		return nil
	}
	return a.Store.FinishUnansweredIncoming(ctx, m, "no_reply")
}
