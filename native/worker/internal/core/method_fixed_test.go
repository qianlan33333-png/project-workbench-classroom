package core

import "testing"

func TestFixedOutputUsesPinnedValueAndSchema(t *testing.T) {
	m := Method{FixedOutputPointer: "/fixed", OutputSchema: map[string]any{"type": "object", "required": []any{"text"}, "properties": map[string]any{"text": map[string]any{"type": "string", "minLength": 1}}}}
	text, fixed, err := m.FixedOutput([]Record{{Fields: map[string]any{"fixed": map[string]any{"text": "unchanged owner template"}}}})
	if err != nil || !fixed || text != `{"text":"unchanged owner template"}` {
		t.Fatal(text, fixed, err)
	}
	if _, fixed, err = m.FixedOutput([]Record{{Fields: map[string]any{"fact": "requires personalization"}}}); err != nil || fixed {
		t.Fatal("missing fixed value must use the configured model", fixed, err)
	}
	if _, _, err = m.FixedOutput([]Record{{Fields: map[string]any{"fixed": map[string]any{"text": ""}}}}); err == nil {
		t.Fatal("invalid fixed output bypassed schema")
	}
}
