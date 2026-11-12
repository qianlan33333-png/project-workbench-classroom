package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/mocks"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type feishuOwnerHost struct{ engine.AgentHost }

func (feishuOwnerHost) OwnerRequest(context.Context, core.Principal) (engine.OwnerRequest, error) {
	return engine.OwnerRequest{ID: "owner-message", Text: "请检查项目状态"}, nil
}

func TestMySQLFeishuCLIResultAndDelegationProvenance(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("real MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
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

	id := uuid.NewString()
	personal := core.Principal{Kind: "personal", SessionID: id + "-superagent"}
	project := core.Principal{Kind: "project", SessionID: id + "-target", ProjectID: id}
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	must(store.PutProject(ctx, owner, core.Project{ID: id, Name: "CLI test project"}))
	// The MCP server's normal personal-principal check reads the same setting.
	var previous []byte
	err = store.DB.QueryRowContext(ctx, "SELECT body FROM wb_settings WHERE id='personal'").Scan(&previous)
	if err != nil && !core.IsMissing(err) {
		t.Fatal(err)
	}
	config, err := json.Marshal(PersonalConfig{SessionID: personal.SessionID})
	must(err)
	_, err = store.DB.ExecContext(ctx, "INSERT INTO wb_settings VALUES('personal',?) ON DUPLICATE KEY UPDATE body=VALUES(body)", config)
	must(err)
	defer func() {
		if previous == nil {
			_, err = store.DB.ExecContext(ctx, "DELETE FROM wb_settings WHERE id='personal'")
		} else {
			_, err = store.DB.ExecContext(ctx, "UPDATE wb_settings SET body=? WHERE id='personal'", previous)
		}
		must(err)
	}()

	const payload = `{"document":{"id":9007199254740993,"text":"private Feishu evidence"}}`
	token := strings.Repeat("test-", 8)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/links/cli/read" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("invalid connector request")
			http.Error(w, "bad request", 400)
			return
		}
		var input struct {
			Operation string `json:"operation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Operation != "document" {
			t.Error("invalid operation", err)
			http.Error(w, "bad request", 400)
			return
		}
		_, _ = io.WriteString(w, payload)
	}))
	defer provider.Close()
	_, port, err := net.SplitHostPort(provider.Listener.Addr().String())
	must(err)
	t.Setenv("WORKBENCH_CONNECTOR_TOKEN", token)
	t.Setenv("WORKBENCH_CONNECTOR_PORT", port)

	temporal := &mocks.Client{}
	temporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
	a := &Server{Store: store, Host: feishuOwnerHost{}, Temporal: temporal}
	left, right := mcp.NewInMemoryTransports()
	s, err := a.MCP(personal).Connect(ctx, left, nil)
	must(err)
	defer s.Close()
	c, err := mcp.NewClient(&mcp.Implementation{Name: "feishu-test"}, nil).Connect(ctx, right, nil)
	must(err)
	defer c.Close()

	result, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "feishu_cli_read", Arguments: map[string]string{"operation": "document"}})
	must(err)
	if result.IsError || len(result.Content) != 1 || result.Content[0].(*mcp.TextContent).Text != `{"data":`+payload+"}" {
		t.Fatalf("unexpected MCP result: %+v", result)
	}
	refs, err := store.SessionSources(ctx, personal)
	must(err)
	if len(refs) != 1 || refs[0].ResourceID != "feishu-cli-user" ||
		!errors.Is(core.AuthorizeAll(ctx, store, project, refs), core.ErrDenied) {
		t.Fatalf("missing private provenance: %+v", refs)
	}
	must(core.AuthorizeAll(ctx, store, personal, refs))
	if err = store.SetGrant(ctx, owner, core.Grant{ResourceID: refs[0].ResourceID, ProjectID: id, Sections: []string{"*"}}); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("private provenance became shareable: %v", err)
	}
	delegate := func(key, instructions string) *mcp.CallToolResult {
		res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "task_delegate",
			Arguments: DelegateInput{OperationKey: key, ProjectID: id, Instructions: instructions}})
		must(err)
		return res
	}
	if res := delegate("private-copy", "private Feishu evidence"); !res.IsError {
		t.Fatal("model-authored instructions escaped into a project")
	}
	if res := delegate("original-owner-request", ""); res.IsError {
		t.Fatalf("direct owner request no longer dispatches: %+v", res.Content)
	}
	temporal.AssertExpectations(t)
}
