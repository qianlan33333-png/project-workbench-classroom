package engine

import (
	"context"
	"errors"
	"sync"
	"testing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

func TestOriginalOperationsPackageKeepsGeneratingAfterOneItemFails(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(context.Context, string) (originalPackage, error) {
		return originalPackage{State: "ready", Run: "original", Total: 5}, nil
	}, activity.RegisterOptions{Name: "PrepareOriginalOperationsPackage"})
	var mu sync.Mutex
	attempted := map[int]bool{}
	env.RegisterActivityWithOptions(func(_ context.Context, id, run string, offset int) error {
		if id != "workflow" || run != "original" {
			return errors.New("generation lost its original binding")
		}
		mu.Lock()
		attempted[offset] = true
		mu.Unlock()
		if offset == 0 {
			return errors.New("source gate changed for one recipient")
		}
		return nil
	}, activity.RegisterOptions{Name: "GenerateOriginalOperationsItem"})
	env.RegisterActivityWithOptions(func(context.Context, Terminal) error { return nil }, activity.RegisterOptions{Name: "RecordTerminal"})
	env.ExecuteWorkflow(OperationsPackage, "workflow")
	if env.GetWorkflowError() == nil || len(attempted) != 5 {
		t.Fatalf("expected partial failure after all five offsets, got %v and %v", env.GetWorkflowError(), attempted)
	}
}
