package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestMySQLReplyPauseResumeRejectsOldDraftAndDedupsHuman(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "QA"}))
	must(s.PutTransport(ctx, owner, Transport{ID: id, Name: "Gateway", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_QA", Projects: []string{id}, Enabled: true}))
	_, e := s.PutReplySettings(ctx, owner, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "qa", Enabled: true})
	must(e)
	incoming := IncomingMessage{ConnectionID: id, ConversationID: "one", ActorID: "one", EventID: "question", Text: "question", DisplayName: "Customer"}
	route, e := s.EnsureReplyRoute(ctx, incoming)
	must(e)
	m, e := s.AcceptMessage(ctx, incoming)
	must(e)
	if m.ExpertSkill != "qa" || m.RouteRevision != 1 {
		t.Fatalf("binding lost: %+v", m)
	}
	second := incoming
	second.EventID = "second"
	second.Text = "Second"
	later, e := s.AcceptMessage(ctx, second)
	must(e)
	admit, e := s.CanAdmitIncoming(ctx, later)
	must(e)
	if admit {
		t.Fatal("later message admitted before first")
	}
	admit, e = s.CanAdmitIncoming(ctx, m)
	must(e)
	if !admit {
		t.Fatal("first message blocked")
	}
	retry := incoming
	retry.DisplayName = "Renamed"
	_, e = s.AcceptMessage(ctx, retry)
	must(e)
	run := Run{ID: id + "reply", Principal: route.Principal, Operation: "reply", Title: "Reply", OriginMessage: m.ID}
	must(s.SaveRun(ctx, run))
	must(s.PutDelivery(ctx, run, Delivery{ID: run.ID, RunID: run.ID, ItemID: "reply", ConnectionID: id, Target: map[string]string{"conversationId": "one", "actorId": "one", "replyTo": "question"}, Text: "old draft"}))
	must(s.SetMessageState(ctx, route.Principal, id, "question", "reply_queued"))
	human := incoming
	human.EventID = "human"
	human.Kind = "human"
	human.Text = "I will handle it"
	_, e = s.AcceptMessage(ctx, human)
	must(e)
	_, e = s.AcceptMessage(ctx, human)
	must(e)
	paused, e := s.Route(ctx, route.ID)
	must(e)
	if paused.AutoReply || paused.Revision != 2 {
		t.Fatalf("human event duplicated or not paused: %+v", paused)
	}
	if _, e = s.ChangeReplyMode(ctx, owner, route.ID, 1, true); !errors.Is(e, ErrConflict) {
		t.Fatal("stale resume accepted", e)
	}
	active, e := s.ChangeReplyMode(ctx, owner, route.ID, 2, true)
	must(e)
	if active.Revision != 3 {
		t.Fatal(active)
	}
	cfg, e := s.ReplySettings(ctx, owner, id)
	must(e)
	cfg.Enabled = false
	cfg, e = s.PutReplySettings(ctx, owner, cfg)
	must(e)
	cfg.Enabled = true
	_, e = s.PutReplySettings(ctx, owner, cfg)
	must(e)
	if _, won, e := s.ClaimDelivery(ctx, run.ID); won || !errors.Is(e, ErrDenied) {
		t.Fatal("old draft sent after resume", won, e)
	}
	expired, e := s.ExpireReplyDraft(ctx, run.ID)
	must(e)
	if !expired {
		t.Fatal("invalid prepared draft not expired")
	}
	dead, e := s.Delivery(ctx, run.ID)
	must(e)
	if dead.State != "failed" {
		t.Fatal(dead)
	}
	var history int
	must(s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_events WHERE id=?`, later.ID+"-history").Scan(&history))
	if history != 1 {
		t.Fatal("queued message disappeared after pause")
	}

	fresh := incoming
	fresh.EventID = "new-question"
	fresh.Text = "new question"
	freshMessage, e := s.AcceptMessage(ctx, fresh)
	must(e)
	if freshMessage.State != "received" || freshMessage.RouteRevision != 3 {
		t.Fatal(freshMessage)
	}
	must(s.MessageDisposition(ctx, route.Principal, id, fresh.EventID, "handoff"))
	paused, e = s.Route(ctx, route.ID)
	must(e)
	if paused.AutoReply || paused.Revision != 4 {
		t.Fatal("handoff was not durable", paused)
	}
	next := incoming
	next.EventID = "paused-question"
	v, e := s.AcceptMessage(ctx, next)
	must(e)
	if v.State != "paused" {
		t.Fatal("paused customer launched", v)
	}
	other := route.Principal
	other.SessionID = id + "other"
	if _, e = s.MessageContext(ctx, other, id, next.EventID); !errors.Is(e, ErrDenied) {
		t.Fatal("cross-object read accepted", e)
	}
}

func TestMySQLTenThousandReplySessionsArePagedWithoutHistory(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	// Bulk fixture only: no models, agents, customer sends or Temporal executions.
	for start := 0; start < 10000; start += 200 {
		marks := []string{}
		args := []any{}
		for i := start; i < start+200; i++ {
			rid := fmt.Sprintf("%s-%05d", id, i)
			v := MessageRoute{ID: rid, ConnectionID: id, ConversationID: rid, ActorID: rid, Principal: Principal{Kind: "project", ProjectID: id, SessionID: "object-" + rid}, DisplayName: fmt.Sprintf("User %05d", i), LastMessageAt: fmt.Sprintf("2026-11-08T01:%02d:%02d.000000Z", i/60%60, i%60), AutoReply: i%2 == 0, Revision: 1}
			marks = append(marks, "(?,?,?,?,?)")
			args = append(args, rid, id, rid, rid, jsonText(v))
		}
		_, e := s.DB.ExecContext(ctx, `INSERT INTO wb_message_routes(id,connection_id,conversation_id,actor_id,body) VALUES `+strings.Join(marks, ","), args...)
		must(e)
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, e := s.Conversations(ctx, owner, id, "", cursor, false)
		must(e)
		if len(page.Items) > 50 {
			t.Fatal("unbounded page")
		}
		for _, row := range page.Items {
			if seen[row.ID] {
				t.Fatal("duplicate page", row.ID)
			}
			seen[row.ID] = true
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(seen) != 10000 {
		t.Fatal("lost conversations", len(seen))
	}
	page, e := s.Conversations(ctx, owner, id, "User 09999", "", false)
	must(e)
	if len(page.Items) != 1 {
		t.Fatal("search failed", page)
	}
	paused, e := s.Conversations(ctx, owner, id, "", "", true)
	must(e)
	for _, row := range paused.Items {
		if row.AutoReply {
			t.Fatal("pause filter failed")
		}
	}
	if _, e = s.Conversations(ctx, Principal{Kind: "project", ProjectID: id, SessionID: "object"}, id, "", "", false); !errors.Is(e, ErrDenied) {
		t.Fatal("customer accessed owner index", e)
	}
}
