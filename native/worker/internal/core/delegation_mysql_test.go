package core

import (
	"errors"
	"strings"
	"testing"
)

func TestMySQLDelegationReportReplayAndSources(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	p := Principal{Kind: "project", ProjectID: id, SessionID: id + "target"}
	today := Principal{Kind: "personal", SessionID: id + "today"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "delegation report fixture"}))
	must(s.BindSession(ctx, today))
	resource := Resource{ID: id + "resource", Name: "shared source", Sections: []Section{{ID: "main", Fields: []string{"text"}}}}
	must(s.PutResource(ctx, owner, resource))
	must(s.BeginSnapshot(ctx, owner, Snapshot{ResourceID: resource.ID, Version: "v1"}))
	must(s.Stage(ctx, owner, resource.ID, "v1", []Record{{ID: "1", Fields: map[string]any{"text": "private evidence"}}}))
	_, e := s.Publish(ctx, owner, resource.ID, "v1", 1)
	must(e)
	must(s.SetGrant(ctx, owner, Grant{ResourceID: resource.ID, ProjectID: id, Sections: []string{"main"}}))
	r := Run{ID: id + "run", Principal: p, Operation: "dispatch", Title: "delegate", NotifySession: today.SessionID}
	must(s.SaveRun(ctx, r))
	refs := []Ref{{ResourceID: resource.ID, Version: "v1", Section: "main"}}
	must(s.RememberRead(ctx, p, refs))
	report := DelegationReport{RunID: r.ID, Text: "private result", Reason: "completed"}
	wrong := p
	wrong.SessionID += "other"
	if e = s.ReportDelegation(ctx, wrong, report); !errors.Is(e, ErrDenied) {
		t.Fatal("wrong Session report accepted", e)
	}
	must(s.ReportDelegation(ctx, p, report))
	// A crash after database commit and replay of the native Session is harmless.
	report.Text = "replacement must not overwrite first report"
	must(s.ReportDelegation(ctx, p, report))
	var count int
	var body string
	must(s.DB.QueryRowContext(ctx, `SELECT COUNT(*),MAX(CAST(body AS CHAR)) FROM wb_events WHERE id=?`, r.ID+"-report").Scan(&count, &body))
	if count != 1 || !strings.Contains(body, "private result") || strings.Contains(body, "replacement") {
		t.Fatal("report replay changed content", body)
	}
	events, e := s.PendingEvents(ctx, owner, 0)
	must(e)
	found := false
	for _, event := range events {
		if event.ID == r.ID+"-report" {
			found = true
		}
	}
	if !found {
		t.Fatal("report not delivered to Today")
	}
	lineage, e := s.SessionSources(ctx, today)
	must(e)
	if Digest(lineage) != Digest(refs) {
		t.Fatal("report lost provenance", lineage)
	}
	must(s.SetGrant(ctx, owner, Grant{ResourceID: resource.ID, ProjectID: id, Sections: []string{}}))
	if e = s.ReportDelegation(ctx, p, report); !errors.Is(e, ErrDenied) {
		t.Fatal("revoked source report allowed", e)
	}
	if _, e = s.Run(ctx, p, r.ID); !errors.Is(e, ErrDenied) {
		t.Fatal("report sources not pinned", e)
	}
	pending, e := s.PendingDelegations(ctx, owner, "")
	must(e)
	for _, r2 := range pending {
		if r2.ID == r.ID {
			t.Fatal("completed report still pending")
		}
	}
}
