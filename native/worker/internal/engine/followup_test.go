package engine

import (
	"context"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"testing"
	"time"
)

func TestFollowupStopsAfterThreeAutomaticReviews(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	env.SetStartTime(start)
	reviews, limits := 0, 0
	env.RegisterActivityWithOptions(func(_ context.Context, in FollowupInput) (FollowupInput, error) {
		reviews++
		in.NextAt = start.Add(time.Duration(reviews+1) * time.Hour)
		return in, nil
	}, activity.RegisterOptions{Name: "ReviewFollowup"})
	env.RegisterActivityWithOptions(func(_ context.Context, in FollowupInput) (FollowupInput, error) {
		limits++
		if in.Checks != 3 {
			t.Errorf("unexpected automatic check count: %d", in.Checks)
		}
		in.Waiting = true
		in.DecisionID = "owner-decision"
		in.NextAt = time.Time{}
		return in, nil
	}, activity.RegisterOptions{Name: "FollowupLimit"})
	env.RegisterActivityWithOptions(func(context.Context, Terminal) error { return nil }, activity.RegisterOptions{Name: "RecordTerminal"})
	env.RegisterDelayedCallback(func() { env.CancelWorkflow() }, 24*time.Hour)
	env.ExecuteWorkflow(Followup, FollowupInput{RunID: "bounded-followup", NextAt: start.Add(time.Hour)})
	if reviews != 3 || limits != 1 {
		t.Fatalf("automatic followup was unbounded: reviews=%d limits=%d", reviews, limits)
	}
	if !env.IsWorkflowCompleted() || env.GetWorkflowError() == nil {
		t.Fatal("owner cancellation did not end waiting")
	}
}
