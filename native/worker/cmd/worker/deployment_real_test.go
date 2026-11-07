package main

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/types/known/durationpb"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func TestRealWorkerDeploymentGateSurvivesRestart(t *testing.T) {
	binary, dsn, address := os.Getenv("WORKBENCH_TEST_WORKER_BINARY"), os.Getenv("WORKBENCH_TEST_GATE_DSN"), os.Getenv("WORKBENCH_TEST_TEMPORAL")
	if binary == "" || dsn == "" || address == "" {
		t.Skip("isolated worker/Temporal/MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance_gate_") {
		t.Fatal("dedicated gate database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	store, err := core.Open(ctx, dsn)
	must(err)
	defer store.DB.Close()
	must(store.Migrate(ctx))
	ns := "gate-" + uuid.NewString()
	temporal, err := client.Dial(client.Options{HostPort: address, Namespace: ns})
	must(err)
	defer temporal.Close()
	_, err = temporal.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: ns, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	must(err)
	var sends atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "synthetic-gate-channel", Version: "1"}, nil)
	type input struct {
		ConnectionID   string            `json:"connectionId"`
		IdempotencyKey string            `json:"idempotencyKey"`
		Target         map[string]string `json:"target"`
		Text           string            `json:"text"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "message_send"}, func(context.Context, *mcp.CallToolRequest, input) (*mcp.CallToolResult, engine.Receipt, error) {
		sends.Add(1)
		return nil, engine.Receipt{State: "accepted", Receipt: "synthetic-only"}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer endpoint.Close()
	id := uuid.NewString()
	p := core.Principal{Kind: "project", ProjectID: id, SessionID: id + "s"}
	owner := core.Principal{Kind: "owner", SessionID: id + "o"}
	must(store.PutProject(ctx, owner, core.Project{ID: id, Name: "gate synthetic"}))
	must(store.PutTransport(ctx, owner, core.Transport{ID: id, Name: "synthetic only", Endpoint: endpoint.URL, TokenEnv: "WORKBENCH_CONNECTOR_GATE_TEST", Projects: []string{id}, Enabled: true}))
	gate := filepath.Join(t.TempDir(), "deploying")
	write := func(state map[string]any) {
		t.Helper()
		data, err := json.Marshal(state)
		must(err)
		must(os.WriteFile(gate+".next", data, 0600))
		must(os.Rename(gate+".next", gate))
	}
	write(map[string]any{"kind": "native", "committed": false})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	listen := listener.Addr().String()
	must(listener.Close())
	log, err := os.CreateTemp(t.TempDir(), "worker-*.log")
	must(err)
	defer log.Close()
	start := func() *exec.Cmd {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary)
		cmd.Env = append(os.Environ(), "WORKBENCH_HOST_TOKEN="+strings.Repeat("g", 40), "WORKBENCH_CONNECTOR_GATE_TEST="+strings.Repeat("c", 40), "WORKBENCH_MYSQL_DSN="+dsn, "TEMPORAL_ADDRESS="+address, "TEMPORAL_NAMESPACE="+ns, "WORKBENCH_LISTEN="+listen, "WORKBENCH_DEPLOYMENT_MARKER="+gate)
		cmd.Stdout = log
		cmd.Stderr = log
		must(cmd.Start())
		return cmd
	}
	stop := func(cmd *exec.Cmd) { t.Helper(); must(cmd.Process.Signal(syscall.SIGTERM)); must(cmd.Wait()) }
	cmd := start()
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	health := func() (bool, bool) {
		r, e := http.Get("http://" + listen + "/health")
		if e != nil {
			return false, false
		}
		defer r.Body.Close()
		var v struct {
			Ready      bool `json:"ready"`
			Background bool `json:"backgroundReady"`
		}
		if json.NewDecoder(r.Body).Decode(&v) != nil {
			return false, false
		}
		return v.Ready, v.Background
	}
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	wait(func() bool { ready, background := health(); return ready && !background })
	run := core.Run{ID: uuid.NewString(), Principal: p, Operation: "message", Title: "synthetic deployment gate"}
	must(store.SaveRun(ctx, run))
	must(store.PutDelivery(ctx, run, core.Delivery{ID: run.ID, RunID: run.ID, ItemID: "one", ConnectionID: id, Target: map[string]string{"actorId": "synthetic"}, Text: "synthetic only"}))
	execution, err := temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: run.ID, TaskQueue: engine.InteractiveQueue}, engine.SubmitMessage, run.ID)
	must(err)
	time.Sleep(600 * time.Millisecond)
	saved, err := store.Delivery(ctx, run.ID)
	must(err)
	if sends.Load() != 0 || saved.State != "prepared" {
		t.Fatal("consumed before commit", sends.Load(), saved.State)
	}
	stop(cmd)
	cmd = start()
	wait(func() bool { ready, background := health(); return ready && !background })
	write(map[string]any{"kind": "native", "committed": true, "backgroundEnabled": false})
	time.Sleep(600 * time.Millisecond)
	if sends.Load() != 0 {
		t.Fatal("consumed before channel release")
	}
	write(map[string]any{"kind": "native", "committed": true, "backgroundEnabled": true})
	wait(func() bool { _, background := health(); return background })
	must(execution.Get(ctx, nil))
	saved, err = store.Delivery(ctx, run.ID)
	must(err)
	if sends.Load() != 1 || saved.State != "accepted" {
		t.Fatal("delivery was lost or repeated", sends.Load(), saved.State)
	}
	stop(cmd)
	t.Log("Real process API available while gated; restart retains same prepared delivery; commit and channel release consume exactly once")
}
