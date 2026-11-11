package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"workbench-native/internal/core"
)

func LocalOperationsReview(ctx workflow.Context, id string) (err error) {
	defer reportFailure(ctx, id, &err)()
	ctx = operationsPackageActivityContext(ctx)
	return workflow.ExecuteActivity(ctx, "PrepareLocalOperationsReview", id).Get(ctx, nil)
}

// The same review/confirmation/connector used by the existing page owns the
// local result. No original package is created, exported, edited or imported.
func (a *Activities) PrepareLocalOperationsReview(ctx context.Context, id string) error {
	defer keepAlive(ctx)()
	run, err := a.Store.ExecutionRun(ctx, id)
	if err != nil {
		return err
	}
	if !core.IsLocalOperations(run) || run.Operations == nil || run.Operations.Review == nil {
		return failure(core.ErrInvalid)
	}
	spec := run.Operations.Review
	if current, e := a.Store.OperationsReview(ctx, id); e == nil && current.Digest != "" {
		return nil
	} else if e != nil && !core.IsMissing(e) {
		return e
	}
	selection, err := a.Store.Run(ctx, run.Principal, spec.Selection.Version)
	if err != nil || selection.Operation != "strategy_select" || selection.Destination != spec.Selection.ResourceID {
		return failure(core.ErrInvalid)
	}
	snapshot, err := a.Store.Snapshot(ctx, spec.Selection.ResourceID, spec.Selection.Version)
	if err != nil || snapshot.State != "published" || snapshot.Count > 5000 {
		return failure(errors.Join(err, core.ErrInvalid))
	}
	drafts := map[string]string{}
	for _, ref := range spec.Drafts {
		generated, err := a.Store.Run(ctx, run.Principal, ref.Version)
		if err != nil || generated.Operation != "generate" || generated.Destination != ref.ResourceID || len(generated.Sources) != 1 || generated.Sources[0] != spec.Selection {
			return failure(core.ErrInvalid)
		}
		snap, err := a.Store.Snapshot(ctx, ref.ResourceID, ref.Version)
		if err != nil || snap.State != "published" {
			return failure(core.ErrConflict)
		}
		stats, err := a.Store.Stats(ctx, run.Principal, generated.ID)
		if err != nil || stats.Failed > 0 || stats.Pending > 0 {
			return failure(errors.Join(err, core.ErrConflict))
		}
		for after := ""; ; {
			page, err := a.Store.Read(ctx, run.Principal, ref, after, 200)
			if err != nil {
				return err
			}
			for _, row := range page.Records {
				if _, exists := drafts[row.ID]; exists {
					return failure(core.ErrConflict)
				}
				text, ok := row.Fields["text"].(string)
				if !ok {
					return failure(core.ErrInvalid)
				}
				drafts[row.ID] = text
			}
			if page.Next == "" {
				break
			}
			after = page.Next
		}
	}
	if len(drafts) != snapshot.Count {
		return failure(core.ErrConflict)
	}
	rows := []core.OperationsReviewRow{}
	blocked := map[string]string{}
	roster := originalPackage{State: "empty", Run: "copybatch-" + strings.ReplaceAll(id, "-", ""), Daily: true, Package: sharedString(run, "package"), Week: sharedString(run, "week"), Scope: []originalOperationsMember{}}
	for after := ""; ; {
		page, err := a.Store.Read(ctx, run.Principal, spec.Selection, after, 200)
		if err != nil {
			return err
		}
		for _, record := range page.Records {
			text, ok := drafts[record.ID]
			if !ok {
				return failure(core.ErrConflict)
			}
			row, reason, err := localOperationsRow(record, text, sharedString(run, "strategy"))
			if err != nil {
				return failure(err)
			}
			if reason != "" {
				blocked[record.ID] = reason
				continue
			}
			rows = append(rows, row)
			roster.Scope = append(roster.Scope, originalOperationsMember{Key: row.Key, ID: row.ID, Reason: row.Reason, ExpiresAt: row.ExpiresAt})
		}
		if page.Next == "" {
			break
		}
		after = page.Next
	}
	if err = a.bindOperationsMaterials(ctx, run, rows); err != nil {
		return err
	}
	if len(rows) > 0 {
		roster.State = "ready"
	}
	roster.Total = len(rows)
	raw, _ := json.Marshal(roster)
	item := core.ItemResult{ID: OriginalOperationsRoster, State: "succeeded", Text: string(raw), InputHash: core.Digest(spec)}
	if err = a.Store.PublishLocalOperationsReview(ctx, id, core.OperationsReview{Package: roster.Package, Week: roster.Week, Run: roster.Run}, rows, item); err != nil {
		return err
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: id + "-prepared", RunID: id, SessionID: run.Principal.SessionID, Kind: "operations.prepared", Text: "本轮稿件已保存，等待一次整批确认。尚未创建 CRM 计划。", Details: map[string]any{"selected": snapshot.Count, "drafts": len(rows), "blocked": blocked}})
}

var localCustomerID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

