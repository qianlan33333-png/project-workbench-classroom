package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestBoundedPopulation(t *testing.T) {
	for _, bad := range []*Selection{{RecordIDs: []string{}}, {Limit: -1}, {Limit: 2001}, {RecordIDs: []string{"a", "a"}}, {RecordIDs: []string{"a"}, Limit: 1}, {RecordIDs: []string{"a"}, After: "a"}} {
		if _, err := BoundedSelection(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid population %+v: %v", bad, err)
		}
	}
	v, err := BoundedSelection(nil)
	if err != nil || v.Limit != 2000 {
		t.Fatal(v, err)
	}
}

func TestMySQLFrozenBoundedPopulation(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	p := Principal{Kind: "project", ProjectID: id, SessionID: id + "s"}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "selection fixture"}))
	resource := Resource{ID: id + "r", Name: "population", OwnerProject: id, Sections: []Section{{ID: "main", Fields: []string{"name"}}}}
	must(s.PutResource(ctx, owner, resource))
	publish := func(version string, n int) {
		must(s.BeginSnapshot(ctx, p, Snapshot{ResourceID: resource.ID, Version: version}))
		for start := 0; start < n; start += 200 {
			rows := []Record{}
			for i := start; i < min(start+200, n); i++ {
				rows = append(rows, Record{ID: fmt.Sprintf("%04d", i), Fields: map[string]any{"name": version}})
			}
			must(s.Stage(ctx, p, resource.ID, version, rows))
		}
		_, err := s.Publish(ctx, p, resource.ID, version, n)
		must(err)
	}
	publish("v1", 450)
	ref := Ref{ResourceID: resource.ID, Version: "v1", Section: "main"}
	cases := []struct {
		selection         Selection
		count             int
		first, last, next string
	}{
		{Selection{Limit: 40}, 40, "0000", "0039", "0039"},
		{Selection{Limit: 225}, 225, "0000", "0224", "0224"},
		{Selection{Limit: 200, After: "0199"}, 200, "0200", "0399", "0399"},
		{Selection{Limit: 200, After: "0399"}, 50, "0400", "0449", ""},
	}
	ids := []string{}
	for i := 0; i < 450; i += 2 {
		ids = append(ids, fmt.Sprintf("%04d", i))
	}
	cases = append(cases, struct {
		selection         Selection
		count             int
		first, last, next string
	}{Selection{RecordIDs: ids}, 225, "0000", "0448", ""})
	for i, c := range cases {
		selection, err := BoundedSelection(&c.selection)
		must(err)
		r := Run{ID: fmt.Sprintf("%srun%d", id, i), Principal: p, Operation: "generate", Title: "fixed population", Sources: []Ref{ref}, Selection: selection}
		must(s.SaveRun(ctx, r))
		info, err := s.SelectionInfo(ctx, p, ref, selection)
		must(err)
		if info.SourceCount != 450 || info.SelectedCount != c.count || info.NextAfter != c.next {
			t.Fatalf("info %+v", info)
		}
		stats, err := s.Stats(ctx, p, r.ID)
		must(err)
		if stats.Total != c.count || stats.Pending != c.count {
			t.Fatalf("wrong population total %+v", stats)
		}
		cursor := ""
		all := []string{}
		for page := 0; page < 5; page++ {
			batch, next, err := s.PreparePage(ctx, r, cursor)
			must(err)
			again, againNext, err := s.PreparePage(ctx, r, cursor)
			must(err)
			if Digest(batch) != Digest(again) || next != againNext {
				t.Fatal("retry changed selection")
			}
			all = append(all, batch...)
			if next == "" {
				break
			}
			cursor = next
		}
		if len(all) != c.count || all[0] != c.first || all[len(all)-1] != c.last {
			t.Fatalf("unexpected population size %d", len(all))
		}
	}
	publish("v2", 451)
	frozen, err := s.Run(ctx, p, id+"run0")
	must(err)
	rows, _, err := s.PreparePage(ctx, frozen, "")
	must(err)
	data, err := s.Prepared(ctx, frozen.ID, rows[0])
	must(err)
	if len(rows) != 40 || data[0].Fields["name"] != "v1" {
		t.Fatal("new publication changed original population")
	}
	if _, err = s.SelectionInfo(ctx, p, ref, &Selection{RecordIDs: []string{"missing"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing ID accepted", err)
	}
}
