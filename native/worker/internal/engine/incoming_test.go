package engine

import (
	"context"
	"fmt"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"testing"
	"time"
)

func TestIncomingWaitsForOriginalTurnAndDefersBusyHost(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	calls, checks, handoffs := 0, 0, 0
	env.RegisterActivityWithOptions(func(context.Context, string) (bool, error) { checks++; return calls >= 5, nil }, activity.RegisterOptions{Name: "IncomingFinished"})
	env.RegisterActivityWithOptions(func(context.Context, string) error {
		calls++
		if calls <= 4 {
			return fmt.Errorf("Host is restarting or earlier turn pending")
		}
		return nil
	}, activity.RegisterOptions{Name: "DispatchProject"})
	env.RegisterActivityWithOptions(func(context.Context, string) error { handoffs++; return nil }, activity.RegisterOptions{Name: "HandoffIncoming"})
	env.ExecuteWorkflow(Incoming, "same-input")
	if e := env.GetWorkflowError(); e != nil {
		t.Fatal(e)
	}
	if calls != 5 || handoffs != 0 || checks != 2 {
		t.Fatalf("calls=%d checks=%d handoffs=%d", calls, checks, handoffs)
	}
}
func TestIncomingWithoutDispositionStopsAndHandsOff(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	handoffs := 0
	env.RegisterActivityWithOptions(func(context.Context, string) (bool, error) { return false, nil }, activity.RegisterOptions{Name: "IncomingFinished"})
	env.RegisterActivityWithOptions(func(context.Context, string) error { return nil }, activity.RegisterOptions{Name: "DispatchProject"})
	env.RegisterActivityWithOptions(func(context.Context, string) error { handoffs++; return nil }, activity.RegisterOptions{Name: "HandoffIncoming"})
	start := env.Now()
	env.ExecuteWorkflow(Incoming, "same-input")
	if e := env.GetWorkflowError(); e != nil {
		t.Fatal(e)
	}
	if handoffs != 1 || env.Now().Sub(start) < 5*time.Minute {
		t.Fatal("silent or premature finish", handoffs, env.Now().Sub(start))
	}
}

func TestIncomingCancellationSettlesTheOriginalInput(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	handoffs := 0
	env.RegisterActivityWithOptions(func(context.Context, string) (bool, error) { return false, nil }, activity.RegisterOptions{Name: "IncomingFinished"})
	env.RegisterActivityWithOptions(func(context.Context, string) error { return nil }, activity.RegisterOptions{Name: "DispatchProject"})
	env.RegisterActivityWithOptions(func(context.Context, string) error { handoffs++; return nil }, activity.RegisterOptions{Name: "HandoffIncoming"})
	env.RegisterActivityWithOptions(func(context.Context, Terminal) error { return nil }, activity.RegisterOptions{Name: "RecordTerminal"})
	env.RegisterDelayedCallback(func() { env.CancelWorkflow() }, time.Second)
	env.ExecuteWorkflow(Incoming, "same-input")
	if env.GetWorkflowError() == nil || handoffs != 1 {
		t.Fatal("cancel did not settle input", handoffs, env.GetWorkflowError())
	}
}
