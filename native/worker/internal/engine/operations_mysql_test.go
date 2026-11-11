package engine

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/types/known/durationpb"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"workbench-native/internal/core"
)

type operationsModel struct {
	mu    sync.Mutex
	calls map[string]string
}

func (m *operationsModel) Generate(_ context.Context, in ModelRequest) (ModelResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls[in.RunID+"/"+in.ItemID] = in.Method.Instructions
	return ModelResult{Text: in.Method.Instructions + ":" + in.ItemID}, nil
}

// Real MySQL + Temporal exercise the tool boundary and saved references. Source
// and model fixtures are explicit; no customer or Provider is contacted.
func TestRealOperationsRefreshSelectAndGenerate(t *testing.T) {
	dsn, address := os.Getenv("WORKBENCH_TEST_DSN"), os.Getenv("WORKBENCH_TEST_TEMPORAL")
	if dsn == "" || address == "" {
		t.Skip("isolated real services not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	store, e := core.Open(ctx, dsn)
	must(e)
	defer store.DB.Close()
	must(store.Migrate(ctx))
	namespace := "operations-" + uuid.NewString()
	c, e := client.Dial(client.Options{HostPort: address, Namespace: namespace})
	must(e)
	defer c.Close()
	_, e = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: namespace, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	must(e)
	prefix := uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: prefix + "o"}
	p := core.Principal{Kind: "project", ProjectID: prefix, SessionID: prefix + "s"}
	must(store.PutProject(ctx, owner, core.Project{ID: p.ProjectID, Name: "operations isolated test"}))
	add := func(id string, fields []string) core.Resource {
		r := core.Resource{ID: prefix + id, Name: id, OwnerProject: p.ProjectID, Sections: []core.Section{{ID: "main", Fields: fields}}}
		must(store.PutResource(ctx, owner, r))
		return r
	}
	source := add("source", []string{"safe_user_ref", "identity_resolved", "strategy_candidates", "customer_id"})
	clean := add("clean", []string{"raw", "archive"})
	policies := add("policies", []string{"key", "status", "audience_filter", "expires_at"})
	history := add("history", []string{"complete", "actions"})
	selected := add("selected", []string{"selected", "reason", "actions", "profile"})
	drafts := add("drafts", []string{"text", "methodDigest", "inputHash"})
	ref := func(r core.Resource, v string) core.Ref {
		return core.Ref{ResourceID: r.ID, Version: v, Section: "main"}
	}
	publish := func(r core.Resource, rows []core.Record) {
		must(store.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: r.ID, Version: "v1"}))
		must(store.Stage(ctx, p, r.ID, "v1", rows))
		_, e := store.Publish(ctx, p, r.ID, "v1", len(rows))
		must(e)
	}
	id := "usr_0123456789ab"
	records := []core.Record{{ID: id, Fields: map[string]any{"safe_user_ref": id, "identity_resolved": true, "customer_id": 10, "strategy_candidates": []any{map[string]any{"strategy_key": "theme:approved", "eligible": true, "action": "content_push", "product_service_key": "hxc"}}}}}
	root := t.TempDir()
	wire, _ := json.Marshal(records[0])
	must(os.WriteFile(filepath.Join(root, "source.jsonl"), append(wire, '\n'), 0600))
	// Existing JSONL source reader expects the configured key in each full row.
	connection := core.Connection{ID: prefix + "connection", Name: "isolated source", Kind: "jsonl", Location: root}
	must(store.PutConnection(ctx, owner, connection))
	source.Revision = 1
	source.ConnectionID = connection.ID
	source.Relation = "source"
	source.KeyField = "safe_user_ref"
	must(store.PutResource(ctx, owner, source))
	auxiliary := add("auxiliary", []string{"complete", "actions"})
	auxiliary.ConnectionID, auxiliary.Relation, auxiliary.KeyField = connection.ID, "auxiliary", "id"
	auxiliary.Revision = 1
	must(store.PutResource(ctx, owner, auxiliary))
	auxiliaryWire, _ := json.Marshal(core.Record{ID: id, Fields: map[string]any{"complete": true, "actions": []any{}}})
	must(os.WriteFile(filepath.Join(root, "auxiliary.jsonl"), append(auxiliaryWire, '\n'), 0600))
	publish(policies, []core.Record{{ID: "policy", Fields: map[string]any{"key": "theme:approved", "status": "published", "expires_at": "2026-11-09T12:00:00+08:00", "audience_filter": map[string]any{"population": "canonical_archive", "exact_safe_user_refs": []any{id}}}}})
	publish(history, []core.Record{{ID: id, Fields: map[string]any{"complete": true, "actions": []any{}}}})
	model := &operationsModel{calls: map[string]string{}}
	a := &Activities{Store: store, Model: model, Temporal: c}
	bw := worker.New(c, BatchQueue, worker.Options{})
	for _, fn := range []any{WriteTask, OperationsTask, Sync, Batch} {
		bw.RegisterWorkflow(fn)
	}
	bw.RegisterActivity(a)
	must(bw.Start())
	defer bw.Stop()
	iw := worker.New(c, InteractiveQueue, worker.Options{})
	iw.RegisterActivity(a)
	must(iw.Start())
	defer iw.Stop()
	execute := func(r core.Run) {
		must(store.SaveRun(ctx, r))
		run, e := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: BatchQueue}, WriteTask, WriteInput{RunID: r.ID, ProjectID: p.ProjectID, Operation: r.Operation})
		must(e)
		must(run.Get(ctx, nil))
	}
	refresh := core.Run{ID: uuid.NewString(), Principal: p, Operation: "data_refresh", Title: "raw source clean", Destination: clean.ID, Operations: &core.OperationsSpec{Refresh: []core.ResourceSelection{{ResourceID: source.ID, Sections: []string{"main"}, Refresh: true}, {ResourceID: auxiliary.ID, Sections: []string{"main"}, Refresh: true}}}}
	execute(refresh)
	page, e := store.Read(ctx, p, ref(clean, refresh.ID), "", 10)
	must(e)
	if len(page.Records) != 1 {
		t.Fatal(page)
	}
	if len(page.Dependencies) != 1 || page.Dependencies[0].ResourceID != source.ID {
		t.Fatal("clean facts unnecessarily depend on auxiliary method/history inputs", page.Dependencies)
	}
	clock, e := a.OperationsRefreshClock(ctx, p, refresh.ID)
	must(e)
	frozenInput, e := store.Checkpoint(ctx, refresh, "operations-input")
	must(e)
	if clock != frozenInput.Input["asOf"] {
		t.Fatal("cycle did not read the original frozen collection time", clock)
	}
	selection := core.Run{ID: uuid.NewString(), Principal: p, Operation: "strategy_select", Title: "select", Destination: selected.ID, Sources: []core.Ref{ref(clean, refresh.ID), ref(policies, "v1"), ref(history, "v1")}, Operations: &core.OperationsSpec{AsOf: "2026-11-08T12:00:00+08:00", StrategyKeys: []string{"theme:approved"}}}
	execute(selection)
	page, e = store.Read(ctx, p, ref(selected, selection.ID), "", 10)
	must(e)
	if len(page.Records) != 1 || page.Records[0].Fields["selected"] != true {
		t.Fatal(page)
	}
	generation := core.Run{ID: uuid.NewString(), Principal: p, Operation: "generate", Title: "reuse existing generator", Destination: drafts.ID, Sources: []core.Ref{ref(selected, selection.ID)}, Method: core.Method{Name: "fixture", Instructions: "summarize selected profile", RequiredFields: []string{"profile"}}}
	execute(generation)
	stats, e := store.Stats(ctx, p, generation.ID)
	must(e)
	if stats.Succeeded != 1 {
		t.Fatal(stats)
	}
	// Readback and query never repeat source refresh or model generation.
	page, e = store.Read(ctx, p, ref(drafts, generation.ID), "", 10)
	must(e)
	if len(page.Records) != 1 {
		t.Fatal(page)
	}
	// A lost completion response resumes the original published result. Even if
	// the live source has changed, a successful source step is never repeated.
	must(os.WriteFile(filepath.Join(root, "source.jsonl"), []byte("invalid changed live source"), 0600))
	execute(refresh)
	page, e = store.Read(ctx, p, ref(clean, refresh.ID), "", 10)
	must(e)
	if len(page.Records) != 1 {
		t.Fatal(page)
	}
	ranked := add("ranked", []string{"priority", "selected"})
	rankRun := core.Run{ID: uuid.NewString(), Principal: p, Operation: "strategy_select", Title: "global priority boundary", Destination: ranked.ID, Operations: &core.OperationsSpec{Limit: 1}}
	must(store.SaveRun(ctx, rankRun))
	must(store.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: ranked.ID, Version: rankRun.ID}))
	candidates := []core.Record{{ID: "a-low", Fields: map[string]any{"priority": 1, "selected": true}}, {ID: "z-high", Fields: map[string]any{"priority": 99, "selected": true}}}
	must(store.Stage(ctx, p, ranked.ID, rankRun.ID, candidates))
	for _, row := range candidates {
		must(store.RecordItem(ctx, rankRun.ID, core.ItemResult{ID: row.ID, State: "succeeded", InputHash: core.Digest(row), Text: `{"selected":true}`}))
	}
	must(store.TrimOperationsSelection(ctx, rankRun))
	_, e = store.Publish(ctx, p, ranked.ID, rankRun.ID, 1)
	must(e)
	page, e = store.Read(ctx, p, ref(ranked, rankRun.ID), "", 10)
	must(e)
	if len(page.Records) != 1 || page.Records[0].ID != "z-high" {
		t.Fatal(page)
	}
	deferred, e := store.Item(ctx, rankRun.ID, "a-low")
	must(e)
	if !strings.Contains(deferred.Text, "deferred_by_limit") {
		t.Fatal(deferred)
	}

	// Both records go through the unchanged existing generator with different
	// frozen method bodies; the model does not choose a skill or a recipient.
	methodSource := add("method-source", []string{"skill", "data"})
	methodDrafts := add("method-drafts", []string{"text", "methodDigest", "inputHash"})
	publish(methodSource, []core.Record{{ID: "a", Fields: map[string]any{"skill": "review", "data": "fact-a"}}, {ID: "b", Fields: map[string]any{"skill": "content", "data": "fact-b"}}})
	methods := map[string]core.Method{"review": {Instructions: "original review method"}, "content": {Instructions: "original content method"}}
	for key, m := range methods {
		m.Digest = core.Digest(m)
		methods[key] = m
	}
	methodRun := core.Run{ID: uuid.NewString(), Principal: p, Operation: "generate", Title: "per-record original methods", Sources: []core.Ref{ref(methodSource, "v1")}, Destination: methodDrafts.ID, Method: core.Method{Digest: core.Digest(methods)}, Methods: methods, MethodPointer: "/skill"}
	execute(methodRun)
	model.mu.Lock()
	if model.calls[methodRun.ID+"/a"] != "original review method" || model.calls[methodRun.ID+"/b"] != "original content method" {
		t.Fatal(model.calls)
	}
	model.mu.Unlock()
	page, e = store.Read(ctx, p, ref(methodDrafts, methodRun.ID), "", 10)
	must(e)
	if len(page.Records) != 2 || page.Records[0].Fields["methodDigest"] != methods["review"].Digest || page.Records[1].Fields["methodDigest"] != methods["content"].Digest {
		t.Fatal(page)
	}
	// New CRM plans are a conflict even before the upstream history sync catches
	// up. A retry of the old selection keeps its original frozen history.
	transport := core.Transport{ID: prefix + "crm", Name: "isolated CRM", Kind: "crm-cli", Endpoint: "http://127.0.0.1:1/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{p.ProjectID}, Enabled: true}
	must(store.PutTransport(ctx, owner, transport))
	dispatch := core.Run{ID: uuid.NewString(), Principal: p, Operation: "dispatch_batch", Title: "already arranged original plan"}
	must(store.SaveRun(ctx, dispatch))
	must(store.PutDelivery(ctx, dispatch, core.Delivery{ID: uuid.NewString(), RunID: dispatch.ID, ItemID: id, ConnectionID: transport.ID, Target: map[string]string{"customerId": "10", "staffId": "20"}, Text: "fixture only"}))
	historyRun := core.Run{ID: uuid.NewString(), Principal: p, Operation: "strategy_select", Title: "history freezing"}
	must(store.SaveRun(ctx, historyRun))
	raw := []core.Record{{ID: id, Fields: map[string]any{"customer_id": "10"}}}
	merged, e := a.operationsHistory(ctx, historyRun, raw, []core.Record{{ID: id, Fields: map[string]any{"complete": true, "actions": []any{}}}}, "2026-11-08T12:00:00+08:00")
	must(e)
	if len(merged) != 1 || len(merged[0].Fields["actions"].([]any)) != 1 {
		t.Fatal(merged)
	}
	if merged[0].Fields["complete"] != false || merged[0].Fields["history_problem"] != "existing_crm_service_unknown" {
		t.Fatal("missing eligibility binding guessed an HXC service", merged)
	}
	frozenHash := core.Digest(merged)
	merged, e = a.operationsHistory(ctx, historyRun, raw, []core.Record{{ID: id, Fields: map[string]any{"complete": false}}}, "2026-11-08T12:00:00+08:00")
	must(e)
	if core.Digest(merged) != frozenHash {
		t.Fatal("retry changed frozen execution history")
	}

	must(a.RecordTerminal(ctx, Terminal{RunID: dispatch.ID, AttemptID: uuid.NewString(), State: "canceled"}))
	historyRun.ID = uuid.NewString()
	must(store.SaveRun(ctx, historyRun))
	merged, e = a.operationsHistory(ctx, historyRun, raw, []core.Record{{ID: id, Fields: map[string]any{"complete": true, "actions": []any{}}}}, "2026-11-08T12:00:00+08:00")
	must(e)
	if len(merged[0].Fields["actions"].([]any)) != 0 {
		t.Fatal("canceled unsubmitted draft still occupied the next cycle", merged)
	}
	products := add("history-products", []string{"actions"})
	publish(products, []core.Record{{ID: id, Fields: map[string]any{"actions": []any{map[string]any{"product_service_key": "relationship"}}}}})
	bound := core.Run{ID: uuid.NewString(), Principal: p, Operation: "dispatch_batch", Title: "saved product binding", Dispatch: &core.DispatchPlan{Eligibility: ref(products, "v1")}, Sources: []core.Ref{ref(products, "v1")}}
	must(store.SaveRun(ctx, bound))
	must(store.PutDelivery(ctx, bound, core.Delivery{ID: uuid.NewString(), RunID: bound.ID, ItemID: id, ConnectionID: transport.ID, Target: map[string]string{"customerId": "11", "staffId": "20"}, Text: "fixture only"}))
	historyRun.ID = uuid.NewString()
	must(store.SaveRun(ctx, historyRun))
	merged, e = a.operationsHistory(ctx, historyRun, []core.Record{{ID: id, Fields: map[string]any{"customer_id": "11"}}}, []core.Record{{ID: id, Fields: map[string]any{"complete": true, "actions": []any{}}}}, "2026-11-08T12:00:00+08:00")
	must(e)
	if merged[0].Fields["complete"] != true || merged[0].Fields["actions"].([]any)[0].(map[string]any)["product_key"] != "relationship" {
		t.Fatal("original product binding was not preserved", merged)
	}

	nullable := add("history-null-product", []string{"actions"})
	publish(nullable, []core.Record{{ID: id, Fields: map[string]any{"actions": []any{map[string]any{"product_service_key": nil}}}}})
	bound.ID = uuid.NewString()
	bound.Dispatch = &core.DispatchPlan{Eligibility: ref(nullable, "v1")}
	bound.Sources = []core.Ref{ref(nullable, "v1")}
	must(store.SaveRun(ctx, bound))
	must(store.PutDelivery(ctx, bound, core.Delivery{ID: uuid.NewString(), RunID: bound.ID, ItemID: id, ConnectionID: transport.ID, Target: map[string]string{"customerId": "12", "staffId": "20"}, Text: "fixture only"}))
	historyRun.ID = uuid.NewString()
	must(store.SaveRun(ctx, historyRun))
	merged, e = a.operationsHistory(ctx, historyRun, []core.Record{{ID: id, Fields: map[string]any{"customer_id": "12"}}}, []core.Record{{ID: id, Fields: map[string]any{"complete": true, "actions": []any{}}}}, "2026-11-08T12:00:00+08:00")
	must(e)
	if merged[0].Fields["complete"] != false || merged[0].Fields["history_problem"] != "existing_crm_service_unknown" {
		t.Fatal("JSON null service allowed another plan", merged)
	}

	// Tomorrow's new source may draft an unconfirmed action again. Confirmed
	// unknown outcomes still hold; a terminal failed node does not block D4.
	asOf := time.Date(2026, 11, 12, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	for _, state := range []string{"draft", "unknown", "failed", "sent", "legacy-valid", "legacy-expired"} {
		person := "person-" + state
		parent := core.Run{ID: uuid.NewString(), Principal: p, Operation: "operations_package", Title: "previous day draft", Shared: map[string]any{"local": true, "week": "2026-W46"}}
		must(store.SaveRun(ctx, parent))
		binding := map[string]any{"slot_key": "d2", "cycle_key": "cycle", "node": "D2"}
		judgment := map[string]any{"action": map[string]any{"generation": map[string]any{"sku_pack_binding": binding}}}
		if strings.HasPrefix(state, "legacy-") {
			judgment = map[string]any{"action": map[string]any{}}
		}
		wire, _ := json.Marshal(judgment)
		expiry := asOf.Add(time.Hour)
		if state == "legacy-expired" {
			expiry = asOf.Add(-time.Hour)
		}
		row := core.OperationsReviewRow{Key: person, ID: person, Customer: "999", Message: "prior exact draft", Slot: core.Digest([]string{parent.ID, person}), Judgment: wire, ExpiresAt: &expiry}
		head := core.OperationsReview{Digest: core.Digest(row)}
		if state != "draft" {
			head.Confirm = uuid.NewString()
		}
		must(store.PublishOperationsReview(ctx, parent.ID, head, []core.OperationsReviewRow{row}))
		if state != "draft" {
			must(store.SaveRun(ctx, core.Run{ID: head.Confirm, Principal: p, Operation: "operations_batch_confirm", Title: "confirmed prior node"}))
			receiptState := state
			if strings.HasPrefix(state, "legacy-") {
				receiptState = "sent"
			}
			must(store.SaveOperationsBatchResult(ctx, head.Confirm, core.OperationsBatchStatus{Entries: map[string]core.OperationsBatchReceipt{person: {State: receiptState, At: asOf.Add(-25 * time.Hour).Format(time.RFC3339)}}}))
		}
		past := map[string]any{"complete": true}
		must(a.localOperationsHistory(ctx, historyRun, person, past, asOf))
		index := past["copy_index"].(map[string]any)
		values, _ := index[person].([]any)
		switch state {
		case "draft":
			if len(values) != 0 {
				t.Fatal("unconfirmed draft reserved tomorrow's action", past)
			}
		case "legacy-valid":
			if past["local_hold_reason"] != "legacy_confirmed_action_window_requires_verification" {
				t.Fatal(past)
			}
		case "legacy-expired":
			if past["local_hold_reason"] != nil {
				t.Fatal("expired previous action blocked a new window", past)
			}
		default:
			if len(values) != 1 {
				t.Fatal(past)
			}
			entry := values[0].(map[string]any)
			expected := map[string]string{"unknown": "pending", "failed": "cancelled", "sent": "sent"}[state]
			if entry["state"] != expected || entry["slot_key"] != "d2" {
				t.Fatal("original outcome changed", past)
			}
		}
	}

}
