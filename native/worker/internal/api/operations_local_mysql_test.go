package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func TestMySQLLocalOperationsSecondCycleConfirmAndCancel(t *testing.T) {
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
	owner := core.Principal{Kind: "owner", SessionID: id + "-o"}
	p := operationsPrincipal(id)
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "two local operations cycles"}))
	api := &Server{Store: s}
	host := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return api.MCP(p)
	}, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer host.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "local-receipt-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: host.URL}, nil)
	must(e)
	defer session.Close()
	must(api.saveOperations(ctx, operationsConfig{ProjectID: id, ReadSource: true, Staff: "20", Transport: "crm-test"}))
	raw := id + "-raw"
	selected := id + "-selected"
	for _, r := range []string{raw, selected} {
		must(s.PutResource(ctx, owner, core.Resource{ID: r, Name: r, OwnerProject: id, Sections: []core.Section{{ID: "main", Fields: []string{"text"}}}}))
	}
	var previous core.Ref
	for _, version := range []string{"v1", "v2"} {
		source := core.Ref{ResourceID: raw, Version: version, Section: "main"}
		must(s.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: raw, Version: version}))
		must(s.Stage(ctx, p, raw, version, []core.Record{{ID: "person", Fields: map[string]any{"text": "facts"}}}))
		_, e = s.Publish(ctx, p, raw, version, 1)
		must(e)
		_, e = s.Read(ctx, p, source, "", 1)
		must(e)
		chosen := core.Ref{ResourceID: selected, Version: version, Section: "main"}
		selection := core.Run{ID: id + "-select-" + version, Principal: p, Operation: "strategy_select", ExplicitInputs: true, Sources: []core.Ref{source}, Destination: selected, Title: "fixed selection"}
		must(s.SaveRun(ctx, selection))
		stored, e := s.Run(ctx, p, selection.ID)
		must(e)
		if len(stored.Lineage) != 0 {
			t.Fatal("previous session reads leaked into fixed selection")
		}
		must(s.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: selected, Version: version, Dependencies: []core.Ref{source}}))
		must(s.Stage(ctx, p, selected, version, []core.Record{{ID: "person", Fields: map[string]any{"text": "selection"}}}))
		_, e = s.Publish(ctx, p, selected, version, 1)
		must(e)
		if stale, e := s.IsStale(ctx, chosen); e != nil || stale {
			t.Fatal("new round is stale", stale, e)
		}
		if previous.ResourceID != "" {
			if stale, e := s.IsStale(ctx, previous); e != nil || !stale {
				t.Fatal("old selection remained current", stale, e)
			}
		}
		previous = chosen
		parent := core.Run{ID: id + "-review-" + version, Principal: p, Operation: "operations_package", ExplicitInputs: true, Title: "local review", Sources: []core.Ref{chosen}, Operations: &core.OperationsSpec{Review: &core.OperationsDrafts{Selection: chosen}}, Shared: map[string]any{"local": true}}
		must(s.SaveRun(ctx, parent))
		row := core.OperationsReviewRow{Key: "person", ID: "person", Customer: "123", Message: "fixed approved text", Editable: true, Revision: 1, Slot: core.Digest([]string{id, version})}
		must(s.PublishLocalOperationsReview(ctx, parent.ID, core.OperationsReview{Digest: core.Digest(row), Exclusive: true}, []core.OperationsReviewRow{row}, core.ItemResult{ID: "original-roster", State: "succeeded", InputHash: core.Digest(version), Text: "{}"}))
		head, e := s.OperationsReview(ctx, parent.ID)
		must(e)
		pending, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "crm_receipts", Arguments: map[string]any{"runId": parent.ID}})
		must(e)
		if pending.IsError {
			t.Fatal(pending.Content)
		}
		wire, e := json.Marshal(pending.StructuredContent)
		must(e)
		var receipt CRMReceiptResult
		must(json.Unmarshal(wire, &receipt))
		if receipt.State != "awaiting_confirmation" || receipt.ReviewRunID != parent.ID || receipt.Target == nil || receipt.Receipt != "" {
			t.Fatalf("pending batch receipt violated MCP contract: %#v", receipt)
		}
		confirmation := core.Run{ID: id + "-confirm-" + version, Principal: p, ParentRunID: parent.ID, Operation: "operations_batch_confirm", ExplicitInputs: true, Title: "confirm"}
		must(s.SaveRun(ctx, confirmation))
		token := core.OperationsReviewVersion(head, "crm-test", "20", "fixture")
		if version == "v1" {
			must(s.ConfirmOperationsReview(ctx, parent, confirmation.ID, token, "20", "crm-test", "fixture", []string{row.Key}))
			must(s.ConfirmOperationsReview(ctx, parent, confirmation.ID, token, "20", "crm-test", "fixture", []string{row.Key}))
			_, manifest, e := s.OperationsBatchManifest(ctx, confirmation.ID)
			must(e)
			if len(manifest) != 1 || manifest[0].Message != row.Message {
				t.Fatal(manifest)
			}
		} else {
			must(s.StopOperationsBatch(ctx, p, confirmation.ID))
			if e = s.ConfirmOperationsReview(ctx, parent, confirmation.ID, token, "20", "crm-test", "fixture", []string{row.Key}); !errors.Is(e, core.ErrConflict) {
				t.Fatal("cancelled confirmation acquired delivery", e)
			}
		}
	}
}

