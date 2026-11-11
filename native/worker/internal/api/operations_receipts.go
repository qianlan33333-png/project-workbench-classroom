package api

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"strconv"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func (a *Server) localCRMReceipts(ctx context.Context, p core.Principal, id string) (*mcp.CallToolResult, CRMReceiptResult, error) {
	// A batch has no single delivery target. Keep the existing delivery-shaped
	// response compatible with its MCP object schema while returning batch data.
	out := CRMReceiptResult{Delivery: core.Delivery{Target: map[string]string{}}}
	if !core.ValidID(id) {
		return nil, out, core.ErrInvalid
	}
	run, e := a.Store.Run(ctx, p, id)
	if e != nil {
		return nil, out, e
	}
	if run.Operation == "operations_batch_confirm" {
		run, e = a.Store.Run(ctx, p, run.ParentRunID)
		if e != nil {
			return nil, out, e
		}
	}
	if run.Operation != "operations_package" {
		return nil, out, core.ErrInvalid
	}
	head, e := a.Store.OperationsReview(ctx, run.ID)
	if e != nil {
		return nil, out, e
	}
	out.ReviewRunID = run.ID
	if head.Confirm == "" {
		out.State = "awaiting_confirmation"
		return nil, out, nil
	}
	result, e := a.Store.OperationsBatchResult(ctx, head.Confirm)
	if e != nil && !core.IsMissing(e) {
		return nil, out, e
	}
	if result.Entries == nil {
		result.Entries = map[string]core.OperationsBatchReceipt{}
	}
	out.Batch = &result
	out.Receipt = result.Plan
	if result.Settled(head.Count) {
		return nil, out, nil
	}
	_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: head.Confirm + "-receipt", TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE}, engine.OperationsBatchReceipt, head.Confirm)
	return nil, out, accepted(e)
}
func (a *Server) crmPlanBatch(ctx context.Context, p core.Principal, plan string) (string, error) {
	id, e := strconv.ParseInt(plan, 10, 64)
	if e != nil || id <= 0 {
		return "", core.ErrInvalid
	}
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT r.id FROM wb_items i JOIN wb_runs r ON r.id=i.run_id WHERE i.item_id=? AND (r.project_id=? OR ?) AND JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.operation'))='operations_batch_confirm' AND JSON_UNQUOTE(JSON_EXTRACT(JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.text')),'$.plan'))=? LIMIT 2`, core.OperationsBatchResultItem, p.ProjectID, p.IsOwner(), plan)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	run := ""
	if rows.Next() {
		if e = rows.Scan(&run); e != nil {
			return "", e
		}
	}
	if rows.Next() {
		return "", core.ErrConflict
	}
	return run, rows.Err()
}
