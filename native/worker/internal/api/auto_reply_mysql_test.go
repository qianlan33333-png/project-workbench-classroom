package api

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
	"testing"
	"workbench-native/internal/core"
)

func TestMySQLCustomerToolsOnlyReadBoundSubjectAndSelectedKnowledge(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated DB required")
	}
	ctx := context.Background()
	s, e := core.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	id := "qa" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	owner := core.Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "QA isolation"}))
	must(s.PutTransport(ctx, owner, core.Transport{ID: id, Name: "test", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_QA", Projects: []string{id}, Enabled: true}))
	publish := func(suffix string, records []core.Record) core.Ref {
		r := core.Resource{ID: id + suffix, Name: suffix, OwnerProject: id, Sections: []core.Section{{ID: "main", Name: "Main", Fields: []string{"text"}}}}
		if suffix == "public" {
			r.OwnerProject = id + "library"
			must(s.PutProject(ctx, owner, core.Project{ID: r.OwnerProject, Name: "Library"}))
		}
		must(s.PutResource(ctx, owner, r))
		if suffix == "public" {
			must(s.SetGrant(ctx, owner, core.Grant{ResourceID: r.ID, ProjectID: id, Sections: []string{"main"}}))
		}
		must(s.BeginSnapshot(ctx, owner, core.Snapshot{ResourceID: r.ID, Version: "v1"}))
		must(s.Stage(ctx, owner, r.ID, "v1", records))
		_, e = s.Publish(ctx, owner, r.ID, "v1", len(records))
		must(e)
		return core.Ref{ResourceID: r.ID, Version: "v1", Section: "main"}
	}
	public := publish("public", []core.Record{{ID: "faq", Fields: map[string]any{"text": "The public answer is grounded."}}})
	subjects := publish("subjects", []core.Record{{ID: "one", Fields: map[string]any{"text": "Alpha private"}}, {ID: "two", Fields: map[string]any{"text": "Beta private"}}})
	cfg, e := s.PutReplySettings(ctx, owner, core.ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "customer-reply", Enabled: true, Knowledge: []core.Ref{public}, Subject: &core.SubjectBinding{ResourceID: subjects.ResourceID, Section: subjects.Section}})
	must(e)
	incoming := core.IncomingMessage{ConnectionID: id, EventID: "one-message", ConversationID: "one", ActorID: "one", Text: "Question"}
	route, e := s.EnsureReplyRoute(ctx, incoming)
	must(e)
	_, e = s.AcceptMessage(ctx, incoming)
	must(e)
	other := incoming
	other.ActorID = "two"
	other.ConversationID = "two"
	other.EventID = "two-message"
	secondRoute, e := s.EnsureReplyRoute(ctx, other)
	must(e)
	_, e = s.AcceptMessage(ctx, other)
	must(e)
	server := &Server{Store: s}
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server.MCP(route.Principal) }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "qa-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	must(e)
	defer session.Close()
	call := func(name string, input any) *mcp.CallToolResult {
		t.Helper()
		r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
		must(e)
		return r
	}
	result := call("message_context", MessageKey{ConnectionID: id, EventID: "one-message"})
	raw, _ := json.Marshal(result.StructuredContent)
	if result.IsError || !strings.Contains(string(raw), "Alpha private") || strings.Contains(string(raw), "Beta private") {
		t.Fatalf("subject leak or missing facts: %s", raw)
	}
	if !call("message_context", MessageKey{ConnectionID: id, EventID: "two-message"}).IsError {
		t.Fatal("read another subject")
	}
	if !call("resource_search", SearchInput{Ref: subjects, Query: "Beta"}).IsError {
		t.Fatal("searched subject table as public knowledge")
	}
	if call("resource_search", SearchInput{Ref: public, Query: "grounded"}).IsError {
		t.Fatal("configured public search denied")
	}
	// The same standard external-MCP guard must record lineage and enforce live grants.
	apiHTTP := httptest.NewServer((&Server{Store: s, Token: strings.Repeat("t", 40)}).Handler())
	defer apiHTTP.Close()
	external := func() int {
		t.Helper()
		body, _ := json.Marshal([]core.Ref{public})
		req, _ := http.NewRequest("POST", apiHTTP.URL+"/session/external-sources", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 40))
		req.Header.Set("X-Workbench-Kind", "project")
		req.Header.Set("X-Workbench-Project", id)
		req.Header.Set("X-Workbench-Session", secondRoute.Principal.SessionID)
		res, err := http.DefaultClient.Do(req)
		must(err)
		defer res.Body.Close()
		io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	if external() != 200 {
		t.Fatal("selected external source denied")
	}
	lineage, e := s.SessionSources(ctx, secondRoute.Principal)
	must(e)
	if len(lineage) == 0 {
		t.Fatal("external source lineage not recorded")
	}
	cfg.Knowledge = nil
	cfg, e = s.PutReplySettings(ctx, owner, cfg)
	must(e)
	if !call("resource_search", SearchInput{Ref: public, Query: "grounded"}).IsError {
		t.Fatal("removed knowledge still accessible")
	}
	cfg.Knowledge = []core.Ref{public}
	cfg, e = s.PutReplySettings(ctx, owner, cfg)
	must(e)
	first, e := s.Message(ctx, owner, id, "one-message")
	must(e)
	must(s.SaveRun(ctx, core.Run{ID: first.ID, Principal: route.Principal, Operation: "incoming", OriginMessage: first.ID, Title: "Metadata settlement", Sources: []core.Ref{public}}))
	must(s.MessageDisposition(ctx, route.Principal, id, "one-message", "ignored"))
	must(s.SetGrant(ctx, owner, core.Grant{ResourceID: public.ResourceID, ProjectID: id, Sections: []string{}}))
	control, e := s.IncomingForRun(ctx, first.ID)
	must(e)
	if control.State != "ignored" {
		t.Fatal("state read lost binding")
	}
	if _, e = s.Run(ctx, route.Principal, first.ID); e == nil {
		t.Fatal("metadata read bypassed business source gate")
	}
	req, _ := http.NewRequest("POST", apiHTTP.URL+"/messages/settle", strings.NewReader(`{"runId":"`+first.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 40))
	req.Header.Set("X-Workbench-Kind", "owner")
	req.Header.Set("X-Workbench-Session", "owner-settlement")
	res, e := http.DefaultClient.Do(req)
	must(e)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("withdrawn source blocked completed message settlement", res.StatusCode)
	}
	if external() == 200 {
		t.Fatal("revoked public source still allowed")
	}
	broken := incoming
	broken.EventID = "broken-source"
	broken.Text = "The question must still be saved after revoke"
	stored, e := s.AcceptMessage(ctx, broken)
	must(e)
	if stored.State != "handoff" {
		t.Fatal("source failure did not hand off", stored.State)
	}
	_, e = s.AcceptMessage(ctx, broken)
	must(e)
	history, e := s.ChannelHistory(ctx, owner, route.Principal.SessionID)
	must(e)
	if len(history) < 2 {
		t.Fatal("channel history missing paused input")
	}
	if _, e = s.ChannelHistory(ctx, route.Principal, route.Principal.SessionID); e == nil {
		t.Fatal("customer read owner-only channel audit")
	}
	tools, e := session.ListTools(ctx, nil)
	must(e)
	for _, tool := range tools.Tools {
		if tool.Name == "message_prepare" || tool.Name == "batch_generate" || tool.Name == "task_delegate" {
			t.Fatal("customer got project operation", tool.Name)
		}
	}
}
