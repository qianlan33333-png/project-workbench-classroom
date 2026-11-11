package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func TestMySQLLocalOperationsProfileDirectory(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("real MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	store, err := core.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Migrate(ctx))
	id := uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	project := operationsPrincipal(id)
	other := operationsPrincipal(id + "-other")
	must(store.PutProject(ctx, owner, core.Project{ID: project.ProjectID, Name: "directory fixture"}))
	must(store.PutProject(ctx, owner, core.Project{ID: other.ProjectID, Name: "other fixture"}))
	resource := core.Resource{ID: id + "-directory", OwnerProject: id, Name: "用户档案",
		Sections: []core.Section{{ID: "main", Fields: []string{"position", "search", "nickname", "detailNickname", "groups", "missingSources"}}}}
	must(store.PutResource(ctx, owner, resource))
	api := &Server{Store: store, Token: strings.Repeat("x", 40)}
	must(api.saveOperations(ctx, operationsConfig{ProjectID: id, ReadSource: true,
		Input: core.Ref{ResourceID: "unavailable-legacy-source", Section: "main"},
		Cycle: &engine.OperationsCycleInput{ProfileDirectory: resource.ID}}))
	records := []core.Record{}
	for i := 0; i < 23; i++ {
		records = append(records, core.Record{ID: fmt.Sprintf("ref-%02d", 22-i), Fields: map[string]any{
			"position": i, "search": []string{"黄小璨", "avi%"}, "nickname": "昵称",
			"detailNickname": "原档案称呼", "groups": []any{map[string]any{"title": "基础信息",
				"fields": []any{map[string]any{"label": "当前需求", "value": "核对", "state": "observed_candidate"}}}},
			"missingSources": []string{},
		}})
	}
	publish := func(version string) {
		t.Helper()
		must(store.BeginSnapshot(ctx, owner, core.Snapshot{ResourceID: resource.ID, Version: version}))
		must(store.Stage(ctx, owner, resource.ID, version, records))
		_, err := store.Publish(ctx, owner, resource.ID, version, len(records))
		must(err)
	}
	publish("v1")
	handler := api.Handler()
	call := func(p core.Principal, query url.Values) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
		t.Helper()
		request := httptest.NewRequest("GET", "/operations/profiles?"+query.Encode(), nil)
		request.Header.Set("Authorization", "Bearer "+api.Token)
		request.Header.Set("X-Workbench-Kind", p.Kind)
		request.Header.Set("X-Workbench-Project", p.ProjectID)
		request.Header.Set("X-Workbench-Session", p.SessionID)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, request)
		result := map[string]json.RawMessage{}
		if out.Code == 200 {
			must(json.Unmarshal(out.Body.Bytes(), &result))
		}
		return out, result
	}
	first, page := call(project, url.Values{"q": {"AVI%"}})
	if first.Code != 200 || string(page["total"]) != "23" || strings.Contains(first.Body.String(), "search") {
		t.Fatal(first.Code, first.Body.String())
	}
	var items []operationsProfileSummary
	must(json.Unmarshal(page["records"], &items))
	if len(items) != 20 || items[0].ID != "ref-22" || items[19].ID != "ref-03" {
		t.Fatal(items)
	}
	var next string
	must(json.Unmarshal(page["next"], &next))
	last, page := call(project, url.Values{"q": {"avi%"}, "after": {next}, "snapshot": {"v1"}})
	if last.Code != 200 || string(page["next"]) != "\"\"" {
		t.Fatal(last.Code, last.Body.String())
	}
	must(json.Unmarshal(page["records"], &items))
	if len(items) != 3 || items[0].ID != "ref-02" {
		t.Fatal(items)
	}
	detail, fields := call(project, url.Values{"id": {"ref-22"}, "snapshot": {"v1"}})
	if detail.Code != 200 || string(fields["nickname"]) != "\"原档案称呼\"" || !strings.Contains(string(fields["groups"]), "observed_candidate") {
		t.Fatal(detail.Code, detail.Body.String())
	}
	denied, _ := call(other, url.Values{"id": {"ref-22"}})
	if denied.Code != 403 {
		t.Fatal(denied.Code)
	}
	publish("v2")
	_, fields = call(project, url.Values{"q": {"avi%"}, "after": {next}})
	if string(fields["state"]) != "\"source_changed\"" {
		t.Fatal(fields)
	}
}
