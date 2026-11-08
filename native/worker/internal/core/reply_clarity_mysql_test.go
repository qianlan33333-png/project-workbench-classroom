package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func replyFixture(t *testing.T) (*Store, Principal, MessageRoute, StoredMessage) {
	s, ctx, id := acceptanceStore(t)
	o := Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(s.PutProject(ctx, o, Project{ID: id, Name: "Reply clarity"}))
	must(s.PutTransport(ctx, o, Transport{ID: id, Name: "sink", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}))
	_, e := s.PutReplySettings(ctx, o, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "qa", Enabled: true})
	must(e)
	in := IncomingMessage{ConnectionID: id, ConversationID: "customer", ActorID: "customer", EventID: "initial", Text: "Synthetic input"}
	route, e := s.EnsureReplyRoute(ctx, in)
	must(e)
	m, e := s.AcceptMessage(ctx, in)
	must(e)
	return s, o, route, m
}
func TestMySQLChannelTimelineSameDayAndSubMicrosecondCursor(t *testing.T) {
	s, o, route, _ := replyFixture(t)
	ctx := t.Context()
	base := time.Date(2026, 11, 8, 12, 0, 0, 123456789, time.UTC)
	_, e := s.DB.ExecContext(ctx, `DELETE FROM wb_messages WHERE route_id=?`, route.ID)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 121; i++ {
		id := fmt.Sprintf("%s-m%03d", route.ID, i)
		at := base.Add(time.Duration(i/30) * time.Microsecond)
		m := StoredMessage{ID: id, IncomingMessage: IncomingMessage{ConnectionID: route.ConnectionID, ConversationID: route.ConversationID, ActorID: route.ActorID, EventID: id, Text: id}, RouteID: route.ID, Principal: route.Principal, State: "received", CreatedAt: at}
		_, e = s.DB.ExecContext(ctx, `INSERT INTO wb_messages(connection_id,event_id,digest,body) VALUES(?,?,?,?)`, route.ConnectionID, id, Digest(m), jsonText(m))
		if e != nil {
			t.Fatal(e)
		}
		if i%2 == 0 {
			r := Run{ID: id + "r", Principal: route.Principal, Operation: "reply", Title: "Timeline fixture", OriginMessage: id}
			if e = s.SaveRun(ctx, r); e != nil {
				t.Fatal(e)
			}
			d := Delivery{ID: r.ID, RunID: r.ID, ItemID: "reply", ConnectionID: route.ConnectionID, Text: "exact reply", Target: map[string]string{"actorId": route.ActorID}, State: "sent", CreatedAt: at, UpdatedAt: at}
			_, e = s.DB.ExecContext(ctx, `INSERT INTO wb_deliveries(id,run_id,item_id,state,body) VALUES(?,?,?,'sent',?)`, d.ID, d.RunID, d.ItemID, jsonText(d))
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	cursor := ""
	seen := map[string]bool{}
	pages := 0
	for {
		v, e := s.ChannelTimeline(ctx, o, route.Principal.SessionID, cursor)
		if e != nil {
			t.Fatal(e)
		}
		pages++
		if len(v.Items) > 50 {
			t.Fatal("page unbounded")
		}
		for _, item := range v.Items {
			if seen[item.Key] {
				t.Fatal("duplicate page item", item.Key)
			}
			seen[item.Key] = true
		}
		if v.Before == "" {
			break
		}
		cursor = v.Before
		if pages > 10 {
			t.Fatal("pagination loop")
		}
	}
	if len(seen) != 182 || pages != 4 {
		t.Fatalf("history lost: items=%d pages=%d", len(seen), pages)
	}
	if _, e = s.ChannelTimeline(ctx, route.Principal, route.Principal.SessionID, ""); !errors.Is(e, ErrDenied) {
		t.Fatal("model history endpoint bypass", e)
	}
}
func TestMySQLOwnerMessageAtomicPauseIdempotenceAndRecoveryProof(t *testing.T) {
	s, o, route, _ := replyFixture(t)
	ctx := t.Context()
	s.DB.SetMaxOpenConns(1)
	input := OwnerMessage{SessionID: route.Principal.SessionID, Key: "ui-one", Text: "Exact owner message", Revision: 1}
	if _, e := s.PrepareOwnerMessage(ctx, route.Principal, input); !errors.Is(e, ErrDenied) {
		t.Fatal(e)
	}
	r, e := s.PrepareOwnerMessage(ctx, o, input)
	if e != nil {
		t.Fatal(e)
	}
	paused, e := s.Route(ctx, route.ID)
	if e != nil || paused.AutoReply || paused.Revision != 2 {
		t.Fatal("pause not atomic", paused, e)
	}
	again, e := s.PrepareOwnerMessage(ctx, o, input)
	if e != nil || again.ID != r.ID {
		t.Fatal("duplicate intent", e)
	}
	bad := input
	bad.Text = "different"
	if _, e = s.PrepareOwnerMessage(ctx, o, bad); !errors.Is(e, ErrConflict) {
		t.Fatal("key changed meaning", e)
	}
	expired, e := s.ExpireOwnerDraft(ctx, r.ID)
	if e != nil || expired {
		t.Fatal("valid original draft not recoverable", e)
	}
	_, won, e := s.ClaimDelivery(ctx, r.ID)
	if e != nil || !won {
		t.Fatal("valid original proof denied", e)
	}
	_, won, e = s.ClaimDelivery(ctx, r.ID)
	if e != nil || won {
		t.Fatal("duplicate submit", e)
	}
	newer := input
	newer.Key = "ui-two"
	newer.Revision = 2
	if _, e = s.PrepareOwnerMessage(ctx, o, newer); !errors.Is(e, ErrConflict) {
		t.Fatal("unknown original request duplicated", e)
	}
	// The standard sender need not emit a self_sent callback. A later customer
	// message must still expose the exact owner reply with its real send state.
	if _, err := s.FinishDelivery(ctx, r.ID, "sent", "synthetic-receipt", ""); err != nil {
		t.Fatal(err)
	}
	later := IncomingMessage{ConnectionID: route.ConnectionID, ConversationID: route.ConversationID, ActorID: route.ActorID, EventID: "after-owner", Text: "Please continue"}
	if _, err := s.AcceptMessage(ctx, later); err != nil {
		t.Fatal(err)
	}
	// Future manual replies cannot push this older event's real context out.
	for i := 0; i < 11; i++ {
		id := fmt.Sprintf("%s-future-%d", r.ID, i)
		run := Run{ID: id, Principal: route.Principal, Operation: "owner_message", Title: "future fixture"}
		if e = s.SaveRun(ctx, run); e != nil {
			t.Fatal(e)
		}
		d := Delivery{ID: id, RunID: id, ItemID: "reply", State: "sent", Text: "future reply", CreatedAt: time.Now().Add(time.Hour)}
		if _, e = s.DB.ExecContext(ctx, `INSERT INTO wb_deliveries(id,run_id,item_id,state,body) VALUES(?,?,?,'sent',?)`, d.ID, d.RunID, d.ItemID, jsonText(d)); e != nil {
			t.Fatal(e)
		}
	}
	history, err := s.MessageHistory(ctx, route.Principal, route.ConnectionID, later.EventID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range history {
		if message.Kind == "human" && strings.Contains(message.Text, input.Text) && strings.Contains(message.Text, "已发送") {
			found = true
		}
	}
	if !found {
		t.Fatal("owner reply absent from the original customer's context")
	}

	newer.Text = "Another owner message"
	r2, e := s.PrepareOwnerMessage(ctx, o, newer)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ChangeReplyMode(ctx, o, route.ID, 2, true)
	if e != nil {
		t.Fatal(e)
	}
	expired, e = s.ExpireOwnerDraft(ctx, r2.ID)
	if e != nil || !expired {
		t.Fatal("resumed AI did not expire stale owner draft", e)
	}
}
func TestMySQLReadProfileOffImmediatelyHidesOldIncomingContext(t *testing.T) {
	s, o, route, _ := replyFixture(t)
	ctx := t.Context()
	resource := Resource{ID: route.Principal.ProjectID + "profile", Name: "profile", OwnerProject: route.Principal.ProjectID, Sections: []Section{{ID: "profile", Fields: []string{"text"}}}}
	if e := s.PutResource(ctx, o, resource); e != nil {
		t.Fatal(e)
	}
	ref := Ref{resource.ID, "v1", "profile"}
	if e := s.BeginSnapshot(ctx, o, Snapshot{ResourceID: ref.ResourceID, Version: ref.Version}); e != nil {
		t.Fatal(e)
	}
	if e := s.Stage(ctx, o, ref.ResourceID, ref.Version, []Record{{ID: "customer", Fields: map[string]any{"text": "synthetic private profile"}}}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Publish(ctx, o, ref.ResourceID, ref.Version, 1); e != nil {
		t.Fatal(e)
	}
	route.Context = []RecordRef{{Ref: ref, ID: "customer"}}
	if _, e := s.DB.ExecContext(ctx, `UPDATE wb_message_routes SET body=? WHERE id=?`, jsonText(route), route.ID); e != nil {
		t.Fatal(e)
	}
	in := IncomingMessage{ConnectionID: route.ConnectionID, ActorID: route.ActorID, ConversationID: route.ConversationID, EventID: "profile-message", Text: "question"}
	if _, e := s.AcceptMessage(ctx, in); e != nil {
		t.Fatal(e)
	}
	if records, e := s.MessageContext(ctx, route.Principal, in.ConnectionID, in.EventID); e != nil || len(records) != 1 {
		t.Fatal("profile fixture missing", e)
	}
	cfg, e := s.ReplySettings(ctx, o, route.Principal.ProjectID)
	if e != nil {
		t.Fatal(e)
	}
	off := false
	cfg.ReadProfile = &off
	if _, e = s.PutReplySettings(ctx, o, cfg); e != nil {
		t.Fatal(e)
	}
	if records, e := s.MessageContext(ctx, route.Principal, in.ConnectionID, in.EventID); e != nil || len(records) != 0 {
		t.Fatal("old profile visible after off", e)
	}
	in.EventID = "after-off"
	m, e := s.AcceptMessage(ctx, in)
	if e != nil || len(m.Context) != 0 {
		t.Fatal("new profile read after off", e)
	}
}
