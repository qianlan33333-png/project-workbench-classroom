package operations

import (
	"context"
	"testing"
)

func TestEmbeddedRulesCleanAndRejectIdentity(t *testing.T) {
	input := map[string]any{"action": "data_refresh", "asOf": "2026-11-08T12:00:00+08:00", "records": []any{map[string]any{"id": "usr_0123456789ab", "fields": map[string]any{"safe_user_ref": "usr_0123456789ab", "profile": map[string]any{"current_need": "keep source fact"}}}}}
	var out map[string]any
	if e := Evaluate(context.Background(), input, &out); e != nil {
		t.Fatal(e)
	}
	records := out["records"].([]any)
	if len(records) != 1 {
		t.Fatal(out)
	}
	input["records"] = []any{map[string]any{"id": "wrong", "fields": map[string]any{"safe_user_ref": "usr_0123456789ab"}}}
	if e := Evaluate(context.Background(), input, &out); e == nil {
		t.Fatal("identity mismatch accepted")
	}
}
func TestEmbeddedRulesMissingHistoryCannotSelect(t *testing.T) {
	input := map[string]any{"action": "strategy_select", "asOf": "2026-11-08T12:00:00+08:00", "records": []any{map[string]any{"id": "usr_0123456789ab", "fields": map[string]any{"safe_user_ref": "usr_0123456789ab"}}}, "strategies": []any{map[string]any{"id": "s", "fields": map[string]any{"key": "theme:original", "status": "published"}}}, "histories": []any{}}
	var out struct {
		Records []struct {
			Fields struct {
				Selected bool
				Reason   string
			}
		}
	}
	if e := Evaluate(context.Background(), input, &out); e != nil {
		t.Fatal(e)
	}
	if len(out.Records) != 1 || out.Records[0].Fields.Selected || out.Records[0].Fields.Reason != "contact_history_incomplete" {
		t.Fatal(out)
	}
}
