package core

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

func TestLibraryPermissionsMySQL(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_ACL_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated ACL MySQL not configured")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.DBName != "workbench_native_acceptance_acl" {
		t.Fatal("use a dedicated acceptance database")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	denied := func(e error) {
		t.Helper()
		if !errors.Is(e, ErrDenied) {
			t.Fatalf("want denied, got %v", e)
		}
	}
	tag := "acl-" + uuid.NewString()[:10]
	owner := Principal{Kind: "owner", SessionID: tag + "-owner"}
	personal := Principal{Kind: "personal", SessionID: tag + "-super"}
	project := Principal{Kind: "project", ProjectID: tag + "-ordinary", SessionID: tag + "-ordinary-session"}
	special := Principal{Kind: "project", ProjectID: tag + "-special", SessionID: tag + "-special-session"}
	must(s.PutProject(ctx, owner, Project{ID: project.ProjectID, Name: "普通项目"}))
	must(s.PutProject(ctx, owner, Project{ID: special.ProjectID, Name: "自动化运营"}))
	must(s.BindSession(ctx, personal))
	must(s.BindSession(ctx, project))
	must(s.BindSession(ctx, special))
	_, err = s.DB.ExecContext(ctx, "INSERT INTO wb_settings(id,body) VALUES('personal',?) ON DUPLICATE KEY UPDATE body=VALUES(body)", jsonText(personal))
	must(err)
	t.Cleanup(func() {
		_, _ = s.DB.ExecContext(ctx, "DELETE FROM wb_settings WHERE id='personal' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.sessionId'))=?", personal.SessionID)
	})
	r, err := s.CreateLibrary(ctx, personal, "create-1", LibraryInput{Name: "黄小璨问答测试资料", Kind: "corpus", Description: "查询直播活动"})
	must(err)
	original := r
	r, err = s.CreateLibrary(ctx, personal, "create-1", LibraryInput{Name: "黄小璨问答测试资料", Kind: "corpus", Description: "查询直播活动"})
	must(err)
	if r.ID != original.ID {
		t.Fatal("duplicated library")
	}
	db, err := s.CreateLibrary(ctx, personal, "default-columns", LibraryInput{Name: "新数据库", Kind: "database"})
	must(err)
	schema, err := s.Resource(ctx, db.ID)
	must(err)
	if strings.Join(schema.Sections[0].Fields, ",") != "名称,内容" {
		t.Fatal("default database fields")
	}
	_, err = s.CreateLibrary(ctx, personal, "explicit-columns", LibraryInput{Name: "指定字段", Kind: "database", Columns: []string{"标题", "结果"}})
	must(err)
	_, err = s.CreateLibrary(ctx, project, "create-denied", LibraryInput{Name: "禁止创建", Kind: "database"})
	denied(err)
	ref := Ref{r.ID, r.Version, "main"}
	dir, err := s.ResourceDirectory(ctx, project)
	must(err)
	found := false
	for _, d := range dir {
		if d.ID == r.ID {
			found = true
			if d.Access != "none" || d.Description != "查询直播活动" || d.Version != "" || len(d.Sections) != 0 || d.Editable {
				t.Fatalf("directory leaked content metadata: %+v", d)
			}
		}
	}
	if !found {
		t.Fatal("directory hides an ungranted user library")
	}
	_, err = s.Read(ctx, project, ref, "", 10)
	denied(err)
	_, err = s.SetResourceGrant(ctx, project, r.ID, ResourceAccess{ProjectID: project.ProjectID, Mode: "manage", Sections: []string{"main"}})
	denied(err)
	perms, err := s.ResourcePermissions(ctx, owner, r.ID)
	must(err)
	perms.Grants = []ResourceAccess{{project.ProjectID, []string{"main"}, "read"}, {special.ProjectID, []string{"main"}, "manage"}}
	next, err := s.PutResourcePermissions(ctx, owner, perms)
	must(err)
	if len(next.Grants) != 2 {
		t.Fatalf("readback: %+v", next)
	}
	_, err = s.PutResourcePermissions(ctx, owner, perms)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale dialog was able to overwrite: %v", err)
	}
	draft := LibraryChange{ResourceID: r.ID, OperationKey: "one", Action: "create", Fields: map[string]any{"title": "记录", "markdown": "# 正文"}}
	_, err = s.ChangeLibraryItem(ctx, project, draft)
	denied(err)
	saved, err := s.ChangeLibraryItem(ctx, special, draft)
	must(err)
	replay, err := s.ChangeLibraryItem(ctx, special, draft)
	must(err)
	if replay != saved {
		t.Fatalf("duplicate content on retry: %+v", replay)
	}
	ref.Version = saved.Version
	page, err := s.Read(ctx, project, ref, "", 10)
	must(err)
	if len(page.Records) != 1 || page.Records[0].Fields["markdown"] != "# 正文" {
		t.Fatalf("unexpected readback %+v", page)
	}
	update := LibraryChange{ResourceID: r.ID, OperationKey: "two", Action: "update", ItemID: saved.ItemID, Fields: map[string]any{"markdown": "# 修改"}}
	changed, err := s.ChangeLibraryItem(ctx, special, update)
	must(err)
	_, err = s.SetResourceGrant(ctx, owner, r.ID, ResourceAccess{ProjectID: special.ProjectID, Mode: "read", Sections: []string{"main"}})
	must(err)
	update.OperationKey = "three"
	_, err = s.ChangeLibraryItem(ctx, special, update)
	denied(err)
	ref.Version = changed.Version
	_, err = s.Read(ctx, special, ref, "", 10)
	must(err)
	_, err = s.SetResourceGrant(ctx, owner, r.ID, ResourceAccess{ProjectID: project.ProjectID})
	must(err)
	_, err = s.Read(ctx, project, ref, "", 10)
	denied(err)
	denied(s.CheckSession(ctx, project))
	drop, err := s.ChangeLibraryItem(ctx, personal, LibraryChange{ResourceID: r.ID, OperationKey: "four", Action: "delete", ItemID: saved.ItemID})
	must(err)
	ref.Version = drop.Version
	page, err = s.Read(ctx, personal, ref, "", 10)
	must(err)
	if page.Count != 0 {
		t.Fatalf("record remained in current library: %+v", page)
	}
	if _, err = s.Resource(ctx, r.ID); err != nil {
		t.Fatalf("library itself was removed: %v", err)
	}

	// Simulate a legacy registered resource and migrate implicit ownership. An
	// explicit downgrade then stops staging and publication of an old job.
	legacy := Resource{ID: tag + "-legacy", Name: "旧运营结果", OwnerProject: special.ProjectID, Sections: []Section{{ID: "main", Fields: []string{"text"}}}}
	must(s.RegisterResource(ctx, owner, legacy))
	denied(s.CheckResourceWrite(ctx, special, legacy.ID))
	_, err = s.DB.ExecContext(ctx, "DELETE FROM wb_settings WHERE id='resource-access-migrated'")
	must(err)
	must(s.migrateResourceGrants(ctx))
	must(s.BeginSnapshot(ctx, special, Snapshot{ResourceID: legacy.ID, Version: "queued"}))
	must(s.Stage(ctx, special, legacy.ID, "queued", []Record{{ID: "one", Fields: map[string]any{"text": "已暂存"}}}))
	_, err = s.SetResourceGrant(ctx, owner, legacy.ID, ResourceAccess{ProjectID: special.ProjectID, Mode: "read", Sections: []string{"main"}})
	must(err)
	denied(s.Stage(ctx, special, legacy.ID, "queued", []Record{{ID: "two", Fields: map[string]any{"text": "禁止"}}}))
	_, err = s.Publish(ctx, special, legacy.ID, "queued", 1)
	denied(err)

	must(s.migrateResourceGrants(ctx))
	denied(s.CheckResourceWrite(ctx, special, legacy.ID))

	policy := Resource{ID: tag + "-policies", Name: "黄小璨口径", OwnerProject: special.ProjectID, Relation: BusinessPolicyRelation, Sections: []Section{{ID: "main", Fields: []string{"name", "answer"}}}}
	must(s.PutResource(ctx, owner, policy))
	expired := time.Now().Add(-time.Hour).UTC()
	for _, in := range []BusinessPolicy{{Topic: "replay", Name: "现行场次", Answer: "现行证据", Enabled: true}, {Topic: "replay", Name: "到期场次", Answer: "过期内容", Enabled: true, ValidUntil: &expired}} {
		_, err = s.PutBusinessPolicy(ctx, owner, policy.ID, in)
		must(err)
	}
	q := PolicyQuery{ResourceID: policy.ID}
	list, err := s.LibraryPolicies(ctx, special, q)
	must(err)
	if len(list.Items) != 1 || list.Items[0].Name != "现行场次" {
		t.Fatalf("expired row leaked to read-only: %+v", list)
	}
	inactive := q
	inactive.IncludeInactive = true
	_, err = s.LibraryPolicies(ctx, special, inactive)
	denied(err)
	list, err = s.LibraryPolicies(ctx, personal, q)
	must(err)
	if len(list.Items) != 1 {
		t.Fatalf("management current lookup returned inactive policies: %+v", list)
	}
	list, err = s.LibraryPolicies(ctx, personal, inactive)
	must(err)
	if len(list.Items) != 2 {
		t.Fatalf("manager cannot see rows to maintain: %+v", list)
	}
	var current BusinessPolicy
	for _, row := range list.Items {
		if row.Name == "现行场次" {
			current = row
		}
	}
	result, err := s.ChangeLibraryItem(ctx, personal, LibraryChange{ResourceID: policy.ID, OperationKey: "disable", Action: "delete", ItemID: current.ID})
	must(err)
	if result.Action != "disable" {
		t.Fatal("expected disable")
	}
	list, err = s.LibraryPolicies(ctx, special, q)
	must(err)
	if len(list.Items) != 0 {
		t.Fatalf("disabled row remained effective: %+v", list)
	}
}
