package core

import (
	"errors"
	"github.com/google/uuid"
	"strings"
	"sync"
	"testing"
)

func TestMySQLReplySubmissionAtomicAndImmutable(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "reply transaction"}))
	transport := Transport{ID: id, Name: "test", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}
	must(s.PutTransport(ctx, owner, transport))
	_, e := s.PutReplySettings(ctx, owner, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "customer-reply", Enabled: true})
	must(e)
	in := IncomingMessage{ConnectionID: id, ConversationID: "one", ActorID: "one", EventID: "first", Text: "hello"}
	route, e := s.EnsureReplyRoute(ctx, in)
	must(e)
	m, e := s.AcceptMessage(ctx, in)
	must(e)
	rid := uuid.NewSHA1(uuid.NameSpaceURL, []byte(m.ID+"/reply")).String()
	// Force the delivery insert to fail after the run insert. Neither run nor
	// inbound disposition may survive the transaction failure.
	_, e = s.DB.ExecContext(ctx, `INSERT INTO wb_deliveries(id,run_id,item_id,state,body) VALUES(?,?,?,'prepared','{}')`, rid, rid, "collision")
	must(e)
	if _, e = s.PrepareReply(ctx, route.Principal, id, in.EventID, "hello back"); e == nil {
		t.Fatal("collision accepted")
	}
	var count int
	must(s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_runs WHERE id=?`, rid).Scan(&count))
	if count != 0 {
		t.Fatal("partial run survived")
	}
	current, e := s.Message(ctx, route.Principal, id, in.EventID)
	must(e)
	if current.State != "received" {
		t.Fatal(current.State)
	}
	_, e = s.DB.ExecContext(ctx, `DELETE FROM wb_deliveries WHERE id=?`, rid)
	must(e)
	run, e := s.PrepareReply(ctx, route.Principal, id, in.EventID, "hello back")
	must(e)
	retry, e := s.PrepareReply(ctx, route.Principal, id, in.EventID, "hello back")
	must(e)
	if run.ID != retry.ID {
		t.Fatal("new ID on retry")
	}
	if _, e = s.PrepareReply(ctx, route.Principal, id, in.EventID, "different"); !errors.Is(e, ErrConflict) {
		t.Fatal("draft overwritten", e)
	}
	other := route.Principal
	other.SessionID = id + "other"
	if _, e = s.PrepareReply(ctx, other, id, in.EventID, "hello back"); !errors.Is(e, ErrDenied) {
		t.Fatal("other Session accepted", e)
	}
	// Successful gate writes must roll back when the connector is not ready.
	d, won, e := s.PrepareReplyDelivery(ctx, run.ID, transport, false)
	must(e)
	if won || d.State != "prepared" {
		t.Fatal(d, won)
	}
	current, e = s.Message(ctx, route.Principal, id, in.EventID)
	must(e)
	if current.State != "reply_queued" {
		t.Fatal("offline attempt consumed message", current.State)
	}
	must(s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_delivery_transports WHERE id=?`, rid).Scan(&count))
	if count != 0 {
		t.Fatal("offline attempt pinned transport")
	}
	stale := transport
	stale.Endpoint = "http://127.0.0.1:9998/mcp"
	_, won, e = s.PrepareReplyDelivery(ctx, run.ID, stale, true)
	must(e)
	if won {
		t.Fatal("claimed with stale connection")
	}
	_, won, e = s.PrepareReplyDelivery(ctx, run.ID, transport, true)
	must(e)
	if !won {
		t.Fatal("could not claim")
	}
	_, won, e = s.PrepareReplyDelivery(ctx, run.ID, transport, true)
	must(e)
	if won {
		t.Fatal("claimed twice")
	}
	// Duplicate tool call after claim returns the same immutable result.
	retry, e = s.PrepareReply(ctx, route.Principal, id, in.EventID, "hello back")
	must(e)
	if retry.ID != run.ID {
		t.Fatal("lost idempotency after claim")
	}
}

