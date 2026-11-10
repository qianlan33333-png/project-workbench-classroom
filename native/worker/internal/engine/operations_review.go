package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"workbench-native/internal/core"
)

func OperationsBatchEnabled(strategy, staff string) bool {
	return strategy != "" && staff != "" && strings.TrimSpace(os.Getenv("WORKBENCH_OPERATIONS_STAFF_NAME")) != "" &&
		os.Getenv("WORKBENCH_OPERATIONS_BATCH_STRATEGY") == strategy &&
		os.Getenv("WORKBENCH_OPERATIONS_ORIGINAL_STAFF_ID") == staff &&
		os.Getenv("WORKBENCH_OPERATIONS_ORIGINAL_SEND") != "1"
}

func (a *Activities) publishOperationsReview(ctx context.Context, run core.Run, batch originalPackage, edit string) error {
	params, err := originalPackageParams(run)
	if err != nil {
		return err
	}
	params["currentPackage"], params["currentWeek"] = params["package"], params["week"]
	if batch.Package != "" {
		params["package"], params["week"] = batch.Package, batch.Week
	}
	params["run"] = batch.Run
	allowed := make(map[string]string, len(batch.Scope))
	for _, member := range batch.Scope {
		allowed[member.Key] = member.ID
	}
	head := core.OperationsReview{Package: params["package"], Week: params["week"], Run: batch.Run, Edit: edit}
	var rows []core.OperationsReviewRow
	for offset := 0; ; {
		params["offset"] = strconv.Itoa(offset)
		raw, err := a.Store.ReadOperationsDirectory(ctx, run.Principal, run.Destination, "package_review", params)
		if err != nil {
			return err
		}
		var page struct {
			State, Run, Digest, IdentityVersion string
			Rows                                []core.OperationsReviewRow
			Next                                int
		}
		if json.Unmarshal(raw, &page) != nil || page.State != "ready" || page.Run != batch.Run || page.Digest == "" ||
			(head.Digest != "" && (page.Digest != head.Digest || page.IdentityVersion != head.IdentityVersion)) ||
			page.IdentityVersion == "" || len(page.Rows) > 20 {
			return core.ErrConflict
		}
		head.Digest, head.IdentityVersion = page.Digest, page.IdentityVersion
		for _, row := range page.Rows {
			if allowed[row.Key] == row.ID && row.ID != "" {
				rows = append(rows, row)
			}
		}
		if page.Next == 0 {
			break
		}
		if page.Next != offset+20 || page.Next >= 5000 {
			return core.ErrInvalid
		}
		offset = page.Next
	}
	return a.Store.PublishOperationsReview(ctx, run.ID, head, rows)
}

func (a *Activities) PublishOriginalOperationsReview(ctx context.Context, id string, batch originalPackage) error {
	defer keepAlive(ctx)()
	run, err := a.Store.ExecutionRun(ctx, id)
	if err != nil || run.Operation != "operations_package" {
		return failure(errors.Join(err, core.ErrDenied))
	}
	return a.publishOperationsReview(ctx, run, batch, "")
}

func (a *Activities) refreshEditedOperationsReview(ctx context.Context, daily, edit string) error {
	parent, err := a.Store.ExecutionRun(ctx, daily)
	if err != nil || parent.Operation != "operations_package" {
		return errors.Join(err, core.ErrDenied)
	}
	item, err := a.Store.Item(ctx, daily, OriginalOperationsRoster)
	if err != nil {
		return err
	}
	var batch originalPackage
	if err = json.Unmarshal([]byte(item.Text), &batch); err != nil {
		return err
	}
	return a.publishOperationsReview(ctx, parent, batch, edit)
}

// Confirmation is serialized with source edits. The batch submission and
// receipt workflow subsequently use only the persisted immutable manifest.
func ConfirmOperationsBatch(ctx workflow.Context, id string) error {
	ctx = operationsPackageActivityContext(ctx)
	return workflow.ExecuteActivity(ctx, "FreezeOperationsBatch", id).Get(ctx, nil)
}

