package api

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"testing"
	"workbench-native/internal/core"
)

func TestCRMBasicsUseOfficialMCPToolsAndSchemas(t *testing.T) {
	a := &Server{}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return a.MCP(core.Principal{Kind: "owner", SessionID: "schema-owner"})
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	host := httptest.NewServer(handler)
	defer host.Close()
	c := mcp.NewClient(&mcp.Implementation{Name: "crm-basics-schema-test", Version: "1"}, nil)
	session, e := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: host.URL}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	result, e := session.ListTools(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	wanted := map[string]int{"strategy_select": 0, "data_refresh": 0, "batch_generate": 0, "crm_push": 0, "crm_receipts": 0}
	for _, tool := range result.Tools {
		if _, ok := wanted[tool.Name]; ok {
			wanted[tool.Name]++
			if tool.InputSchema == nil || tool.OutputSchema == nil {
				t.Fatalf("missing standard schemas for %s", tool.Name)
			}
		}
	}
	for name, n := range wanted {
		if n != 1 {
			t.Fatalf("tool %s registered %d times", name, n)
		}
	}
	// Invalid inputs are rejected by the standard SDK before touching any store.
	for _, name := range []string{"data_refresh", "crm_push", "crm_receipts"} {
		out, e := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if e == nil && (out == nil || !out.IsError) {
			t.Fatalf("%s accepted an incomplete request", name)
		}
	}
}
