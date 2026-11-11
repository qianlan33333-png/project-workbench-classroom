package api

import (
	"context"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workbench-native/internal/core"
)

type CRMRefreshInput struct {
	Sources      []core.ResourceSelection `json:"sources,omitempty"`
	OperationKey string                   `json:"operationKey"`
	Destination  string                   `json:"destination"`
}

type CRMPushInput struct {
	OperationKey string                 `json:"operationKey"`
	Title        string                 `json:"title"`
	Plan         *DispatchSelection     `json:"plan,omitempty"`
	Review       *core.OperationsDrafts `json:"review,omitempty"`
}
type CRMReceiptResult struct {
	core.Delivery
	Batch       *core.OperationsBatchStatus `json:"batch,omitempty"`
	ReviewRunID string                      `json:"reviewRunId,omitempty"`
}
type CRMReceiptInput struct {
	PlanID     string `json:"planId,omitempty"`
	RunID      string `json:"runId,omitempty"`
	DeliveryID string `json:"deliveryId,omitempty"`
}

func (a *Server) crmTools(s *mcp.Server, p core.Principal) {
	mcp.AddTool(s, &mcp.Tool{Name: "data_refresh", Description: "Refresh registered raw sources, execute their original cleaning on this workbench, and publish fixed result references. Omitting sources retains the existing CRM CLI collection export. Read run_get for completion and count."}, func(ctx context.Context, _ *mcp.CallToolRequest, in CRMRefreshInput) (*mcp.CallToolResult, RunRef, error) {
		if len(in.Sources) > 0 {
			return a.refreshOperations(ctx, p, RefreshDataInput{OperationKey: in.OperationKey, Sources: in.Sources, Destination: in.Destination})
		}
		if p.Kind != "project" || !core.ValidID(in.OperationKey) || !core.ValidID(in.Destination) {
			return nil, RunRef{}, core.ErrDenied
		}
		if err := a.Store.RequireSourceKind(ctx, p, in.Destination, core.CRMArchiveSource); err != nil {
			return nil, RunRef{}, err
		}
		return a.syncResource(ctx, p, SyncInput{OperationKey: in.OperationKey, Destination: in.Destination, Title: "更新 CRM 资料"})
	})
	mcp.AddTool(s, &mcp.Tool{Name: "crm_push", Description: "Prepare a batch from fixed selection and draft references for one owner confirmation. The installed CRM CLI then submits and approves the original plan. Use crm_receipts for per-recipient execution results."}, func(ctx context.Context, _ *mcp.CallToolRequest, in CRMPushInput) (*mcp.CallToolResult, RunRef, error) {
		if in.Review != nil {
			if in.Plan != nil {
				return nil, RunRef{}, core.ErrInvalid
			}
			return a.prepareLocalOperations(ctx, p, in)
		}
		if in.Plan == nil {
			return nil, RunRef{}, core.ErrInvalid
		}
		if p.Kind != "project" {
			return nil, RunRef{}, core.ErrDenied
		}
		transport, err := a.Store.Transport(ctx, p, in.Plan.ConnectionID)
		if err != nil {
			return nil, RunRef{}, err
		}
		if transport.Kind != "crm-cli" {
			return nil, RunRef{}, core.ErrInvalid
		}
		resource, err := a.Store.Resource(ctx, in.Plan.Eligibility.ResourceID)
		if err != nil {
			return nil, RunRef{}, err
		}
		for _, check := range resource.Checks {
			if check.ID == in.Plan.EligibilityCheck && check.Section == in.Plan.Eligibility.Section && check.Bindings["customerId"] != "" && check.Bindings["staffId"] != "" && check.Bindings["businessSlot"] != "" {
				return a.prepareDispatch(ctx, p, DispatchRequest{OperationKey: in.OperationKey, Title: in.Title, Plan: *in.Plan})
			}
		}
		return nil, RunRef{}, core.ErrInvalid
	})
	mcp.AddTool(s, &mcp.Tool{Name: "crm_receipts", Description: "Read stored per-recipient results for a bound CRM plan. Reconcile an operations batch only while submission is unresolved. Other deliveries retain their existing receipt refresh. Never submits or resends."}, func(ctx context.Context, _ *mcp.CallToolRequest, in CRMReceiptInput) (*mcp.CallToolResult, CRMReceiptResult, error) {
		if p.Kind != "project" && !p.IsOwner() {
			return nil, CRMReceiptResult{}, core.ErrDenied
		}

		if in.RunID != "" && in.DeliveryID == "" && in.PlanID == "" {
			return a.localCRMReceipts(ctx, p, in.RunID)
		}
		if in.PlanID != "" {
			if in.RunID != "" || in.DeliveryID != "" {
				return nil, CRMReceiptResult{}, core.ErrInvalid
			}
			batch, err := a.crmPlanBatch(ctx, p, in.PlanID)
			if err != nil {
				return nil, CRMReceiptResult{}, err
			}
			if batch != "" {
				return a.localCRMReceipts(ctx, p, batch)
			}
			ref, err := a.crmPlanDelivery(ctx, p, in.PlanID)
			if err != nil {
				return nil, CRMReceiptResult{}, err
			}
			in.RunID, in.DeliveryID = ref.RunID, ref.DeliveryID
		}
		if !core.ValidID(in.RunID) || !core.ValidID(in.DeliveryID) {
			return nil, CRMReceiptResult{}, core.ErrInvalid
		}
		result, delivery, err := a.reconcileDelivery(ctx, p, ReceiptRequest{RunID: in.RunID, DeliveryID: in.DeliveryID}, true)
		return result, CRMReceiptResult{Delivery: delivery}, err
	})
}

func (a *Server) crmPlanDelivery(ctx context.Context, p core.Principal, planID string) (ReceiptRequest, error) {
	var ref ReceiptRequest
	id, err := strconv.ParseInt(planID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != planID {
		return ref, core.ErrInvalid
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT d.run_id,d.id
		FROM wb_deliveries d JOIN wb_runs r ON r.id=d.run_id
		JOIN wb_transports t ON t.id=JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.connectionId'))
		WHERE (r.project_id=? OR ?) AND JSON_UNQUOTE(JSON_EXTRACT(t.body,'$.kind'))='crm-cli'
		AND JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.receipt'))=? LIMIT 2`, p.ProjectID, p.IsOwner(), planID)
	if err != nil {
		return ref, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return ref, err
		}
		return ref, core.ErrDenied
	}
	if err = rows.Scan(&ref.RunID, &ref.DeliveryID); err != nil {
		return ref, err
	}
	if rows.Next() {
		return ref, core.ErrDenied
	}
	return ref, rows.Err()
}
