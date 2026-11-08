package core

import (
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestMySQLConversationControlRestoreAndSharedGate(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "o"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "Control acceptance"}))
	c := Transport{ID: id, Name: "Gateway", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}
	must(s.PutTransport(ctx, owner, c))
	_, e := s.PutReplySettings(ctx, owner, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "qa", Enabled: true})
	must(e)
	in := IncomingMessage{ConnectionID: id, ConversationID: "a", ActorID: "a", ExternalUserID: "external-a", EventID: "first", Text: "first"}
	a, e := s.EnsureReplyRoute(ctx, in)
	must(e)
	_, e = s.AcceptMessage(ctx, in)
	must(e)
	b := in
	b.ActorID = "b"
	b.ConversationID = "b"
	b.ExternalUserID = "external-b"
	rb, e := s.EnsureReplyRoute(ctx, b)
	must(e)
	past := time.Now().Add(-5 * 24 * time.Hour)
	in.EventID = "return"
	in.OccurredAt = &past
	again, e := s.EnsureReplyRoute(ctx, in)
	must(e)
	if rb.Principal == a.Principal || again.Principal != a.Principal {
		t.Fatal("Session identity changed or crossed")
	}
	old := Run{ID: id + "old", Principal: a.Principal, Operation: "message", Title: "old"}
	must(s.SaveRun(ctx, old))
	must(s.PutDelivery(ctx, old, Delivery{ID: old.ID, RunID: old.ID, ItemID: "one", ConnectionID: id, Target: map[string]string{"actorId": "a"}, Text: "old"}))
	must(s.MessageDisposition(ctx, a.Principal, id, "first", "handoff"))
	state, e := s.ControlConversation(ctx, owner, ConversationControl{Op: "status", ConnectionID: id, ExternalUserID: "external-a", Scope: "binding/1"})
	must(e)
	if state.Mode != "human" || state.Version != 2 {
		t.Fatal(state)
	}
	linked := c
	linked.ID = id + "batch"
	linked.ModeConnectionID = id
	must(s.PutTransport(ctx, owner, linked))
	cross := Run{ID: id + "cross", Principal: a.Principal, Operation: "message", Title: "cross"}
	must(s.SaveRun(ctx, cross))
	must(s.PutDelivery(ctx, cross, Delivery{ID: cross.ID, RunID: cross.ID, ItemID: "one", ConnectionID: linked.ID, Target: map[string]string{"externalUserId": "external-a"}, Text: "paused"}))
	unknown := cross
	unknown.ID = id + "unknown"
	must(s.SaveRun(ctx, unknown))
	must(s.PutDelivery(ctx, unknown, Delivery{ID: unknown.ID, RunID: unknown.ID, ItemID: "one", ConnectionID: linked.ID, Target: map[string]string{"externalUserId": "unmapped"}, Text: "unknown"}))
	if _, won, e := s.ClaimDelivery(ctx, unknown.ID); won || !errors.Is(e, ErrDenied) {
		t.Fatal("unmapped cross transport allowed", won, e)
	}
	if _, won, e := s.ClaimDelivery(ctx, cross.ID); won || !errors.Is(e, ErrDenied) {
		t.Fatal("linked transport bypass", won, e)
	}
	q := ConversationControl{Op: "resume", ConnectionID: id, ExternalUserID: "external-a", SessionID: state.SessionID, Version: state.Version, RequestID: uuid.NewString(), Scope: "binding/1", Actor: "owner"}
	resumed, e := s.ControlConversation(ctx, owner, q)
	must(e)
	repeat, e := s.ControlConversation(ctx, owner, q)
	must(e)
	if resumed != repeat || resumed.Mode != "auto" || resumed.Version != 3 || resumed.SessionID != a.Principal.SessionID {
		t.Fatal(resumed, repeat)
	}
	changed := q
	changed.Actor = "other"
	if _, e = s.ControlConversation(ctx, owner, changed); !errors.Is(e, ErrConflict) {
		t.Fatal("changed request accepted", e)
	}
	stale := q
	stale.RequestID = uuid.NewString()
	if _, e = s.ControlConversation(ctx, owner, stale); !errors.Is(e, ErrConflict) {
		t.Fatal("stale resume accepted", e)
	}
	for _, draft := range []Run{old, cross} {
		if _, won, e := s.ClaimDelivery(ctx, draft.ID); won || !errors.Is(e, ErrDenied) {
			t.Fatal("old draft revived", won, e)
		}
	}
	fresh := old
	fresh.ID = id + "fresh"
	must(s.SaveRun(ctx, fresh))
	must(s.PutDelivery(ctx, fresh, Delivery{ID: fresh.ID, RunID: fresh.ID, ItemID: "one", ConnectionID: id, Target: map[string]string{"actorId": "a"}, Text: "fresh"}))
	if _, won, e := s.ClaimDelivery(ctx, fresh.ID); !won || e != nil {
		t.Fatal("fresh blocked", won, e)
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_events WHERE session_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.kind'))='conversation.mode.changed'`, a.Principal.SessionID)
	must(e)
	var events []RunEvent
	for rows.Next() {
		event, e := scanJSON[RunEvent](rows)
		must(e)
		events = append(events, event)
	}
	must(rows.Err())
	rows.Close()
	count := 0
	c.Enabled = false
	must(s.PutTransport(ctx, owner, c))
	for _, event := range events {
		if event.SessionID != a.Principal.SessionID || event.Kind != "conversation.mode.changed" {
			continue
		}
		count++
		r, e := s.QueueModeNotification(ctx, event)
		must(e)
		_, e = s.QueueModeNotification(ctx, event)
		must(e)
		if _, won, e := s.ClaimDelivery(ctx, r.ID); !won || e != nil {
			t.Fatal("control event blocked", won, e)
		}
	}
	if count != 2 {
		t.Fatal("events lost or duplicated", count)
	}
}

