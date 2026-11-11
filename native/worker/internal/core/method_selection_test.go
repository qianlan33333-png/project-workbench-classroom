package core

import "testing"

func TestFrozenPerRecordMethodsUseTheSelectedSkill(t *testing.T) {
	run := Run{MethodPointer: "/actions/0/generation/skill_key", Methods: map[string]Method{"review": {Instructions: "original review method"}, "content": {Instructions: "original content method"}}}
	for key, want := range map[string]string{"review": "original review method", "content": "original content method"} {
		row := Record{ID: key, Fields: map[string]any{"actions": []any{map[string]any{"generation": map[string]any{"skill_key": key}}}}}
		got, e := run.MethodFor([]Record{row})
		if e != nil || got.Instructions != want {
			t.Fatal(got, e)
		}
	}
	if _, e := run.MethodFor([]Record{{Fields: map[string]any{"actions": []any{}}}}); e == nil {
		t.Fatal("missing published method accepted")
	}
}
