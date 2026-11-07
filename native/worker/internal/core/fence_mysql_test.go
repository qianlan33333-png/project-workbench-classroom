package core

import (
	"errors"
	"testing"
)

func TestMySQLStaleWorkerCannotCommitOrReacquire(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "o"}
	p := Principal{Kind: "project", ProjectID: id, SessionID: id + "s"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "Fencing"}))
	resource := Resource{ID: id, Name: "input", OwnerProject: id, Sections: []Section{{ID: "data", Fields: []string{"name"}}}}
	must(s.PutResource(ctx, owner, resource))
	run := Run{ID: id + "r", Title: "stale commit", Principal: p}
	must(s.SaveRun(ctx, run))
	a, e := s.BeginWrite(ctx, id, id+"a")
	must(e)
	stale := WithFence(ctx, a)
	must(s.BeginSnapshot(stale, p, Snapshot{ResourceID: id, Version: "a"}))
	row := []Record{{ID: "1", Fields: map[string]any{"name": "old"}}}
	must(s.Stage(stale, p, id, "a", row))
	must(s.EndWrite(ctx, a))
	b, e := s.BeginWrite(ctx, id, id+"b")
	must(e)
	fresh := WithFence(ctx, b)
	must(s.BeginSnapshot(fresh, p, Snapshot{ResourceID: id, Version: "b"}))
	// The old context is deliberately still live, just like a partitioned worker.
	if stale.Err() != nil {
		t.Fatal("test did not retain live context")
	}
	if e = s.Stage(stale, p, id, "a", row); !errors.Is(e, ErrDenied) {
		t.Fatal("late stage", e)
	}
	if _, e = s.Publish(stale, p, id, "a", 1); !errors.Is(e, ErrDenied) {
		t.Fatal("canceled version published", e)
	}
	if e = s.RecordItem(stale, run.ID, ItemResult{ID: "1", State: "succeeded", InputHash: "x"}); !errors.Is(e, ErrDenied) {
		t.Fatal("late result", e)
	}
	if _, e = s.BeginWrite(ctx, id, id+"a"); !errors.Is(e, ErrDenied) {
		t.Fatal("old acquire retry stole active writer", e)
	}
	must(s.EndWrite(ctx, a)) // A duplicate cleanup cannot cancel B.
	must(s.Stage(fresh, p, id, "b", row))
	_, e = s.Publish(fresh, p, id, "b", 1)
	must(e)
	// Cancellation can arrive before acquisition's database commit.
	must(s.EndWrite(ctx, WriteFence{ProjectID: id, Execution: id + "early"}))
	if _, e = s.BeginWrite(ctx, id, id+"early"); !errors.Is(e, ErrDenied) {
		t.Fatal("late acquisition revived canceled attempt", e)
	}
}
