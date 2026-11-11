package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"workbench-native/internal/core"
)

func TestMySQLVaultCreateSaveAndExistingAccess(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL not configured")
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
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	id := "vault-" + uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	project := core.Principal{Kind: "project", ProjectID: id + "-project", SessionID: id + "-session"}
	token := strings.Repeat("v", 40)
	root := t.TempDir()
	handler := (&Server{Store: store, Token: token, VaultRoot: root}).Handler()
	call := func(p core.Principal, method, path string, payload any, want int, target any) {
		t.Helper()
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Workbench-Kind", p.Kind)
		r.Header.Set("X-Workbench-Project", p.ProjectID)
		r.Header.Set("X-Workbench-Session", p.SessionID)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, r)
		if result.Code != want {
			t.Fatalf("%s %s returned %d, want %d: %s", method, path, result.Code, want, result.Body.String())
		}
		if target != nil {
			if err = json.Unmarshal(result.Body.Bytes(), target); err != nil {
				t.Fatal(err)
			}
		}
	}
	create := map[string]string{"id": id, "name": "隔离语料"}
	call(project, "POST", "/vault-create", create, 403, nil)
	var empty, again core.Resource
	call(owner, "POST", "/vault-create", create, http.StatusOK, &empty)
	call(owner, "POST", "/vault-create", create, http.StatusOK, &again)
	if empty.CurrentVersion == "" || empty.CurrentVersion != again.CurrentVersion {
		t.Fatal("create retry changed version")
	}
	doc := "主题/说明.md"
	text := "# 中文说明\n\n保留 [[链接]] 原文\n"
	input := map[string]any{"resourceId": id, "version": empty.CurrentVersion,
		"changes": []core.VaultChange{{Path: doc, Text: &text}}}
	var published, retried core.Snapshot
	call(project, "POST", "/vault-save", input, 403, nil)
	call(owner, "POST", "/vault-save", input, 200, &published)
	call(owner, "POST", "/vault-save", input, 200, &retried)
	if published.State != "published" || published.Count != 1 || published.Version != retried.Version {
		t.Fatalf("publish or retry mismatch: %+v", published)
	}
	got, err := os.ReadFile(filepath.Join(root, id, published.Version, filepath.FromSlash(doc)))
	if err != nil || string(got) != text {
		t.Fatalf("source file mismatch: %q %v", got, err)
	}
	oldPath := "/resource-page?resourceId=" + id + "&section=text&version=" + empty.CurrentVersion
	newPath := "/resource-page?resourceId=" + id + "&section=text&version=" + published.Version
	var oldPage, page core.Page
	call(owner, "GET", oldPath, nil, 200, &oldPage)
	if oldPage.Count != 0 {
		t.Fatalf("old snapshot changed: %+v", oldPage)
	}
	call(project, "GET", newPath, nil, 403, nil)
	if err = store.PutProject(ctx, owner, core.Project{ID: project.ProjectID, Name: "existing project"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SetGrant(ctx, owner, core.Grant{ResourceID: id, ProjectID: project.ProjectID, Sections: []string{"text"}}); err != nil {
		t.Fatal(err)
	}
	call(project, "GET", newPath, nil, 200, &page)
	if len(page.Records) != 1 || page.Records[0].Fields["text"] != text {
		t.Fatalf("existing resource read failed: %+v", page)
	}
	other := "这是并发旧稿"
	input["changes"] = []core.VaultChange{{Path: doc, Text: &other}}
	call(owner, "POST", "/vault-save", input, 409, nil)
}