func TestMySQLReplyOfflineInvalidationUnblocksQueue(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "offline invalidation"}))
	tr := Transport{ID: id, Name: "test", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}
	must(s.PutTransport(ctx, owner, tr))
	_, e := s.PutReplySettings(ctx, owner, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "customer-reply", Enabled: true})
	must(e)
	in := IncomingMessage{ConnectionID: id, ConversationID: "one", ActorID: "one", EventID: "first", Text: "hello"}
	route, e := s.EnsureReplyRoute(ctx, in)
	must(e)
	_, e = s.AcceptMessage(ctx, in)
	must(e)
	run, e := s.PrepareReply(ctx, route.Principal, id, in.EventID, "draft")
	must(e)
	paused, e := s.ChangeReplyMode(ctx, owner, route.ID, route.Revision, false)
	must(e)
	_, e = s.ChangeReplyMode(ctx, owner, route.ID, paused.Revision, true)
	must(e)
	d, won, e := s.PrepareReplyDelivery(ctx, run.ID, tr, false)
	must(e)
	if won || d.State != "failed" {
		t.Fatal("offline old draft not terminated", d, won)
	}
	in.EventID = "fresh"
	m, e := s.AcceptMessage(ctx, in)
	must(e)
	allowed, e := s.CanAdmitIncoming(ctx, m)
	must(e)
	if !allowed {
		t.Fatal("old draft blocked subsequent input")
	}
}

func TestMySQLReplyFinishesLegacyPartialSubmission(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "legacy reply recovery"}))
	tr := Transport{ID: id, Kind: "wecom-gateway", Name: "test", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}
	must(s.PutTransport(ctx, owner, tr))
	_, e := s.PutReplySettings(ctx, owner, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "customer-reply", Enabled: true})
	must(e)
	for _, withDelivery := range []bool{false, true} {
		event := "run-only"
		if withDelivery {
			event = "run-and-delivery"
		}
		in := IncomingMessage{ConnectionID: id, ConversationID: event, ActorID: event, EventID: event, Text: "hello"}
		route, e := s.EnsureReplyRoute(ctx, in)
		must(e)
		m, e := s.AcceptMessage(ctx, in)
		must(e)
		rid := uuid.NewSHA1(uuid.NameSpaceURL, []byte(m.ID+"/reply")).String()
		run := Run{ID: rid, Principal: route.Principal, Operation: "reply", Title: "回复新消息", Goal: strings.Repeat("legacy paragraph\n", 9) + "last", OriginMessage: m.ID}
		must(s.SaveRun(ctx, run))
		if withDelivery {
			must(s.PutDelivery(ctx, run, Delivery{ID: rid, RunID: rid, ItemID: "reply", ConnectionID: id, Target: map[string]string{"conversationId": event, "actorId": event, "replyTo": event}, Text: run.Goal}))
		}
		got, e := s.PrepareReply(ctx, route.Principal, id, event, run.Goal)
		must(e)
		if got.ID != rid || len(got.ReplyParts) != 0 {
			t.Fatal("legacy reply layout or ID changed")
		}
		delivery, e := s.Delivery(ctx, rid)
		must(e)
		if delivery.Text != run.Goal || delivery.ReplyPart != 0 {
			t.Fatal("legacy reply body was changed")
		}
		current, e := s.Message(ctx, route.Principal, id, event)
		must(e)
		if current.State != "reply_queued" {
			t.Fatal("legacy input not queued", current.State)
		}
		_, won, e := s.PrepareReplyDelivery(ctx, rid, tr, true)
		must(e)
		if !won {
			t.Fatal("legacy draft not sendable")
		}
	}
}

func TestMySQLReplyConcurrentConfigurationAndDuplicateSubmission(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "concurrent configuration"}))
	must(s.PutTransport(ctx, owner, Transport{ID: id, Name: "test", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	cfg, e := s.PutReplySettings(ctx, owner, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "customer-reply", Enabled: true})
	must(e)
	in := IncomingMessage{ConnectionID: id, ConversationID: "one", ActorID: "one", EventID: "first", Text: "hello"}
	route, e := s.EnsureReplyRoute(ctx, in)
	must(e)
	_, e = s.AcceptMessage(ctx, in)
	must(e)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 13)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.PrepareReply(ctx, route.Principal, id, in.EventID, "same draft")
			if err != nil && !errors.Is(err, ErrDenied) {
				errs <- err
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, err := s.PutReplySettings(ctx, owner, cfg)
		if err != nil {
			errs <- err
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
