package engine

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/types/known/durationpb"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"workbench-native/internal/core"
)

type testModel struct {
	Calls atomic.Int32
	Slow  atomic.Bool
}

func (m *testModel) Generate(ctx context.Context, in ModelRequest) (ModelResult, error) {
	m.Calls.Add(1)
	if m.Slow.Load() {
		select {
		case <-ctx.Done():
			return ModelResult{}, ctx.Err()
		case <-time.After(120 * time.Millisecond):
		}
	}
	return ModelResult{Text: "Generated " + in.ItemID}, nil
}

// Uses the real Temporal service and real isolated MySQL, not the SDK time-skipping mock.
func TestRealTemporalWorkerRecovery(t *testing.T) {
	address, dsn := os.Getenv("WORKBENCH_TEST_TEMPORAL"), os.Getenv("WORKBENCH_TEST_DSN")
	if address == "" || dsn == "" {
		t.Skip("real Temporal and MySQL acceptance settings not supplied")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	count := 50
	if raw := os.Getenv("WORKBENCH_TEST_ITEMS"); raw != "" {
		var parseErr error
		count, parseErr = strconv.Atoi(raw)
		if parseErr != nil || count < 50 || count > 10000 {
			t.Fatal("item count must be 50..10000")
		}
	}
	defer cancel()
	store, e := core.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer store.DB.Close()
	if e = store.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	name := "test-" + uuid.NewString()
	c, e := client.Dial(client.Options{HostPort: address, Namespace: name})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, e = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: name, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)}); e != nil {
		t.Fatal(e)
	}
	prefix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	p := core.Principal{Kind: "project", ProjectID: prefix, SessionID: prefix + "s"}
	owner := core.Principal{Kind: "owner", SessionID: prefix + "o"}
	if e = store.PutProject(ctx, owner, core.Project{ID: p.ProjectID, Name: "Recovery fixture"}); e != nil {
		t.Fatal(e)
	}
	source := core.Resource{ID: prefix + "source", Name: "Input", OwnerProject: p.ProjectID, Sections: []core.Section{{ID: "data", Fields: []string{"name"}}}}
	output := core.Resource{ID: prefix + "output", Name: "Result", OwnerProject: p.ProjectID, Sections: []core.Section{{ID: "text", Fields: []string{"text"}}}}
	for _, r := range []core.Resource{source, output} {
		if e = store.PutResource(ctx, owner, r); e != nil {
			t.Fatal(e)
		}
	}
	if e = store.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: source.ID, Version: "v1"}); e != nil {
		t.Fatal(e)
	}
	rows := []core.Record{}
	for i := 0; i < count; i++ {
		rows = append(rows, core.Record{ID: fmt.Sprintf("%06d", i), Fields: map[string]any{"name": fmt.Sprintf("Fixture %d", i)}})
	}
	for start := 0; start < len(rows); start += 200 {
		end := min(start+200, len(rows))
		if e = store.Stage(ctx, p, source.ID, "v1", rows[start:end]); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = store.Publish(ctx, p, source.ID, "v1", count); e != nil {
		t.Fatal(e)
	}
	run := core.Run{ID: uuid.NewString(), Principal: p, Operation: "generate", Title: "Recovery acceptance", Sources: []core.Ref{{ResourceID: source.ID, Version: "v1", Section: "data"}}, Destination: output.ID, Method: core.Method{Name: "fixture", Instructions: "Generate fixture", RequiredFields: []string{"name"}}}
	if e = store.SaveRun(ctx, run); e != nil {
		t.Fatal(e)
	}
	model := &testModel{}
	model.Slow.Store(true)
	activities := &Activities{Store: store, Model: model}
	iw := worker.New(c, InteractiveQueue, worker.Options{})
	iw.RegisterActivity(activities)
	if e = iw.Start(); e != nil {
		t.Fatal(e)
	}
	defer iw.Stop()
	newWorker := func() worker.Worker {
		w := worker.New(c, BatchQueue, worker.Options{MaxConcurrentActivityExecutionSize: 4, WorkerStopTimeout: time.Second})
		w.RegisterWorkflow(Batch)
		w.RegisterActivity(activities)
		if e := w.Start(); e != nil {
			t.Fatal(e)
		}
		return w
	}
	w := newWorker()
	execution, e := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: run.ID, TaskQueue: BatchQueue}, Batch, BatchInput{RunID: run.ID})
	if e != nil {
		w.Stop()
		t.Fatal(e)
	}
	for model.Calls.Load() < 5 {
		select {
		case <-ctx.Done():
			w.Stop()
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	w.Stop()
	before, e := store.Stats(ctx, p, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	model.Slow.Store(false)
	w = newWorker()
	defer w.Stop()
	var result BatchOutput
	if e = execution.Get(ctx, &result); e != nil {
		t.Fatal(e)
	}
	stats, e := store.Stats(ctx, p, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if stats.Succeeded != count || stats.Failed != 0 || stats.Pending != 0 {
		t.Fatalf("wrong final counts: %+v", stats)
	}
	snap, e := store.Snapshot(ctx, output.ID, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	if snap.State != "published" || snap.Count != count {
		t.Fatalf("not published: %+v", snap)
	}
	t.Logf("Real Temporal recovery: %d succeeded before worker stop, %d after restart; %d model attempts; published=%s", before.Succeeded, stats.Succeeded, model.Calls.Load(), snap.State)
}
