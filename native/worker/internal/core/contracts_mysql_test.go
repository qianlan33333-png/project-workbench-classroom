package core

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"os"
	"strings"
	"testing"
)

func acceptanceStore(t *testing.T) (*Store, context.Context, string) {
	t.Helper()
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("real MySQL settings not supplied")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	s, e := Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	return s, ctx, "t" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}
func TestMySQLSnapshotSchemaAndResultLineage(t *testing.T) {
	s, ctx, prefix := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: prefix + "owner"}
	p := Principal{Kind: "project", ProjectID: prefix, SessionID: prefix + "s"}
	pa := Principal{Kind: "personal", SessionID: prefix + "pa"}
	if e := s.PutProject(ctx, owner, Project{ID: prefix, Name: "contract fixture"}); e != nil {
		t.Fatal(e)
	}
	r := Resource{ID: prefix + "r", Name: "contract", OwnerProject: prefix, Sections: []Section{{ID: "main", Fields: []string{"name"}}}, RecordSchema: map[string]any{"type": "object", "required": []any{"name"}}}
	if e := s.PutResource(ctx, owner, r); e != nil {
		t.Fatal(e)
	}
	if e := s.BeginSnapshot(ctx, p, Snapshot{ResourceID: r.ID, Version: "v1"}); e != nil {
		t.Fatal(e)
	}
	if e := s.Stage(ctx, p, r.ID, "v1", []Record{{ID: "1", Fields: map[string]any{"text": "wrong contract"}}}); !errors.Is(e, ErrInvalid) {
		t.Fatal("invalid record staged", e)
	}
	if e := s.Stage(ctx, p, r.ID, "v1", []Record{{ID: "1", Fields: map[string]any{"name": "private evidence"}}}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Publish(ctx, p, r.ID, "v1", 1); e != nil {
		t.Fatal(e)
	}
	ref := Ref{ResourceID: r.ID, Version: "v1", Section: "main"}
	run := Run{ID: prefix + "run", Principal: p, Operation: "generate", Title: "source-bearing result", Sources: []Ref{ref}}
	if e := s.SaveRun(ctx, run); e != nil {
		t.Fatal(e)
	}
	if e := s.RecordItem(ctx, run.ID, ItemResult{ID: "1", State: "succeeded", InputHash: "test", Text: "private derivative"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Items(ctx, pa, run.ID, ""); e != nil {
		t.Fatal(e)
	}
	refs, e := s.SessionSources(ctx, pa)
	if e != nil || len(refs) != 1 || refs[0] != ref {
		t.Fatalf("result lost source: %+v %v", refs, e)
	}
	outsider := Principal{Kind: "project", ProjectID: prefix + "other", SessionID: prefix + "os"}
	if e := AuthorizeAll(ctx, s, outsider, refs); !errors.Is(e, ErrDenied) {
		t.Fatal("derivative source bypass", e)
	}
	// Method/schema changes cannot rewrite the contract of an in-progress version.
	resource, e := s.Resource(ctx, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	resource.RecordSchema = map[string]any{"type": "object", "required": []any{"different"}}
	if e = s.PutResource(ctx, owner, resource); e != nil {
		t.Fatal(e)
	}
	snapshot, e := s.Snapshot(ctx, r.ID, "v1")
	if e != nil {
		t.Fatal(e)
	}
	if Digest(snapshot.RecordSchema) != Digest(r.RecordSchema) {
		t.Fatal("published schema changed")
	}
}
func TestMySQLSourceRejectsMissingWatermark(t *testing.T) {
	s, ctx, prefix := acceptanceStore(t)
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	t.Setenv("WORKBENCH_SOURCE_CONTRACT_TEST", dsn)
	table := prefix + "src"
	if _, e := s.DB.ExecContext(ctx, "CREATE TABLE `"+table+"` (id VARCHAR(40), value TEXT, watermark VARCHAR(40) NULL)"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.ExecContext(ctx, "DROP TABLE `"+table+"`") })
	if _, e := s.DB.ExecContext(ctx, "INSERT INTO `"+table+"` VALUES('1','one','v1'),('2','two',NULL)"); e != nil {
		t.Fatal(e)
	}
	owner := Principal{Kind: "owner", SessionID: prefix + "o"}
	p := Principal{Kind: "project", ProjectID: prefix, SessionID: prefix + "s"}
	if e := s.PutProject(ctx, owner, Project{ID: prefix, Name: "source fixture"}); e != nil {
		t.Fatal(e)
	}
	c := Connection{ID: prefix + "c", Name: "fixture", Kind: "mysql", SecretEnv: "WORKBENCH_SOURCE_CONTRACT_TEST"}
	if e := s.PutConnection(ctx, owner, c); e != nil {
		t.Fatal(e)
	}
	r := Resource{ID: prefix + "r", Name: "source", OwnerProject: prefix, ConnectionID: c.ID, Relation: table, KeyField: "id", WatermarkField: "watermark", Sections: []Section{{ID: "main", Fields: []string{"value", "watermark"}}}}
	if e := s.PutResource(ctx, owner, r); e != nil {
		t.Fatal(e)
	}
	run := Run{ID: prefix + "run", Principal: p, Operation: "sync", Title: "read source", Destination: r.ID}
	if _, e := s.SyncSource(ctx, run, func() {}); !errors.Is(e, ErrInvalid) {
		t.Fatal("null watermark published", e)
	}
	if _, e := s.DB.ExecContext(ctx, "UPDATE `"+table+"` SET watermark='v1'"); e != nil {
		t.Fatal(e)
	}
	snap, e := s.SyncSource(ctx, run, func() {})
	if e != nil {
		t.Fatal(e)
	}
	if snap.Count != 2 || snap.SourceWatermark != "v1" || snap.State != "published" {
		t.Fatalf("invalid snapshot %+v", snap)
	}
}
