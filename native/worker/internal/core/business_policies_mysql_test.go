package core

import (
	"errors"
	"testing"
	"time"
)

func TestMySQLBusinessPolicyLookupAndExpiryHandoff(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "business policies"}))
	transport := Transport{ID: id, Name: "test", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}
	must(s.PutTransport(ctx, owner, transport))
	_, err := s.PutReplySettings(ctx, owner, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "customer-reply", Enabled: true})
	must(err)
	resource := Resource{ID: id + "facts", Name: "Business policies", OwnerProject: id, Relation: BusinessPolicyRelation,
		Sections: []Section{{ID: "current", Fields: []string{"name", "answer"}}}}
	must(s.PutResource(ctx, owner, resource))
	now := time.Now().UTC()
	past, future, deadline := now.Add(-time.Hour), now.Add(time.Hour), now.Add(2*time.Second)
	for _, policy := range []BusinessPolicy{
		{Topic: "replay", Name: "Current show", Answer: "current answer", Aliases: "today", Enabled: true, ValidUntil: &deadline},
		{Topic: "replay", Name: "Permanent answer", Answer: "permanent answer", Enabled: true},
		{Topic: "replay", Name: "Expired show", Answer: "expired", Enabled: true, ValidUntil: &past},
		{Topic: "replay", Name: "Future show", Answer: "future", Enabled: true, ValidFrom: &future},
		{Topic: "replay", Name: "Disabled show", Answer: "disabled"},
	} {
		_, err = s.PutBusinessPolicy(ctx, owner, resource.ID, policy)
		must(err)
	}
	in := IncomingMessage{ConnectionID: id, ConversationID: "one", ActorID: "one", EventID: "first", Text: "Is there a replay?"}
	route, err := s.EnsureReplyRoute(ctx, in)
	must(err)
	_, err = s.AcceptMessage(ctx, in)
	must(err)
	items, err := s.LookupBusinessFacts(ctx, route.Principal, id, in.EventID, resource.ID, "replay", "")
	must(err)
	if len(items.Items) != 2 || items.Items[0].Name != "Current show" || items.Items[1].Name != "Permanent answer" {
		t.Fatalf("wrong active rows: %+v", items)
	}
	items, err = s.LookupBusinessFacts(ctx, route.Principal, id, in.EventID, resource.ID, "replay", "Current　show")
	must(err)
	if len(items.Items) != 1 || items.Items[0].Answer != "current answer" {
		t.Fatalf("alias lookup: %+v", items)
	}
	other := route.Principal
	other.SessionID = id + "other"
	_, err = s.LookupBusinessFacts(ctx, other, id, in.EventID, resource.ID, "replay", "")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("unbound customer lookup: %v", err)
	}
	if _, err = s.ListBusinessPolicies(ctx, route.Principal, resource.ID); !errors.Is(err, ErrDenied) {
		t.Fatalf("customer can list unfiltered policies: %v", err)
	}
	run, err := s.PrepareReply(ctx, route.Principal, id, in.EventID, "current answer")
	must(err)
	msg, err := s.Message(ctx, route.Principal, id, in.EventID)
	must(err)
	if msg.BusinessValidUntil == nil || !msg.BusinessValidUntil.Equal(deadline.Truncate(time.Microsecond)) || msg.State != "reply_queued" {
		t.Fatalf("reply failed to carry its deadline: %+v", msg)
	}
	time.Sleep(time.Until(deadline) + 10*time.Millisecond)
	delivery, claimed, err := s.PrepareReplyDelivery(ctx, run.ID, transport, true)
	must(err)
	msg, err = s.Message(ctx, route.Principal, id, in.EventID)
	must(err)
	currentRoute, err := s.Route(ctx, route.ID)
	must(err)
	if claimed || delivery.State != "failed" || msg.State != "handoff" || currentRoute.AutoReply {
		t.Fatalf("expired draft was not handed off: claimed=%v delivery=%+v message=%s auto=%v", claimed, delivery, msg.State, currentRoute.AutoReply)
	}
}
