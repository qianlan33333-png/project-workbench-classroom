package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type mysqlSyncFixture struct {
	ctx      context.Context
	store    *core.Store
	temporal client.Client
	owner    core.Principal
	personal core.Principal
	resource core.Resource
}

func newMySQLSyncFixture(t *testing.T, dialOptions ...grpc.DialOption) mysqlSyncFixture {
	t.Helper()
	dsn, address := os.Getenv("WORKBENCH_TEST_DSN"), os.Getenv("WORKBENCH_TEST_TEMPORAL")
	if dsn == "" || address == "" {
		t.Skip("isolated MySQL and Temporal required")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	s, err := core.Open(ctx, dsn)
	requireSyncTest(t, err)
	t.Cleanup(func() { s.DB.Close() })
	requireSyncTest(t, s.Migrate(ctx))
	id := "p_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	namespace := "mysql-sync-" + id
	c, err := client.Dial(client.Options{HostPort: address, Namespace: namespace, ConnectionOptions: client.ConnectionOptions{DialOptions: dialOptions}})
	requireSyncTest(t, err)
	t.Cleanup(c.Close)
	_, err = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{
		Namespace: namespace, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour),
	})
	requireSyncTest(t, err)
	f := mysqlSyncFixture{ctx: ctx, store: s, temporal: c, owner: core.Principal{Kind: "owner", SessionID: id + "o"},
		personal: core.Principal{Kind: "personal", SessionID: id + "s"},
		resource: core.Resource{ID: id, Name: "Source", ConnectionID: id + "c", Relation: id, KeyField: "id",
			Sections: []core.Section{{ID: "text", Fields: []string{"value"}}}}}
	t.Setenv("WORKBENCH_SOURCE_SYNC_REVIEW", dsn)
	requireSyncTest(t, s.PutConnection(ctx, f.owner, core.Connection{ID: id + "c", Name: "Source", Kind: "mysql", SecretEnv: "WORKBENCH_SOURCE_SYNC_REVIEW"}))
	_, err = s.DB.ExecContext(ctx, "CREATE TABLE `"+id+"` (id INT PRIMARY KEY, value VARCHAR(32))")
	requireSyncTest(t, err)
	_, err = s.DB.ExecContext(ctx, "INSERT INTO `"+id+"` VALUES (1,'fresh'),(2,'fresh')")
	requireSyncTest(t, err)
	requireSyncTest(t, s.RegisterResource(ctx, f.owner, f.resource))
	var previous []byte
	err = s.DB.QueryRowContext(ctx, "SELECT body FROM wb_settings WHERE id='personal'").Scan(&previous)
	if !errors.Is(err, sql.ErrNoRows) {
		requireSyncTest(t, err)
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO wb_settings VALUES('personal',JSON_OBJECT('sessionId',?)) ON DUPLICATE KEY UPDATE body=VALUES(body)", f.personal.SessionID)
	requireSyncTest(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if previous == nil {
			_, err := s.DB.ExecContext(ctx, "DELETE FROM wb_settings WHERE id='personal' AND body->>'$.sessionId'=?", f.personal.SessionID)
			requireSyncTest(t, err)
			return
		}
		_, err := s.DB.ExecContext(ctx, "UPDATE wb_settings SET body=? WHERE id='personal' AND body->>'$.sessionId'=?", previous, f.personal.SessionID)
		requireSyncTest(t, err)
	})
	return f
}

