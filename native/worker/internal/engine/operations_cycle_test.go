package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"workbench-native/internal/core"

	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
)

func TestOperationsCycleSharesRefreshAcrossSeparateBatches(t *testing.T) {
	const id = "operations-test-cycle"
	const stamp = "2026-11-11T01:00:00Z"
	p := core.Principal{Kind: "project", ProjectID: "ops", SessionID: "operations-ops"}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: id})
	register := func(name string, fn any) {
		env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
	runID := func(key string) string {
		return uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/"+key)).String()
	}
	register("OperationsCreateCycle", func(context.Context, string, OperationsCycleInput) (string, error) { return stamp, nil })
	register("OperationsRefreshClock", func(_ context.Context, _ core.Principal, clean string) (string, error) {
		if clean != runID(id+"-data") {
			return "", fmt.Errorf("unexpected clean run: %s", clean)
		}
		return stamp, nil
	})
	var operations []string
	register("OperationsInvoke", func(_ context.Context, call OperationCall) (json.RawMessage, error) {
		if call.Name == "run_get" {
			return json.Marshal(OperationReply{Execution: "Completed"})
		}
		key := call.Arguments["operationKey"].(string)
		operations = append(operations, key)
		if call.Name == "resource_sync" && call.Arguments["destination"] != "profiles" {
			return nil, fmt.Errorf("profile refresh pointed to the wrong resource: %v", call.Arguments)
		}
		if call.Name == "strategy_select" {
			data := call.Arguments["data"].(map[string]any)
			if data["version"] != runID(id+"-data") || call.Arguments["asOf"] != stamp {
				return nil, fmt.Errorf("selection did not share the frozen data and clock: %v", call.Arguments)
			}
			strategy, dest := "hxc:ongoing", "selected"
			if key == id+"-additional-select" {
				strategy, dest = "hxc:onboarding", "newcomer-selected"
			}
			if !reflect.DeepEqual(call.Arguments["strategyKeys"], []any{strategy}) || call.Arguments["destination"] != dest {
				return nil, fmt.Errorf("selection crossed strategies: %v", call.Arguments)
			}
		}
		return json.Marshal(OperationReply{RunID: runID(key)})
	})
	register("OperationsSelectedCount", func(context.Context, core.Principal, core.Ref) (int, error) { return 1, nil })
	register("OperationsGenerationPage", func(context.Context, core.Principal, core.Ref, string) (core.SelectionInfo, error) {
		return core.SelectionInfo{SelectedCount: 1}, nil
	})
	register("OperationsCycleBatchesSubmitted", func(_ context.Context, _ string, batches []string) error {
		if !reflect.DeepEqual(batches, []string{runID(id + "-crm"), runID(id + "-additional-crm")}) {
			return fmt.Errorf("missing prepared batch references: %v", batches)
		}
		return nil
	})
	env.ExecuteWorkflow(OperationsCycle, OperationsCycleInput{Principal: p, Section: "main", CleanDestination: "clean", ProfileDirectory: "profiles",
		StrategyKeys: []string{"hxc:ongoing"}, SelectedDestination: "selected", DraftDestination: "drafts",
		AdditionalBatch: &OperationsCycleBatch{"hxc:onboarding", "newcomer-selected", "newcomer-drafts"}})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var expected []string
	for _, suffix := range []string{"-data", "-select", "-generate-0", "-crm", "-additional-select", "-additional-generate-0", "-additional-crm", "-profile-directory"} {
		expected = append(expected, id+suffix)
	}
	if !reflect.DeepEqual(operations, expected) {
		t.Fatalf("unexpected workflow calls: %v", operations)
	}
}