func TestMySQLNewcomerConfirmationUsesLocalBatch(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := core.Open(ctx, dsn)
	must(err)
	defer s.DB.Close()
	must(s.Migrate(ctx))
	id := uuid.NewString()
	p := operationsPrincipal(id)
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "newcomer confirmation fixture"}))
	input := core.Ref{ResourceID: id + "-input", Section: "main"}
	must(s.PutResource(ctx, owner, core.Resource{ID: input.ResourceID, Name: "review", OwnerProject: id, Sections: []core.Section{{ID: "main", Fields: []string{"text"}}}}))
	const strategy = "hxc:onboarding"
	cycle := &engine.OperationsCycleInput{StrategyKeys: []string{"hxc:ongoing"}, AdditionalBatch: &engine.OperationsCycleBatch{
		Strategy: strategy, SelectedDestination: "newcomer-selected", DraftDestination: "newcomer-drafts",
	}}
	cfg := operationsConfig{ProjectID: id, ReadSource: true, Input: input, Staff: "20", Transport: "crm-test", Cycle: cycle}
	t.Setenv("WORKBENCH_OPERATIONS_BATCH_STRATEGY", "hxc:ongoing")
	t.Setenv("WORKBENCH_OPERATIONS_ADDITIONAL_BATCH_STRATEGY", strategy)
	t.Setenv("WORKBENCH_OPERATIONS_ORIGINAL_STAFF_ID", cfg.Staff)
	t.Setenv("WORKBENCH_OPERATIONS_STAFF_NAME", "隔离员工")
	t.Setenv("WORKBENCH_OPERATIONS_ORIGINAL_SEND", "0")
	tc := &mocks.Client{}
	api := &Server{Store: s, Temporal: tc, Token: strings.Repeat("z", 40)}
	must(api.saveOperations(ctx, cfg))
	parent := core.Run{ID: id + "-review", Principal: p, Operation: "operations_package", ExplicitInputs: true, Title: "原策略整批稿件", Destination: input.ResourceID,
		Shared:     map[string]any{"local": true, "day": operationsDay(time.Now()), "strategy": strategy},
		Operations: &core.OperationsSpec{Review: &core.OperationsDrafts{Selection: core.Ref{ResourceID: "newcomer-selected"}, Drafts: []core.Ref{{ResourceID: "newcomer-drafts"}}}}}
	must(s.SaveRun(ctx, parent))
	row := core.OperationsReviewRow{Key: "one", ID: "one", Customer: "123", Message: "隔离稿件", Reason: "D2", Revision: 1, Editable: true, Slot: core.Digest(id)}
	must(s.PublishLocalOperationsReview(ctx, parent.ID, core.OperationsReview{Digest: core.Digest(row), Exclusive: true}, []core.OperationsReviewRow{row}, core.ItemResult{ID: "original-roster", State: "succeeded", InputHash: core.Digest(id), Text: "{}"}))
	call := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+api.Token)
		req.Header.Set("X-Workbench-Kind", p.Kind)
		req.Header.Set("X-Workbench-Project", p.ProjectID)
		req.Header.Set("X-Workbench-Session", p.SessionID)
		out := httptest.NewRecorder()
		api.Handler().ServeHTTP(out, req)
		return out
	}
	view := call(http.MethodGet, "/operations/source-today?daily="+parent.ID, nil)
	var draft struct {
		SendEnabled   bool
		ReviewVersion string
	}
	if view.Code != 200 || json.Unmarshal(view.Body.Bytes(), &draft) != nil || !draft.SendEnabled || draft.ReviewVersion == "" {
		t.Fatalf("review unavailable: %d %s", view.Code, view.Body.String())
	}
	confirm := uuid.NewSHA1(uuid.NameSpaceURL, []byte(id+"/operations-batch/"+parent.ID+"/"+draft.ReviewVersion)).String()
	tc.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(o client.StartWorkflowOptions) bool { return o.ID == confirm }), mock.Anything,
		engine.WriteInput{RunID: confirm, ProjectID: id, Operation: "operations_batch_confirm"}).Return(&mocks.WorkflowRun{}, nil).Once()
	body, _ := json.Marshal(map[string]string{"daily": parent.ID, "reviewVersion": draft.ReviewVersion})
	if response := call(http.MethodPost, "/operations/source-confirm", body); response.Code != 200 || !strings.Contains(response.Body.String(), confirm) {
		t.Fatalf("confirmation entry failed: %d %s", response.Code, response.Body.String())
	}
	tc.AssertExpectations(t)
	cycle.AdditionalBatch = nil
	must(api.saveOperations(ctx, cfg))
	if response := call(http.MethodPost, "/operations/source-confirm", body); response.Code != 403 {
		t.Fatalf("withdrawn strategy could confirm: %d %s", response.Code, response.Body.String())
	}
}
