package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
	"workbench-native/internal/core"
)

// Original history stays read-only. Include this workbench's own existing CRM
// tasks as well, so a new cycle cannot recreate a pending/sent local plan while
// the upstream source is catching up. Freeze the merged read per original run.
func (a *Activities) operationsHistory(ctx context.Context, r core.Run, records, histories []core.Record, stamp string) ([]core.Record, error) {
	byID := map[string]core.Record{}
	for _, row := range histories {
		byID[row.ID] = row
	}
	at, e := time.Parse(time.RFC3339Nano, stamp)
	if e != nil {
		return nil, e
	}
	tz, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		return nil, e
	}
	at = at.In(tz)

	year, number := at.ISOWeek()
	week := fmt.Sprintf("%04d-W%02d", year, number)
	monday := at.AddDate(0, 0, -(int(at.Weekday())+6)%7)
	monday = time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, tz)
	out := []core.Record{}
	for _, row := range records {
		key := "operations-history-" + stepID(r.ID, row.ID)
		frozen, err := a.Store.Checkpoint(ctx, r, key)
		if err == nil {
			out = append(out, core.Record{ID: row.ID, Fields: frozen.Input})
			continue
		}
		if !core.IsMissing(err) {
			return nil, err
		}
		past, exists := byID[row.ID]
		if !exists {
			continue
		}
		if err := a.localOperationsHistory(ctx, r, row.ID, past.Fields, at); err != nil {
			return nil, err
		}
		raw := row.Fields
		if v, ok := row.Fields["raw"].(map[string]any); ok {
			raw = v
		}
		customer := fmt.Sprint(raw["customer_id"])
		id, parseErr := strconv.ParseInt(customer, 10, 64)
		if parseErr == nil && id > 0 {
			rows, err := a.Store.DB.QueryContext(ctx, `SELECT d.body,COALESCE(CASE WHEN JSON_TYPE(JSON_EXTRACT(e.body,'$.actions[0].product_service_key'))='STRING' THEN NULLIF(JSON_UNQUOTE(JSON_EXTRACT(e.body,'$.actions[0].product_service_key')),'null') ELSE NULL END,'') FROM wb_deliveries d JOIN wb_runs r ON r.id=d.run_id JOIN wb_transports t ON t.id=JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.connectionId')) LEFT JOIN wb_records e ON e.resource_id=JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.dispatch.eligibility.resourceId')) AND e.version=JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.dispatch.eligibility.version')) AND e.item_id=d.item_id WHERE JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.principal.projectId'))=? AND JSON_UNQUOTE(JSON_EXTRACT(t.body,'$.kind'))='crm-cli' AND JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.target.customerId'))=? AND NOT (d.state='failed' AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.error')),'')='run_canceled_before_submission') AND NOT (d.state='prepared' AND EXISTS(SELECT 1 FROM wb_decisions q WHERE q.run_id=d.run_id AND q.state='canceled')) AND (d.state IN ('prepared','submitting','accepted','pending_confirmation','unknown') OR JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.updatedAt'))>=?) LIMIT 201`, r.Principal.ProjectID, customer, monday.UTC().Format(time.RFC3339))
			if err != nil {
				return nil, err
			}
			actions, _ := past.Fields["actions"].([]any)
			count := 0
			for rows.Next() {
				var wire []byte
				var service string
				if err = rows.Scan(&wire, &service); err != nil {
					rows.Close()
					return nil, err
				}
				var delivery core.Delivery
				if err = json.Unmarshal(wire, &delivery); err != nil {
					rows.Close()
					return nil, err
				}
				count++
				if service == "" {
					past.Fields["complete"] = false
					past.Fields["history_problem"] = "existing_crm_service_unknown"
				}
				actions = append(actions, map[string]any{"action_id": delivery.ID, "week": week, "product_key": service, "status": delivery.State, "source": "workbench_crm_plan", "plan_id": delivery.Receipt})
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			if count > 200 {
				past.Fields["complete"] = false
			}
			past.Fields["actions"] = actions
		}
		frozen, err = a.Store.FreezeCheckpoint(ctx, r, key, core.Checkpoint{Input: past.Fields})
		if err != nil {
			return nil, err
		}
		out = append(out, core.Record{ID: row.ID, Fields: frozen.Input})
	}
	return out, nil
}

