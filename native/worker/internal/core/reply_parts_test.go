package core

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestReplyTextParts(t *testing.T) {
	first := strings.Repeat("甲", 35) + "。"
	second := strings.Repeat("乙", 40)
	for _, test := range []struct {
		name  string
		text  string
		limit int
		want  []string
	}{
		{"legacy reply", first + second, 0, nil},
		{"exact limit", second, 40, nil},
		{"explicit breaks", first + "\r\n\n" + second, 40, []string{first, second}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := splitReplyText(test.text, 0, test.limit, false)
			if err != nil || !slices.Equal(got, test.want) {
				t.Fatalf("parts = %q, error = %v", got, err)
			}
		})
	}
	if _, err := splitReplyText(strings.Repeat("甲\n", 9), 0, 40, false); !errors.Is(err, ErrInvalid) {
		t.Fatal("an oversized multipart reply must fail before any part is persisted", err)
	}
}

func TestProjectReplySplitsWithoutToolLimits(t *testing.T) {
	text := "第一段。\r\n\n 第二段。\n"
	parts, err := splitReplyText(text, 0, 0, true)
	if err != nil || !slices.Equal(parts, []string{"第一段。", "第二段。"}) {
		t.Fatalf("Project reply parts = %q, error = %v", parts, err)
	}
	parts, err = splitReplyText(text, 0, 0, false)
	if err != nil || len(parts) != 0 {
		t.Fatalf("unbounded personal reply must stay intact: %q, %v", parts, err)
	}
}

func TestReplySingleMessageAndPartLimitsAreIndependent(t *testing.T) {
	for _, count := range []int{40, 41, 50} {
		text := strings.Repeat("甲", count)
		parts, err := splitReplyText(text, 50, 40, false)
		if err != nil || len(parts) != 0 {
			t.Fatalf("%d characters should stay in one message: %q, %v", count, parts, err)
		}
	}
	first, second := strings.Repeat("甲", 40), strings.Repeat("乙", 11)
	parts, err := splitReplyText(first+"\n"+second, 50, 40, false)
	if err != nil || !slices.Equal(parts, []string{first, second}) {
		t.Fatalf("51 characters should split at 40: %q, %v", parts, err)
	}
	parts, err = splitReplyText(first+"\n乙。", 50, 40, false)
	if err != nil || !slices.Equal(parts, []string{first, "乙。"}) {
		t.Fatalf("a short multiline draft must preserve its two bubbles: %q, %v", parts, err)
	}
	for _, draft := range []string{
		first + second,
		"小程序入口：https://example.com/miniprogram/customer-service-activation",
		"服务单号：CRM-2026-11-11-1234567890123456789012345678901234567890",
	} {
		if _, err = splitReplyText(draft, 50, 40, false); !errors.Is(err, ErrInvalid) {
			t.Fatalf("draft was silently truncated: %q, %v", draft, err)
		}
	}
	for _, limits := range [][2]int{{50, 0}, {39, 40}, {-1, 40}} {
		if _, err = splitReplyText(first, limits[0], limits[1], false); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid limits %v accepted: %v", limits, err)
		}
	}
}

