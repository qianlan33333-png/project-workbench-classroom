package core

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestMySQLResourceLifecycle(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("WORKBENCH_TEST_DSN not supplied; real MySQL test not run")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("test requires isolated acceptance database")
	}
	ctx := context.Background()
	s, e := Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	prefix := "t" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	owner := Principal{Kind: "owner", SessionID: prefix + "owner"}
	for _, id := range []string{"A", "B", "C"} {
		if e = s.PutProject(ctx, owner, Project{ID: prefix + id, Name: id}); e != nil {
			t.Fatal(e)
		}
	}
	a := Principal{Kind: "project", ProjectID: prefix + "A", SessionID: prefix + "As"}
	b := Principal{Kind: "project", ProjectID: prefix + "B", SessionID: prefix + "Bs"}
	r := Resource{ID: prefix + "D", Name: "shared", Sections: []Section{{ID: "public", Fields: []string{"name"}}, {ID: "private", Fields: []string{"secret"}}}}
	if e = s.PutResource(ctx, owner, r); e != nil {
		t.Fatal(e)
	}
	snap := Snapshot{ResourceID: r.ID, Version: "v1"}
	if e = s.BeginSnapshot(ctx, owner, snap); e != nil {
		t.Fatal(e)
	}
	records := []Record{{ID: "1", Fields: map[string]any{"name": "one", "secret": "hidden"}}, {ID: "2", Fields: map[string]any{"name": "two", "secret": "hidden"}}}
	if e = s.Stage(ctx, owner, r.ID, "v1", records); e != nil {
		t.Fatal(e)
	}
	if e = s.Stage(ctx, owner, r.ID, "v1", records); e != nil {
		t.Fatal("idempotent page failed", e)
	}
	if _, e = s.Publish(ctx, owner, r.ID, "v1", 3); e == nil {
		t.Fatal("invalid count published")
	}
	if _, e = s.Publish(ctx, owner, r.ID, "v1", 2); e != nil {
		t.Fatal(e)
	}
	ref := Ref{r.ID, "v1", "public"}
	if _, e = s.Read(ctx, a, ref, "", 1); !errors.Is(e, ErrDenied) {
		t.Fatal("ungranted read", e)
	}
	if e = s.SetGrant(ctx, owner, Grant{r.ID, a.ProjectID, []string{"public"}}); e != nil {
		t.Fatal(e)
	}
	page, e := s.Read(ctx, a, ref, "", 1)
	if e != nil {
		t.Fatal(e)
	}
	if page.Next != "1" || len(page.Records) != 1 || len(page.Records[0].Fields) != 1 {
		t.Fatalf("page leaked/incorrect: %+v", page)
	}
	if _, e = s.Read(ctx, b, ref, "", 1); !errors.Is(e, ErrDenied) {
		t.Fatal("B read D", e)
	}
	if _, e = s.Read(ctx, a, Ref{r.ID, "v1", "private"}, "", 1); !errors.Is(e, ErrDenied) {
		t.Fatal("private section leaked", e)
	}
	if e = s.SetGrant(ctx, owner, Grant{r.ID, a.ProjectID, nil}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Read(ctx, a, ref, "", 1); !errors.Is(e, ErrDenied) {
		t.Fatal("revocation failed", e)
	}
	for _, v := range []string{"v2", "v3"} {
		if e = s.BeginSnapshot(ctx, owner, Snapshot{ResourceID: r.ID, Version: v}); e != nil {
			t.Fatal(e)
		}
		if e = s.Stage(ctx, owner, r.ID, v, records); e != nil {
			t.Fatal(e)
		}
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, v := range []string{"v2", "v3"} {
		wg.Add(1)
		go func(v string) { defer wg.Done(); _, err := s.Publish(ctx, owner, r.ID, v, 2); results <- err }(v)
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("publish race: ok %d conflicts %d", ok, conflicts)
	}
	if e = s.BindSession(ctx, a); e != nil {
		t.Fatal(e)
	}
	changed := a
	changed.ProjectID = b.ProjectID
	if e = s.BindSession(ctx, changed); !errors.Is(e, ErrDenied) {
		t.Fatal("Session rebound", e)
	}
	var version string
	if e = s.DB.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); e != nil {
		t.Fatal(e)
	}
	t.Log("validated real MySQL", version)
}
