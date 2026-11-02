package workbench

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestGatewayRecreatedBindingCannotReuseForeignSession(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "changed ownership after upgrade"
		if legacy {
			name = "existing retired ownership at upgrade"
		}
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			c, a := gatewaySetup(t, s, newGatewayProvider(t))
			receiveGateway(t, s, c, a.AccountID, "contact", "message.received", "first", "first owner's message", time.Now())
			rows, err := s.GatewaySessions()
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			old := rows[0]
			key := digest([]byte(encode([]any{c.ID, c.TenantID, a.AccountID, "contact", old.BindingVersion})))
			b := a
			b.ProjectID = testProject(t, s, "second owner").ID
			if _, err = s.SaveGatewayBinding(b); err != nil {
				t.Fatal(err)
			}
			if err = s.DeleteProject(b.ProjectID); err != nil {
				t.Fatal(err)
			}
			if legacy {
				// Previous builds retained the old hash when the current owner was deleted.
				if _, err = s.DB.Exec("UPDATE gateway_sessions SET external_key=? WHERE id=?", key, old.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = s.DB.Exec(gatewaySchema); err != nil {
					t.Fatal(err)
				}
			}
			fresh := GatewayBinding{ProjectID: testProject(t, s, "third owner").ID, ConnectionID: c.ID, AccountID: a.AccountID, NotifyChatID: "third-ops", Enabled: true}
			fresh, err = s.SaveGatewayBinding(fresh)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.ID == a.ID || fresh.Version != old.BindingVersion {
				t.Fatal("expected same epoch number under new binding", fresh)
			}
			// This is the exact unscoped lookup used by the rollback binary.
			var count int
			if err = s.DB.QueryRow("SELECT count(*) FROM gateway_sessions WHERE external_key=?", key).Scan(&count); err != nil || count != 0 {
				t.Fatal("legacy hash still points at old project", count, err)
			}
			receiveGateway(t, s, c, a.AccountID, "contact", "message.received", "third", "private third-owner input", time.Now())
			r := queueGatewayTest(t, s, "third")
			if r.ProjectID != fresh.ProjectID || r.SessionID == old.ID {
				t.Fatal("message crossed ownership epochs")
			}
			var active string
			if err = s.DB.QueryRow("SELECT id FROM gateway_sessions WHERE external_key=?", key).Scan(&active); err != nil || active != r.SessionID {
				t.Fatal("legacy lookup cannot find active owner", active, err)
			}
			ss, err := s.Session(old.ID)
			if err != nil || ss.ProjectID != a.ProjectID {
				t.Fatal("old history lost its owner", ss, err)
			}
		})
	}
}

func TestGatewayEditRetainsManualModeAndCRMPair(t *testing.T) {
	s, c, b, grant := crmRig(t)
	crmInbound(t, s, c, "customer", "external")
	q := crmRequest{V: 1, Op: "status", StaffUserID: grant.StaffUserID, ExternalUserID: "external", Actor: "staff"}
	active := crmCall(t, s, c, grant, q, 200)
	if err := s.handoffGatewayVersion(active.SessionID, nil, "test handoff", "summary"); err != nil {
		t.Fatal(err)
	}
	human := crmCall(t, s, c, grant, q, 200)
	old := b
	b.NotifyChatID = "new-ops"
	b, err := s.SaveGatewayBinding(b)
	if err != nil || b.Version != old.Version || b.Revision <= old.Revision {
		t.Fatal("edit changed ownership or did not advance CAS revision", b, err)
	}
	if state := crmCall(t, s, c, grant, q, 200); state != human || state.Mode != "human" {
		t.Fatal(state)
	}
	receiveGateway(t, s, c, b.AccountID, "customer", "message.received", "paused", "more context", time.Now())
	m, err := getJSON[Message](s.DB, "SELECT data FROM inbox WHERE event_id='paused'")
	if err != nil || m.State != "handled" || m.SessionID != human.SessionID {
		t.Fatal(m, err)
	}
	alert, err := getJSON[Delivery](s.DB, "SELECT data FROM deliveries WHERE json_extract(data,'$.kind')='gateway.handoff'")
	if err != nil || alert.State != "pending" || alert.Gateway.NotifyChatID != "new-ops" {
		t.Fatal(alert, err)
	}
	// A retry with an uncertain receipt must neither target the previous
	// group nor submit the original idempotency key to the new group.
	alert.Gateway.Attempts = 1
	if _, err = s.DB.Exec("UPDATE deliveries SET data=? WHERE id=?", encode(alert), alert.ID); err != nil {
		t.Fatal(err)
	}
	b.NotifyChatID = "another-ops"
	if _, err = s.SaveGatewayBinding(b); err != nil {
		t.Fatal(err)
	}
	uncertain, err := getJSON[Delivery](s.DB, "SELECT data FROM deliveries WHERE id=?", alert.ID)
	if err != nil || uncertain.State != "unknown" || uncertain.Gateway.NotifyChatID != "new-ops" {
		t.Fatal(uncertain, err)
	}
	old.TimeoutSeconds--
	if _, err = s.SaveGatewayBinding(old); !errors.Is(err, ErrConflict) {
		t.Fatal("stale config update was accepted", err)
	}
}

