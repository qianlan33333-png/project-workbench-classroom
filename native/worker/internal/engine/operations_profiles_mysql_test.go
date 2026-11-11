package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/types/known/durationpb"
	"workbench-native/internal/core"
)

type profileTestModel struct{ calls atomic.Int32 }

func (m *profileTestModel) Generate(context.Context, ModelRequest) (ModelResult, error) {
	if m.calls.Add(1) == 1 {
		return ModelResult{Text: `{"current_need":{"value":"希望完善获客流程","ref":"crm:one"}}`}, nil
	}
	return ModelResult{}, errors.New("fixture model unavailable")
}

func TestRealScheduledOperationsProfileRefresh(t *testing.T) {
	dsn, address := os.Getenv("WORKBENCH_TEST_DSN"), os.Getenv("WORKBENCH_TEST_TEMPORAL")
	if dsn == "" || address == "" {
		t.Skip("isolated MySQL and Temporal required")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	module, err := filepath.Abs("../../../components/operations-source/directory.py")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKBENCH_OPERATIONS_DIRECTORY_MODULE", module)
	ctx, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := core.Open(ctx, dsn)
	must(err)
	defer s.DB.Close()
	must(s.Migrate(ctx))
	id := uuid.NewString()
	c, err := client.Dial(client.Options{HostPort: address, Namespace: id})
	must(err)
	defer c.Close()
	_, err = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: id, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	must(err)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = c.OperatorService().DeleteNamespace(cleanup, &operatorservice.DeleteNamespaceRequest{Namespace: id})
	}()
	project := core.Principal{Kind: "project", ProjectID: id, SessionID: id + "-project"}
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "isolated profiles"}))
	source, dest := id+"-source", id+"-directory"
	for key, fields := range map[string][]string{source: {"raw"}, dest: {"groups", "nickname", "position", "search"}} {
		must(s.PutResource(ctx, owner, core.Resource{ID: key, OwnerProject: id, Name: key, Sections: []core.Section{{ID: "main", Fields: fields}}}))
	}
	makeInput := func(version, evidenceRef, product string) core.Ref {
		t.Helper()
		must(s.SaveRun(ctx, core.Run{ID: version, Principal: project, Operation: "data_refresh", Title: "fixture input"}))
		must(s.BeginSnapshot(ctx, project, core.Snapshot{ResourceID: source, Version: version}))
		must(s.Stage(ctx, project, source, version, []core.Record{{ID: "usr_fixture", Fields: map[string]any{"raw": map[string]any{
			"_localProfileInput": map[string]any{"position": 0, "search": []string{"客户"}, "snapshot": version,
				"user":    map[string]any{"safe_user_ref": "usr_fixture", "企微昵称": "客户", "active_product_keys": []string{product}},
				"listing": map[string]any{"企微昵称": "客户"}, "relationship": nil, "assessment": nil},
			"_localProfileEvidence": []any{map[string]any{"ref": evidenceRef, "at": "2026-11-11T00:00:00Z", "text": "想改善获客"}},
		}}}}))
		_, err := s.Publish(ctx, project, source, version, 1)
		must(err)
		return core.Ref{ResourceID: source, Version: version, Section: "main"}
	}
	first := makeInput(id+"-data1", "crm:one", "第一轮产品")
	model := &profileTestModel{}
	a := &Activities{Store: s, Model: model, Temporal: c}
	bw := worker.New(c, BatchQueue, worker.Options{})
	bw.RegisterWorkflow(WriteTask)
	bw.RegisterWorkflow(OperationsProfiles)
	bw.RegisterActivity(a)
	must(bw.Start())
	defer bw.Stop()
	iw := worker.New(c, InteractiveQueue, worker.Options{})
	iw.RegisterActivity(a)
	must(iw.Start())
	defer iw.Stop()

	run1 := id + "-profile-run-1"
	method := core.Method{Name: "客户档案更新", Instructions: customerProfileInstructions, RequiredFields: []string{"evidence"}, MaxInputBytes: 24000}
	method.Digest = core.Digest(method)
	run := func(runID string, input core.Ref) core.Run {
		r := core.Run{ID: runID, Principal: project, Operation: OperationsProfileRefresh, Title: "档案", Sources: []core.Ref{input}, Method: method, Destination: dest}
		must(s.SaveRun(ctx, r))
		return r
	}
	run(run1, first)
	handle, err := c.ScheduleClient().Create(ctx, client.ScheduleOptions{ID: "profile-fixture", Spec: client.ScheduleSpec{
		Intervals: []client.ScheduleIntervalSpec{{Every: 3 * time.Second}}},
		Action: &client.ScheduleWorkflowAction{ID: "profile-natural", Workflow: WriteTask, TaskQueue: BatchQueue,
			Args: []any{WriteInput{RunID: run1, ProjectID: id, Operation: OperationsProfileRefresh}}},
		Overlap: enums.SCHEDULE_OVERLAP_POLICY_SKIP, RemainingActions: 1})
	must(err)
	defer handle.Delete(context.Background())
	for {
		r, err := s.Resource(ctx, dest)
		must(err)
		if r.CurrentVersion == run1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("natural schedule did not publish", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	start := func(r core.Run) error {
		running, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: BatchQueue},
			WriteTask, WriteInput{RunID: r.ID, ProjectID: id, Operation: r.Operation})
		must(err)
		return running.Get(ctx, nil)
	}
	must(start(run(id+"-profile-reuse", makeInput(id+"-data-reuse", "crm:one", "同证据更新产品"))))
	if model.calls.Load() != 1 {
		t.Fatalf("unchanged evidence called the model again: %d", model.calls.Load())
	}
	must(start(run(id+"-profile-run-2", makeInput(id+"-data2", "crm:two", "第二轮产品"))))
	latest := id + "-profile-run-3"
	must(start(run(latest, makeInput(id+"-data3", "crm:two", "第三轮产品"))))
	if err := start(run(id+"-profile-old-retry", first)); err == nil {
		t.Fatal("older source overwrote the current archive")
	}
	current, err := s.Resource(ctx, dest)
	must(err)
	if current.CurrentVersion != latest {
		t.Fatalf("current archive is %s, expected %s", current.CurrentVersion, latest)
	}
	rows, err := s.ReadIDs(ctx, project, core.Ref{ResourceID: dest, Version: latest, Section: "main"}, []string{"usr_fixture"})
	must(err)
	if len(rows) != 1 {
		t.Fatalf("got %d profiles", len(rows))
	}
	fields := rows[0].Fields
	var fact, semantic bool
	for _, value := range fields["groups"].([]any) {
		for _, value := range value.(map[string]any)["fields"].([]any) {
			field := value.(map[string]any)
			fact = fact || field["label"] == "当前产品" && field["value"] == "第三轮产品"
			semantic = semantic || field["label"] == "当前需求" && field["value"] == "希望完善获客流程" && field["sourceRef"] == "crm:one"
		}
	}
	if !fact || !semantic || model.calls.Load() != 3 {
		t.Fatalf("daily facts=%t, prior dated expert evidence=%t, model calls=%d", fact, semantic, model.calls.Load())
	}
}
