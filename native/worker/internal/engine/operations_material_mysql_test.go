package engine

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"workbench-native/internal/core"
)

func TestMySQLOperationsMaterialsResumeCompletedPrefix(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	s, e := core.Open(ctx, dsn)
	must(e)
	defer s.DB.Close()
	must(s.Migrate(ctx))
	id := uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: id + "o"}
	p := core.Principal{Kind: "project", ProjectID: id, SessionID: id + "s"}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "material interruption"}))
	calls := map[string]int{}
	failSecond := true
	server := mcp.NewServer(&mcp.Implementation{Name: "isolated-materials", Version: "1"}, nil)
	type input struct {
		IDs          []string            `json:"ids"`
		Drafts       []map[string]string `json:"drafts"`
		ConnectionID string              `json:"connectionId"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "operations_material_versions"}, func(_ context.Context, _ *mcp.CallToolRequest, in input) (*mcp.CallToolResult, map[string]any, error) {
		if len(in.Drafts) != 1 || len(in.IDs) != 0 {
			return nil, nil, errors.New("one original card required")
		}
		card := in.Drafts[0]
		calls[card["page"]]++
		if card["page"] == "pages/report?id=second" && failSecond {
			failSecond = false
			return nil, nil, errors.New("interrupted second preparation")
		}
		materialID := "101"
		if card["page"] == "pages/report?id=second" {
			materialID = "102"
		}
		return nil, map[string]any{"cards": []core.OperationsMaterial{{ID: materialID, Title: card["title"], AppID: card["appId"], Page: card["page"], Version: 1, Cover: strings.Repeat("a", 64)}}}, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer httpServer.Close()
	t.Setenv("WORKBENCH_CONNECTOR_TEST", strings.Repeat("k", 40))
	transport := core.Transport{ID: id + "crm", Name: "fixture", Kind: "crm-cli", Endpoint: httpServer.URL, TokenEnv: "WORKBENCH_CONNECTOR_TEST", Enabled: true, Projects: []string{id}}
	must(s.PutTransport(ctx, owner, transport))
	_, e = s.DB.ExecContext(ctx, `INSERT INTO wb_settings VALUES(?,?)`, "operations-"+id, `{"transport":"`+transport.ID+`"}`)
	must(e)
	run := core.Run{ID: uuid.NewString(), Principal: p, Operation: "operations_package", Title: "frozen draft cards", Shared: map[string]any{"local": true}}
	must(s.SaveRun(ctx, run))
	rows := []core.OperationsReviewRow{{ID: "first", Materials: []core.OperationsMaterial{{AppID: "wx-fixture", Title: "report", Page: "pages/report?id=first"}}}, {ID: "second", Materials: []core.OperationsMaterial{{AppID: "wx-fixture", Title: "report", Page: "pages/report?id=second"}}}}
	a := Activities{Store: s}
	if e = a.bindOperationsMaterialVersions(ctx, run, rows); e == nil {
		t.Fatal("interruption was hidden")
	}
	must(a.bindOperationsMaterialVersions(ctx, run, rows))
	if calls["pages/report?id=first"] != 1 || calls["pages/report?id=second"] != 2 || rows[0].Materials[0].ID != "101" || rows[1].Materials[0].ID != "102" {
		t.Fatal("completed card was repeated or paired incorrectly", calls, rows)
	}
}
