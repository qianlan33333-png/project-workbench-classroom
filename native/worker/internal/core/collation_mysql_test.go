package core

import (
	"errors"
	"strings"
	"testing"
)

func TestMySQLIdentifierCaseDoesNotExpandGrant(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "Owner"}
	p := Principal{Kind: "project", ProjectID: id + "Project", SessionID: id + "Session"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: p.ProjectID, Name: "case boundary"}))
	r := Resource{ID: id + "Resource", Name: "case-sensitive sections", Sections: []Section{{ID: "public", Fields: []string{"text"}}, {ID: "PUBLIC", Fields: []string{"secret"}}}}
	must(s.PutResource(ctx, owner, r))
	must(s.BeginSnapshot(ctx, owner, Snapshot{ResourceID: r.ID, Version: "v1"}))
	must(s.Stage(ctx, owner, r.ID, "v1", []Record{{ID: "1", Fields: map[string]any{"text": "public", "secret": "must remain private"}}}))
	_, e := s.Publish(ctx, owner, r.ID, "v1", 1)
	must(e)
	must(s.SetGrant(ctx, owner, Grant{ResourceID: r.ID, ProjectID: p.ProjectID, Sections: []string{"public"}}))
	ref := Ref{ResourceID: r.ID, Version: "v1", Section: "public"}
	page, e := s.Read(ctx, p, ref, "", 1)
	must(e)
	if page.Records[0].Fields["text"] != "public" {
		t.Fatal(page)
	}
	ref.Section = "PUBLIC"
	if _, e = s.Read(ctx, p, ref, "", 1); !errors.Is(e, ErrDenied) {
		t.Fatal("case-insensitive section grant", e)
	}
	ref.Section = "public"
	p.ProjectID = strings.ToLower(p.ProjectID)
	if _, e = s.Read(ctx, p, ref, "", 1); !errors.Is(e, ErrDenied) {
		t.Fatal("case-insensitive project grant", e)
	}
	upper := Principal{Kind: "personal", SessionID: id + "Binding"}
	lower := Principal{Kind: "project", ProjectID: p.ProjectID, SessionID: id + "binding"}
	must(s.BindSession(ctx, upper))
	must(s.BindSession(ctx, lower))
}

func TestMySQLRecordIDPreservesTrailingSpace(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	p := Principal{Kind: "owner", SessionID: id + "Owner"}
	r := Resource{ID: id + "Resource", Name: "exact record identity", Sections: []Section{{ID: "body", Fields: []string{"text"}}}}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutResource(ctx, p, r))
	must(s.BeginSnapshot(ctx, p, Snapshot{ResourceID: r.ID, Version: "v1"}))
	must(s.Stage(ctx, p, r.ID, "v1", []Record{{ID: "customer", Fields: map[string]any{"text": "first"}}, {ID: "customer ", Fields: map[string]any{"text": "second"}}}))
	_, e := s.Publish(ctx, p, r.ID, "v1", 2)
	must(e)
	ref := Ref{ResourceID: r.ID, Version: "v1", Section: "body"}
	for key, value := range map[string]string{"customer": "first", "customer ": "second"} {
		rows, e := s.ReadIDs(ctx, p, ref, []string{key})
		must(e)
		if len(rows) != 1 || rows[0].ID != key || rows[0].Fields["text"] != value {
			t.Fatal(key, rows)
		}
	}
	rows, e := s.ReadIDs(ctx, p, ref, []string{"customer  "})
	must(e)
	if len(rows) != 0 {
		t.Fatal("nonexistent identity matched", rows)
	}
	page, e := s.Read(ctx, p, ref, "", 1)
	must(e)
	if len(page.Records) != 1 || page.Records[0].ID != "customer" {
		t.Fatal(page)
	}
	page, e = s.Read(ctx, p, ref, "customer", 1)
	must(e)
	if len(page.Records) != 1 || page.Records[0].ID != "customer " {
		t.Fatal(page)
	}
}