func TestMySQLConflictingExternalIdentityIsUnrouted(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "o"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "Identity"}))
	must(s.PutTransport(ctx, owner, Transport{ID: id, Name: "Gateway", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	_, e := s.PutReplySettings(ctx, owner, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "qa", Enabled: true})
	must(e)
	in := IncomingMessage{ConnectionID: id, ConversationID: "a", ActorID: "a", ExternalUserID: "one", EventID: "1", Text: "first"}
	r, e := s.EnsureReplyRoute(ctx, in)
	must(e)
	_, e = s.AcceptMessage(ctx, in)
	must(e)
	in.ExternalUserID = "two"
	in.EventID = "2"
	m, e := s.AcceptMessage(ctx, in)
	must(e)
	repeat, e := s.AcceptMessage(ctx, in)
	must(e)
	if m.State != "unrouted" || m.Principal.SessionID != "" || m.ID != repeat.ID {
		t.Fatal("identity mixed", m)
	}
	paused, e := s.Route(ctx, r.ID)
	must(e)
	if paused.AutoReply || paused.Revision != 2 || paused.ExternalUserID != "one" {
		t.Fatal(paused)
	}
}

func TestMySQLModeOutboxRetainsLateCommitsAndIgnoresBusinessBacklog(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	early, e := s.DB.BeginTx(ctx, nil)
	must(e)
	defer early.Rollback()
	first := RunEvent{ID: id + "late", SessionID: id, Kind: "conversation.mode.changed", Text: "late commit"}
	_, e = early.ExecContext(ctx, `INSERT INTO wb_events(id,session_id,run_id,body) VALUES(?,?,?,?)`, first.ID, id, "", jsonText(first))
	must(e)
	second := first
	second.ID = id + "fast"
	_, e = s.DB.ExecContext(ctx, `INSERT INTO wb_events(id,session_id,run_id,body) VALUES(?,?,?,?)`, second.ID, id, "", jsonText(second))
	must(e)
	noise, e := s.DB.BeginTx(ctx, nil)
	must(e)
	for i := 0; i < 1000; i++ {
		v := RunEvent{ID: uuid.NewString(), SessionID: id, Kind: "message.history", Text: "synthetic"}
		_, e = noise.ExecContext(ctx, `INSERT INTO wb_events(id,session_id,run_id,body) VALUES(?,?,?,?)`, v.ID, id, "", jsonText(v))
		must(e)
	}
	must(noise.Commit())
	pending, e := s.PendingModeEvents(ctx)
	must(e)
	seen := map[string]bool{}
	for _, v := range pending {
		seen[v.ID] = true
	}
	if !seen[second.ID] || seen[first.ID] {
		t.Fatal("uncommitted/out-of-order event read incorrectly")
	}
	must(s.MarkModeQueued(ctx, second.ID))
	must(early.Commit())
	pending, e = s.PendingModeEvents(ctx)
	must(e)
	seen = map[string]bool{}
	for _, v := range pending {
		seen[v.ID] = true
	}
	if !seen[first.ID] || seen[second.ID] {
		t.Fatal("late commit lost behind queued event")
	}
}
