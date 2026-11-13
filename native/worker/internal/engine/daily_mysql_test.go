package engine

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.temporal.io/sdk/testsuite"
	"workbench-native/internal/core"
)

func TestDailySummaryFreezesOtherRunsOnce(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	s, err := core.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	p := core.Principal{Kind: "personal", SessionID: id}
	previous := core.Run{ID: id + "-previous", Principal: p, Operation: "daily", Title: "上一轮安排", WorkflowID: "personal-daily-original"}
	if err = s.SaveRun(ctx, previous); err != nil {
		t.Fatal(err)
	}
	model := &evidenceModel{}
	a := &Activities{Store: s, Model: model}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a)
	for range 2 {
		if _, err = env.ExecuteActivity(a.DailySummary, id, p, "personal-daily-current"); err != nil {
			t.Fatal(err)
		}
	}
	if len(model.Inputs) != 1 {
		t.Fatalf("retried summary ran the model %d times", len(model.Inputs))
	}
	entries, ok := model.Inputs[0]["recentRuns"].([]any)
	if !ok {
		t.Fatal("frozen input lacks recent runs")
	}
	hasID := func(id string) bool {
		return slices.ContainsFunc(entries, func(row any) bool {
			return row.(map[string]any)["id"] == id
		})
	}
	if hasID(id) || !hasID(previous.ID) {
		t.Fatal("summary must retain previous executions and exclude its own run")
	}
}