func requireSyncTest(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRealTemporalMySQLSyncResumesAfterStartFailure(t *testing.T) {
	var failStart, failDescribe atomic.Bool
	f := newMySQLSyncFixture(t, grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, next grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if failStart.Load() && strings.HasSuffix(method, "/StartWorkflowExecution") {
			return status.Error(codes.InvalidArgument, "injected start rejection before acceptance")
		}
		if failDescribe.Load() && strings.HasSuffix(method, "/DescribeWorkflowExecution") {
			return status.Error(codes.Unavailable, "injected describe outage")
		}
		return next(ctx, method, req, reply, conn, opts...)
	}))
	previous := f.resource.ID + "old"
	requireSyncTest(t, f.store.BeginSnapshot(f.ctx, f.owner, core.Snapshot{ResourceID: f.resource.ID, Version: previous}))
	requireSyncTest(t, f.store.Stage(f.ctx, f.owner, f.resource.ID, previous, []core.Record{{ID: "1", Fields: map[string]any{"value": "previous"}}}))
	_, err := f.store.Publish(f.ctx, f.owner, f.resource.ID, previous, 1)
	requireSyncTest(t, err)
	a := &Server{Store: f.store, Temporal: f.temporal, Token: strings.Repeat("t", 40)}
	handler := a.Handler()
	request := func(method, path string, in any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(in)
		requireSyncTest(t, err)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(f.ctx)
		r.Header.Set("Authorization", "Bearer "+a.Token)
		r.Header.Set("X-Workbench-Kind", f.owner.Kind)
		r.Header.Set("X-Workbench-Session", f.owner.SessionID)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	failStart.Store(true)
	w := request("POST", "/resource-sync-start", SyncInput{OperationKey: "original", Destination: f.resource.ID})
	if w.Code == 200 || !strings.Contains(w.Body.String(), "injected start rejection") {
		t.Fatalf("start was not rejected: %d %s", w.Code, w.Body)
	}
	failStart.Store(false)
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(f.personal.SessionID+"/original")).String()
	_, err = f.temporal.DescribeWorkflowExecution(f.ctx, id, "")
	var missing *serviceerror.NotFound
	if !errors.As(err, &missing) {
		t.Fatalf("Temporal should have no execution for %s: %v", id, err)
	}
	statusPath := "/resource-sync-status?resourceId=" + f.resource.ID
	read := func() mysqlSyncStatus {
		w := request("GET", statusPath, nil)
		if w.Code != 200 {
			t.Fatalf("read sync status: %d %s", w.Code, w.Body)
		}
		var out mysqlSyncStatus
		requireSyncTest(t, json.Unmarshal(w.Body.Bytes(), &out))
		return out
	}
	failDescribe.Store(true)
	unknown := read()
	if unknown.Run == nil || unknown.Run.Execution != "Unknown" || unknown.CanRetry {
		t.Fatalf("outage was treated as confirmed absence: %+v", unknown)
	}
	failDescribe.Store(false)
	saved := read()
	if saved.Run == nil || saved.Run.ID != id || saved.Run.Execution != "NotStarted" || !saved.CanRetry {
		t.Fatalf("saved task cannot be recovered: %+v", saved)
	}
	batch := worker.New(f.temporal, engine.BatchQueue, worker.Options{})
	batch.RegisterWorkflow(engine.WriteTask)
	batch.RegisterWorkflow(engine.Sync)
	batch.RegisterActivity(&engine.Activities{Store: f.store, Temporal: f.temporal})
	requireSyncTest(t, batch.Start())
	defer batch.Stop()
	interactive := worker.New(f.temporal, engine.InteractiveQueue, worker.Options{})
	interactive.RegisterActivity(&engine.Activities{Store: f.store, Temporal: f.temporal})
	requireSyncTest(t, interactive.Start())
	defer interactive.Stop()
	trigger := f.resource.ID + "reject"
	_, err = f.store.DB.ExecContext(f.ctx, "CREATE TRIGGER `"+trigger+"` BEFORE INSERT ON wb_events FOR EACH ROW BEGIN IF NEW.id='"+id+"-published' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected receipt failure'; END IF; END")
	requireSyncTest(t, err)
	t.Cleanup(func() {
		_, err := f.store.DB.ExecContext(context.Background(), "DROP TRIGGER IF EXISTS `"+trigger+"`")
		requireSyncTest(t, err)
	})
	retry := func() {
		w = request("POST", "/resource-sync-retry", ResumeRequest{RunID: id})
		if w.Code != 200 || !strings.Contains(w.Body.String(), id) {
			t.Fatalf("retry original: %d %s", w.Code, w.Body)
		}
	}
	publication := func() (int, int) {
		var pins, receipts int
		requireSyncTest(t, f.store.DB.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM wb_run_sources WHERE run_id=? AND resource_id=? AND version=?", id, f.resource.ID, id).Scan(&pins))
		requireSyncTest(t, f.store.DB.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM wb_events WHERE id=?", id+"-published").Scan(&receipts))
		return pins, receipts
	}
	retry()
	err = f.temporal.GetWorkflow(f.ctx, id, "").Get(f.ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "injected receipt failure") {
		t.Fatalf("receipt failure did not stop publication: %v", err)
	}
	failed := read()
	pins, receipts := publication()
	if failed.Run == nil || failed.Run.ID != id || failed.Run.Published || !failed.CanRetry || failed.CurrentVersion != previous || pins != 0 || receipts != 0 {
		t.Fatalf("publication was partially committed: %+v, pins=%d, receipts=%d", failed, pins, receipts)
	}
	_, err = f.store.DB.ExecContext(f.ctx, "DROP TRIGGER `"+trigger+"`")
	requireSyncTest(t, err)
	retry()
	requireSyncTest(t, f.temporal.GetWorkflow(f.ctx, id, "").Get(f.ctx, nil))
	final := read()
	pins, receipts = publication()
	if final.Run == nil || final.Run.ID != id || !final.Run.Published || final.CurrentVersion != id || final.CanRetry || pins != 1 || receipts != 1 {
		t.Fatalf("unexpected publication: %+v, pins=%d, receipts=%d", final, pins, receipts)
	}
	if w = request("POST", "/resource-sync-retry", ResumeRequest{RunID: id}); w.Code != 409 {
		t.Fatalf("published workflow could be retried: %d %s", w.Code, w.Body)
	}
	t.Logf("run %s recovered after the start and receipt failures; the snapshot, source pin and receipt committed together", id)
}

func TestRealTemporalMySQLSyncFencesOverlappingAttempts(t *testing.T) {
	f := newMySQLSyncFixture(t)
	r := core.Run{ID: f.resource.ID + "r", Principal: f.personal, Operation: "sync", Destination: f.resource.ID, Title: "Overlapping retry"}
	requireSyncTest(t, f.store.SaveRun(f.ctx, r))
	w := worker.New(f.temporal, engine.BatchQueue, worker.Options{MaxConcurrentActivityExecutionSize: 2})
	w.RegisterActivity(&engine.Activities{Store: f.store})
	w.RegisterWorkflowWithOptions(func(ctx workflow.Context, id string) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 15 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}})
		var fence core.WriteFence
		if err := workflow.ExecuteActivity(ctx, "BeginWrite", id, workflow.GetInfo(ctx).WorkflowExecution.RunID).Get(ctx, &fence); err != nil {
			return err
		}
		retry := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 15 * time.Second, HeartbeatTimeout: 2 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2, InitialInterval: time.Millisecond}})
		err := workflow.ExecuteActivity(retry, "OverlapSync", engine.TaskInput{RunID: id, Fence: fence}).Get(retry, nil)
		return errors.Join(err, workflow.ExecuteActivity(ctx, "EndWrite", id, fence.Execution).Get(ctx, nil))
	}, workflow.RegisterOptions{Name: "OverlapMySQLSync"})
	newAttempt := make(chan struct{})
	oldResult := make(chan [4]error, 1)
	w.RegisterActivityWithOptions(func(ctx context.Context, input engine.TaskInput) error {
		attempt := activity.GetInfo(ctx).Attempt
		fence, err := f.store.BeginSyncAttempt(ctx, input.Fence, attempt)
		if err != nil {
			return err
		}
		ctx = core.WithFence(ctx, fence)
		if attempt == 1 {
			if err := f.store.BeginSnapshot(ctx, r.Principal, core.Snapshot{ResourceID: r.Destination, Version: r.ID}); err != nil {
				return err
			}
			old := []core.Record{{ID: "1", Fields: map[string]any{"value": "old"}}}
			if err := f.store.Stage(ctx, r.Principal, r.Destination, r.ID, old); err != nil {
				return err
			}
			select {
			case <-newAttempt:
			case <-f.ctx.Done():
				return f.ctx.Err()
			}
			// Deliberately issue late writes even if the SDK canceled this attempt.
			stale, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, reacquire := f.store.BeginSyncAttempt(stale, input.Fence, attempt)
			_, reset := f.store.SyncRunSource(stale, r, func() {})
			stage := f.store.Stage(stale, r.Principal, r.Destination, r.ID, []core.Record{{ID: "2", Fields: map[string]any{"value": "old"}}})
			_, publish := f.store.Publish(stale, r.Principal, r.Destination, r.ID, 2)
			oldResult <- [4]error{reacquire, reset, stage, publish}
			return stage
		}
		// SyncSource calls this callback immediately before staging, after reset.
		var late [4]error
		var observed bool
		_, err = f.store.SyncRunSource(ctx, r, func() {
			close(newAttempt)
			select {
			case late = <-oldResult:
				observed = true
			case <-ctx.Done():
			}
		})
		if err != nil {
			return err
		}
		if !observed {
			return errors.New("old attempt did not try late writes")
		}
		for _, err := range late {
			if !errors.Is(err, core.ErrDenied) {
				return errors.New("stale attempt reached a MySQL mutation")
			}
		}
		return nil
	}, activity.RegisterOptions{Name: "OverlapSync"})
	requireSyncTest(t, w.Start())
	defer w.Stop()
	run, err := f.temporal.ExecuteWorkflow(f.ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: engine.BatchQueue}, "OverlapMySQLSync", r.ID)
	requireSyncTest(t, err)
	requireSyncTest(t, run.Get(f.ctx, nil))
	page, err := f.store.Read(f.ctx, r.Principal, core.Ref{ResourceID: r.Destination, Version: r.ID, Section: "text"}, "", 10)
	requireSyncTest(t, err)
	if page.Count != 2 || len(page.Records) != 2 || page.Records[0].Fields["value"] != "fresh" || page.Records[1].Fields["value"] != "fresh" {
		t.Fatalf("mixed or incomplete publication: %+v", page)
	}
	t.Log("Temporal heartbeat timeout created an overlapping retry; MySQL refused reacquisition, late reset, staging and publication from attempt 1")
}
