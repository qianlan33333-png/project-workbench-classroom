package api

import (
	"context"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workbench-native/internal/core"
)

type CRMRefreshInput struct {
	OperationKey string `json:"operationKey"`
	Destination  string `json:"destination"`
}

type CRMReceiptInput struct {
	PlanID     string `json:"planId,omitempty"`
	RunID      string `json:"runId,omitempty"`
	DeliveryID string `json:"deliveryId,omitempty"`
}

func (a *Server) crmTools(s *mcp.Server, p core.Principal) {
	mcp.AddTool(s, &mcp.Tool{Name: "data_refresh", Description: "Export raw CRM facts with the installed CRM CLI and publish a registered collection through the existing resource sync. Read run_get for completion and count."}, func(ctx context.Context, _ *mcp.CallToolRequest, in CRMRefreshInput) (*mcp.CallToolResult, RunRef, error) {
		if p.Kind != "project" || !core.ValidID(in.OperationKey) || !core.ValidID(in.Destination) {
			return nil, RunRef{}, core.ErrDenied
		}
		if err := a.Store.RequireSourceKind(ctx, p, in.Destination, core.CRMArchiveSource); err != nil {
			return nil, RunRef{}, err
		}
		return a.syncResource(ctx, p, SyncInput{OperationKey: in.OperationKey, Destination: in.Destination, Title: "更新 CRM 资料"})
	})
	mcp.AddTool(s, &mcp.Tool{Name: "crm_push", Description: "Prepare a batch using fixed drafts and a registered CRM recipient list. After the existing owner confirmation, the installed CRM CLI submits and approves the original plans. Read message_batch_read for plan identifiers and states."}, func(ctx context.Context, _ *mcp.CallToolRequest, in DispatchRequest) (*mcp.CallToolResult, RunRef, error) {
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
				return a.prepareDispatch(ctx, p, in)
			}
		}
		return nil, RunRef{}, core.ErrInvalid
	})
	mcp.AddTool(s, &mcp.Tool{Name: "crm_receipts", Description: "Refresh an original CRM plan bound to this workbench's delivery. Returns the stored state while the existing receipt task queries CRM. Never submits or resends."}, func(ctx context.Context, _ *mcp.CallToolRequest, in CRMReceiptInput) (*mcp.CallToolResult, core.Delivery, error) {
		if p.Kind != "project" && !p.IsOwner() {
			return nil, core.Delivery{}, core.ErrDenied
		}
		if in.PlanID != "" {
			if in.RunID != "" || in.DeliveryID != "" {
				return nil, core.Delivery{}, core.ErrInvalid
			}
			ref, err := a.crmPlanDelivery(ctx, p, in.PlanID)
			if err != nil {
				return nil, core.Delivery{}, err
			}
			in.RunID, in.DeliveryID = ref.RunID, ref.DeliveryID
		}
		if !core.ValidID(in.RunID) || !core.ValidID(in.DeliveryID) {
			return nil, core.Delivery{}, core.ErrInvalid
		}
		return a.reconcileDelivery(ctx, p, ReceiptRequest{RunID: in.RunID, DeliveryID: in.DeliveryID}, true)
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
