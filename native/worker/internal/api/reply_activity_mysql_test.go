package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workbench-native/internal/core"
)

func TestMySQLReplyActivityUsesOnlyBoundInbound(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("real MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	store, err := core.Open(ctx, dsn)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(err)
	defer store.DB.Close()
	must(store.Migrate(ctx))

	id := uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	personal := core.Principal{Kind: "personal", SessionID: id + "-personal"}
	project := core.Principal{Kind: "project", ProjectID: id + "-reply", SessionID: id + "-project"}
	other := core.Principal{Kind: "project", ProjectID: id + "-other", SessionID: id + "-other-project"}
	for _, p := range []core.Principal{project, other} {
		must(store.PutProject(ctx, owner, core.Project{ID: p.ProjectID, Name: "Reply activity fixture"}))
	}
	transport := id + "-channel"
	must(store.PutTransport(ctx, owner, core.Transport{
		ID: transport, Name: "test", Endpoint: "http://127.0.0.1:1/mcp",
		TokenEnv: "WORKBENCH_CONNECTOR_TEST", Enabled: true, Projects: []string{project.ProjectID, other.ProjectID},
	}))
	_, err = store.PutReplySettings(ctx, owner, core.ReplySettings{
		ProjectID: project.ProjectID, ConnectionID: transport, ExpertSkill: "customer-reply", Enabled: true,
	})
	must(err)

	from := time.Date(2026, 11, 4, 13, 14, 23, 334_000_000, time.UTC)
	to := from.Add(7 * 24 * time.Hour)
	route := func(p core.Principal, key string) string {
		r := core.MessageRoute{ID: id + key, ConnectionID: transport, ActorID: key, ConversationID: key,
			Enabled: true, AutoReply: true, Principal: p}
		must(store.PutRoute(ctx, owner, r))
		return r.ID
	}
	first := route(project, "-one")
	project.SessionID = id + "-second"
	second := route(project, "-two")
	excluded := route(other, "-other")
	record := func(key, routeID, projectID, kind, state, text string, at time.Time) {
		body, e := json.Marshal(map[string]any{
			"id": key, "routeId": routeID, "kind": kind, "state": state, "text": text, "createdAt": at,
			"principal": map[string]string{"projectId": projectID},
		})
		must(e)
		_, e = store.DB.ExecContext(ctx, `INSERT INTO wb_messages(connection_id,event_id,digest,body) VALUES (?,?,?,?)`,
			transport, key, strings.Repeat("0", 64), body)
		must(e)
	}
	record("before", first, project.ProjectID, "", "received", "before", from.Add(-time.Microsecond))
	record("at-start", first, project.ProjectID, "", "reply_accepted", "unable to sign in", from)
	record("human", first, project.ProjectID, "human", "human", "human reply", from.Add(time.Minute))
	record("foreign", excluded, other.ProjectID, "", "received", "other project", from.Add(time.Minute))
	record("unrouted", "", "", "", "unrouted", "unrouted", from.Add(time.Minute))
	record("at-end", first, project.ProjectID, "", "received", "at end", to)
	out, err := store.ReadReplyActivity(ctx, personal, project.ProjectID, from, to)
	must(err)
	if out.Inbound != 1 || out.Conversations != 1 || out.Coverage != "insufficient_conversations" || len(out.Texts) != 0 {
		t.Fatalf("single-conversation activity: %+v", out)
	}

	record("second", second, project.ProjectID, "", "handoff", "access denied", from.Add(time.Second))
	record("missing-text", second, project.ProjectID, "", "paused", "", from.Add(2*time.Second))
	a := &Server{Store: store}
	openMCP := func(p core.Principal) *mcp.ClientSession {
		t.Helper()
		left, right := mcp.NewInMemoryTransports()
		server, e := a.MCP(p).Connect(ctx, left, nil)
		must(e)
		t.Cleanup(func() { _ = server.Close() })
		session, e := mcp.NewClient(&mcp.Implementation{Name: "activity-test"}, nil).Connect(ctx, right, nil)
		must(e)
		t.Cleanup(func() { _ = session.Close() })
		return session
	}
	session := openMCP(personal)
	in := replyActivityInput{ProjectID: project.ProjectID, From: from.Format(time.RFC3339Nano), To: to.Format(time.RFC3339Nano)}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "reply_activity", Arguments: in})
	must(err)
	if result.IsError {
		t.Fatal(result.Content)
	}
	raw, err := json.Marshal(result.StructuredContent)
	must(err)
	must(json.Unmarshal(raw, &out))
	if out.Inbound != 3 || out.Conversations != 2 || out.WithText != 2 || out.TextConversations != 2 ||
		out.Coverage != "complete" || len(out.Texts) != 2 || out.ByState["handoff"] != 1 ||
		out.Texts[0].Conversation == out.Texts[1].Conversation {
		t.Fatalf("unexpected scoped MCP result: %s", raw)
	}
	refs, err := store.SessionSources(ctx, personal)
	must(err)
	if len(refs) != 1 || !errors.Is(core.AuthorizeAll(ctx, store, other, refs), core.ErrDenied) {
		t.Fatal("personal-only evidence escaped project isolation", refs)
	}
	for _, p := range []core.Principal{owner, project, other} {
		tools, e := openMCP(p).ListTools(ctx, nil)
		must(e)
		for _, tool := range tools.Tools {
			if tool.Name == "reply_activity" {
				t.Fatalf("%s received personal-only tool", p.Kind)
			}
		}
	}
}
