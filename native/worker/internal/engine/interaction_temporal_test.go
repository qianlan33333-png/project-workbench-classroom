package engine

import (
	"context"
	"fmt"
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
)

type interactionModel struct{ calls atomic.Int32 }

func (m *interactionModel) Generate(_ context.Context, in ModelRequest) (ModelResult, error) {
	m.calls.Add(1)
	if in.ItemID == "review-1" {
		return ModelResult{Text: `{"state":"waiting","summary":"需要本人选择下一步","question":"是否继续合成验收？"}`}, nil
	}
	return ModelResult{Text: `{"state":"completed","summary":"已收到本人决定，合成事项结束"}`}, nil
}

func TestRealInteractionRestartAndBatchReceipts(t *testing.T) {
	address, dsn := os.Getenv("WORKBENCH_TEST_TEMPORAL"), os.Getenv("WORKBENCH_TEST_DSN")
	if address == "" || dsn == "" {
		t.Skip("real Temporal/MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	ns := "interaction-" + uuid.NewString()
	c, e := client.Dial(client.Options{HostPort: address, Namespace: ns})
	must(e)
	defer c.Close()
	_, e = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: ns, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	must(e)
	prefix := uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: prefix + "o"}
	p := core.Principal{Kind: "project", ProjectID: prefix, SessionID: prefix + "s"}
	must(s.PutProject(ctx, owner, core.Project{ID: prefix, Name: "interaction acceptance"}))
	model := &interactionModel{}
	a := &Activities{Store: s, Temporal: c, Model: model}
	start := func() worker.Worker {
		w := worker.New(c, InteractiveQueue, worker.Options{MaxConcurrentActivityExecutionSize: 4})
		for _, fn := range []any{Followup, DispatchBatch, SubmitMessage, ReceiptOnly, AutomaticReceipt, ReceiptSweep} {
			w.RegisterWorkflow(fn)
		}
		w.RegisterActivity(a)
		must(w.Start())
		return w
	}
	w := start()
	defer func() { w.Stop() }()
	wait := func(predicate func() bool) {
		t.Helper()
		for !predicate() {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	follow := core.Run{ID: uuid.NewString(), Principal: p, Operation: "followup", Goal: "合成等待与重启", Title: "合成等待与重启"}
	must(s.SaveRun(ctx, follow))
	run, e := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: follow.ID, TaskQueue: InteractiveQueue}, Followup, FollowupInput{RunID: follow.ID, NextAt: time.Now()})
	must(e)
	var decision core.Decision
	wait(func() bool {
		decision, e = s.Decision(ctx, p, follow.ID+"-d1")
		return e == nil && decision.State == "waiting"
	})
	w.Stop()
	decision.Answer = "继续合成验收"
	decision, e = s.Answer(ctx, owner, decision)
	must(e)
	signal := Decision{ID: decision.ID, Revision: decision.Revision, Digest: decision.Digest}
	must(c.SignalWorkflow(ctx, follow.ID, "", "decision", signal))
	must(c.SignalWorkflow(ctx, follow.ID, "", "decision", signal))
	w = start()
	must(run.Get(ctx, nil))
	if model.calls.Load() != 2 {
		t.Fatal("duplicate answer repeated model", model.calls.Load())
	}
	canceled := follow
	canceled.ID = uuid.NewString()
	must(s.SaveRun(ctx, canceled))
	r, e := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: canceled.ID, TaskQueue: InteractiveQueue}, Followup, FollowupInput{RunID: canceled.ID, NextAt: time.Now().Add(time.Hour)})
	must(e)
	must(c.CancelWorkflow(ctx, canceled.ID, ""))
	if r.Get(ctx, nil) == nil {
		t.Fatal("cancel completed normally")
	}
	if model.calls.Load() != 2 {
		t.Fatal("canceled timer called model")
	}

	var sends, reads atomic.Int32
	var confirmed atomic.Bool
	connector := mcp.NewServer(&mcp.Implementation{Name: "synthetic-channel", Version: "1"}, nil)
	type args struct {
		ConnectionID   string            `json:"connectionId"`
		IdempotencyKey string            `json:"idempotencyKey"`
		Target         map[string]string `json:"target"`
		Text           string            `json:"text,omitempty"`
		Receipt        string            `json:"receipt,omitempty"`
		State          string            `json:"state,omitempty"`
	}
	mcp.AddTool(connector, &mcp.Tool{Name: "message_send"}, func(_ context.Context, _ *mcp.CallToolRequest, v args) (*mcp.CallToolResult, Receipt, error) {
		sends.Add(1)
		return nil, Receipt{State: "pending_confirmation", Receipt: "phone-" + v.IdempotencyKey}, nil
	})
	mcp.AddTool(connector, &mcp.Tool{Name: "message_receipt"}, func(_ context.Context, _ *mcp.CallToolRequest, v args) (*mcp.CallToolResult, Receipt, error) {
		reads.Add(1)
		state := "pending_confirmation"
		if confirmed.Load() {
			state = "sent"
		}
		return nil, Receipt{State: state, Receipt: v.Receipt}, nil
	})
	server := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return connector }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer server.Close()
	t.Setenv("WORKBENCH_CONNECTOR_TEST", strings.Repeat("x", 40))
	must(s.PutTransport(ctx, owner, core.Transport{ID: prefix, Name: "synthetic", Endpoint: server.URL, TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{prefix}, Enabled: true}))
	check := core.RecordCheck{ID: "eligible", Name: "Explicit fixture eligibility", Section: "identity", Schema: map[string]any{"type": "object", "required": []any{"eligible", "actor"}, "properties": map[string]any{"eligible": map[string]any{"const": true}, "actor": map[string]any{"type": "string", "minLength": 1}}}, Bindings: map[string]string{"actorId": "/actor"}}
	src := core.Resource{ID: prefix + "content", Name: "content", OwnerProject: prefix, Sections: []core.Section{{ID: "content", Fields: []string{"text"}}}}
	identity := core.Resource{ID: prefix + "identity", Name: "identities", OwnerProject: prefix, Sections: []core.Section{{ID: "identity", Fields: []string{"eligible", "actor"}}}, Checks: []core.RecordCheck{check}}
	publish := func(resource core.Resource, records []core.Record) {
		must(s.PutResource(ctx, owner, resource))
		must(s.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: resource.ID, Version: "v1"}))
		must(s.Stage(ctx, p, resource.ID, "v1", records))
		_, e = s.Publish(ctx, p, resource.ID, "v1", len(records))
		must(e)
	}
	content, identities := []core.Record{}, []core.Record{}
	for i := 0; i < 7; i++ {
		id := fmt.Sprint(i)
		content = append(content, core.Record{ID: id, Fields: map[string]any{"text": "合成消息，无真实客户"}})
		identities = append(identities, core.Record{ID: id, Fields: map[string]any{"eligible": i < 6, "actor": "fixture-" + id}})
	}
	publish(src, content)
	publish(identity, identities)
	plan := core.DispatchPlan{Source: core.Ref{ResourceID: src.ID, Version: "v1", Section: "content"}, Eligibility: core.Ref{ResourceID: identity.ID, Version: "v1", Section: "identity"}, Check: check, EligibilitySchema: check.Schema, ConnectionID: prefix, TextPointer: "/text", TargetPointers: check.Bindings}
	batch := core.Run{ID: uuid.NewString(), Principal: p, Operation: "dispatch_batch", Title: "synthetic review and receipts", Dispatch: &plan, Sources: []core.Ref{plan.Source, plan.Eligibility}}
	must(s.SaveRun(ctx, batch))
	br, e := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: batch.ID, TaskQueue: InteractiveQueue}, DispatchBatch, DispatchBatchInput{RunID: batch.ID})
	must(e)
	wait(func() bool {
		decision, e = s.Decision(ctx, p, batch.ID+"-d1")
		return e == nil && decision.State == "waiting"
	})
	if sends.Load() != 0 {
		t.Fatal("sent before owner confirmation")
	}
	w.Stop()
	decision.Answer = "发 " + batch.ID[:8]
	decision, e = s.Answer(ctx, owner, decision)
	must(e)
	signal = Decision{ID: decision.ID, Revision: decision.Revision, Digest: decision.Digest}
	must(c.SignalWorkflow(ctx, batch.ID, "", "decision", signal))
	must(c.SignalWorkflow(ctx, batch.ID, "", "decision", signal))
	w = start()
	must(br.Get(ctx, nil))
	stats, e := s.DeliveryStats(ctx, p, batch.ID)
	must(e)
	if sends.Load() != 6 || stats.States["pending_confirmation"] != 6 {
		t.Fatal("submit was confused with actual send", sends.Load(), stats)
	}
	confirmed.Store(true)
	deliveries, e := s.Deliveries(ctx, p, batch.ID, "")
	must(e)
	for _, d := range deliveries {
		rr, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: d.ID + "-receipt", TaskQueue: InteractiveQueue}, AutomaticReceipt, d.ID)
		must(err)
		must(rr.Get(ctx, nil))
	}
	stats, e = s.DeliveryStats(ctx, p, batch.ID)
	must(e)
	if stats.States["sent"] != 6 || sends.Load() != 6 || reads.Load() != 6 {
		t.Fatal("receipt resent or missing", stats, sends.Load(), reads.Load())
	}
	must(a.SummarizeBatchReceipts(ctx))
	t.Log("Real Temporal/MySQL: followup wait survives Worker restart, duplicate answer continues once, canceled timer makes no model call; batch 7 source / 6 eligible / zero send before owner / six phone-pending / six receipt-confirmed, no resend. Channel/model are synthetic. Namespace", ns)
}
