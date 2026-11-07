package core

import (
	"errors"
	"testing"
)

func TestMySQLDecisionsAndMemoryKeepSourceBoundary(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	project := Principal{Kind: "project", ProjectID: id, SessionID: id + "p"}
	reader := Principal{Kind: "personal", SessionID: id + "today"}
	b := Principal{Kind: "project", ProjectID: id + "b", SessionID: id + "bs"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "personal lineage fixture"}))
	resource := Resource{ID: id + "r", Name: "private context", OwnerProject: id, Sections: []Section{{ID: "main", Fields: []string{"text"}}}}
	must(s.PutResource(ctx, owner, resource))
	must(s.BeginSnapshot(ctx, project, Snapshot{ResourceID: resource.ID, Version: "v1"}))
	must(s.Stage(ctx, project, resource.ID, "v1", []Record{{ID: "1", Fields: map[string]any{"text": "private A fact"}}}))
	_, e := s.Publish(ctx, project, resource.ID, "v1", 1)
	must(e)
	refs := []Ref{{ResourceID: resource.ID, Version: "v1", Section: "main"}}
	r := Run{ID: id + "run", Principal: project, Operation: "followup", Title: "source-bearing decision", Sources: refs}
	must(s.SaveRun(ctx, r))
	_, e = s.Ask(ctx, r, 1, "private A fact; choose an action")
	must(e)
	// No resource_read or run_list before decision_list: the decision itself is a read.
	_, e = s.Decisions(ctx, reader)
	must(e)
	lineage, e := s.SessionSources(ctx, reader)
	must(e)
	found := false
	for _, ref := range lineage {
		if ref == refs[0] {
			found = true
		}
	}
	if !found {
		t.Fatal("decision dropped source")
	}
	if e = AuthorizeAll(ctx, s, b, lineage); !errors.Is(e, ErrDenied) {
		t.Fatal("decision could be forwarded to B", e)
	}
	must(s.PutMemory(ctx, reader, Memory{ID: id + "memory", Text: "derived private fact", Source: "agent inference", Confirmed: true}))
	fresh := Principal{Kind: "personal", SessionID: id + "fresh"}
	memories, e := s.Memories(ctx, fresh)
	must(e)
	for _, m := range memories {
		if m.ID == id+"memory" {
			if m.Confirmed {
				t.Fatal("agent self-confirmed memory")
			}
			if len(m.Sources) == 0 {
				t.Fatal("memory lost sources")
			}
			m.Text = "edited"
			m.Sources = nil
			must(s.PutMemory(ctx, owner, m))
		}
	}
	lineage, e = s.SessionSources(ctx, fresh)
	must(e)
	if e = AuthorizeAll(ctx, s, b, lineage); !errors.Is(e, ErrDenied) {
		t.Fatal("memory could launder private data", e)
	}
	m, e := scanJSON[Memory](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_memory WHERE id=?`, id+"memory"))
	must(e)
	if len(m.Sources) == 0 {
		t.Fatal("editing silently dropped source")
	}
	m.Revision = 2
	m.Forgotten = true
	must(s.PutMemory(ctx, owner, m))
}
