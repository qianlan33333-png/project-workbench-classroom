package engine

import (
	"context"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestUnavailableConnectorUsesDurableTimersBeyondRetryBudget(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	calls := 0
	env.RegisterActivityWithOptions(func(context.Context, string) (Receipt, error) {
		calls++
		if calls <= 40 {
			return Receipt{State: "prepared"}, nil
		}
		return Receipt{State: "accepted"}, nil
	}, activity.RegisterOptions{Name: "SendDelivery"})
	started := env.Now()
	env.ExecuteWorkflow(SubmitMessage, "same-delivery")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if calls != 41 || env.Now().Sub(started) < 20*time.Minute {
		t.Fatalf("wait did not persist: %d / %v", calls, env.Now().Sub(started))
	}
}

func TestUnknownSendDoesNotEnterTheAvailabilityRetryLoop(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	calls := 0
	env.RegisterActivityWithOptions(func(context.Context, string) (Receipt, error) { calls++; return Receipt{State: "unknown"}, nil }, activity.RegisterOptions{Name: "SendDelivery"})
	env.ExecuteWorkflow(SubmitMessage, "same-delivery")
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("unknown send retried: %d", calls)
	}
}
