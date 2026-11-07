package engine

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/types/known/durationpb"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"workbench-native/internal/core"
)

func partitionProbe(ctx workflow.Context, id string) (err error) {
	execution := workflow.GetInfo(ctx).WorkflowExecution.RunID
	defer finishWrite(ctx, id, execution, &err)()
	var fence core.WriteFence
	if err = workflow.ExecuteActivity(activityContext(ctx, InteractiveQueue), "BeginWrite", id, execution).Get(ctx, &fence); err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{TaskQueue: "partitioned-activity", StartToCloseTimeout: 60 * time.Second, HeartbeatTimeout: 2 * time.Second, WaitForCancellation: true, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}})
	return workflow.ExecuteActivity(ctx, "PartitionedWrite", TaskInput{id, fence}).Get(ctx, nil)
}

// This test drops only the old worker's real Temporal TCP connections. Its
// MySQL stays reachable. An injected third-party callback ignores cancellation
// to verify that the database fence independently rejects late completion.
func TestRealTemporalPartitionRejectsLateCommit(t *testing.T) {
	address, dsn := os.Getenv("WORKBENCH_TEST_TEMPORAL"), os.Getenv("WORKBENCH_TEST_DSN")
	if address == "" || dsn == "" {
		t.Skip("real isolated Temporal/MySQL settings absent")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	store, e := core.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer store.DB.Close()
	if e = store.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	namespace := "partition-test-" + uuid.NewString()
	c, e := client.Dial(client.Options{HostPort: address, Namespace: namespace})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	_, e = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: namespace, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	prefix := "t" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	p := core.Principal{Kind: "project", ProjectID: prefix, SessionID: prefix + "s"}
	owner := core.Principal{Kind: "owner", SessionID: prefix + "o"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(store.PutProject(ctx, owner, core.Project{ID: prefix, Name: "Partition fixture"}))
	must(store.PutResource(ctx, owner, core.Resource{ID: prefix, Name: "Publication", OwnerProject: prefix, Sections: []core.Section{{ID: "data", Fields: []string{"value"}}}}))
	must(store.SaveRun(ctx, core.Run{ID: prefix + "old", Principal: p, Title: "Partition", Operation: "sync", Destination: prefix}))
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	must(e)
	defer listener.Close()
	var cut atomic.Bool
	var mu sync.Mutex
	var connections []net.Conn
	go func() {
		for {
			in, e := listener.Accept()
			if e != nil {
				return
			}
			if cut.Load() {
				in.Close()
				continue
			}
			out, e := net.Dial("tcp", address)
			if e != nil {
				in.Close()
				continue
			}
			mu.Lock()
			connections = append(connections, in, out)
			mu.Unlock()
			go func() { defer in.Close(); defer out.Close(); io.Copy(in, out) }()
			go func() { defer in.Close(); defer out.Close(); io.Copy(out, in) }()
		}
	}()
	isolated, e := client.Dial(client.Options{HostPort: listener.Addr().String(), Namespace: namespace})
	must(e)
	defer isolated.Close()
	a := &Activities{Store: store, Temporal: c}
	healthy := worker.New(c, InteractiveQueue, worker.Options{})
	healthy.RegisterWorkflow(partitionProbe)
	healthy.RegisterActivity(a)
	must(healthy.Start())
	defer healthy.Stop()
	ready := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var staleLive atomic.Bool
	var commitErr error
	old := worker.New(isolated, "partitioned-activity", worker.Options{DisableWorkflowWorker: true, WorkerStopTimeout: time.Second, MaxHeartbeatThrottleInterval: 100 * time.Millisecond, DefaultHeartbeatThrottleInterval: 50 * time.Millisecond})
	old.RegisterActivityWithOptions(func(actx context.Context, in TaskInput) error {
		actx = core.WithFence(actx, in.Fence)
		if e := store.BeginSnapshot(actx, p, core.Snapshot{ResourceID: prefix, Version: "old"}); e != nil {
			return e
		}
		if e := store.Stage(actx, p, prefix, "old", []core.Record{{ID: "1", Fields: map[string]any{"value": "old"}}}); e != nil {
			return e
		}
		close(ready)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-release:
				staleLive.Store(actx.Err() == nil)
				writeCtx, stop := context.WithTimeout(context.WithoutCancel(actx), 5*time.Second)
				_, commitErr = store.Publish(writeCtx, p, prefix, "old", 1)
				stop()
				close(done)
				return commitErr
			case <-ticker.C:
				activity.RecordHeartbeat(actx)
			}
		}
	}, activity.RegisterOptions{Name: "PartitionedWrite"})
	must(old.Start())
	defer old.Stop()
	run, e := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: prefix + "old", TaskQueue: InteractiveQueue}, partitionProbe, prefix+"old")
	must(e)
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cut.Store(true)
	mu.Lock()
	for _, conn := range connections {
		conn.Close()
	}
	mu.Unlock()
	must(c.CancelWorkflow(ctx, run.GetID(), ""))
	if e = run.Get(ctx, nil); e == nil {
		t.Fatal("expected canceled/timeout execution")
	}
	next, e := store.BeginWrite(ctx, prefix, prefix+"next")
	must(e)
	fresh := core.WithFence(ctx, next)
	must(store.BeginSnapshot(fresh, p, core.Snapshot{ResourceID: prefix, Version: "new"}))
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	if !errors.Is(commitErr, core.ErrDenied) {
		t.Fatal("late old publish was not fenced", commitErr)
	}
	must(store.Stage(fresh, p, prefix, "new", []core.Record{{ID: "1", Fields: map[string]any{"value": "new"}}}))
	_, e = store.Publish(fresh, p, prefix, "new", 1)
	must(e)
	t.Logf("Real TCP partition: original Activity context live=%v; injected uncancellable late MySQL callback denied; new version published", staleLive.Load())
}
