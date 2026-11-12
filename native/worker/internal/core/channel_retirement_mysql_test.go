package core

import (
	"errors"
	"testing"
)

func TestMySQLPersonalChannelSwitchPreservesDeliveryReceipts(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "-owner"}
	personal := Principal{Kind: "personal", SessionID: id + "-personal"}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	oldID, nextID, otherID := id+"-old", id+"-next", id+"-other"
	makeChannel := func(connection, kind string) NotificationTarget {
		must(s.PutTransport(ctx, owner, Transport{
			ID: connection, Kind: kind, Name: connection, Enabled: true,
			Endpoint: "http://127.0.0.1:7383/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST",
		}))
		must(s.PutRoute(ctx, owner, MessageRoute{ID: connection, ConnectionID: connection,
			ActorID: connection, ConversationID: connection, Principal: personal, Enabled: true}))
		return NotificationTarget{ConnectionID: connection, Target: map[string]string{"actorId": connection, "conversationId": connection}}
	}
	former := makeChannel(oldID, "feishu")
	other := makeChannel(otherID, "wechat")
	must(s.PutNotificationSettings(ctx, owner, NotificationSettings{
		Enabled: true, SessionID: personal.SessionID, Targets: []NotificationTarget{former, other},
	}))
	before, err := s.NotificationSettings(ctx)
	must(err)
	defer s.DB.ExecContext(ctx, "DELETE FROM wb_settings WHERE id='notifications' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.sessionId'))=?", personal.SessionID)

	run := Run{ID: id + "-send", Principal: personal, Operation: "notification", Title: "pinned notification"}
	must(s.SaveRun(ctx, run))
	must(s.PutDelivery(ctx, run, Delivery{ID: run.ID, RunID: run.ID, ItemID: "send",
		ConnectionID: oldID, Target: former.Target, Text: "frozen output"}))
	_, claimed, err := s.ClaimDelivery(ctx, run.ID)
	must(err)
	if !claimed {
		t.Fatal("fixture delivery wasn't claimed")
	}
	_, err = s.FinishDelivery(ctx, run.ID, "unknown", "original-platform-receipt", "")
	must(err)

	if err := s.RetirePersonalTransport(ctx, owner, oldID, otherID); !errors.Is(err, ErrDenied) {
		t.Fatalf("cross-provider switch accepted: %v", err)
	}
	makeChannel(nextID, "feishu")
	must(s.RetirePersonalTransport(ctx, owner, oldID, nextID))
	must(s.RetirePersonalTransport(ctx, owner, oldID, nextID))
	after, err := s.NotificationSettings(ctx)
	must(err)
	if !after.Enabled || after.After != before.After || len(after.Targets) != 1 || after.Targets[0].ConnectionID != otherID {
		t.Fatalf("switch changed unrelated notifications or retargeted without a user choice: %+v", after)
	}
	if _, err := s.Transport(ctx, personal, oldID); !errors.Is(err, ErrDenied) {
		t.Fatalf("retired channel remains enabled: %v", err)
	}
	pinned, err := s.DeliveryTransport(ctx, run.ID)
	must(err)
	if pinned.ID != oldID {
		t.Fatalf("receipt was retargeted: %+v", pinned)
	}
}
