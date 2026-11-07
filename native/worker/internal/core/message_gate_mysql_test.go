package core

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMySQLMessageGatesAndFrozenConfirmation(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "o"}
	p := Principal{Kind: "project", ProjectID: id, SessionID: id + "s"}
	personal := Principal{Kind: "personal", SessionID: id + "pa"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "admission"}))
	must(s.PutTransport(ctx, owner, Transport{ID: id, Name: "test", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	var wins atomic.Int32
	var wg sync.WaitGroup
	for _, suffix := range []string{"a", "b"} {
		wg.Add(1)
		go func(suffix string) {
			defer wg.Done()
			e := s.PutRoute(ctx, owner, MessageRoute{ID: id + suffix, ConnectionID: id, ConversationID: "room", ActorID: suffix, Principal: p, Enabled: true, AutoReply: true})
			if e == nil {
				wins.Add(1)
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e)
			}
		}(suffix)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("customer contexts shared", wins.Load())
	}
	must(s.PutRoute(ctx, owner, MessageRoute{ID: id + "pa", ConnectionID: id, ConversationID: "owner", ActorID: "owner", Principal: personal, Enabled: true}))
	// A second owner channel can share 今日 without relaxing customer isolation.
	must(s.PutRoute(ctx, owner, MessageRoute{ID: id + "pb", ConnectionID: id, ConversationID: "owner2", ActorID: "owner2", Principal: personal, Enabled: true}))
	in := IncomingMessage{ConnectionID: id, EventID: "old-send", ConversationID: "owner", ActorID: "owner", Text: "发"}
	old, e := s.AcceptMessage(ctx, in)
	must(e)
	r := Run{ID: id + "message", Principal: p, Operation: "message", Title: "immutable review"}
	must(s.SaveRun(ctx, r))
	delivery := Delivery{ID: r.ID, RunID: r.ID, ItemID: "one", ConnectionID: id, Target: map[string]string{"actorId": "recipient"}, Text: "fixed"}
	must(s.PutDelivery(ctx, r, delivery))
	_, e = s.Ask(ctx, r, 1, "send this fixed text?")
	must(e)
	duplicate, e := s.AcceptMessage(ctx, in)
	must(e)
	if old.DecisionID != duplicate.DecisionID || duplicate.DecisionID == r.ID+"-d1" {
		t.Fatal("old send matched a new decision")
	}
	chosen, matched, e := s.OwnerMessageDecision(ctx, duplicate)
	must(e)
	if matched && chosen.RunID == r.ID {
		t.Fatal("replay approved new output")
	}
	_, claimed, e := s.ClaimDelivery(ctx, delivery.ID)
	must(e)
	if !claimed {
		t.Fatal("claim")
	}
	_, e = s.FinishDelivery(ctx, delivery.ID, "pending_confirmation", "provider-msgid", "")
	must(e)
	saved, e := s.FinishDelivery(ctx, delivery.ID, "unknown", "", "late empty receipt")
	must(e)
	if saved.State != "pending_confirmation" || saved.Receipt != "provider-msgid" {
		t.Fatal("late query lost send proof", saved)
	}
	route, e := s.SessionRoute(ctx, p)
	must(e)
	inbound, e := s.AcceptMessage(ctx, IncomingMessage{ConnectionID: id, EventID: "reply", ConversationID: route.ConversationID, ActorID: route.ActorID, Text: "question"})
	must(e)
	reply := Run{ID: id + "reply", Principal: p, Operation: "reply", Title: "reply"}
	must(s.SaveRun(ctx, reply))
	must(s.PutDelivery(ctx, reply, Delivery{ID: reply.ID, RunID: reply.ID, ItemID: "reply", ConnectionID: id, Target: map[string]string{"replyTo": inbound.EventID}, Text: "draft"}))
	must(s.SetMessageState(ctx, p, id, inbound.EventID, "reply_queued"))
	must(s.MessageDisposition(ctx, p, id, inbound.EventID, "handoff"))
	if _, _, e = s.ClaimDelivery(ctx, reply.ID); !errors.Is(e, ErrDenied) {
		t.Fatal("queued human takeover ignored", e)
	}
	config := NotificationSettings{Enabled: true, SessionID: personal.SessionID, Targets: []NotificationTarget{{ConnectionID: id, Target: map[string]string{"conversationId": "owner", "actorId": "owner"}}}}
	must(s.PutNotificationSettings(ctx, owner, config))
	n := Run{ID: id + "notify", Principal: personal, Operation: "notification", Title: "notice"}
	must(s.SaveRun(ctx, n))
	must(s.PutDelivery(ctx, n, Delivery{ID: n.ID, RunID: n.ID, ItemID: "notice", ConnectionID: id, Target: config.Targets[0].Target, Text: "result"}))
	config.Targets[0].Target["replyTo"] = "another-room-message"
	if e = s.PutNotificationSettings(ctx, owner, config); !errors.Is(e, ErrInvalid) {
		t.Fatal("recipient override accepted", e)
	}
	delete(config.Targets[0].Target, "replyTo")
	config.Enabled = false
	must(s.PutNotificationSettings(ctx, owner, config))
	if _, _, e = s.ClaimDelivery(ctx, n.ID); !errors.Is(e, ErrDenied) {
		t.Fatal("disabled notification sent", e)
	}
}
