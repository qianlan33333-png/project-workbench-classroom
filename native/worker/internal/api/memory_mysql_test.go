package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workbench-native/internal/core"
)

func TestMySQLMemoryToolReturnsCanonicalRevision(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	store, err := core.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p := core.Principal{Kind: "personal", SessionID: uuid.NewString()}
	server := (&Server{Store: store}).MCP(p)
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer endpoint.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "memory-sequence", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	memory := core.Memory{ID: uuid.NewString(), Text: "synthetic personal rule", Source: "test owner text", Confirmed: true}
	save := func(in core.Memory) core.Memory {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "memory_save", Arguments: in})
		if err != nil || result.IsError {
			t.Fatalf("save: %v / %v", err, result)
		}
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var out core.Memory
		if err = json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	memory = save(memory)
	if memory.Revision != 1 || memory.Confirmed {
		t.Fatal(memory)
	}
	stale := memory
	memory.Text = "corrected synthetic rule"
	memory = save(memory)
	if memory.Revision != 2 {
		t.Fatal(memory)
	}
	conflict, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "memory_save", Arguments: stale})
	if err != nil || !conflict.IsError {
		t.Fatal("stale revision was accepted", err, conflict)
	}
	memory.Forgotten = true
	memory = save(memory)
	if memory.Revision != 3 || !memory.Forgotten {
		t.Fatal(memory)
	}
	list, err := store.Memories(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range list {
		if v.ID == memory.ID {
			t.Fatal("forgotten memory is still active")
		}
	}
}
