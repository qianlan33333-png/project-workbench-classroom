package api

import (
	"context"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type DispatchRequest struct {
	OperationKey string            `json:"operationKey"`
	Title        string            `json:"title"`
	Plan         DispatchSelection `json:"plan"`
}
type DispatchSelection struct {
	Source           core.Ref `json:"source"`
	Eligibility      core.Ref `json:"eligibility"`
	EligibilityCheck string   `json:"eligibilityCheck"`
	ConnectionID     string   `json:"connectionId"`
	TextPointer      string   `json:"textPointer"`
	DecodeJSONField  string   `json:"decodeJsonField,omitempty"`
}

func (a *Server) dispatchBatchTools(s *mcp.Server, p core.Principal) {
	mcp.AddTool(s, &mcp.Tool{Name: "message_batch_prepare", Description: "Prepare an immutable message batch from a fixed resource section. Separately check source-backed eligibility with JSON Schema, exclude missing data, and present samples to the owner. Never sends until a real owner confirms. Generic JSON Pointer mapping; no campaign logic."}, func(ctx context.Context, _ *mcp.CallToolRequest, in DispatchRequest) (*mcp.CallToolResult, RunRef, error) {
		if p.Kind != "project" || !core.ValidID(in.OperationKey) {
			return nil, RunRef{}, core.ErrDenied
		}
		eligibility, e := a.Store.Resource(ctx, in.Plan.Eligibility.ResourceID)
		if e != nil {
			return nil, RunRef{}, e
		}
		if eligibility.ConnectionID == "" {
			return nil, RunRef{}, core.ErrInvalid
		}
		var selected core.RecordCheck
		for _, check := range eligibility.Checks {
			if check.ID == in.Plan.EligibilityCheck && check.Section == in.Plan.Eligibility.Section {
				selected = check
			}
		}
		if selected.ID == "" || len(selected.Bindings) == 0 {
			return nil, RunRef{}, core.ErrDenied
		}
		plan := core.DispatchPlan{Source: in.Plan.Source, Eligibility: in.Plan.Eligibility, Check: selected, EligibilitySchema: selected.Schema, ConnectionID: in.Plan.ConnectionID, TextPointer: in.Plan.TextPointer, TargetPointers: selected.Bindings, DecodeJSONField: in.Plan.DecodeJSONField}
		if e = plan.Validate(); e != nil {
			return nil, RunRef{}, e
		}
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/"+in.OperationKey)).String()
		r := core.Run{ID: id, Principal: p, Operation: "dispatch_batch", Title: in.Title, Dispatch: &plan, Sources: []core.Ref{plan.Source, plan.Eligibility}}
		if e = a.Store.SaveRun(ctx, r); e != nil {
			return nil, RunRef{}, e
		}
		_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.DispatchBatch, engine.DispatchBatchInput{RunID: id})
		return nil, RunRef{RunID: id}, accepted(e)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "message_batch_read", Description: "Read fixed drafts or real per-recipient receipts from the original batch, default 5, maximum 20 records. Read only; never resends."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ItemsInput) (*mcp.CallToolResult, struct {
		Items []core.Delivery    `json:"items"`
		Stats core.DeliveryStats `json:"stats"`
		Next  string             `json:"next,omitempty"`
	}, error) {
		out := struct {
			Items []core.Delivery    `json:"items"`
			Stats core.DeliveryStats `json:"stats"`
			Next  string             `json:"next,omitempty"`
		}{}
		if in.Limit <= 0 {
			in.Limit = 5
		}
		if in.Limit > 20 {
			return nil, out, core.ErrInvalid
		}
		var e error
		out.Items, e = a.Store.Deliveries(ctx, p, in.RunID, in.After)
		if len(out.Items) > in.Limit {
			out.Items = out.Items[:in.Limit]
			out.Next = out.Items[len(out.Items)-1].ItemID
		}
		if e == nil {
			out.Stats, e = a.Store.DeliveryStats(ctx, p, in.RunID)
		}
		return nil, out, e
	})
}
