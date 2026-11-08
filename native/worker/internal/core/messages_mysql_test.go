package core

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMySQLMessageDedupIsolationAndOneSendClaim(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	p := Principal{Kind: "project", ProjectID: id, SessionID: id + "s"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "Message isolation"}))
	must(s.PutTransport(ctx, owner, Transport{ID: id, Name: "test", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	route := MessageRoute{ID: id, ConnectionID: id, ConversationID: "room", ActorID: "one", Principal: p, AutoReply: true, Enabled: true}
	must(s.PutRoute(ctx, owner, route))
	inbound := IncomingMessage{ConnectionID: id, EventID: "platform-1", ConversationID: "room", ActorID: "one", Text: "help"}
	first, e := s.AcceptMessage(ctx, inbound)
	must(e)
	duplicate, e := s.AcceptMessage(ctx, inbound)
	must(e)
	if first.ID != duplicate.ID || first.State != "received" {
		t.Fatal("message duplicated or wrong route")
	}
	changed := inbound
	changed.Text = "changed"
	if _, e = s.AcceptMessage(ctx, changed); !errors.Is(e, ErrConflict) {
		t.Fatal("changed replay accepted", e)
	}
	other := p
	other.SessionID = id + "other"
	if _, e = s.Message(ctx, other, id, inbound.EventID); !errors.Is(e, ErrDenied) {
		t.Fatal("other Session read message", e)
	}
	unknown := inbound
	unknown.EventID = "other"
	unknown.ActorID = "unbound"
	m, e := s.AcceptMessage(ctx, unknown)
	must(e)
	if m.State != "unrouted" {
		t.Fatal("unknown identity routed")
	}
	must(s.MessageDisposition(ctx, p, id, inbound.EventID, "handoff"))
	must(s.MessageDisposition(ctx, p, id, inbound.EventID, "handoff"))
	if e = s.SetMessageState(ctx, p, id, inbound.EventID, "reply_queued"); !errors.Is(e, ErrConflict) {
		t.Fatal("human takeover reversed", e)
	}
	r := Run{ID: id + "run", Principal: p, Operation: "message", Title: "fixture"}
	must(s.SaveRun(ctx, r))
	d := Delivery{ID: r.ID, RunID: r.ID, ItemID: "one", ConnectionID: id, Target: map[string]string{"actorId": "unpaused-recipient"}, Text: "hello"}
	must(s.PutDelivery(ctx, r, d))
	must(s.PutDelivery(ctx, r, d))
	var claims atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, won, e := s.ClaimDelivery(ctx, d.ID)
			if e != nil {
				t.Error(e)
			}
			if won {
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("multiple send claims", claims.Load())
	}
	saved, e := s.FinishDelivery(ctx, d.ID, "unknown", "", "lost response")
	must(e)
	if saved.State != "unknown" {
		t.Fatal(saved.State)
	}
	_, won, e := s.ClaimDelivery(ctx, d.ID)
	must(e)
	if won {
		t.Fatal("unknown result resent")
	}
	saved, e = s.FinishDelivery(ctx, d.ID, "sent", "platform-1", "")
	must(e)
	saved, e = s.FinishDelivery(ctx, d.ID, "delivered", "platform-1", "")
	must(e)
	if saved.State != "delivered" {
		t.Fatal("delivery confirmation not recorded")
	}
	saved, e = s.FinishDelivery(ctx, d.ID, "unknown", "", "")
	must(e)
	if saved.State != "delivered" {
		t.Fatal("stale response overwrote delivery")
	}
}

func TestMySQLMessageContextBindsObjectAndHonorsRevocation(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	p := Principal{Kind: "project", ProjectID: id, SessionID: id + "s"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "Object binding"}))
	r := Resource{ID: id, Name: "shared", Sections: []Section{{ID: "profile", Fields: []string{"name"}}}}
	must(s.PutResource(ctx, owner, r))
	must(s.BeginSnapshot(ctx, owner, Snapshot{ResourceID: id, Version: "v1"}))
	must(s.Stage(ctx, owner, id, "v1", []Record{{ID: "one", Fields: map[string]any{"name": "one"}}, {ID: "two", Fields: map[string]any{"name": "two"}}}))
	_, e := s.Publish(ctx, owner, id, "v1", 2)
	must(e)
	must(s.SetGrant(ctx, owner, Grant{id, id, []string{"profile"}}))
	must(s.PutTransport(ctx, owner, Transport{ID: id, Name: "test", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	route := MessageRoute{ID: id, ConnectionID: id, ConversationID: "room", ActorID: "one", Principal: p, AutoReply: true, Enabled: true, Context: []RecordRef{{Ref: Ref{id, "v1", "profile"}, ID: "one"}}}
	must(s.PutRoute(ctx, owner, route))
	_, e = s.AcceptMessage(ctx, IncomingMessage{ConnectionID: id, EventID: "1", ConversationID: "room", ActorID: "one", Text: "help"})
	must(e)
	rows, e := s.MessageContext(ctx, p, id, "1")
	must(e)
	if len(rows) != 1 || rows[0].ID != "one" {
		t.Fatal("other object exposed")
	}
	must(s.SetGrant(ctx, owner, Grant{id, id, nil}))
	if _, e = s.MessageContext(ctx, p, id, "1"); !errors.Is(e, ErrDenied) {
		t.Fatal("revoked source read", e)
	}
}