func (a *Activities) FreezeOperationsBatch(ctx context.Context, id string) error {
	defer keepAlive(ctx)()
	run, err := a.Store.ExecutionRun(ctx, id)
	if err != nil || run.Operation != "operations_batch_confirm" {
		return failure(errors.Join(err, core.ErrDenied))
	}
	parent, err := a.Store.Run(ctx, run.Principal, run.ParentRunID)
	if err != nil || parent.Operation != "operations_package" {
		return failure(errors.Join(err, core.ErrDenied))
	}
	head, err := a.Store.OperationsReview(ctx, parent.ID)
	if err != nil {
		return err
	}
	if head.Confirm == id {
		return a.StartOperationsBatchDelivery(ctx, id)
	}
	staff, transport, version := sharedString(run, "staff"), sharedString(run, "transport"), sharedString(run, "version")
	name := strings.TrimSpace(os.Getenv("WORKBENCH_OPERATIONS_STAFF_NAME"))
	if sharedString(parent, "day") != originalOperationsDay(time.Now()) ||
		!OperationsBatchEnabled(sharedString(parent, "strategy"), staff) ||
		head.Edit != "" || head.Confirm != "" || version != core.OperationsReviewVersion(head, transport, staff, name) {
		return a.operationsBatchSourceChanged(ctx, id, version)
	}
	params := map[string]string{"package": head.Package, "week": head.Week, "run": head.Run, "version": "1",
		"day": sharedString(parent, "day"), "strategy": sharedString(parent, "strategy"),
		"currentPackage": sharedString(parent, "package"), "currentWeek": sharedString(parent, "week")}
	raw, err := a.Store.ReadOperationsDirectory(ctx, run.Principal, parent.Destination, "package_review", params)
	if err != nil {
		return err
	}
	var checked struct {
		State, Digest, IdentityVersion string
		Exclusive                      bool
		ActiveKeys                     []string
	}
	if json.Unmarshal(raw, &checked) != nil || checked.State != "ready" || !checked.Exclusive || checked.IdentityVersion == "" {
		return a.operationsBatchSourceChanged(ctx, id, version)
	}
	if checked.Digest != head.Digest || checked.IdentityVersion != head.IdentityVersion {
		if err := a.refreshEditedOperationsReview(ctx, parent.ID, ""); err != nil {
			return err
		}
		head, err = a.Store.OperationsReview(ctx, parent.ID)
		if err != nil {
			return err
		}
		if version != core.OperationsReviewVersion(head, transport, staff, name) ||
			head.Digest != checked.Digest || head.IdentityVersion != checked.IdentityVersion {
			return a.operationsBatchSourceChanged(ctx, id, version)
		}
	}
	c, err := a.Store.Transport(ctx, run.Principal, transport)
	if err != nil || c.Kind != "crm-cli" {
		return failure(errors.Join(err, core.ErrDenied))
	}
	if err = a.Store.ConfirmOperationsReview(ctx, parent, id, version, staff, transport, name, checked.ActiveKeys); errors.Is(err, core.ErrConflict) {
		return a.operationsBatchSourceChanged(ctx, id, version)
	}
	if err != nil {
		return err
	}
	return a.StartOperationsBatchDelivery(ctx, id)
}

func sharedString(run core.Run, key string) string {
	value, _ := run.Shared[key].(string)
	return value
}

func (a *Activities) operationsBatchSourceChanged(ctx context.Context, id, version string) error {
	return a.Store.RecordItem(ctx, id, core.ItemResult{ID: OriginalOperationsConfirmation, State: "succeeded", InputHash: version, Text: "source_changed"})
}

// The ordinary receipt sweep also discovers committed confirmations after
// restarts. A connector claim distinguishes a new batch from readback.
func OperationsBatchDelivery(ctx workflow.Context, id string) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		TaskQueue: InteractiveQueue, StartToCloseTimeout: 12 * time.Minute, HeartbeatTimeout: 2 * time.Minute,
		WaitForCancellation: true, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	return workflow.ExecuteActivity(ctx, "ReadOrSubmitOperationsBatch", id).Get(ctx, nil)
}

func (a *Activities) ReadOrSubmitOperationsBatch(ctx context.Context, id string) error {
	defer keepAlive(ctx)()
	run, manifest, err := a.Store.OperationsBatchManifest(ctx, id)
	if err != nil {
		return err
	}
	previous, err := a.Store.OperationsBatchResult(ctx, id)
	if err != nil && !core.IsMissing(err) {
		return err
	}
	if len(previous.Entries) == len(manifest) {
		settled := true
		for _, v := range previous.Entries {
			settled = settled && (v.State == "sent" || v.State == "failed")
		}
		if settled {
			return nil
		}
	}
	transport, err := a.Store.Transport(ctx, run.Principal, sharedString(run, "transport"))
	if err != nil || transport.Kind != "crm-cli" {
		return errors.Join(err, core.ErrDenied)
	}
	send := OperationsBatchEnabled(sharedString(run, "strategy"), sharedString(run, "staff"))
	session, err := connectConnectorWithTimeout(ctx, transport, 11*time.Minute)
	if err != nil {
		return err
	}
	defer session.Close()
	name := "operations_batch_receipt"
	if send {
		name = "operations_batch_send"
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]string{"connectionId": transport.ID, "runId": id}})
	if err != nil || result.IsError {
		return errors.Join(err, errors.New("CRM 批量处理结果待核实"))
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	var out core.OperationsBatchStatus
	if err = json.Unmarshal(raw, &out); err != nil || len(out.Entries) != len(manifest) {
		return core.ErrInvalid
	}
	for _, row := range manifest {
		switch out.Entries[row.Key].State {
		case "accepted", "pending_confirmation", "sent", "failed", "unknown":
		default:
			return core.ErrInvalid
		}
	}
	return a.Store.SaveOperationsBatchResult(ctx, id, out)
}

