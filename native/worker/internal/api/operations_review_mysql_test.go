package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"workbench-native/internal/core"
)

func TestMySQLOperationsReviewLocalAndFrozen(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") && !strings.Contains(dsn, "/wbop_") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := core.Open(ctx, dsn)
	must(err)
	defer s.DB.Close()
	must(s.Migrate(ctx))
	id := uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	p := operationsPrincipal(id)
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "local operations review"}))
	api := &Server{Store: s, Token: strings.Repeat("x", 40)}
	input := core.Ref{ResourceID: id + "-resource", Section: "text"}
	must(s.PutResource(ctx, owner, core.Resource{ID: input.ResourceID, Name: "原工作包", OwnerProject: id, Sections: []core.Section{{ID: "text", Name: "正文", Fields: []string{"text"}}}}))
	cfg := operationsConfig{ProjectID: id, ReadSource: true, Input: input, Staff: "20", Transport: "crm-test"}
	must(api.saveOperations(ctx, cfg))
	t.Setenv("WORKBENCH_OPERATIONS_ORIGINAL_STAFF_ID", "20")
	t.Setenv("WORKBENCH_OPERATIONS_STAFF_NAME", "实际运营员工")
	t.Setenv("WORKBENCH_OPERATIONS_BATCH_STRATEGY", "hxc:onboarding")
	parent := core.Run{ID: id + "-daily", Title: "新人承接", Principal: p, Operation: "operations_package", ExplicitInputs: true,
		Destination: input.ResourceID, Shared: map[string]any{"day": operationsDay(time.Now()), "strategy": "hxc:onboarding"}}
	must(s.SaveRun(ctx, parent))
	draft := core.OperationsReviewRow{Key: "one", ID: "private-profile", Customer: "987654321", Nickname: "客户甲",
		Reason: "原策略 D2", Message: "旧稿", Editable: true, Slot: core.Digest(id)}
	review := core.OperationsReview{Run: "copybatch-" + strings.Repeat("1", 32), Digest: core.Digest("original"), Week: "2026-W46", Package: "weekpkg-source"}
	must(s.PublishOperationsReview(ctx, parent.ID, review, []core.OperationsReviewRow{draft}))
	items, err := s.Items(ctx, p, parent.ID, "")
	must(err)
	if len(items) != 0 {
		t.Fatal("generic run reader exposed connector-only review data")
	}
	get := func(url string) string {
		t.Helper()
		r := httptest.NewRequest("GET", url, nil)
		r.Header.Set("Authorization", "Bearer "+api.Token)
		r.Header.Set("X-Workbench-Kind", p.Kind)
		r.Header.Set("X-Workbench-Session", p.SessionID)
		r.Header.Set("X-Workbench-Project", p.ProjectID)
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("GET %s: %d %s", url, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	today := "/operations/source-today"
	if v := get(today); !strings.Contains(v, parent.ID) {
		t.Fatal(v)
	}
	if v := get(today + "?daily=" + parent.ID); !strings.Contains(v, "旧稿") || !strings.Contains(v, "客户甲") || strings.Contains(v, draft.Customer) {
		t.Fatal(v)
	}
	before, err := s.OperationsReview(ctx, parent.ID)
	must(err)
	oldVersion := core.OperationsReviewVersion(before, cfg.Transport, cfg.Staff, "实际运营员工")
	must(s.BeginOperationsEdit(ctx, parent.ID, "edit-id", review.Digest, draft.Key, draft.Revision))
	if err = s.ConfirmOperationsReview(ctx, parent, "stale", oldVersion, cfg.Staff, cfg.Transport, "实际运营员工"); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("confirmation raced edit: %v", err)
	}
	draft.Message, draft.Revision, review.Edit = "最后保存的正文", 1, "edit-id"
	review.Digest = core.Digest("edited")
	must(s.PublishOperationsReview(ctx, parent.ID, review, []core.OperationsReviewRow{draft}))
	after, err := s.OperationsReview(ctx, parent.ID)
	must(err)
	version := core.OperationsReviewVersion(after, cfg.Transport, cfg.Staff, "实际运营员工")
	confirmation := core.Run{ID: id + "-confirm", ParentRunID: parent.ID, Principal: p, Title: "审核确认", Operation: "operations_batch_confirm", ExplicitInputs: true}
	must(s.SaveRun(ctx, confirmation))
	must(s.ConfirmOperationsReview(ctx, parent, confirmation.ID, version, cfg.Staff, cfg.Transport, "实际运营员工"))
	must(s.ConfirmOperationsReview(ctx, parent, confirmation.ID, version, cfg.Staff, cfg.Transport, "实际运营员工"))
	_, manifest, err := s.OperationsBatchManifest(ctx, confirmation.ID)
	must(err)
	if len(manifest) != 1 || manifest[0].Message != draft.Message || manifest[0].Customer != draft.Customer {
		t.Fatal(manifest)
	}
	must(s.SaveOperationsBatchResult(ctx, confirmation.ID, core.OperationsBatchStatus{Plan: "43", Entries: map[string]core.OperationsBatchReceipt{
		"one": {State: "sent", At: "2026-11-11T10:05:00+08:00"},
	}}))
	must(s.SaveOperationsBatchResult(ctx, confirmation.ID, core.OperationsBatchStatus{Entries: map[string]core.OperationsBatchReceipt{"one": {State: "unknown"}}}))
	if v := get(today + "?daily=" + parent.ID); !strings.Contains(v, "已发送") || !strings.Contains(v, draft.Message) || strings.Contains(v, draft.Customer) {
		t.Fatal(v)
	}
	if v := get(today); strings.Contains(v, parent.ID) {
		t.Fatal(v)
	}
	if v := get(today + "?history=1"); !strings.Contains(v, parent.ID) {
		t.Fatal(v)
	}
	newDay := parent
	newDay.ID += "-next"
	must(s.SaveRun(ctx, newDay))
	review.Edit = ""
	must(s.PublishOperationsReview(ctx, newDay.ID, review, []core.OperationsReviewRow{draft}))
	newHead, err := s.OperationsReview(ctx, newDay.ID)
	must(err)
	if newHead.Count != 0 {
		b, _ := json.Marshal(newHead)
		t.Fatalf("same action entered a second run: %s", b)
	}
}
