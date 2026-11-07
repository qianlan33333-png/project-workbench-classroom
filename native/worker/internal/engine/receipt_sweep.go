package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"workbench-native/internal/core"
)

// Receipt checks never invoke message_send. Provider ambiguity is bounded:
// three automatic checks, or 288 for phone confirmation, then manual follow-up.
func ReceiptSweep(ctx workflow.Context) error {
	ctx = activityContext(ctx, InteractiveQueue)
	if e := workflow.ExecuteActivity(ctx, "QueueReceiptChecks").Get(ctx, nil); e != nil {
		return e
	}
	return workflow.ExecuteActivity(ctx, "SummarizeBatchReceipts").Get(ctx, nil)
}
func (a *Activities) QueueReceiptChecks(ctx context.Context) error {
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT id FROM wb_deliveries WHERE state IN ('submitting','unknown','accepted','pending_confirmation') AND COALESCE(JSON_EXTRACT(body,'$.receiptChecks'),0)<IF(state='pending_confirmation',288,3) AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.updatedAt')) < DATE_FORMAT(UTC_TIMESTAMP()-INTERVAL 5 MINUTE,'%Y-%m-%dT%H:%i:%s') ORDER BY JSON_UNQUOTE(JSON_EXTRACT(body,'$.updatedAt')) LIMIT 100`)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id + "-receipt-check", TaskQueue: InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE}, AutomaticReceipt, id)
		if e != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(e) {
			return e
		}
	}
	return nil
}
func AutomaticReceipt(ctx workflow.Context, id string) error {
	ctx = activityContext(ctx, InteractiveQueue)
	if e := workflow.ExecuteActivity(ctx, "MarkReceiptCheck", id, workflow.GetInfo(ctx).WorkflowExecution.RunID).Get(ctx, nil); e != nil {
		return e
	}
	return workflow.ExecuteActivity(ctx, "ReconcileDelivery", id).Get(ctx, nil)
}
func (a *Activities) MarkReceiptCheck(ctx context.Context, id, attempt string) error {
	_, e := a.Store.DB.ExecContext(ctx, `UPDATE wb_deliveries SET body=JSON_SET(body,'$.receiptChecks',CAST(COALESCE(JSON_EXTRACT(body,'$.receiptChecks'),0) AS UNSIGNED)+1,'$.receiptCheckAttempt',?,'$.updatedAt',DATE_FORMAT(UTC_TIMESTAMP(6),'%Y-%m-%dT%H:%i:%s.%fZ')) WHERE id=? AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(body,'$.receiptCheckAttempt')),'')<>?`, attempt, id, attempt)
	return e
}

func (a *Activities) SummarizeBatchReceipts(ctx context.Context) error {
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT body FROM wb_runs WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='dispatch_batch' ORDER BY created_at DESC LIMIT 100`)
	if e != nil {
		return e
	}
	runs := []core.Run{}
	for rows.Next() {
		var raw []byte
		var r core.Run
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal(raw, &r); e != nil {
			rows.Close()
			return e
		}
		runs = append(runs, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, r := range runs {
		stats, e := a.Store.DeliveryStats(ctx, r.Principal, r.ID)
		if e != nil {
			continue
		}
		if stats.Total == 0 || stats.States["prepared"] == stats.Total {
			continue
		}
		text := fmt.Sprintf("本批回执：共 %d 条，已发送 %d，已送达 %d，待手机确认 %d，渠道受理 %d，失败 %d，未知 %d。", stats.Total, stats.States["sent"], stats.States["delivered"], stats.States["pending_confirmation"], stats.States["accepted"], stats.States["failed"], stats.States["unknown"]+stats.States["submitting"])
		if e = a.Store.Emit(ctx, core.RunEvent{ID: r.ID + "-receipts-" + core.Digest(stats)[:16], SessionID: r.Principal.SessionID, RunID: r.ID, Kind: "message.batch_receipts", Text: text, Details: stats}); e != nil {
			return e
		}
	}
	return nil
}
