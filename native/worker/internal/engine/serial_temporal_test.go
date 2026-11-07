package engine

import (
	"context"
	"github.com/google/uuid"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/types/known/durationpb"
	"os"
	"sync/atomic"
	"testing"
	"time"
	"workbench-native/internal/core"
)

func TestRealTemporalWriterCancellation(t *testing.T) {
	address := os.Getenv("WORKBENCH_TEST_TEMPORAL")
	if address == "" {
		t.Skip("real Temporal not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name := "serial-test-" + uuid.NewString()
	c, e := client.Dial(client.Options{HostPort: address, Namespace: name})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	_, e = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: name, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	var active, maximum, started atomic.Int32
	hold := func(ctx context.Context, in TaskInput) error {
		id := in.RunID
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		started.Add(1)
		activity.RecordHeartbeat(ctx)
		if id == "third" {
			return nil
		}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				time.Sleep(300 * time.Millisecond)
				return ctx.Err()
			case <-ticker.C:
				activity.RecordHeartbeat(ctx)
			}
		}
	}
	w := worker.New(c, BatchQueue, worker.Options{MaxConcurrentActivityExecutionSize: 4, MaxHeartbeatThrottleInterval: 100 * time.Millisecond, DefaultHeartbeatThrottleInterval: 50 * time.Millisecond})
	w.RegisterActivityWithOptions(func(context.Context, string, string) (core.WriteFence, error) {
		return core.WriteFence{ProjectID: "one-project", Epoch: 1, Execution: "test"}, nil
	}, activity.RegisterOptions{Name: "BeginWrite"})
	w.RegisterWorkflow(WriteTask)
	w.RegisterWorkflow(Sync)
	w.RegisterWorkflow(Batch)
	w.RegisterActivityWithOptions(hold, activity.RegisterOptions{Name: "SyncResource"})
	var generationActive, generationStarts atomic.Int32
	w.RegisterActivityWithOptions(func(context.Context, PageInput) (PreparedPage, error) {
		return PreparedPage{IDs: []string{"fail", "two", "three", "four"}}, nil
	}, activity.RegisterOptions{Name: "PreparePage"})
	w.RegisterActivityWithOptions(func(ctx context.Context, in ItemInput) error {
		generationActive.Add(1)
		defer generationActive.Add(-1)
		generationStarts.Add(1)
		for generationStarts.Load() < 4 {
			activity.RecordHeartbeat(ctx)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
		if in.ID == "fail" {
			return temporal.NewNonRetryableApplicationError("fixture failure", "Invalid", nil)
		}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				time.Sleep(300 * time.Millisecond)
				return ctx.Err()
			case <-ticker.C:
				activity.RecordHeartbeat(ctx)
			}
		}
	}, activity.RegisterOptions{Name: "GenerateItem"})
	w.RegisterActivityWithOptions(func(context.Context, FailedItem) error {
		return temporal.NewNonRetryableApplicationError("receipt write failed", "Invalid", nil)
	}, activity.RegisterOptions{Name: "RecordFailure"})
	iw := worker.New(c, InteractiveQueue, worker.Options{})
	iw.RegisterActivityWithOptions(func(context.Context, string, string) error { return nil }, activity.RegisterOptions{Name: "EndWrite"})
	iw.RegisterActivityWithOptions(func(context.Context, string, string) (core.WriteFence, error) {
		return core.WriteFence{ProjectID: "one-project", Epoch: 1, Execution: "test"}, nil
	}, activity.RegisterOptions{Name: "BeginWrite"})
	iw.RegisterActivityWithOptions(func(context.Context, Terminal) error { return nil }, activity.RegisterOptions{Name: "RecordTerminal"})
	if e = w.Start(); e != nil {
		t.Fatal(e)
	}
	defer w.Stop()
	if e = iw.Start(); e != nil {
		t.Fatal(e)
	}
	defer iw.Stop()
	start := func(id string, op string) client.WorkflowRun {
		t.Helper()
		r, e := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: BatchQueue}, WriteTask, WriteInput{RunID: id, ProjectID: "one-project", Operation: op})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	first := start("first", "sync")
	for started.Load() < 1 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	second := start("second", "sync")
	time.Sleep(400 * time.Millisecond)
	if e = c.CancelWorkflow(ctx, second.GetID(), ""); e != nil {
		t.Fatal(e)
	}
	wait, stop := context.WithTimeout(ctx, 5*time.Second)
	e = second.Get(wait, nil)
	stop()
	if !temporal.IsCanceledError(e) {
		t.Fatal("queued cancel did not finish independently", e)
	}
	if active.Load() != 1 {
		t.Fatal("queued cancellation disturbed active writer", active.Load())
	}
	third := start("third", "sync")
	if e = c.CancelWorkflow(ctx, first.GetID(), ""); e != nil {
		t.Fatal(e)
	}
	if e = first.Get(ctx, nil); !temporal.IsCanceledError(e) {
		t.Fatal("active cancel", e)
	}
	if e = third.Get(ctx, nil); e != nil {
		t.Fatal("next writer", e)
	}
	if maximum.Load() != 1 || active.Load() != 0 || started.Load() != 2 {
		t.Fatal("writers overlapped or canceled job executed", maximum.Load(), active.Load(), started.Load())
	}
	broken := start("broken-batch", "generate")
	if e = broken.Get(ctx, nil); e == nil {
		t.Fatal("expected failure")
	}
	if generationActive.Load() != 0 {
		t.Fatal("workflow ended before live activities exited", generationActive.Load())
	}
	t.Log("Real Temporal: queued cancellation independent; active cancellation drained; next project writer did not overlap; RecordFailure failure drained all four model activities")
}