func (a *Activities) QueueOperationsReviewMaintenance(ctx context.Context) error {
	day := originalOperationsDay(time.Now())
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT r.id, r.project_id, COALESCE(JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.text')),'')
		FROM wb_runs r JOIN wb_items roster ON roster.run_id=r.id AND roster.item_id=?
		LEFT JOIN wb_items i ON i.run_id=r.id AND i.item_id=?
		WHERE JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.shared.day'))=?
		  AND JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.operation'))='operations_package'
		  AND JSON_EXTRACT(JSON_UNQUOTE(JSON_EXTRACT(roster.body,'$.text')),'$.state')='ready'
		  AND (i.item_id IS NULL OR JSON_EXTRACT(JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.text')),'$.Edit')<>'')
		ORDER BY r.created_at LIMIT 20`, OriginalOperationsRoster, core.OperationsReviewItem, day)
	if err != nil {
		return err
	}
	type pending struct{ id, project, header string }
	var work []pending
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.project, &item.header); err != nil {
			rows.Close()
			return err
		}
		work = append(work, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range work {
		if item.header == "" {
			described, err := a.Temporal.DescribeWorkflowExecution(ctx, item.id, "")
			if err != nil || described.GetWorkflowExecutionInfo().GetStatus() != enums.WORKFLOW_EXECUTION_STATUS_COMPLETED {
				continue
			}
		} else {
			described, err := a.Temporal.DescribeWorkflowExecution(ctx, ProjectWriterID(item.project), "")
			var missing *serviceerror.NotFound
			if (err != nil && !errors.As(err, &missing)) || (err == nil && described.GetWorkflowExecutionInfo().GetStatus() == enums.WORKFLOW_EXECUTION_STATUS_RUNNING) {
				continue
			}
		}
		var head core.OperationsReview
		if item.header != "" && json.Unmarshal([]byte(item.header), &head) != nil {
			continue
		}
		if err := a.refreshEditedOperationsReview(ctx, item.id, head.Edit); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

func (a *Activities) QueueOperationsBatchReceipts(ctx context.Context) error {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT r.id FROM wb_runs r
		JOIN wb_items i ON i.run_id=JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.parentRunId')) AND i.item_id=?
		LEFT JOIN wb_items result ON result.run_id=r.id AND result.item_id=?
		WHERE JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.operation'))='operations_batch_confirm'
		  AND JSON_UNQUOTE(JSON_EXTRACT(JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.text')),'$.Confirm'))=r.id
		  AND (result.item_id IS NULL OR JSON_SEARCH(JSON_EXTRACT(JSON_UNQUOTE(JSON_EXTRACT(result.body,'$.text')),'$.entries.*.state'),'one','unknown') IS NOT NULL
		  OR JSON_SEARCH(JSON_EXTRACT(JSON_UNQUOTE(JSON_EXTRACT(result.body,'$.text')),'$.entries.*.state'),'one','accepted') IS NOT NULL
		  OR JSON_SEARCH(JSON_EXTRACT(JSON_UNQUOTE(JSON_EXTRACT(result.body,'$.text')),'$.entries.*.state'),'one','pending_confirmation') IS NOT NULL)
		ORDER BY COALESCE(JSON_UNQUOTE(JSON_EXTRACT(result.body,'$.createdAt')), '') LIMIT 20`, core.OperationsReviewItem, core.OperationsBatchResultItem)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		if err := a.StartOperationsBatchDelivery(ctx, id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (a *Activities) StartOperationsBatchDelivery(ctx context.Context, id string) error {
	_, err := a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id + "-delivery", TaskQueue: InteractiveQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE}, OperationsBatchDelivery, id)
	if temporal.IsWorkflowExecutionAlreadyStartedError(err) {
		return nil
	}
	return err
}
