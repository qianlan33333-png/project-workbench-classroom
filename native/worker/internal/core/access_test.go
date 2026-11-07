package core

import (
	"context"
	"errors"
	"testing"
)

type testGate struct {
	resources map[string]Resource
	snapshots map[string]Snapshot
	grants    map[string]bool
}

func (g *testGate) Resource(_ context.Context, id string) (Resource, error) {
	v, ok := g.resources[id]
	if !ok {
		return v, ErrDenied
	}
	return v, nil
}
func (g *testGate) Snapshot(_ context.Context, id, version string) (Snapshot, error) {
	v, ok := g.snapshots[id+"/"+version]
	if !ok {
		return v, ErrDenied
	}
	return v, nil
}
func (g *testGate) Granted(_ context.Context, r, p, section string) (bool, error) {
	return g.grants[r+"/"+p+"/"+section] || g.grants[r+"/"+p+"/*"], nil
}
func TestResourceMatrixAndRevocation(t *testing.T) {
	g := &testGate{resources: map[string]Resource{"A": {ID: "A", OwnerProject: "A"}, "D": {ID: "D"}}, snapshots: map[string]Snapshot{}, grants: map[string]bool{"D/A/public": true, "D/B/public": true}}
	for _, id := range []string{"A", "D"} {
		g.snapshots[id+"/v1"] = Snapshot{ResourceID: id, Version: "v1", State: "published", Sections: []Section{{ID: "public"}, {ID: "private"}}}
	}
	for _, tc := range []struct {
		project, resource, section string
		allow                      bool
	}{{"A", "A", "private", true}, {"B", "A", "public", false}, {"A", "D", "public", true}, {"B", "D", "private", false}, {"C", "D", "public", false}} {
		e := Authorize(context.Background(), g, Principal{Kind: "project", ProjectID: tc.project, SessionID: "session"}, Ref{tc.resource, "v1", tc.section})
		if (e == nil) != tc.allow {
			t.Fatalf("%+v: %v", tc, e)
		}
	}
	if e := Authorize(context.Background(), g, Principal{Kind: "personal", SessionID: "today"}, Ref{"D", "v1", "private"}); e != nil {
		t.Fatal(e)
	}
	delete(g.grants, "D/A/public")
	if e := Authorize(context.Background(), g, Principal{Kind: "project", ProjectID: "A", SessionID: "session"}, Ref{"D", "v1", "public"}); !errors.Is(e, ErrDenied) {
		t.Fatal("revoked reference remained accessible", e)
	}
}
func TestDerivedResourceCannotLaunderSource(t *testing.T) {
	g := &testGate{resources: map[string]Resource{"D": {ID: "D"}, "output": {ID: "output", OwnerProject: "A"}}, snapshots: map[string]Snapshot{
		"D/v1":      {State: "published", Sections: []Section{{ID: "secret"}}},
		"output/v1": {State: "published", Sections: []Section{{ID: "text"}}, Dependencies: []Ref{{"D", "v1", "secret"}}},
	}, grants: map[string]bool{"output/B/text": true, "D/A/secret": true}}
	if e := Authorize(context.Background(), g, Principal{Kind: "project", ProjectID: "B", SessionID: "B"}, Ref{"output", "v1", "text"}); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	g.grants["D/B/secret"] = true
	if e := Authorize(context.Background(), g, Principal{Kind: "project", ProjectID: "B", SessionID: "B"}, Ref{"output", "v1", "text"}); e != nil {
		t.Fatal(e)
	}
}
func TestContextBudgetAndMissingFacts(t *testing.T) {
	m := Method{RequiredFields: []string{"name"}, MaxInputBytes: 256}
	if _, e := PrepareInput(m, []Record{{Fields: map[string]any{"other": "x"}}}); e == nil {
		t.Fatal("missing required fact accepted")
	}
	if _, e := PrepareInput(m, []Record{{Fields: map[string]any{"name": "A"}}, {Fields: map[string]any{"name": "B"}}}); e == nil {
		t.Fatal("conflicting identity accepted")
	}
	if _, e := PrepareInput(m, []Record{{Fields: map[string]any{"name": string(make([]byte, 512))}}}); e == nil {
		t.Fatal("oversize context silently accepted")
	}
}
