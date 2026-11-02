package workbench

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func TestGatewayTimeoutAndHandoffCommitTogether(t *testing.T) {
	for _, outcome := range []string{"callback-wins", "handoff-insert-fails", "legacy-terminal-recovery"} {
		t.Run(outcome, func(t *testing.T) {
			s := testStore(t)
			p := newGatewayProvider(t)
			p.status = "queued"
			c, b := gatewaySetup(t, s, p)
			receiveGateway(t, s, c, b.AccountID, "client", "message.received", "one", "question", time.Now())
			r := queueGatewayTest(t, s, "one")
			completeGatewayTest(t, s, r, `{"action":"reply","text":"answer"}`)
			NewWorker(s, nil, nil).gatewayDeliverOne(t.Context())
			d := gatewayOutbox(t, s)[0]
			if d.State != "accepted" {
				t.Fatal(d.State)
			}
			d.State = "unknown"
			switch outcome {
			case "callback-wins":
				raw, _ := json.Marshal(map[string]string{"messageId": d.Receipt, "status": "sent"})
				if err := s.Transaction(t.Context(), func(tx *sql.Tx) error { return s.gatewayStatusTx(tx, c.ID, raw) }); err != nil {
					t.Fatal(err)
				}
				if err := s.saveGatewayDelivery(d); err != nil {
					t.Fatal(err)
				}
				rows := gatewayOutbox(t, s)
				g, err := s.gatewaySession(r.SessionID)
				if err != nil || len(rows) != 1 || rows[0].State != "sent" || g.Mode != "auto" {
					t.Fatal(rows, g.Mode, err)
				}
				return
			case "handoff-insert-fails":
				_, err := s.DB.Exec(`CREATE TRIGGER inject_handoff_fault BEFORE INSERT ON deliveries WHEN json_extract(NEW.data,'$.kind')='gateway.handoff' BEGIN SELECT RAISE(ABORT,'injected'); END`)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.saveGatewayDelivery(d); err == nil {
					t.Fatal("fault did not abort")
				}
				rows := gatewayOutbox(t, s)
				g, err := s.gatewaySession(r.SessionID)
				if err != nil || len(rows) != 1 || rows[0].State != "accepted" || g.Mode != "auto" {
					t.Fatal("partial timeout commit", rows, g.Mode, err)
				}
				if _, err = s.DB.Exec("DROP TRIGGER inject_handoff_fault"); err != nil {
					t.Fatal(err)
				}
				if err = s.saveGatewayDelivery(d); err != nil {
					t.Fatal(err)
				}
			case "legacy-terminal-recovery":
				if _, err := s.DB.Exec("UPDATE deliveries SET state='unknown',data=? WHERE id=?", encode(d), d.ID); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := s.recoverGatewayDeliveries(); err != nil {
					t.Fatal(err)
				}
			}
			rows := gatewayOutbox(t, s)
			g, err := s.gatewaySession(r.SessionID)
			if err != nil || len(rows) != 2 || rows[0].State != "unknown" || rows[1].Kind != "gateway.handoff" || g.Mode != "human" {
				t.Fatal(rows, g.Mode, err)
			}
		})
	}
}

func TestGatewayArchivedProjectDropsPendingReply(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, b := gatewaySetup(t, s, p)
	receiveGateway(t, s, c, b.AccountID, "client", "message.received", "one", "question", time.Now())
	r := queueGatewayTest(t, s, "one")
	completeGatewayTest(t, s, r, `{"action":"reply","text":"answer"}`)
	project, err := s.Project(b.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Archived = true
	if _, err = s.UpdateProject(project.ID, project); err != nil {
		t.Fatal(err)
	}
	NewWorker(s, nil, nil).gatewayDeliverOne(t.Context())
	rows := gatewayOutbox(t, s)
	if p.sends != 0 || len(rows) != 1 || rows[0].State != "cancelled" {
		t.Fatal(p.sends, rows)
	}
}

func TestGatewayHistoryToolSearchIsScoped(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, b := gatewaySetup(t, s, p)
	var mine Run
	for _, contact := range []string{"my-customer", "other-customer"} {
		receiveGateway(t, s, c, b.AccountID, contact, "message.received", contact, "shared question", time.Now())
		r := queueGatewayTest(t, s, contact)
		completeGatewayTest(t, s, r, `{"action":"reply","text":"unique answer"}`)
		if contact == "my-customer" {
			mine = r
		}
	}
	ss, err := s.Session(mine.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"shared question", "unique answer"} {
		raw, _ := json.Marshal(map[string]string{"query": term})
		out, err := s.appTool(RuntimeInput{Run: mine, Session: ss}, "search_history", raw)
		if err != nil {
			t.Fatal(err)
		}
		hits := out.([]Run)
		if len(hits) != 1 || hits[0].ID != mine.ID {
			t.Fatal(hits)
		}
	}
}
