package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/types/known/durationpb"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func TestHostDeliveryErrors(t *testing.T) {
	for _, code := range []int{400, 401, 403, 409, 503} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		m := modelBridge{URL: s.URL, Client: s.Client()}
		err := m.Dispatch(context.Background(), core.Run{})
		s.Close()
		if err == nil {
			t.Fatal(code, "accepted")
		}
		if code == 503 && (errors.Is(err, core.ErrInvalid) || errors.Is(err, core.ErrDenied)) {
			t.Fatal("transient failure became terminal")
		}
		if code == 403 && !errors.Is(err, core.ErrDenied) {
			t.Fatal("identity rejection must be terminal")
		}
	}
}

// Exercise real Temporal retries beyond systemd's five-second restart delay,
// including an accepted request whose response is lost. No customer channel.
func TestRealDispatchSurvivesHostRestart(t *testing.T) {
	address, dsn := os.Getenv("WORKBENCH_TEST_TEMPORAL"), os.Getenv("WORKBENCH_TEST_DSN")
	if address == "" || dsn == "" {
		t.Skip("real Temporal/MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
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
	ns := "dispatch-" + uuid.NewString()
	c, err := client.Dial(client.Options{HostPort: address, Namespace: ns})
	must(err)
	defer c.Close()
	_, err = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: ns, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	must(err)
	project := uuid.NewString()
	p := core.Principal{Kind: "project", ProjectID: project, SessionID: project + "s"}
	must(s.PutProject(ctx, core.Principal{Kind: "owner", SessionID: project + "o"}, core.Project{ID: project, Name: "restart acceptance"}))
	run := core.Run{ID: uuid.NewString(), Principal: p, Operation: "incoming", Goal: "synthetic input", Title: "Host restart acceptance"}
	must(s.SaveRun(ctx, run))
	var attempts, accepted atomic.Int32
	readyAt := time.Now().Add(7 * time.Second)
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if time.Now().Before(readyAt) {
			w.WriteHeader(503)
			return
		}
		var received core.Run
		if json.NewDecoder(r.Body).Decode(&received) != nil || received.ID != run.ID {
			w.WriteHeader(400)
			return
		}
		if accepted.CompareAndSwap(0, 1) {
			w.WriteHeader(503)
			return
		} // accepted, response lost
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"accepted":true}`))
	}))
	defer host.Close()
	a := &engine.Activities{Store: s, Temporal: c, Host: modelBridge{URL: host.URL, Client: host.Client()}}
	w := worker.New(c, engine.InteractiveQueue, worker.Options{})
	w.RegisterWorkflow(engine.Dispatch)
	w.RegisterActivity(a)
	must(w.Start())
	defer w.Stop()
	execution, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: run.ID, TaskQueue: engine.InteractiveQueue}, engine.Dispatch, run.ID)
	must(err)
	must(execution.Get(ctx, nil))
	if attempts.Load() < 5 || accepted.Load() != 1 {
		t.Fatalf("attempts=%d accepted=%d", attempts.Load(), accepted.Load())
	}
	t.Logf("Host unavailable for 7s; %d delivery attempts, exactly %d accepted input", attempts.Load(), accepted.Load())
}