func TestGatewayScopeReductionRejectsOldOperations(t *testing.T) {
	s, c, b, grant := crmRig(t)
	crmInbound(t, s, c, "first", "external-a")
	crmInbound(t, s, c, "second", "external-b")
	req := crmRequest{V: 1, Op: "status", Actor: "op", StaffUserID: grant.StaffUserID, ExternalUserID: "external-a"}
	first := crmCall(t, s, c, grant, req, 200)
	if err := s.SetGatewayMode(first.SessionID, "human", "operator"); err != nil {
		t.Fatal(err)
	}
	first = crmCall(t, s, c, grant, req, 200)
	resume := req
	resume.Op = "resume"
	resume.Version = first.Version
	resume.BindingVersion = first.BindingVersion
	resume.SessionID = first.SessionID
	resume.RequestID = newID()
	crmCall(t, s, c, grant, resume, 200)
	receiveGateway(t, s, c, b.AccountID, "first", "message.received", "old-scope", "before narrowing", time.Now())
	oldRun := queueGatewayTest(t, s, "old-scope")
	b.ContactIDs = []string{"second"}
	var err error
	b, err = s.SaveGatewayBinding(b)
	if err != nil {
		t.Fatal(err)
	}
	crmCall(t, s, c, grant, req, 404)
	crmCall(t, s, c, grant, resume, 404)
	if err = s.SetGatewayMode(first.SessionID, "auto", "owner action"); err == nil {
		t.Fatal("owner resumed a removed contact")
	}
	completeGatewayTest(t, s, oldRun, `{"action":"reply","text":"revoked"}`)
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM deliveries WHERE json_extract(data,'$.kind')='gateway.answer'").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	req.ExternalUserID = "external-b"
	if state := crmCall(t, s, c, grant, req, 200); state.BindingVersion != grant.BindingVersion || state.Mode != "auto" {
		t.Fatal(state)
	}
}

func TestGatewayDisabledBindingRecordsMessageTombstone(t *testing.T) {
	s := testStore(t)
	c, b := gatewaySetup(t, s, newGatewayProvider(t))
	b.Enabled = false
	var err error
	b, err = s.SaveGatewayBinding(b)
	if err != nil {
		t.Fatal(err)
	}
	event := receiveGateway(t, s, c, b.AccountID, "customer", "message.received", "off", "do not retain body", time.Now())
	b.Enabled = true
	if _, err = s.SaveGatewayBinding(b); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if i == 1 {
			event.EventID = newID()
		}
		raw, _ := json.Marshal(event)
		if err = s.ReceiveGateway(c.ID, event, raw); err != nil {
			t.Fatal(err)
		}
	}
	if rows, err := s.GatewaySessions(); err != nil || len(rows) != 0 {
		t.Fatal("replayed ignored message created a session", rows, err)
	}
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM inbox WHERE source=? AND event_id='off' AND session_id='' AND state='handled' AND data NOT LIKE '%do not retain%'", gatewaySource).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	receiveGateway(t, s, c, b.AccountID, "customer", "message.received", "on", "new input", time.Now())
	if run := queueGatewayTest(t, s, "on"); run.ProjectID != b.ProjectID {
		t.Fatal(run)
	}
}
