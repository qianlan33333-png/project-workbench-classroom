package engine

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"os"
	"strings"
	"testing"
	"workbench-native/internal/core"
)

func TestMySQLDispatchBindsEligibilityAndRecipient(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("real MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	s, e := core.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.Migrate(ctx))
	id := uuid.NewString()
	p := core.Principal{Kind: "project", ProjectID: id, SessionID: id + "s"}
	owner := core.Principal{Kind: "owner", SessionID: id + "o"}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "dispatch fixture"}))
	check := core.RecordCheck{ID: "eligible", Name: "Explicit eligible records", Section: "identity", Schema: map[string]any{"type": "object", "required": []any{"eligible", "actor"}, "properties": map[string]any{"eligible": map[string]any{"const": true}, "actor": map[string]any{"type": "string", "minLength": 1}}}, Bindings: map[string]string{"actorId": "/actor"}}
	src := core.Resource{ID: id + "content", Name: "generated", OwnerProject: id, Sections: []core.Section{{ID: "content", Fields: []string{"text", "actor"}}}}
	identity := core.Resource{ID: id + "identity", Name: "authoritative", OwnerProject: id, Sections: []core.Section{{ID: "identity", Fields: []string{"eligible", "actor"}}}, Checks: []core.RecordCheck{check}}
	must(s.PutResource(ctx, owner, src))
	must(s.PutResource(ctx, owner, identity))
	publish := func(resource core.Resource, version string, records []core.Record) {
		must(s.BeginSnapshot(ctx, p, core.Snapshot{ResourceID: resource.ID, Version: version}))
		must(s.Stage(ctx, p, resource.ID, version, records))
		_, e = s.Publish(ctx, p, resource.ID, version, len(records))
		must(e)
	}
	publish(src, "v1", []core.Record{{ID: "a", Fields: map[string]any{"text": "approved draft", "actor": "wrong-recipient"}}, {ID: "b", Fields: map[string]any{"text": "blocked draft"}}, {ID: "c", Fields: map[string]any{"text": "unknown eligibility"}}, {ID: "d", Fields: map[string]any{"text": "outside approved subset"}}})
	publish(identity, "v1", []core.Record{{ID: "a", Fields: map[string]any{"eligible": true, "actor": "authoritative-recipient"}}, {ID: "b", Fields: map[string]any{"eligible": false, "actor": "blocked"}}, {ID: "d", Fields: map[string]any{"eligible": true, "actor": "outside-subset"}}})
	must(s.PutTransport(ctx, owner, core.Transport{ID: id, Name: "fixture", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	plan := core.DispatchPlan{Source: core.Ref{ResourceID: src.ID, Version: "v1", Section: "content"}, Eligibility: core.Ref{ResourceID: identity.ID, Version: "v1", Section: "identity"}, Check: check, EligibilitySchema: check.Schema, ConnectionID: id, TextPointer: "/text", TargetPointers: check.Bindings, RecordIDs: []string{"a", "b", "c"}}
	must(plan.Validate())
	r := core.Run{ID: id + "run", Principal: p, Operation: "dispatch_batch", Title: "fixed review", Dispatch: &plan, Sources: []core.Ref{plan.Source, plan.Eligibility}}
	must(s.SaveRun(ctx, r))
	a := Activities{Store: s}
	_, e = a.PrepareDispatchPage(ctx, r.ID, "")
	must(e)
	_, e = a.PrepareDispatchPage(ctx, r.ID, "")
	must(e)
	messages, e := s.Deliveries(ctx, p, r.ID, "")
	must(e)
	if len(messages) != 1 || messages[0].Target["actorId"] != "authoritative-recipient" {
		t.Fatal("model-selected recipient or eligibility bypass", messages)
	}
	stats, e := s.DeliveryStats(ctx, p, r.ID)
	must(e)
	if stats.States["prepared"] != 1 {
		t.Fatal("preparation sent anything")
	}
	if _, _, e = s.ClaimDelivery(ctx, messages[0].ID); !errors.Is(e, core.ErrDenied) {
		t.Fatal("unconfirmed batch claimed directly", e)
	}
	approved, e := a.PrepareDispatchReview(ctx, r.ID)
	must(e)
	if approved {
		t.Fatal("self approval")
	}
	d, e := s.Decision(ctx, p, r.ID+"-d1")
	must(e)
	wrong := d
	wrong.Digest = "changed"
	wrong.Answer = "发"
	if _, e = s.Answer(ctx, owner, wrong); !errors.Is(e, core.ErrConflict) {
		t.Fatal("stale approval accepted", e)
	}
	d.Answer = "发 " + r.ID[:8]
	_, e = s.Answer(ctx, owner, d)
	must(e)
	approved, e = a.PrepareDispatchReview(ctx, r.ID)
	must(e)
	if !approved {
		t.Fatal("saved owner decision not reused")
	}
	publish(identity, "v2", []core.Record{{ID: "a", Fields: map[string]any{"eligible": false, "actor": "authoritative-recipient"}}})
	if _, _, e = s.ClaimDelivery(ctx, messages[0].ID); !errors.Is(e, core.ErrConflict) {
		t.Fatal("changed eligibility still submitted", e)
	}
	t.Log("Real MySQL: ineligible/missing records excluded, generated recipient override ignored, no sends before review, fixed approval reused, newer eligibility blocks old reviewed batch")
}