func TestMySQLReplyPartsFollowReceiptAndStopOnUnknownOrHandoff(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.PutProject(ctx, owner, Project{ID: id, Name: "split replies"}))
	transport := Transport{ID: id, Name: "fixture", Endpoint: "http://127.0.0.1:9999/mcp", TokenEnv: "WORKBENCH_CONNECTOR_TEST", Projects: []string{id}, Enabled: true}
	must(s.PutTransport(ctx, owner, transport))
	_, err := s.PutReplySettings(ctx, owner, ReplySettings{ProjectID: id, ConnectionID: id, ExpertSkill: "customer-reply", Enabled: true})
	must(err)

	for _, outcome := range []string{"sent", "unknown", "handoff", "failed", "timeout"} {
		in := IncomingMessage{ConnectionID: id, ConversationID: outcome, ActorID: outcome, EventID: outcome, Text: "hello"}
		route, err := s.EnsureReplyRoute(ctx, in)
		must(err)
		_, err = s.AcceptMessage(ctx, in)
		must(err)
		text := strings.Repeat("甲", 40) + "\n乙"
		run, err := s.PrepareReplyWithLimits(ctx, route.Principal, id, in.EventID, text, 0, 40)
		must(err)
		secondID := ReplyPartID(run.ID, 2)
		if _, err = s.Delivery(ctx, secondID); !IsMissing(err) {
			t.Fatal("second part was eligible before the first receipt", err)
		}
		_, claimed, err := s.PrepareReplyDelivery(ctx, run.ID, transport, true)
		must(err)
		if !claimed {
			t.Fatal("first part did not claim")
		}
		retry, err := s.PrepareReplyWithLimits(ctx, route.Principal, id, in.EventID, text, 0, 40)
		must(err)
		if retry.ID != run.ID {
			t.Fatal("retry changed the run")
		}
		if _, err := s.PrepareReply(ctx, route.Principal, id, in.EventID, text); !errors.Is(err, ErrConflict) {
			t.Fatal("retry changed the segmentation", err)
		}
		if outcome == "unknown" {
			_, err = s.FinishDelivery(ctx, run.ID, "unknown", "", "")
			must(err)
			_, err = s.FinishDelivery(ctx, run.ID, "sent", "late-1", "")
			must(err)
			if _, err = s.Delivery(ctx, secondID); !IsMissing(err) {
				t.Fatal("unknown outcome restarted delivery", err)
			}
			continue
		}
		_, err = s.FinishDelivery(ctx, run.ID, "accepted", "receipt-1", "")
		must(err)
		if _, err = s.Delivery(ctx, secondID); !IsMissing(err) {
			t.Fatal("accepted is only queue admission; the second part must wait", err)
		}
		if outcome == "failed" || outcome == "timeout" {
			state := "failed"
			if outcome == "timeout" {
				state = "unknown"
				_, err = s.DB.ExecContext(ctx, `UPDATE wb_deliveries SET body=JSON_SET(body,'$.receiptChecks',3) WHERE id=?`, run.ID)
				must(err)
			}
			_, err = s.FinishDelivery(ctx, run.ID, state, "receipt-1", "")
			must(err)
			message, err := s.Message(ctx, route.Principal, id, in.EventID)
			must(err)
			if message.State != "reply_"+state {
				t.Fatal("failed or timed-out part blocked the customer queue", message.State)
			}
			_, err = s.FinishDelivery(ctx, run.ID, "sent", "receipt-1", "")
			must(err)
			if _, err = s.Delivery(ctx, secondID); !IsMissing(err) {
				t.Fatal("failed or timed-out part restarted delivery", err)
			}
			continue
		}
		if outcome == "handoff" {
			paused, err := s.ChangeReplyMode(ctx, owner, route.ID, route.Revision, false)
			must(err)
			_, err = s.ChangeReplyMode(ctx, owner, route.ID, paused.Revision, true)
			must(err)
			newInput := in
			newInput.EventID += "-after-resume"
			newMessage, err := s.AcceptMessage(ctx, newInput)
			must(err)
			admitted, err := s.CanAdmitIncoming(ctx, newMessage)
			must(err)
			if !admitted {
				t.Fatal("a pending part from before handoff blocked a new message")
			}
		}
		_, err = s.FinishDelivery(ctx, run.ID, "sent", "receipt-1", "")
		must(err)
		if outcome == "handoff" {
			if _, err = s.Delivery(ctx, secondID); !IsMissing(err) {
				t.Fatal("late receipt resumed the reply after handoff", err)
			}
			continue
		}
		second, claimed, err := s.PrepareReplyDelivery(ctx, secondID, transport, true)
		must(err)
		if !claimed || second.Text != "乙" || second.RunID != run.ID || second.Target["replyTo"] != in.EventID {
			t.Fatal("second part lost its immutable message binding", second)
		}
		_, err = s.FinishDelivery(ctx, run.ID, "delivered", "receipt-1", "")
		must(err)
		message, err := s.Message(ctx, route.Principal, id, in.EventID)
		must(err)
		if message.State != "reply_submitting" {
			t.Fatal("late first-part receipt changed second-part state", message.State)
		}
		_, err = s.FinishDelivery(ctx, secondID, "sent", "receipt-2", "")
		must(err)
		message, err = s.Message(ctx, route.Principal, id, in.EventID)
		must(err)
		if message.State != "reply_sent" {
			t.Fatal("last part did not finish the reply", message.State)
		}
	}
}
