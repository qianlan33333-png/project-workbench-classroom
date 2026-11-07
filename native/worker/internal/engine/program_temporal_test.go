package engine

import (
	"context"
	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/types/known/durationpb"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"workbench-native/internal/core"
)

type recoverableModel struct {
	fail  atomic.Bool
	calls atomic.Int32
}

func (m *recoverableModel) Generate(_ context.Context, in ModelRequest) (ModelResult, error) {
	m.calls.Add(1)
	if m.fail.Load() && in.ItemID == "b" {
		return ModelResult{}, temporal.NewNonRetryableApplicationError("fixture rejection", "Invalid", nil)
	}
	return ModelResult{Text: "Summary " + in.ItemID}, nil
}
func TestRealProgramResumeAndSchedule(t *testing.T) {
	address, dsn := os.Getenv("WORKBENCH_TEST_TEMPORAL"), os.Getenv("WORKBENCH_TEST_DSN")
	if address == "" || dsn == "" {
		t.Skip("real Temporal/MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	s, e := core.Open(ctx, dsn)
	must(e)
	defer s.DB.Close()
	must(s.Migrate(ctx))
	namespace := "program-" + uuid.NewString()
	c, e := client.Dial(client.Options{HostPort: address, Namespace: namespace})
	must(e)
	defer c.Close()
	_, e = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: namespace, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	must(e)
	prefix := uuid.NewString()
	p := core.Principal{Kind: "project", ProjectID: prefix, SessionID: prefix + "s"}
	owner := core.Principal{Kind: "owner", SessionID: prefix + "o"}
	must(s.PutProject(ctx, owner, core.Project{ID: prefix, Name: "program acceptance"}))
	source := core.Resource{ID: prefix + "src", Name: "source", OwnerProject: prefix, Sections: []core.Section{{ID: "data", Fields: []string{"name"}}}}
	out := core.Resource{ID: prefix + "out", Name: "output", OwnerProject: prefix, Sections: []core.Section{{ID: "result", Fields: []string{"text"}}}}
	must(s.PutResource(ctx, owner, source))
	must(s.PutResource(ctx, owner, out))
	publish := func(version string) {
		must(s.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: source.ID, Version: version}))
		must(s.Stage(ctx, p, source.ID, version, []core.Record{{ID: "a", Fields: map[string]any{"name": version}}, {ID: "b", Fields: map[string]any{"name": version}}, {ID: "c", Fields: map[string]any{"name": version}}}))
		_, e = s.Publish(ctx, p, source.ID, version, 3)
		must(e)
	}
	publish("v1")
	model := &recoverableModel{}
	model.fail.Store(true)
	a := &Activities{Store: s, Model: model, Temporal: c}
	bw := worker.New(c, BatchQueue, worker.Options{MaxConcurrentActivityExecutionSize: 4})
	for _, fn := range []any{WriteTask, Program, Sync, Batch, ScheduledProgram} {
		bw.RegisterWorkflow(fn)
	}
	bw.RegisterActivity(a)
	must(bw.Start())
	defer bw.Stop()
	iw := worker.New(c, InteractiveQueue, worker.Options{})
	iw.RegisterActivity(a)
	must(iw.Start())
	defer iw.Stop()
	r := core.Run{ID: uuid.NewString(), Principal: p, Operation: "pipeline", Title: "one round", Destination: out.ID, Program: &core.PipelinePlan{Sources: []core.ResourceSelection{{ResourceID: source.ID, Sections: []string{"data"}}}}, Method: core.Method{Name: "summary", Instructions: "summarize", RequiredFields: []string{"name"}}}
	must(s.SaveRun(ctx, r))
	run, e := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: BatchQueue}, WriteTask, WriteInput{RunID: r.ID, ProjectID: p.ProjectID, Operation: r.Operation})
	must(e)
	must(run.Get(ctx, nil))
	stats, e := s.Stats(ctx, p, r.ID)
	must(e)
	if stats.Succeeded != 2 || stats.Failed != 1 {
		t.Fatal(stats)
	}
	steps, e := s.RunSteps(ctx, p, r.ID)
	must(e)
	if len(steps) != 1 {
		t.Fatal(steps)
	}
	generation := steps[0]
	publish("v2")
	model.fail.Store(false)
	run, e = c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE}, WriteTask, WriteInput{RunID: r.ID, ProjectID: p.ProjectID, Operation: r.Operation})
	must(e)
	must(run.Get(ctx, nil))
	stats, e = s.Stats(ctx, p, r.ID)
	must(e)
	if stats.Succeeded != 3 || stats.Failed != 0 || model.calls.Load() != 4 {
		t.Fatal("successful items regenerated", stats, model.calls.Load())
	}
	saved, e := s.Run(ctx, p, generation.ID)
	must(e)
	if saved.Sources[0].Version != "v1" {
		t.Fatal("resume changed pinned input")
	}
	items, e := s.Items(ctx, p, r.ID, "")
	must(e)
	if len(items) != 3 {
		t.Fatal("root results missing")
	}
	h, e := c.ScheduleClient().Create(ctx, client.ScheduleOptions{ID: prefix, Spec: client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: time.Hour}}}, Action: &client.ScheduleWorkflowAction{ID: prefix + "scheduled", Workflow: ScheduledProgram, TaskQueue: BatchQueue, Args: []any{r}}, Paused: true})
	must(e)
	defer h.Delete(context.Background())
	must(h.Trigger(ctx, client.ScheduleTriggerOptions{}))
	for {
		d, err := h.Describe(ctx)
		must(err)
		if len(d.Info.RecentActions) > 0 {
			execution := d.Info.RecentActions[0].StartWorkflowResult
			must(c.GetWorkflow(ctx, execution.WorkflowID, execution.FirstExecutionRunID).Get(ctx, nil))
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if model.calls.Load() != 7 {
		t.Fatal("scheduled occurrence did not generate exactly three new records", model.calls.Load())
	}
	t.Log("Real Temporal: grouped program partial result, same-ID resume only failed item using v1 despite v2 publication, root item read, and paused native Schedule triggered one independent new occurrence")
}
