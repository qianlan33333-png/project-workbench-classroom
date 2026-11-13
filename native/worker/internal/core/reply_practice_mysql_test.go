package core

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMySQLReplyPracticeKeepsReplyInItsSession(t *testing.T) {
	s, ctx, project := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: project + "-owner"}
	personal := Principal{Kind: "personal", SessionID: project + "-personal"}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: project, Name: "classroom"}))
	if _, err := s.EnableReplyPractice(ctx, owner, project); !errors.Is(err, ErrDenied) {
		t.Fatalf("ordinary project entered practice: %v", err)
	}
	_, err := s.InitReplyProject(ctx, owner, project)
	must(err)
	cfg, err := s.EnableReplyPractice(ctx, owner, project)
	must(err)
	binding, err := s.Transport(ctx, owner, cfg.ConnectionID)
	must(err)
	if binding.Endpoint != "" || binding.TokenEnv != "" || cfg.Notify || cfg.ReadProfile == nil || *cfg.ReadProfile {
		t.Fatalf("practice can reach a channel or customer profile: %+v %+v", binding, cfg)
	}
	input := ReplyPracticeInput{ProjectID: project, ExerciseID: uuid.NewString(), MessageID: uuid.NewString(), Text: "first question"}
	if _, err := s.AcceptReplyPractice(ctx, personal, input); !errors.Is(err, ErrDenied) {
		t.Fatalf("non-owner simulated inbound: %v", err)
	}
	m, err := s.AcceptReplyPractice(ctx, owner, input)
	must(err)
	retry, err := s.AcceptReplyPractice(ctx, owner, input)
	must(err)
	if retry.ID != m.ID || retry.Principal != m.Principal {
		t.Fatalf("retry moved the message: %+v", retry)
	}
	const answer = "practice only\nsecond line"
	reply, err := s.PrepareReplyWithLimits(ctx, m.Principal, m.ConnectionID, m.EventID, answer, 4, 4)
	must(err)
	replay, err := s.PrepareReply(ctx, m.Principal, m.ConnectionID, m.EventID, answer)
	must(err)
	if reply.ID != replay.ID {
		t.Fatal("retry created a second reply")
	}
	d, claimed, err := s.ClaimDelivery(ctx, reply.ID)
	must(err)
	if claimed || d.State != PracticeReplyState || d.Text != answer || d.Receipt != "" {
		t.Fatalf("invalid local completion: claimed=%v, delivery=%+v", claimed, d)
	}
	if _, err := s.Run(ctx, personal, reply.ID); !errors.Is(err, ErrDenied) {
		t.Fatalf("practice reply visible in SuperAgent: %v", err)
	}
	timeline, err := s.ChannelTimeline(ctx, owner, m.Principal.SessionID, "")
	must(err)
	if len(timeline.Items) != 2 || timeline.Items[1].Text != answer || timeline.Items[1].State != PracticeReplyState {
		t.Fatalf("unexpected exercise timeline: %+v", timeline)
	}
	if _, err := s.PrepareOwnerMessage(ctx, owner, OwnerMessage{SessionID: m.Principal.SessionID, Key: uuid.NewString(), Text: "external", Revision: 1}); !errors.Is(err, ErrDenied) {
		t.Fatalf("practice became an outbound route: %v", err)
	}
	input.MessageID = uuid.NewString()
	input.Text = "follow-up"
	next, err := s.AcceptReplyPractice(ctx, owner, input)
	must(err)
	if ok, err := s.CanAdmitIncoming(ctx, next); !ok || err != nil || next.Principal != m.Principal {
		t.Fatalf("local reply did not release original session: %v %v", ok, err)
	}
	must(s.FinishUnansweredIncoming(ctx, next, "MISSING_CREDENTIAL"))
	failed, err := s.Message(ctx, owner, next.ConnectionID, next.EventID)
	must(err)
	if failed.State != "practice_failed" || failed.Failure != "MISSING_CREDENTIAL" {
		t.Fatalf("technical failure lost its reason: %+v", failed)
	}
	input.MessageID = uuid.NewString()
	next, err = s.AcceptReplyPractice(ctx, owner, input)
	must(err)
	if ok, err := s.CanAdmitIncoming(ctx, next); !ok || err != nil || next.Principal != m.Principal {
		t.Fatalf("failed turn did not release original session: %v %v", ok, err)
	}
	must(s.MessageDisposition(ctx, next.Principal, next.ConnectionID, next.EventID, "handoff"))
	var controls int
	must(s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM wb_events WHERE session_id=? AND body->>'$.kind'='conversation.mode.changed'", next.Principal.SessionID).Scan(&controls))
	if controls != 0 {
		t.Fatal("handoff created a channel control event")
	}
	now := time.Now()
	if _, err := s.ReadReplyActivity(ctx, personal, project, now.Add(-time.Hour), now.Add(time.Hour)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("practice was counted as real activity: %v", err)
	}
	input.ExerciseID, input.MessageID = uuid.NewString(), uuid.NewString()
	fresh, err := s.AcceptReplyPractice(ctx, owner, input)
	must(err)
	if fresh.Principal == m.Principal || fresh.State != "received" {
		t.Fatalf("new exercise inherited old history or handoff: %+v", fresh)
	}
}
