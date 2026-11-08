package engine

import (
	"context"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/types/known/durationpb"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"workbench-native/internal/core"
)

func TestRealModeNotificationRestartUsesOriginalEvent(t *testing.T) {
	address, dsn := os.Getenv("WORKBENCH_TEST_TEMPORAL"), os.Getenv("WORKBENCH_TEST_DSN")
	if address == "" || dsn == "" {
		t.Skip("isolated Temporal/MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated DB required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	admin, e := client.Dial(client.Options{HostPort: address})
	must(e)
	defer admin.Close()
	ns := "handoff-acceptance-" + uuid.NewString()
	_, e = admin.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: ns, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	must(e)
	defer admin.OperatorService().DeleteNamespace(context.Background(), &operatorservice.DeleteNamespaceRequest{Namespace: ns})
	c, e := client.Dial(client.Options{HostPort: address, Namespace: ns})
	must(e)
	defer c.Close()
	s, e := core.Open(ctx, dsn)
	must(e)
	defer s.DB.Close()
	must(s.Migrate(ctx))
	id := uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: id + "o"}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "Synthetic handoff"}))
	var available atomic.Bool
	var sends, reads, probes atomic.Int32
	ms := mcp.NewServer(&mcp.Implementation{Name: "synthetic-control", Version: "1"}, nil)
	type args struct {
		Text           string            `json:"text"`
		Target         map[string]string `json:"target"`
		Receipt        string            `json:"receipt,omitempty"`
		ConnectionID   string            `json:"connectionId"`
		IdempotencyKey string            `json:"idempotencyKey"`
		State          string            `json:"state,omitempty"`
	}
	mcp.AddTool(ms, &mcp.Tool{Name: "message_send"}, func(_ context.Context, _ *mcp.CallToolRequest, in args) (*mcp.CallToolResult, Receipt, error) {
		sends.Add(1)
		if in.Target["kind"] != "conversation.mode.changed" {
			t.Error("wrong destination")
		}
		return nil, Receipt{State: "unknown", Receipt: in.Target["eventId"]}, nil
	})
	mcp.AddTool(ms, &mcp.Tool{Name: "message_receipt"}, func(_ context.Context, _ *mcp.CallToolRequest, in args) (*mcp.CallToolResult, Receipt, error) {
		reads.Add(1)
		if in.Text == "" {
			t.Error("control event lost on reconciliation")
		}
		return nil, Receipt{State: "sent", Receipt: in.Receipt}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			probes.Add(1)
			http.Error(w, "not ready", 503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Setenv("WORKBENCH_CONNECTOR_TEST", strings.Repeat("x", 40))
	must(s.PutTransport(ctx, owner, core.Transport{ID: id, Name: "test", Endpoint: server.URL, TokenEnv: "WORKBENCH_CONNECTOR_TEST", Enabled: true, Projects: []string{id}}))
	_, e = s.PutReplySettings(ctx, owner, core.ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "qa", Enabled: true})
	must(e)
	in := core.IncomingMessage{ConnectionID: id, ConversationID: "a", ActorID: "a", ExternalUserID: "external-a", EventID: "first", Text: "synthetic"}
	route, e := s.EnsureReplyRoute(ctx, in)
	must(e)
	_, e = s.AcceptMessage(ctx, in)
	must(e)
	must(s.MessageDisposition(ctx, route.Principal, id, in.EventID, "handoff"))
	pending, e := s.PendingModeEvents(ctx)
	must(e)
	if len(pending) != 1 {
		t.Fatal("test requires fresh isolated database", len(pending))
	}
	// Simulate a crash after persisting the delivery, before queue acknowledgement.
	run, e := s.QueueModeNotification(ctx, pending[0])
	must(e)
	activities := &Activities{Store: s, Temporal: c}
	start := func() worker.Worker {
		w := worker.New(c, InteractiveQueue, worker.Options{})
		w.RegisterWorkflow(SendMessage)
		w.RegisterActivity(activities)
		must(w.Start())
		return w
	}
	w := start()
	must(activities.QueueModeNotifications(ctx))
	for probes.Load() == 0 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	w.Stop()
	available.Store(true)
	// A lost queue acknowledgement also restarts with the same workflow/delivery.
	_, e = s.DB.ExecContext(ctx, `DELETE FROM wb_connector_state WHERE id=?`, "mode-queued-"+pending[0].ID)
	must(e)
	must(activities.QueueModeNotifications(ctx))
	w = start()
	defer w.Stop()
	must(c.GetWorkflow(ctx, run.ID, "").Get(ctx, nil))
	d, e := s.Delivery(ctx, run.ID)
	must(e)
	if d.State != "unknown" || sends.Load() != 1 {
		t.Fatal("original event resent/lost", d.State, sends.Load())
	}
	result, e := activities.ReconcileDelivery(ctx, run.ID)
	must(e)
	if result.State != "sent" || sends.Load() != 1 || reads.Load() != 1 {
		t.Fatal("receipt reconciliation resent", result, sends.Load(), reads.Load())
	}
	t.Log("Isolated namespace: restart and lost queue acknowledgement retained original event; one send and one receipt reconciliation")
}
