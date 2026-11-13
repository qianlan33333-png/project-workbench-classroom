package core

import (
	"slices"
	"testing"
)

func TestMySQLRunCatalogUsesBoundProjectSession(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	p := Principal{Kind: "project", ProjectID: id, SessionID: id + "primary"}
	other := Principal{Kind: "project", ProjectID: id, SessionID: id + "other"}
	if err := s.PutProject(ctx, owner, Project{ID: id, Name: "Overview fixture"}); err != nil {
		t.Fatal(err)
	}
	root := Run{ID: id + "root", Title: "Primary", Principal: p}
	for _, r := range []Run{
		root,
		{ID: id + "child", Title: "Child", ParentRunID: root.ID, Principal: p},
		{ID: id + "other-run", Title: "Other", Principal: other},
	} {
		if err := s.SaveRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.RunCatalog(ctx, p, "")
	if err != nil || len(items) != 1 || items[0].ID != root.ID || items[0].CreatedAt.IsZero() {
		t.Fatalf("unexpected project catalog: %v %v", items, err)
	}
	items, err = s.RunCatalog(ctx, p, root.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("excluded run appears in catalog: %v %v", items, err)
	}
	if _, _, err := s.RunOverviewFacts(ctx, other, root); err != ErrDenied {
		t.Fatalf("unbound reader saw task facts: %v", err)
	}
	if err := s.RecordItem(ctx, root.ID, ItemResult{ID: "one", State: "succeeded", InputHash: "fixture"}); err != nil {
		t.Fatal(err)
	}
	stats, _, err := s.RunOverviewFacts(ctx, p, root)
	if err != nil || stats.Succeeded != 1 {
		t.Fatalf("task result unavailable: %+v %v", stats, err)
	}
}

func TestMySQLUnreadableDecisionDoesNotHideOtherDecisions(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	p := Principal{Kind: "personal", SessionID: id + "personal"}
	r := Run{ID: id + "run", Title: "Visible", Principal: p}
	if err := s.SaveRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	d, err := s.Ask(ctx, r, 1, "Visible question")
	if err != nil {
		t.Fatal(err)
	}
	missing := Decision{ID: id + "missing", RunID: id + "missing-run", Digest: Digest("unavailable"), State: "waiting", Question: "Hidden question", Revision: 1}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO wb_decisions(id,run_id,revision,content_digest,state,body) VALUES(?,?,?,?,?,?)`, missing.ID, missing.RunID, missing.Revision, missing.Digest, missing.State, jsonText(missing)); err != nil {
		t.Fatal(err)
	}
	defer s.DB.ExecContext(ctx, `DELETE FROM wb_decisions WHERE id IN (?,?)`, d.ID, missing.ID)
	got, err := s.Decisions(ctx, owner)
	has := func(id string) bool { return slices.ContainsFunc(got, func(d Decision) bool { return d.ID == id }) }
	if err != nil || has(missing.ID) || !has(d.ID) {
		t.Fatalf("decision list lost valid data or exposed missing data: %v", err)
	}
}
