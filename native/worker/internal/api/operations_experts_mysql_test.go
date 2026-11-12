package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type trialHost struct {
	engine.AgentHost
	calls int
}

func (h *trialHost) Generate(_ context.Context, input engine.ModelRequest) (engine.ModelResult, error) {
	h.calls++
	return engine.ModelResult{Text: input.Method.Instructions}, nil
}

func TestMySQLOperationsExpertTrialAndPinnedCycle(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL settings not supplied")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	store, err := core.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Migrate(ctx))
	project := uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: project + "-owner"}
	p := operationsPrincipal(project)
	must(store.PutProject(ctx, owner, core.Project{ID: project, Name: "Expert trial fixture"}))
	ref := core.Ref{ResourceID: project + "-source-method", Section: "method", Version: "original"}
	must(store.PutResource(ctx, owner, core.Resource{ID: ref.ResourceID, Name: "Method fixture", OwnerProject: project,
		Sections: []core.Section{{ID: ref.Section, Name: "Methods", Fields: []string{"method"}}}}))
	const skill = "fixture"
	must(store.BeginSnapshot(ctx, owner, core.Snapshot{ResourceID: ref.ResourceID, Version: ref.Version}))
	method := core.Method{Name: "Fixture", Instructions: "Original", RequiredFields: []string{"profile"}, FixedOutputPointer: "/fixedOutput"}
	must(store.Stage(ctx, owner, ref.ResourceID, ref.Version, []core.Record{{ID: skill, Fields: map[string]any{"method": method}}}))
	_, err = store.Publish(ctx, owner, ref.ResourceID, ref.Version, 1)
	must(err)
	host := &trialHost{}
	api := Server{Store: store, Host: host, Token: strings.Repeat("e", 32)}
	cycle := engine.OperationsCycleInput{Principal: p, Section: "text", StrategyKeys: []string{"onboarding"},
		AdditionalBatch: &engine.OperationsCycleBatch{Strategy: "ongoing"}, MethodSource: &core.ResourceSelection{
			ResourceID: ref.ResourceID, Sections: []string{ref.Section}, Refresh: true},
		MethodRefs: map[string]core.RecordRef{skill: {ID: skill, Ref: ref}}}
	must(api.saveOperations(ctx, operationsConfig{ProjectID: project, Cycle: &cycle}))
	post := func(input operationsExpertInput, want int) []byte {
		t.Helper()
		raw, err := json.Marshal(input)
		must(err)
		req := httptest.NewRequest(http.MethodPost, "/operations/experts/save", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+api.Token)
		req.Header.Set("X-Workbench-Kind", owner.Kind)
		req.Header.Set("X-Workbench-Session", owner.SessionID)
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("action %s status %d want %d: %s", input.Action, w.Code, want, w.Body)
		}
		return w.Body.Bytes()
	}
	var copied operationsExpert
	content := "Edited method"
	must(json.Unmarshal(post(operationsExpertInput{ID: "operations:" + project + ":original:" + skill, Action: "copy",
		Revision: ref.Version, Content: &content}, http.StatusOK), &copied))
	op := operationsExpertInput{ID: copied.ID, Revision: copied.Revision, Strategy: "onboarding"}
	op.Action = "trial"
	op.Sample = map[string]any{"profile": "synthetic", "fixedOutput": "fixed"}
	post(op, http.StatusOK)
	if host.calls != 0 {
		t.Fatal("fixed rendering invoked the model")
	}
	op.Action = "enable"
	post(op, http.StatusConflict)
	op.Action = "trial"
	op.Sample = map[string]any{"profile": "synthetic"}
	post(op, http.StatusOK)
	if host.calls != 1 {
		t.Fatal("model-backed trial did not run")
	}
	op.Action = "enable"
	post(op, http.StatusOK)
	activities := &engine.Activities{Store: store}
	id := uuid.NewString()
	first, err := activities.OperationsCreateCycle(ctx, id, cycle)
	must(err)
	if !strings.Contains(first, copied.Revision) || strings.Contains(first, "ongoing") {
		t.Fatal("cycle did not pin only its configured strategy", first)
	}
	after, err := api.readOperations(ctx, project)
	must(err)
	after.ExpertOverrides = nil
	must(api.saveOperations(ctx, after))
	resumed, err := activities.OperationsCreateCycle(ctx, id, cycle)
	must(err)
	if first != resumed {
		t.Fatal("resuming replaced the original pinned methods")
	}
	next, err := activities.OperationsCreateCycle(ctx, uuid.NewString(), cycle)
	must(err)
	if strings.HasPrefix(next, "{") {
		t.Fatal("a new cycle unexpectedly used the disabled copy", next)
	}
}
