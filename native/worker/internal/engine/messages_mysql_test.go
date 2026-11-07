package engine

import (
	"context"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"workbench-native/internal/core"
)

func TestMySQLMCPUnknownSendIsReconciledWithoutResending(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("real MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	store, e := core.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer store.DB.Close()
	if e = store.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	id := "t" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	p := core.Principal{Kind: "project", ProjectID: id, SessionID: id + "s"}
	owner := core.Principal{Kind: "owner", SessionID: id + "o"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(store.PutProject(ctx, owner, core.Project{ID: id, Name: "MCP receipt fixture"}))
	var sends, reads atomic.Int32
	var confirmed atomic.Bool
	toolServer := mcp.NewServer(&mcp.Implementation{Name: "fixture-channel", Version: "1"}, nil)
	type Args struct {
		ConnectionID   string            `json:"connectionId"`
		IdempotencyKey string            `json:"idempotencyKey"`
		Target         map[string]string `json:"target"`
		Text           string            `json:"text,omitempty"`
		Receipt        string            `json:"receipt,omitempty"`
		State          string            `json:"state,omitempty"`
	}
	mcp.AddTool(toolServer, &mcp.Tool{Name: "message_send"}, func(context.Context, *mcp.CallToolRequest, Args) (*mcp.CallToolResult, Receipt, error) {
		sends.Add(1)
		return nil, Receipt{State: "unknown", Receipt: "platform-task", Error: "lost acknowledgement"}, nil
	})
	mcp.AddTool(toolServer, &mcp.Tool{Name: "message_receipt"}, func(context.Context, *mcp.CallToolRequest, Args) (*mcp.CallToolResult, Receipt, error) {
		reads.Add(1)
		if confirmed.Load() {
			return nil, Receipt{State: "sent", Receipt: "platform-task"}, nil
		}
		return nil, Receipt{State: "unknown", Receipt: "platform-task"}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return toolServer }, &mcp.StreamableHTTPOptions{Stateless: true})
	server := httptest.NewServer(handler)
	defer server.Close()
	t.Setenv("WORKBENCH_CONNECTOR_TEST", strings.Repeat("x", 40))
	must(store.PutTransport(ctx, owner, core.Transport{ID: id, Name: "fixture", Endpoint: server.URL, TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	run := core.Run{ID: id, Principal: p, Operation: "message", Title: "MCP lost response"}
	must(store.SaveRun(ctx, run))
	must(store.PutDelivery(ctx, run, core.Delivery{ID: id, RunID: id, ItemID: "one", ConnectionID: id, Target: map[string]string{"actorId": "fixture"}, Text: "Synthetic only"}))
	activities := Activities{Store: store}
	first, e := activities.SendDelivery(ctx, id)
	must(e)
	if first.State != "unknown" {
		t.Fatal(first)
	}
	second, e := activities.SendDelivery(ctx, id)
	must(e)
	if second.State != "unknown" || sends.Load() != 1 || reads.Load() != 1 {
		t.Fatal("retry resent", second, sends.Load(), reads.Load())
	}
	confirmed.Store(true)
	final, e := activities.ReconcileDelivery(ctx, id)
	must(e)
	if final.State != "sent" || sends.Load() != 1 {
		t.Fatal("receipt did not reconcile safely", final, sends.Load())
	}
}