// Feed the existing review and per-recipient receipts back into the original
// copy-index contract. A frozen draft, unknown submission or actual send keeps
// its original business binding across subsequent daily selections.
func (a *Activities) localOperationsHistory(ctx context.Context, r core.Run, ref string, past map[string]any, asOf time.Time) error {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT r.id,JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.shared.week')),JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.text')) FROM wb_items i JOIN wb_runs r ON r.id=i.run_id WHERE r.project_id=? AND JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.operation'))='operations_package' AND i.item_id LIKE 'original-review-row-%' AND JSON_UNQUOTE(JSON_EXTRACT(JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.text')),'$.id'))=? LIMIT 201`, r.Principal.ProjectID, ref)
	if err != nil {
		return err
	}
	type prior struct {
		run, week string
		row       core.OperationsReviewRow
	}
	var items []prior
	for rows.Next() {
		var item prior
		var raw string
		if err = rows.Scan(&item.run, &item.week, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(raw), &item.row); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(items) > 200 {
		past["complete"] = false
		past["history_problem"] = "local_copy_history_exceeds_read_budget"
		return nil
	}
	index, _ := past["copy_index"].(map[string]any)
	if index == nil {
		index = map[string]any{}
		past["copy_index"] = index
	}
	values, _ := index[ref].([]any)
	for _, item := range items {
		if item.row.Blocked != "" {
			continue
		}
		var judgment struct {
			Action map[string]any `json:"action"`
		}
		if json.Unmarshal(item.row.Judgment, &judgment) != nil {
			return core.ErrInvalid
		}
		generation, _ := judgment.Action["generation"].(map[string]any)
		binding, _ := generation["sku_pack_binding"].(map[string]any)
		head, e := a.Store.OperationsReview(ctx, item.run)
		if e != nil {
			return e
		}
		// Drafting does not reserve a business action. Tomorrow's new source
		// may produce a new draft; only the confirmed transaction owns a slot.
		if head.Confirm == "" {
			continue
		}
		if binding == nil && head.Confirm != "" {
			// Old review rows store UI sections, not a machine-readable node.
			// Their frozen unexpired window still owns the customer's action;
			// do not derive a new key and bypass its existing claim.
			if item.row.ExpiresAt == nil || asOf.Before(*item.row.ExpiresAt) {
				past["local_hold_reason"] = "legacy_confirmed_action_window_requires_verification"
			}
			result, e := a.Store.OperationsBatchResult(ctx, head.Confirm)
			if e != nil && !core.IsMissing(e) {
				return e
			}
			receipt := result.Entries[item.row.Key]
			if !receipt.Submitted() && receipt.State != "failed" {
				past["local_hold_reason"] = "legacy_batch_delivery_pending"
			}
			if receipt.Submitted() {
				at, e := time.Parse(time.RFC3339, receipt.At)
				if e != nil || asOf.Sub(at) < 24*time.Hour {
					if receipt.State == "accepted" || receipt.State == "pending_confirmation" {
						past["local_hold_reason"] = "crm_submission_cooldown_24h"
					} else {
						past["local_hold_reason"] = "actual_send_cooldown_24h"
					}
				}
			}
			continue
		}
		entry := map[string]any{"run_id": item.run, "item_key": item.row.Key, "week": item.week, "state": "draft", "binding": binding, "slot_key": binding["slot_key"], "scene": judgment.Action["scene"]}
		if head.Confirm != "" {
			entry["handoff"] = head.Confirm
			entry["state"] = "pending"
			result, e := a.Store.OperationsBatchResult(ctx, head.Confirm)
			if e != nil && !core.IsMissing(e) {
				return e
			}
			receipt := result.Entries[item.row.Key]
			if receipt.Submitted() && receipt.At != "" {
				// The local operations ledger uses CRM acceptance to complete
				// a slot. The persisted receipt keeps the original channel state.
				entry["state"] = "sent"
				entry["sent_at"] = receipt.At
			} else if receipt.State == "failed" {
				// Preserve the failed slot, but do not hold unrelated later nodes
				// as though the original provider outcome were still unknown.
				entry["state"] = "cancelled"
			}
		}
		values = append(values, entry)
	}
	index[ref] = values
	return nil
}
