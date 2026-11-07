package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"workbench-native/internal/core"
)

func TestMySQLExternalMCPProvenanceAndExport(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("real MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	s, e := core.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.Migrate(ctx))
	id := uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: id + "o"}
	a := core.Principal{Kind: "project", ProjectID: id + "a", SessionID: id + "as"}
	b := core.Principal{Kind: "project", ProjectID: id + "b", SessionID: id + "bs"}
	personal := core.Principal{Kind: "personal", SessionID: id + "today"}
	for _, p := range []core.Principal{a, b} {
		must(s.PutProject(ctx, owner, core.Project{ID: p.ProjectID, Name: "source boundary test"}))
	}
	r := core.Resource{ID: id, Name: "shared docs", Sections: []core.Section{{ID: "public", Fields: []string{"text"}}, {ID: "private", Fields: []string{"secret"}}}}
	must(s.PutResource(ctx, owner, r))
	must(s.BeginSnapshot(ctx, owner, core.Snapshot{ResourceID: id, Version: "v1"}))
	longText := strings.Repeat("正文", 32768) + "终点验证"
	must(s.Stage(ctx, owner, id, "v1", []core.Record{{ID: "1", Fields: map[string]any{"text": longText, "secret": "hidden"}}}))
	_, e = s.Publish(ctx, owner, id, "v1", 1)
	must(e)
	must(s.SetGrant(ctx, owner, core.Grant{ResourceID: id, ProjectID: a.ProjectID, Sections: []string{"public"}}))
	token := strings.Repeat("x", 40)
	server := (&Server{Store: s, Token: token}).Handler()
	call := func(p core.Principal, method, path string, v any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(v)
		request := httptest.NewRequest(method, path, bytes.NewReader(raw))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-Workbench-Kind", p.Kind)
		request.Header.Set("X-Workbench-Project", p.ProjectID)
		request.Header.Set("X-Workbench-Session", p.SessionID)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, request)
		return w
	}
	refs := []core.Ref{{ResourceID: id, Version: "v1", Section: "public"}}
	if w := call(personal, "POST", "/session/external-sources", refs); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	lineage, e := s.SessionSources(ctx, personal)
	must(e)
	if len(lineage) != 1 {
		t.Fatal("extra MCP read had no lineage", lineage)
	}
	if e = core.AuthorizeAll(ctx, s, b, lineage); e == nil {
		t.Fatal("Today could launder extra MCP data into B")
	}
	if w := call(b, "POST", "/session/external-sources", refs); w.Code != 403 {
		t.Fatal("B guessed registered source", w.Code)
	}
	if w := call(a, "GET", "/resource-export?resourceId="+id+"&version=v1&section=public", nil); w.Code != 200 || strings.Contains(w.Body.String(), "hidden") {
		t.Fatal("export boundary", w.Code, w.Body.String())
	}
	if w := call(a, "GET", "/resource-export?resourceId="+id+"&version=v1&section=private", nil); w.Code != 403 {
		t.Fatal("hidden export", w.Code)
	}
	mcpServer := (&Server{Store: s}).MCP(a)
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer endpoint.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "resource-reader-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint.URL}, nil)
	must(e)
	defer session.Close()
	search, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "resource_search", Arguments: SearchInput{Ref: refs[0], Query: "终点验证"}})
	must(e)
	raw, _ := json.Marshal(search.StructuredContent)
	if search.IsError || !strings.Contains(string(raw), "终点验证") {
		t.Fatal("search omitted match near document end", search)
	}
	excerpt, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "resource_excerpt", Arguments: ExcerptInput{Ref: refs[0], ID: "1", Pointer: "/text", Offset: 65536, Limit: 4}})
	must(e)
	raw, _ = json.Marshal(excerpt.StructuredContent)
	var fragment ExcerptResult
	must(json.Unmarshal(raw, &fragment))
	if excerpt.IsError || fragment.Text != "终点验证" || fragment.Next != nil {
		t.Fatal("long Unicode record cannot be read", excerpt)
	}
	hidden, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "resource_excerpt", Arguments: ExcerptInput{Ref: refs[0], ID: "1", Pointer: "/secret"}})
	must(e)
	if !hidden.IsError {
		t.Fatal("excerpt bypassed field projection")
	}
	must(s.SetGrant(ctx, owner, core.Grant{ResourceID: id, ProjectID: a.ProjectID, Sections: nil}))
	if w := call(a, "POST", "/session/external-sources", refs); w.Code != 403 {
		t.Fatal("revoked plugin source", w.Code)
	}
	if w := call(a, "GET", "/resource-export?resourceId="+id+"&version=v1&section=public", nil); w.Code != 403 {
		t.Fatal("revoked export", w.Code)
	}
}
