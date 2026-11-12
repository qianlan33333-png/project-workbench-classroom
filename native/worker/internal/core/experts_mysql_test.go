package core

import (
	"errors"
	"sync"
	"testing"
)

func TestMySQLSessionExpertFirstBinding(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "-owner"}
	p := Principal{Kind: "project", ProjectID: id, SessionID: id + "-ordinary"}
	if err := s.PutProject(ctx, owner, Project{ID: id, Name: "Expert fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSession(ctx, p); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	outcome := make(chan error, 2)
	for _, ref := range []string{"shared:fixture-a", "project:" + id + ":fixture-b"} {
		group.Add(1)
		go func(ref string) {
			defer group.Done()
			_, err := s.BindSessionExpert(ctx, p, ref)
			outcome <- err
		}(ref)
	}
	group.Wait()
	close(outcome)
	success, conflict := 0, 0
	for err := range outcome {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrConflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("first binding had %d successes and %d conflicts", success, conflict)
	}
	ref, err := s.SessionExpert(ctx, p)
	if err != nil || ref == "" {
		t.Fatal(ref, err)
	}
	if again, err := s.BindSessionExpert(ctx, p, ref); err != nil || again != ref {
		t.Fatal("idempotent retry failed", again, err)
	}
	if _, err := s.BindSessionExpert(ctx, p, "project:other:fixture-a"); !errors.Is(err, ErrDenied) {
		t.Fatal("accepted another project's expert", err)
	}
}

func TestMySQLReplyExpertDefaultKeepsQueuedAndExistingCustomers(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "-owner"}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "Reply expert fixture"}))
	must(s.PutTransport(ctx, owner, Transport{ID: id, Name: "fixture", Endpoint: "http://127.0.0.1:9999/mcp",
		TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	cfg, err := s.PutReplySettings(ctx, owner, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "old-expert", Enabled: true})
	must(err)
	first := IncomingMessage{ConnectionID: id, EventID: "first", ActorID: "old", ConversationID: "old", Text: "first message"}
	old, err := s.EnsureReplyRoute(ctx, first)
	must(err)
	// Represent a customer that existed before the compatible runtime was installed.
	_, err = s.DB.ExecContext(ctx, "UPDATE wb_sessions SET expert_ref='' WHERE id=?", old.Principal.SessionID)
	must(err)
	queued, err := s.AcceptMessage(ctx, first)
	must(err)
	if queued.ExpertSkill != "old-expert" || queued.ExpertRef != "" {
		t.Fatal("pre-migration input was changed", queued)
	}
	replacement := "project:" + id + ":new-expert"
	_, err = s.SetProjectExpertDefault(ctx, owner, id, replacement, "", map[string]string{"old-expert": "shared:old-expert"})
	must(err)
	unchanged, err := s.ReplySettings(ctx, owner, id)
	must(err)
	if unchanged.Revision != cfg.Revision || unchanged.ExpertSkill != cfg.ExpertSkill {
		t.Fatal("default changed the send gate", unchanged)
	}
	admitted, err := s.CanAdmitIncoming(ctx, queued)
	must(err)
	if !admitted {
		t.Fatal("default change blocked a queued message")
	}
	resolved, err := s.ResolveLegacyExpertRef(ctx, id, "legacy:old-expert")
	must(err)
	if resolved != "shared:old-expert" {
		t.Fatal("queued slug did not preserve its canonical source", resolved)
	}
	first.EventID = "second"
	later, err := s.AcceptMessage(ctx, first)
	must(err)
	if later.ExpertRef != "legacy:old-expert" || later.ExpertSkill != "old-expert" {
		t.Fatal("existing customer switched experts", later.ExpertRef, later.ExpertSkill)
	}
	newInput := IncomingMessage{ConnectionID: id, EventID: "new", ActorID: "new", ConversationID: "new", Text: "new message"}
	newRoute, err := s.EnsureReplyRoute(ctx, newInput)
	must(err)
	fresh, err := s.AcceptMessage(ctx, newInput)
	must(err)
	if fresh.ExpertRef != replacement || fresh.ExpertSkill != "new-expert" || fresh.Principal != newRoute.Principal {
		t.Fatal("new customer did not get the new expert", fresh.ExpertRef, fresh.ExpertSkill)
	}
	if _, err := s.BindSessionExpert(ctx, newRoute.Principal, "shared:unauthorized"); !errors.Is(err, ErrDenied) {
		t.Fatal("ordinary binding changed a customer session", err)
	}
}
