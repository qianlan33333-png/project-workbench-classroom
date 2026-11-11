package api

import "testing"

func TestOperationsDraftRequiresEvidenceAndContent(t *testing.T) {
	draft := operationsDraft{Interested: true, Focus: "首次使用", Reason: "已有加入时间", Evidence: []string{"account", "material"}, Message: "可以先试一个小问题。"}
	facts := map[string]any{"evidence": map[string]any{"account": map[string]any{"created_at": "2026-11-09"}}}
	knowledge := map[string]any{"knowledge_excerpts": []any{map[string]any{"id": "material", "text": "首次使用说明"}}}
	if !draftEvidenceValid(draft, facts, knowledge) {
		t.Fatal("the draft should use the exact facts and material in this run")
	}
	for _, missing := range []string{"unknown", "another-user"} {
		draft.Evidence = []string{"account", missing}
		if draftEvidenceValid(draft, facts, knowledge) {
			t.Fatal("a reference absent from the frozen input cannot qualify a draft")
		}
	}
	draft.Evidence = []string{"account"}
	draft.Message = " "
	if draftEvidenceValid(draft, facts, knowledge) {
		t.Fatal("blank message was accepted")
	}
}

func TestOperationsProfileOutputUsesOnlyOriginalFields(t *testing.T) {
	method := operationsMethod("operations-profile", "整理当前档案。", "profile")
	if err := method.ValidateOutput(`{"主要卡点":"确定第一步"}`); err != nil {
		t.Fatal(err)
	}
	if method.ValidateOutput(`{"发送决定":"立即发送"}`) == nil {
		t.Fatal("an external action is not a profile field")
	}
}
