package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/types/known/durationpb"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func TestMySQLOperationsReviewLocalAndFrozen(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") && !strings.Contains(dsn, "/wbop_") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	p := operationsPrincipal(id)
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "local operations review"}))
	api := &Server{Store: s, Token: strings.Repeat("x", 40)}
	input := core.Ref{ResourceID: id + "-resource", Section: "text"}
	must(s.PutResource(ctx, owner, core.Resource{ID: input.ResourceID, Name: "原工作包", OwnerProject: id, Sections: []core.Section{{ID: "text", Name: "正文", Fields: []string{"text"}}}}))
	cfg := operationsConfig{ProjectID: id, ReadSource: true, Input: input, Staff: "20", Transport: "crm-test"}
	must(api.saveOperations(ctx, cfg))
	t.Setenv("WORKBENCH_OPERATIONS_ORIGINAL_STAFF_ID", "20")
	t.Setenv("WORKBENCH_OPERATIONS_STAFF_NAME", "实际运营员工")
	t.Setenv("WORKBENCH_OPERATIONS_BATCH_STRATEGY", "hxc:onboarding")
	parent := core.Run{ID: id + "-daily", Title: "新人承接", Principal: p, Operation: "operations_package", ExplicitInputs: true,
		Destination: input.ResourceID, Shared: map[string]any{"day": operationsDay(time.Now()), "strategy": "hxc:onboarding"}}
	must(s.SaveRun(ctx, parent))
	draft := core.OperationsReviewRow{Key: "one", ID: "private-profile", Customer: "987654321", Nickname: "客户甲",
		Reason: "原策略 D2", Message: "旧稿", Editable: true, Slot: core.Digest(id)}
	review := core.OperationsReview{Run: "copybatch-" + strings.Repeat("1", 32), Digest: core.Digest("original"), Week: "2026-W46", Package: "weekpkg-source"}
	must(s.PublishOperationsReview(ctx, parent.ID, review, []core.OperationsReviewRow{draft}))
	items, err := s.Items(ctx, p, parent.ID, "")
	must(err)
	if len(items) != 0 {
		t.Fatal("generic run reader exposed connector-only review data")
	}
	get := func(url string) string {
		t.Helper()
		r := httptest.NewRequest("GET", url, nil)
		r.Header.Set("Authorization", "Bearer "+api.Token)
		r.Header.Set("X-Workbench-Kind", p.Kind)
		r.Header.Set("X-Workbench-Session", p.SessionID)
		r.Header.Set("X-Workbench-Project", p.ProjectID)
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("GET %s: %d %s", url, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	today := "/operations/source-today"
	if v := get(today); !strings.Contains(v, parent.ID) {
		t.Fatal(v)
	}
	if v := get(today + "?daily=" + parent.ID); !strings.Contains(v, "旧稿") || !strings.Contains(v, "客户甲") || strings.Contains(v, draft.Customer) {
		t.Fatal(v)
	}
	before, err := s.OperationsReview(ctx, parent.ID)
	must(err)
	oldVersion := core.OperationsReviewVersion(before, cfg.Transport, cfg.Staff, "实际运营员工")
	must(s.BeginOperationsEdit(ctx, parent.ID, "edit-id", review.Digest, draft.Key, draft.Revision))
	if err = s.ConfirmOperationsReview(ctx, parent, "stale", oldVersion, cfg.Staff, cfg.Transport, "实际运营员工", []string{"one"}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("confirmation raced edit: %v", err)
	}
	draft.Message, draft.Revision, review.Edit = "最后保存的正文", 1, "edit-id"
	review.Digest = core.Digest("edited")
	must(s.PublishOperationsReview(ctx, parent.ID, review, []core.OperationsReviewRow{draft}))
	after, err := s.OperationsReview(ctx, parent.ID)
	must(err)
	version := core.OperationsReviewVersion(after, cfg.Transport, cfg.Staff, "实际运营员工")
	confirmation := core.Run{ID: id + "-confirm", ParentRunID: parent.ID, Principal: p, Title: "审核确认", Operation: "operations_batch_confirm", ExplicitInputs: true}
	must(s.SaveRun(ctx, confirmation))
	must(s.ConfirmOperationsReview(ctx, parent, confirmation.ID, version, cfg.Staff, cfg.Transport, "实际运营员工", []string{"one"}))
	must(s.ConfirmOperationsReview(ctx, parent, confirmation.ID, version, cfg.Staff, cfg.Transport, "实际运营员工", []string{"one"}))
	_, manifest, err := s.OperationsBatchManifest(ctx, confirmation.ID)
	must(err)
	if len(manifest) != 1 || manifest[0].Message != draft.Message || manifest[0].Customer != draft.Customer {
		t.Fatal(manifest)
	}
	must(s.SaveOperationsBatchResult(ctx, confirmation.ID, core.OperationsBatchStatus{Plan: "43", Entries: map[string]core.OperationsBatchReceipt{
		"one": {State: "accepted"},
	}}))
	must(s.SaveOperationsBatchResult(ctx, confirmation.ID, core.OperationsBatchStatus{Entries: map[string]core.OperationsBatchReceipt{"one": {State: "unknown"}}}))
	_, receipt, err := api.localCRMReceipts(ctx, p, confirmation.ID)
	must(err)
	if receipt.Receipt != "43" || receipt.Batch.Entries["one"].State != "accepted" || receipt.Batch.Entries["one"].At == "" {
		t.Fatal(receipt)
	}
	// The server has no Temporal client in this test. Settled rows must not
	// attempt to start a receipt workflow, including during the sweep.
	must((&engine.Activities{Store: s}).QueueOperationsBatchReceipts(ctx))
	if v := get(today + "?daily=" + parent.ID); !strings.Contains(v, "已发送") || !strings.Contains(v, "CRM 已受理。") ||
		!strings.Contains(v, draft.Message) || strings.Contains(v, draft.Customer) {
		t.Fatal(v)
	}
	if v := get(today); strings.Contains(v, parent.ID) {
		t.Fatal(v)
	}
	if v := get(today + "?history=1"); !strings.Contains(v, parent.ID) {
		t.Fatal(v)
	}
	newDay := parent
	newDay.ID += "-next"
	must(s.SaveRun(ctx, newDay))
	review.Edit = ""
	must(s.PublishOperationsReview(ctx, newDay.ID, review, []core.OperationsReviewRow{draft}))
	newHead, err := s.OperationsReview(ctx, newDay.ID)
	must(err)
	if newHead.Count != 0 {
		b, _ := json.Marshal(newHead)
		t.Fatalf("same action entered a second run: %s", b)
	}
}

func TestRealTemporalOperationsBatchUsesOneConfirmedManifest(t *testing.T) {
	dsn, address := os.Getenv("WORKBENCH_TEST_DSN"), os.Getenv("WORKBENCH_TEST_TEMPORAL")
	if dsn == "" || address == "" {
		t.Skip("isolated MySQL and Temporal required")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") && !strings.Contains(dsn, "/wbop_") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	namespace := "opsbatch-" + id
	tc, err := client.Dial(client.Options{HostPort: address, Namespace: namespace})
	must(err)
	defer tc.Close()
	_, err = tc.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{
		Namespace: namespace, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	must(err)

	p := operationsPrincipal(id)
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "isolated batch operations"}))
	api := &Server{Store: s, Temporal: tc, Token: strings.Repeat("t", 40)}
	t.Setenv("WORKBENCH_OPERATIONS_ORIGINAL_STAFF_ID", "20")
	t.Setenv("WORKBENCH_OPERATIONS_ORIGINAL_SEND", "0")
	t.Setenv("WORKBENCH_OPERATIONS_STAFF_NAME", "测试员工")
	t.Setenv("WORKBENCH_OPERATIONS_BATCH_STRATEGY", "hxc:onboarding")
	t.Setenv("WORKBENCH_CONNECTOR_OP_BATCH_TEST", strings.Repeat("k", 40))
	t.Setenv("WORKBENCH_SOURCE_OP_BATCH_TEST", strings.Repeat("s", 40))

	head := core.OperationsReview{Run: "copybatch-" + strings.Repeat("a", 32), Digest: core.Digest("source"),
		IdentityVersion: core.Digest("archive"), Week: "2026-W46", Package: "weekpkg-isolated"}
	sourceServer := mcp.NewServer(&mcp.Implementation{Name: "batch-source", Version: "1"}, nil)
	type sourceInput struct {
		View   string            `json:"view"`
		Params map[string]string `json:"params"`
	}
	var withdrawDuringRead atomic.Bool
	mcp.AddTool(sourceServer, &mcp.Tool{Name: "operations_read"}, func(ctx context.Context, _ *mcp.CallToolRequest, input sourceInput) (*mcp.CallToolResult, map[string]any, error) {
		if input.View != "package_review" || input.Params["run"] != head.Run {
			return nil, nil, errors.New("unexpected source request")
		}
		if withdrawDuringRead.Swap(false) {
			cfg, err := api.readOperations(ctx, id)
			if err != nil {
				return nil, nil, err
			}
			cfg.ReadSource = false
			if err = api.saveOperations(ctx, cfg); err != nil {
				return nil, nil, err
			}
		}
		return nil, map[string]any{"state": "ready", "digest": head.Digest, "identityVersion": head.IdentityVersion,
			"exclusive": true, "activeKeys": []string{"one", "two"}}, nil
	})
	source := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return sourceServer }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer source.Close()

	var sends atomic.Int32
	crmServer := mcp.NewServer(&mcp.Implementation{Name: "batch-crm", Version: "1"}, nil)
	type sendInput struct {
		ConnectionID string `json:"connectionId"`
		RunID        string `json:"runId"`
	}
	mcp.AddTool(crmServer, &mcp.Tool{Name: "operations_batch_send"}, func(_ context.Context, _ *mcp.CallToolRequest, input sendInput) (*mcp.CallToolResult, core.OperationsBatchStatus, error) {
		req := httptest.NewRequest("GET", "/operations/batch-manifest?id="+input.RunID+"&connection="+input.ConnectionID, nil)
		req.Header.Set("Authorization", "Bearer "+api.Token)
		req.Header.Set("X-Workbench-Kind", "owner")
		req.Header.Set("X-Workbench-Session", "channel-connector")
		out := httptest.NewRecorder()
		api.Handler().ServeHTTP(out, req)
		var frozen struct {
			Staff string
			Rows  []core.OperationsBatchItem
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &frozen) != nil || frozen.Staff != "20" ||
			len(frozen.Rows) != 2 || frozen.Rows[0].Customer != "100" || frozen.Rows[0].Message != "已确认稿" ||
			len(frozen.Rows[0].Materials) != 1 || frozen.Rows[0].Materials[0].ID != "1070" || sends.Add(1) != 1 {
			return nil, core.OperationsBatchStatus{}, errors.New("batch manifest changed or was submitted twice")
		}
		return nil, core.OperationsBatchStatus{Plan: "42", Entries: map[string]core.OperationsBatchReceipt{
			"one": {State: "accepted", At: "2026-11-11T10:05:00+08:00"}, "two": {State: "failed", Error: "CRM 明确拒绝"},
		}}, nil
	})
	crm := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return crmServer }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer crm.Close()

	sourceID, connectionID := id+"-resource", id+"-source"
	must(s.PutConnection(ctx, owner, core.Connection{ID: connectionID, Name: "isolated source", Kind: "mcp",
		Location: source.URL, SecretEnv: "WORKBENCH_SOURCE_OP_BATCH_TEST", SourceTool: "operations_refresh"}))
	must(s.PutResource(ctx, owner, core.Resource{ID: sourceID, Name: "isolated source", OwnerProject: id, ConnectionID: connectionID,
		Sections: []core.Section{{ID: "text", Name: "正文", Fields: []string{"text"}}}}))
	transport := id + "-crm"
	must(s.PutTransport(ctx, owner, core.Transport{ID: transport, Name: "isolated CRM", Kind: "crm-cli", Enabled: true,
		Projects: []string{id}, Endpoint: crm.URL, TokenEnv: "WORKBENCH_CONNECTOR_OP_BATCH_TEST"}))
	cfg := operationsConfig{ProjectID: id, ReadSource: true, Input: core.Ref{ResourceID: sourceID, Section: "text"}, Staff: "20", Transport: transport}
	must(api.saveOperations(ctx, cfg))
	parent := core.Run{ID: id + "-daily", Title: "新人承接", Principal: p, Operation: "operations_package", ExplicitInputs: true,
		Destination: sourceID, Shared: map[string]any{"day": operationsDay(time.Now()), "strategy": "hxc:onboarding"}}
	must(s.SaveRun(ctx, parent))
	must(s.PublishOperationsReview(ctx, parent.ID, head, []core.OperationsReviewRow{
		{Key: "one", ID: "profile-one", Customer: "100", Message: "已确认稿", Slot: core.Digest(id + "/1"), Materials: []core.OperationsMaterial{{ID: "1070", Title: "小程序", AppID: "wx-test", Page: "pages/start"}}},
		{Key: "two", ID: "profile-two", Customer: "101", Message: "独立话术", Slot: core.Digest(id + "/2")},
	}))
	published, err := s.OperationsReview(ctx, parent.ID)
	must(err)
	activities := &engine.Activities{Store: s, Temporal: tc}
	batch := worker.New(tc, engine.BatchQueue, worker.Options{})
	batch.RegisterWorkflow(engine.WriteTask)
	batch.RegisterWorkflow(engine.ConfirmOperationsBatch)
	batch.RegisterActivity(activities)
	must(batch.Start())
	defer batch.Stop()
	interactive := worker.New(tc, engine.InteractiveQueue, worker.Options{})
	interactive.RegisterWorkflow(engine.OperationsBatchDelivery)
	interactive.RegisterActivity(activities)
	must(interactive.Start())
	defer interactive.Stop()

	confirm := func() string {
		t.Helper()
		raw, err := json.Marshal(map[string]string{"daily": parent.ID, "reviewVersion": core.OperationsReviewVersion(published, transport, "20", "测试员工")})
		must(err)
		req := httptest.NewRequest("POST", "/operations/source-confirm", bytes.NewReader(raw)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+api.Token)
		req.Header.Set("X-Workbench-Kind", p.Kind)
		req.Header.Set("X-Workbench-Session", p.SessionID)
		req.Header.Set("X-Workbench-Project", p.ProjectID)
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, req)
		var ref RunRef
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ref) != nil || ref.RunID == "" {
			t.Fatalf("confirmation %d: %s", w.Code, w.Body.String())
		}
		return ref.RunID
	}
	ref := confirm()
	must(tc.GetWorkflow(ctx, ref, "").Get(ctx, nil))
	must(tc.GetWorkflow(ctx, ref+"-delivery", "").Get(ctx, nil))
	if confirm() != ref || sends.Load() != 1 {
		t.Fatal("duplicate confirmation did not reuse original batch")
	}
	result, err := s.OperationsBatchResult(ctx, ref)
	must(err)
	if result.Plan != "42" || result.Entries["one"].State != "accepted" || result.Entries["two"].State != "failed" || !result.Settled(2) {
		t.Fatal(result)
	}
	allowed, err := api.operationsBatchWriteAllowed(ctx, ref, transport)
	must(err)
	if !allowed {
		t.Fatal("unchanged confirmed batch unexpectedly blocked")
	}
	withdrawDuringRead.Store(true)
	allowed, err = api.operationsBatchWriteAllowed(ctx, ref, transport)
	must(err)
	if allowed || withdrawDuringRead.Load() {
		t.Fatal("source-read permission withdrawn during the final read did not block submission")
	}
}