func localOperationsRow(record core.Record, text, strategy string) (core.OperationsReviewRow, string, error) {
	var out struct {
		Status, Text  string
		BlockedReason string   `json:"blocked_reason"`
		Evidence      []string `json:"evidence_refs"`
	}
	if json.Unmarshal([]byte(text), &out) != nil {
		return core.OperationsReviewRow{}, "", core.ErrInvalid
	}
	if out.Status == "blocked" && out.Text == "" && out.BlockedReason != "" {
		return core.OperationsReviewRow{}, out.BlockedReason, nil
	}
	if out.Status != "draft" || strings.TrimSpace(out.Text) == "" || record.Fields["selected"] != true {
		return core.OperationsReviewRow{}, "", core.ErrInvalid
	}
	if reason, _ := record.Fields["deliveryBlocked"].(string); reason != "" {
		return core.OperationsReviewRow{}, reason, nil
	}
	profile, _ := record.Fields["profile"].(map[string]any)
	actions, _ := record.Fields["actions"].([]any)
	if len(actions) != 1 {
		return core.OperationsReviewRow{}, "", core.ErrInvalid
	}
	action, ok := actions[0].(map[string]any)
	if !ok {
		return core.OperationsReviewRow{}, "", core.ErrInvalid
	}
	if action["strategy_key"] != strategy {
		return core.OperationsReviewRow{}, "strategy_scope_changed", nil
	}
	customer := fmt.Sprint(profile["customer_id"])
	if !localCustomerID.MatchString(customer) {
		return core.OperationsReviewRow{}, "crm_identity_missing", nil
	}
	slot, _ := record.Fields["crmSlot"].(string)
	if slot == "" {
		return core.OperationsReviewRow{}, "business_slot_missing", nil
	}
	generation, _ := action["generation"].(map[string]any)
	binding, _ := generation["sku_pack_binding"].(map[string]any)
	var identity any = []any{"original-action", record.ID, slot}
	if binding["slot_key"] != nil && binding["slot_key"] != "" {
		for _, key := range []string{"slot_key", "cycle_key", "node"} {
			if binding[key] == nil || binding[key] == "" {
				return core.OperationsReviewRow{}, "service_node_incomplete", nil
			}
		}
		identity = []any{"service-node", record.ID, binding["slot_key"], binding["cycle_key"], binding["node"]}
	} else if strategy == "hxc:onboarding" {
		if binding["window_start"] == nil || binding["window_end"] == nil {
			return core.OperationsReviewRow{}, "newcomer_window_unverified", nil
		}
		identity = []any{"newcomer-window", record.ID, strategy, binding["window_start"], binding["window_end"]}
	}
	var expires *time.Time
	for _, value := range []any{record.Fields["actionExpiresAt"], binding["window_end"], binding["entitlement_end"]} {
		if text, ok := value.(string); ok {
			if at, err := time.Parse(time.RFC3339, text); err == nil && (expires == nil || at.Before(*expires)) {
				expires = &at
			}
		}
	}
	key := core.Digest(identity)
	judgment, _ := json.Marshal(map[string]any{"strategy": strategy, "action": action, "crmSlot": slot, "evidence_refs": out.Evidence})
	name, _ := profile["CRM展示名"].(string)
	reason, _ := action["why_now"].(string)
	row := core.OperationsReviewRow{Key: key, ID: record.ID, Nickname: name, Reason: reason, Message: out.Text, Progress: "工作台原策略稿件，待整批确认", Revision: 1, Editable: true, Judgment: judgment, Attachments: json.RawMessage("[]"), Customer: customer, Slot: key, ExpiresAt: expires, Materials: []core.OperationsMaterial{}}
	cards, _ := record.Fields["attachments"].([]any)
	for _, value := range cards {
		card, ok := value.(map[string]any)
		if !ok || (card["verified"] != true && card["status"] != "source_readonly_route") || (card["type"] != "mini_program" && card["type"] != "miniprogram") {
			return row, "material_unverified", nil
		}
		material := core.OperationsMaterial{ID: fmt.Sprint(card["material_id"])}
		material.Title, _ = card["title"].(string)
		material.AppID, _ = card["appid"].(string)
		material.Page, _ = card["page"].(string)
		material.Page = strings.TrimPrefix(material.Page, "/")
		if card["status"] == "source_readonly_route" {
			material.ID = ""
		}
		if (material.ID != "" && !localCustomerID.MatchString(material.ID)) || material.Title == "" || material.AppID == "" || material.Page == "" {
			return row, "material_unverified", nil
		}
		row.Materials = append(row.Materials, material)
	}
	row.Attachments, _ = json.Marshal(cards)

	return row, "", nil
}

// Reuse the existing maintenance sweep only for the DB-commit/start gap.
// Failed/canceled executions require explicit run_resume; no new task is made.
func (a *Activities) recoverLocalOperationsPreparation(ctx context.Context) error {
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT id FROM wb_runs WHERE (JSON_EXTRACT(body,'$.shared.local')=true AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='operations_package' OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='operations_batch_confirm') AND NOT EXISTS(SELECT 1 FROM wb_items i WHERE i.run_id=wb_runs.id AND i.item_id=?) AND NOT EXISTS(SELECT 1 FROM wb_items m WHERE m.run_id=wb_runs.id AND m.item_id='operations-preparation-start-observed') AND NOT EXISTS(SELECT 1 FROM wb_items c WHERE c.run_id=wb_runs.id AND c.item_id='operations-batch-cancelled') ORDER BY created_at LIMIT 20`, core.OperationsReviewItem)
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
		_, e := a.Temporal.DescribeWorkflowExecution(ctx, id, "")
		var missing *serviceerror.NotFound
		if e == nil {
			if e = a.Store.RecordItem(ctx, id, core.ItemResult{ID: "operations-preparation-start-observed", State: "succeeded", InputHash: core.Digest(id), Text: "Temporal execution observed; explicit resume owns failed or cancelled tasks."}); e != nil {
				return e
			}
			continue
		}
		if !errors.As(e, &missing) {
			return e
		}
		run, e := a.Store.ExecutionRun(ctx, id)
		if e != nil {
			continue
		}
		operation := "operations_local_review"
		if run.Operation == "operations_batch_confirm" {
			operation = run.Operation
		}
		_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, WriteTask, WriteInput{RunID: id, ProjectID: run.Principal.ProjectID, Operation: operation})
		if e != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(e) {
			return e
		}
	}
	return nil
}
