package engine

import (
	"testing"
	"workbench-native/internal/core"
)

func localSelectionFixture() core.Record {
	return core.Record{ID: "usr_0123456789ab", Fields: map[string]any{"selected": true, "profile": map[string]any{"customer_id": "123"}, "crmSlot": "original-slot", "actions": []any{map[string]any{"strategy_key": "hxc:ongoing", "evidence_refs": []any{"source:v1", "s"}, "generation": map[string]any{"sku_pack_binding": map[string]any{"slot_key": "original-slot", "cycle_key": "cycle", "node": "FOLLOWUP", "window_end": "2026-12-10T00:00:00+08:00"}}}}}}
}
func TestLocalReviewPreservesBusinessSlotAndFixedText(t *testing.T) {
	record := localSelectionFixture()
	row, why, err := localOperationsRow(record, `{"status":"draft","text":"exact reviewed copy","evidence_refs":["source:v1"]}`, "hxc:ongoing")
	if err != nil || why != "" || row.Customer != "123" || row.Message != "exact reviewed copy" || row.ExpiresAt == nil {
		t.Fatal(row, why, err)
	}
	record.Fields["day"] = "tomorrow"
	again, _, _ := localOperationsRow(record, `{"status":"draft","text":"different draft","evidence_refs":["source:v1"]}`, "hxc:ongoing")
	if row.Slot != again.Slot {
		t.Fatal("cross-day or copy edits recreated an action slot")
	}
	blocked, why, err := localOperationsRow(record, `{"status":"blocked","text":"","blocked_reason":"missing source"}`, "hxc:ongoing")
	if err != nil || why != "missing source" || blocked.Message != "" {
		t.Fatal(blocked, why, err)
	}
	record.Fields["profile"].(map[string]any)["customer_id"] = "unknown"
	if _, why, err = localOperationsRow(record, `{"status":"draft","text":"x","evidence_refs":["s"]}`, "hxc:ongoing"); err != nil || why != "crm_identity_missing" {
		t.Fatal(why, err)
	}
}
func TestLocalReviewOnlyAcceptsSourceBoundAttachments(t *testing.T) {
	record := localSelectionFixture()
	record.Fields["attachments"] = []any{map[string]any{"type": "mini_program", "material_id": float64(21), "verified": true, "title": "Original card", "appid": "wx123", "page": "pages/original"}}
	row, why, err := localOperationsRow(record, `{"status":"draft","text":"x","evidence_refs":["s"]}`, "hxc:ongoing")
	if err != nil || why != "" || len(row.Materials) != 1 || row.Materials[0].ID != "21" {
		t.Fatal(row, why, err)
	}
	record.Fields["attachments"].([]any)[0].(map[string]any)["verified"] = false
	if _, why, err = localOperationsRow(record, `{"status":"draft","text":"x","evidence_refs":["s"]}`, "hxc:ongoing"); err != nil || why != "material_unverified" {
		t.Fatal(why, err)
	}
}
