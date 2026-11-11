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
	"go.temporal.io/api/operatorservice/v1"
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
	initial, err := s.OperationsBatchResult(ctx, confirmation.ID)
	must(err)
	must(s.SaveOperationsBatchResult(ctx, confirmation.ID, core.OperationsBatchStatus{Entries: map[string]core.OperationsBatchReceipt{"one": {State: "unknown"}}}))
	_, receipt, err := api.localCRMReceipts(ctx, p, confirmation.ID)
	must(err)
	if receipt.Receipt != "43" || receipt.Batch.Entries["one"].State != "accepted" || receipt.Batch.Entries["one"].At == "" ||
		receipt.Batch.Entries["one"].At != initial.Entries["one"].At {
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
	delivered := core.OperationsBatchReceipt{State: "delivered", At: "2026-11-11T10:05:00+08:00"}
	must(s.SaveOperationsBatchResult(ctx, confirmation.ID, core.OperationsBatchStatus{Entries: map[string]core.OperationsBatchReceipt{"one": delivered}}))
	must(s.SaveOperationsBatchResult(ctx, confirmation.ID, core.OperationsBatchStatus{Entries: map[string]core.OperationsBatchReceipt{"one": {State: "accepted"}}}))
	history, err := s.OperationsBatchResult(ctx, confirmation.ID)
	must(err)
	if history.Entries["one"] != delivered {
		t.Fatal(history)
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
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 12*time.Second)
		defer stop()
		if _, err := tc.OperatorService().DeleteNamespace(cleanup, &operatorservice.DeleteNamespaceRequest{Namespace: namespace}); err != nil {
			t.Errorf("remove isolated Temporal namespace: %v", err)
		}
	}()

	p := operationsPrincipal(id)
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "isolated batch operations"}))
	api := &Server{Store: s, Temporal: tc, Token: strings.Repeat("t", 40)}
	t.Setenv("WORKBENCH_OPERATIONS_ORIGINAL_STAFF_ID", "20")
	t.Setenv("WORKBENCH_OPERATIONS_ORIGINAL_SEND", "0")
	t.Setenv("WORKBENCH_OPERATIONS_STAFF_NAME", "测试员工")
	t.Setenv("WORKBENCH_OPERATIONS_BATCH_STRATEGY", "")
	t.Setenv("WORKBENCH_OPERATIONS_ADDITIONAL_BATCH_STRATEGY", "")
	t.Setenv("WORKBENCH_CONNECTOR_OP_BATCH_TEST", strings.Repeat("k", 40))
	t.Setenv("WORKBENCH_SOURCE_OP_BATCH_TEST", strings.Repeat("s", 40))

	head := core.OperationsReview{Run: "copybatch-" + strings.Repeat("a", 32), Digest: core.Digest("source"),
		IdentityVersion: core.Digest("archive"), Week: "2026-W46", Package: "weekpkg-isolated"}
	var sourceCalls atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceCalls.Add(1)
		http.Error(w, "original source unavailable", http.StatusServiceUnavailable)
	}))
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
		allowed, err := api.operationsBatchWriteAllowed(ctx, input.RunID, input.ConnectionID)
		if err != nil || !allowed {
			return nil, core.OperationsBatchStatus{}, errors.New("saved batch was rechecked against its source")
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &frozen) != nil || frozen.Staff != "20" ||
			len(frozen.Rows) != 2 || frozen.Rows[0].Customer != "100" || frozen.Rows[0].Message != "已确认稿" ||
			len(frozen.Rows[0].Materials) != 1 || frozen.Rows[0].Materials[0].ID != "1070" {
			return nil, core.OperationsBatchStatus{}, errors.New("batch manifest changed")
		}
		sends.Add(1)
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
	cfg := operationsConfig{ProjectID: id, ReadSource: true, Input: core.Ref{ResourceID: sourceID, Section: "text"}, Staff: "20", Transport: transport, Cycle: &engine.OperationsCycleInput{}}
	must(api.saveOperations(ctx, cfg))
	selection := core.Ref{ResourceID: sourceID, Version: "old-selection", Section: "text"}
	for _, version := range []string{selection.Version, "new-selection"} {
		must(s.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: sourceID, Version: version}))
		must(s.Stage(ctx, p, sourceID, version, []core.Record{{ID: "example", Fields: map[string]any{"text": "fixture"}}}))
		_, err = s.Publish(ctx, p, sourceID, version, 1)
		must(err)
	}
	parent := core.Run{ID: id + "-daily", Title: "新人承接", Principal: p, Operation: "operations_package", ExplicitInputs: true,
		Destination: sourceID, Sources: []core.Ref{selection}, Shared: map[string]any{"local": true, "day": operationsDay(time.Now()), "strategy": "hxc:onboarding"},
		Operations: &core.OperationsSpec{Review: &core.OperationsDrafts{Selection: selection}}}
	must(s.SaveRun(ctx, parent))
	expired := time.Now().Add(-time.Hour)
	drafts := []core.OperationsReviewRow{
		{Key: "one", ID: "profile-one", Customer: "100", Message: "已确认稿", Slot: core.Digest(id + "/1"), ExpiresAt: &expired, Materials: []core.OperationsMaterial{{ID: "1070"}}},
		{Key: "two", ID: "profile-two", Customer: "101", Message: "独立话术", Slot: core.Digest(id + "/2")},
	}
	roster := core.ItemResult{ID: "original-roster", State: "succeeded", Text: "{}"}
	must(s.PublishLocalOperationsReview(ctx, parent.ID, head, drafts, roster))
	published, err := s.OperationsReview(ctx, parent.ID)
	must(err)
	activities := &engine.Activities{Store: s, Temporal: tc}
	batch := worker.New(tc, engine.BatchQueue, worker.Options{})
	batch.RegisterWorkflow(engine.WriteTask)
	batch.RegisterWorkflow(engine.ConfirmOperationsBatch)
	batch.RegisterWorkflow(engine.LocalOperationsReview)
	batch.RegisterActivity(activities)
	must(batch.Start())
	defer batch.Stop()
	interactive := worker.New(tc, engine.InteractiveQueue, worker.Options{})
	interactive.RegisterWorkflow(engine.OperationsBatchDelivery)
	interactive.RegisterActivity(activities)
	must(interactive.Start())
	defer interactive.Stop()

	confirm := func(daily, version string) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(map[string]string{"daily": daily, "reviewVersion": version})
		must(err)
		req := httptest.NewRequest("POST", "/operations/source-confirm", bytes.NewReader(raw)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+api.Token)
		req.Header.Set("X-Workbench-Kind", p.Kind)
		req.Header.Set("X-Workbench-Session", p.SessionID)
		req.Header.Set("X-Workbench-Project", p.ProjectID)
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, req)
		return w
	}
	confirmToday := func() string {
		t.Helper()
		w := confirm(parent.ID, core.OperationsReviewVersion(published, transport, "20", "测试员工"))
		var ref RunRef
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &ref) != nil || ref.RunID == "" {
			t.Fatalf("confirmation %d: %s", w.Code, w.Body.String())
		}
		return ref.RunID
	}
	ref := confirmToday()
	must(tc.GetWorkflow(ctx, ref, "").Get(ctx, nil))
	must(tc.GetWorkflow(ctx, ref+"-delivery", "").Get(ctx, nil))
	if confirmToday() != ref || sends.Load() != 1 {
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
	old := parent
	old.ID = id + "-old-daily"
	old.Shared = map[string]any{"local": true, "day": "2026-11-10", "strategy": "hxc:onboarding"}
	must(s.SaveRun(ctx, old))
	for i := range drafts {
		drafts[i].Slot = core.Digest(old.ID + "/" + drafts[i].Key)
	}
	must(s.PublishLocalOperationsReview(ctx, old.ID, head, drafts, roster))
	oldHead, err := s.OperationsReview(ctx, old.ID)
	must(err)
	oldVersion := core.OperationsReviewVersion(oldHead, transport, "20", "测试员工")
	if response := confirm(old.ID, oldVersion); response.Code != http.StatusConflict {
		t.Fatalf("the historical review allowed a new confirmation: %d", response.Code)
	}
	oldRef := uuid.NewString()
	must(s.SaveRun(ctx, core.Run{ID: oldRef, Principal: p, Operation: "operations_batch_confirm", ParentRunID: old.ID,
		Title: "原确认", ExplicitInputs: true, Destination: sourceID,
		Shared: map[string]any{"version": oldVersion, "staff": cfg.Staff, "transport": transport, "strategy": "hxc:onboarding"}}))
	must(s.ConfirmOperationsReview(ctx, old, oldRef, oldVersion, "20", transport, "测试员工", nil))
	preSubmit := core.OperationsBatchReceipt{State: "failed", Error: "发送账号、原稿或来源授权已变化，未提交。"}
	must(s.SaveOperationsBatchResult(ctx, oldRef, core.OperationsBatchStatus{Entries: map[string]core.OperationsBatchReceipt{"one": preSubmit, "two": preSubmit}}))
	resume := func(principal core.Principal) *mcp.CallToolResult {
		t.Helper()
		left, right := mcp.NewInMemoryTransports()
		server, err := api.MCP(principal).Connect(ctx, left, nil)
		must(err)
		defer server.Close()
		session, err := mcp.NewClient(&mcp.Implementation{Name: "resume-test"}, nil).Connect(ctx, right, nil)
		must(err)
		defer session.Close()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "run_resume", Arguments: map[string]string{"runId": oldRef}})
		must(err)
		return result
	}
	if denied := resume(p); !denied.IsError {
		t.Fatal("project principal reopened a confirmed failed send")
	}
	if continued := resume(owner); continued.IsError {
		t.Fatalf("owner could not resume the original confirmation: %v", continued.Content)
	}
	must(tc.GetWorkflow(ctx, oldRef+"-delivery", "").Get(ctx, nil))
	oldResult, err := s.OperationsBatchResult(ctx, oldRef)
	must(err)
	if oldResult.Plan != "42" || !oldResult.Settled(2) || sends.Load() != 2 || !resume(owner).IsError {
		t.Fatal("original confirmation was not resumed once", oldResult, sends.Load())
	}
	if sourceCalls.Load() != 0 {
		t.Fatalf("source was contacted %d times after a draft was saved", sourceCalls.Load())
	}

	// Exercise the configured review entry with actual saved inputs and the
	// Temporal prepare workflow. Substituting a method must fail before review.
	fields := []string{"text", "selected", "actions", "profile", "crmSlot"}
	for _, name := range []string{"raw", "history", "strategy", "methods", "clean", "selected", "drafts"} {
		must(s.PutResource(ctx, owner, core.Resource{ID: id + "-" + name, Name: name, OwnerProject: id,
			Sections: []core.Section{{ID: "text", Fields: fields}}}))
	}
	refresh := func(name string) core.ResourceSelection {
		return core.ResourceSelection{ResourceID: id + "-" + name, Refresh: true, Sections: []string{"text"}}
	}
	rawSource, historySource, strategySource, methodSource := refresh("raw"), refresh("history"), refresh("strategy"), refresh("methods")
	refFor := func(name, version string) core.Ref {
		return core.Ref{ResourceID: id + "-" + name, Version: version, Section: "text"}
	}
	clean := core.Run{ID: uuid.NewString(), Principal: p, Operation: "data_refresh", ExplicitInputs: true, Title: "fixture refresh",
		Destination: id + "-clean", Operations: &core.OperationsSpec{Refresh: []core.ResourceSelection{rawSource, historySource, strategySource, methodSource}}}
	must(s.SaveRun(ctx, clean))
	stamp := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	_, err = s.FreezeCheckpoint(ctx, clean, "operations-input", core.Checkpoint{Input: map[string]any{"asOf": stamp}})
	must(err)
	fromCycle := func(name string) core.Ref {
		version := uuid.NewSHA1(uuid.NameSpaceURL, []byte(clean.ID+"/sync/"+id+"-"+name)).String()
		return refFor(name, version)
	}
	publish := func(ref core.Ref, row core.Record) {
		must(s.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: ref.ResourceID, Version: ref.Version}))
		must(s.Stage(ctx, p, ref.ResourceID, ref.Version, []core.Record{row}))
		_, err := s.Publish(ctx, p, ref.ResourceID, ref.Version, 1)
		must(err)
	}
	fixture := core.Record{ID: "review-customer", Fields: map[string]any{"text": "input"}}
	inputs := []core.Ref{refFor("clean", clean.ID), fromCycle("strategy"), fromCycle("history")}
	for _, input := range inputs {
		publish(input, fixture)
	}
	method := core.RecordRef{ID: "method", Ref: fromCycle("methods")}
	publish(method.Ref, fixture)
	const strategy = "hxc:ongoing"
	selected := refFor("selected", uuid.NewString())
	must(s.SaveRun(ctx, core.Run{ID: selected.Version, Principal: p, Operation: "strategy_select", ExplicitInputs: true, Title: "fixture selection",
		Sources: inputs, Destination: selected.ResourceID, Operations: &core.OperationsSpec{AsOf: stamp, StrategyKeys: []string{strategy}}}))
	publish(selected, core.Record{ID: fixture.ID, Fields: map[string]any{"selected": true, "crmSlot": "fixture-slot",
		"profile": map[string]any{"customer_id": "102"}, "actions": []any{map[string]any{"strategy_key": strategy}}}})
	draft := refFor("drafts", uuid.NewString())
	generated := core.Run{ID: draft.Version, Principal: p, Operation: "generate", ExplicitInputs: true, Title: "fixture draft",
		Sources: []core.Ref{selected}, Destination: draft.ResourceID, MethodPointer: "/actions/0/generation/skill_key",
		MethodRefs: map[string]core.RecordRef{"method": method}}
	must(s.SaveRun(ctx, generated))
	publish(draft, core.Record{ID: fixture.ID, Fields: map[string]any{"text": `{"status":"draft","text":"保存这份稿件"}`}})
	must(s.RecordItem(ctx, generated.ID, core.ItemResult{ID: fixture.ID, State: "succeeded"}))
	cfg.Cycle = &engine.OperationsCycleInput{StrategyKeys: []string{strategy}, Sources: []core.ResourceSelection{rawSource}, HistorySource: &historySource,
		StrategySource: &strategySource, MethodSource: &methodSource, MethodRefs: generated.MethodRefs, Section: "text",
		CleanDestination: clean.Destination, SelectedDestination: selected.ResourceID, DraftDestination: draft.ResourceID}
	must(api.saveOperations(ctx, cfg))
	request := CRMPushInput{OperationKey: "test-review", Review: &core.OperationsDrafts{Selection: selected, Drafts: []core.Ref{draft}}}
	inline := generated
	inline.ID, inline.MethodRefs = uuid.NewString(), nil
	must(s.SaveRun(ctx, inline))
	request.Review.Drafts[0].Version = inline.ID
	if _, _, err := api.prepareLocalOperations(ctx, p, request); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("unbound method entered original strategy review: %v", err)
	}
	request.Review.Drafts[0] = draft
	_, prepared, err := api.prepareLocalOperations(ctx, p, request)
	must(err)
	must(tc.GetWorkflow(ctx, prepared.RunID, "").Get(ctx, nil))
	preparedHead, err := s.OperationsReview(ctx, prepared.RunID)
	must(err)
	if preparedHead.Count != 1 || preparedHead.Confirm != "" || sends.Load() != 2 || sourceCalls.Load() != 0 {
		t.Fatal("original review did not save one draft without sending", preparedHead)
	}
	cfg.ReadSource = false
	must(api.saveOperations(ctx, cfg))
	allowed, err = api.operationsBatchWriteAllowed(ctx, ref, transport)
	must(err)
	if allowed {
		t.Fatal("withdrawn project permission allowed submission")
	}
}
