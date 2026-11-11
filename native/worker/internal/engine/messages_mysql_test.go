package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
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
	var available atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available.Load() {
			http.Error(w, "temporarily unavailable", 503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Setenv("WORKBENCH_CONNECTOR_TEST", strings.Repeat("x", 40))
	must(store.PutTransport(ctx, owner, core.Transport{ID: id, Name: "fixture", Endpoint: server.URL, TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	run := core.Run{ID: id, Principal: p, Operation: "message", Title: "MCP lost response"}
	must(store.SaveRun(ctx, run))
	must(store.PutDelivery(ctx, run, core.Delivery{ID: id, RunID: id, ItemID: "one", ConnectionID: id, Target: map[string]string{"actorId": "fixture"}, Text: "Synthetic only"}))
	activities := Activities{Store: store}
	waiting, e := activities.SendDelivery(ctx, id)
	must(e)
	saved, e := store.Delivery(ctx, id)
	must(e)
	if waiting.State != "prepared" || saved.State != "prepared" || sends.Load() != 0 {
		t.Fatal("unavailable connector consumed delivery", waiting, saved, sends.Load())
	}
	available.Store(true)
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

// Uses the real MCP SDK and database, with a local synthetic provider only.
func TestMySQLReplySingleConnectionAndUnknownReceipt(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL not configured")
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
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(store.Migrate(ctx))
	id := "t" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	owner := core.Principal{Kind: "owner", SessionID: id + "owner"}
	must(store.PutProject(ctx, owner, core.Project{ID: id, Name: "single connection"}))
	var connects, sends, reads atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "reply-fixture", Version: "1"}, nil)
	type Args struct {
		ConnectionID   string            `json:"connectionId"`
		IdempotencyKey string            `json:"idempotencyKey"`
		Target         map[string]string `json:"target"`
		Text           string            `json:"text,omitempty"`
		Receipt        string            `json:"receipt,omitempty"`
		State          string            `json:"state,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "message_send"}, func(context.Context, *mcp.CallToolRequest, Args) (*mcp.CallToolResult, Receipt, error) {
		sends.Add(1)
		return nil, Receipt{State: "unknown", Receipt: "fixture-receipt"}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "message_receipt"}, func(context.Context, *mcp.CallToolRequest, Args) (*mcp.CallToolResult, Receipt, error) {
		reads.Add(1)
		return nil, Receipt{State: "sent", Receipt: "fixture-receipt"}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	var takeover func()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var req struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(raw, &req)
			if req.Method == "initialize" || req.Method == "server/discover" {
				connects.Add(1)
				if takeover != nil {
					takeover()
				}
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer endpoint.Close()
	t.Setenv("WORKBENCH_CONNECTOR_TEST", strings.Repeat("x", 40))
	must(store.PutTransport(ctx, owner, core.Transport{ID: id, Name: "fixture", Endpoint: endpoint.URL, TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	_, e = store.PutReplySettings(ctx, owner, core.ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "customer-reply", Enabled: true})
	must(e)
	in := core.IncomingMessage{ConnectionID: id, ConversationID: "one", ActorID: "one", EventID: "question", Text: "question"}
	route, e := store.EnsureReplyRoute(ctx, in)
	must(e)
	_, e = store.AcceptMessage(ctx, in)
	must(e)
	run, e := store.PrepareReply(ctx, route.Principal, id, in.EventID, "synthetic reply")
	must(e)
	a := Activities{Store: store}
	receipt, e := a.SendDelivery(ctx, run.ID)
	must(e)
	if receipt.State != "unknown" || sends.Load() != 1 || connects.Load() != 1 {
		t.Fatal("reply did not reuse prepared connection", receipt, sends.Load(), connects.Load())
	}
	receipt, e = a.SendDelivery(ctx, run.ID)
	must(e)
	if receipt.State != "sent" || sends.Load() != 1 || reads.Load() != 1 {
		t.Fatal("unknown send retried", receipt, sends.Load(), reads.Load())
	}
	// Take over during connection establishment, before the final claim.
	in.EventID = "takeover"
	_, e = store.AcceptMessage(ctx, in)
	must(e)
	run, e = store.PrepareReply(ctx, route.Principal, id, in.EventID, "must not send")
	must(e)
	takeover = func() {
		_, err := store.ChangeReplyMode(ctx, owner, route.ID, route.Revision, false)
		if err != nil {
			t.Error(err)
		}
	}
	receipt, e = a.SendDelivery(ctx, run.ID)
	must(e)
	if receipt.State != "failed" || sends.Load() != 1 {
		t.Fatal("late draft escaped takeover", receipt, sends.Load())
	}
}
