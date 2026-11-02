package workbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gw "projectworkbench/pkg/wecomgateway"
)

func crmRig(t *testing.T) (*Store, GatewayConnection, GatewayBinding, crmGrant) {
	t.Helper()
	s := testStore(t)
	c, b := gatewaySetup(t, s, newGatewayProvider(t))
	g := crmGrant{KeyID: "test", BindingID: b.ID, BindingVersion: b.Version, StaffUserID: "staff", CallbackOrigin: "https://crm.test", CallbackURL: "https://crm.test/receive", secret: "synthetic-only-fixture-hmac"}
	s.crmGrants = map[string]crmGrant{g.KeyID: g}
	return s, c, b, g
}
func crmHTTP(s *Store, c GatewayConnection, g crmGrant, raw []byte, modify func(*http.Request)) *httptest.ResponseRecorder {
	router := gin.New()
	api := API{Store: s}
	api.mountGateway(router.Group(""), router.Group("/owner"))
	req := httptest.NewRequest("POST", "/gateway/hooks/"+c.ID, bytes.NewReader(raw))
	tm := fmt.Sprint(time.Now().Unix())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AR-Key", g.KeyID)
	req.Header.Set("X-AR-Time", tm)
	req.Header.Set("X-AR-Sig", crmMAC(g, "crm-to-wb", tm, raw))
	if modify != nil {
		modify(req)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
func crmCall(t *testing.T, s *Store, c GatewayConnection, g crmGrant, q crmRequest, code int) crmState {
	t.Helper()
	r := crmHTTP(s, c, g, []byte(encode(q)), nil)
	if r.Code != code {
		t.Fatalf("CRM %s: got %d %s, want %d", q.Op, r.Code, r.Body.String(), code)
	}
	var st crmState
	if code == 200 {
		if e := json.Unmarshal(r.Body.Bytes(), &st); e != nil {
			t.Fatal(e)
		}
	}
	return st
}
func crmInbound(t *testing.T, s *Store, c GatewayConnection, contact, external string) {
	t.Helper()
	m := gw.Message{MessageID: newID(), AccountID: c.Accounts[0], ContactID: contact, ExternalUserID: external, Direction: "in", Type: "text", SentAt: time.Now()}
	m.Content.Text = "first"
	data, _ := json.Marshal(map[string]any{"message": m})
	ev := gw.Event{EventID: newID(), Type: "message.received", AccountID: m.AccountID, TenantID: c.TenantID, OccurredAt: m.SentAt, Data: data}
	if e := s.ReceiveGateway(c.ID, ev, []byte(encode(ev))); e != nil {
		t.Fatal(e)
	}
}
func TestCRMContractSignatureAndParser(t *testing.T) {
	var fixture struct{ KeyID, Timestamp, Secret, Direction, Body, Signature string }
	raw, e := os.ReadFile("../../docs/plans/auto-reply-contract-fixture.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	if got := crmMAC(crmGrant{KeyID: fixture.KeyID, secret: fixture.Secret}, fixture.Direction, fixture.Timestamp, []byte(fixture.Body)); got != fixture.Signature {
		t.Fatal("frozen signing vector mismatch")
	}
	s, c, _, g := crmRig(t)
	q := crmRequest{V: 1, Op: "status", StaffUserID: "staff", ExternalUserID: "external", Actor: "employee"}
	raw = []byte(encode(q))
	for _, m := range []func(*http.Request){func(r *http.Request) {
		r.Header.Set("X-AR-Sig", crmMAC(g, "wb-to-crm", r.Header.Get("X-AR-Time"), raw))
	}, func(r *http.Request) { r.Header.Set("X-AR-Time", "-9223372036854775808") }, func(r *http.Request) { r.Header.Set("X-Gateway-Event-Id", "") }, func(r *http.Request) { r.Header.Add("X-AR-Key", "test") }, func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(string(raw) + " ")) }} {
		if code := crmHTTP(s, c, g, raw, m).Code; code != 401 {
			t.Fatal("unauthenticated request accepted", code)
		}
	}
	for _, data := range []string{strings.Replace(string(raw), "\"op\":", "\"op\":\"resume\",\"op\":", 1), strings.Replace(string(raw), "\"actor\":", "\"Actor\":", 1), strings.Replace(string(raw), "\"actor\":", "\"requestId\":\"x\",\"actor\":", 1), strings.Repeat("x", 16<<10+1)} {
		if code := crmHTTP(s, c, g, []byte(data), nil).Code; code != 400 {
			t.Fatal("invalid request", code)
		}
	}
}
func TestCRMRelationshipAndResumeCAS(t *testing.T) {
	s, c, b, g := crmRig(t)
	q := crmRequest{V: 1, Op: "status", StaffUserID: "staff", ExternalUserID: "external", Actor: "operator"}
	crmCall(t, s, c, g, q, 404)
	crmInbound(t, s, c, "same-contact", "external")
	read := crmCall(t, s, c, g, q, 200)
	if read.Mode != "auto" {
		t.Fatal(read)
	}
	q.StaffUserID = "wrong"
	crmCall(t, s, c, g, q, 403)
	q.StaffUserID = "staff"
	other := c
	other.ID = "another-connection"
	crmCall(t, s, other, g, q, 404)
	if e := s.SetGatewayMode(read.SessionID, "human", "pause"); e != nil {
		t.Fatal(e)
	}
	paused := crmCall(t, s, c, g, q, 200)
	q.Op = "resume"
	q.Version = paused.Version
	q.BindingVersion = paused.BindingVersion
	q.SessionID = paused.SessionID
	q.RequestID = newID()
	wrong := q
	wrong.SessionID = "another"
	crmCall(t, s, c, g, wrong, 409)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() { codes <- crmHTTP(s, c, g, []byte(encode(q)), nil).Code })
	}
	wg.Wait()
	close(codes)
	for n := range codes {
		if n != 200 {
			t.Fatal("idempotent concurrent recovery", n)
		}
	}
	st := crmCall(t, s, c, g, q, 200)
	if st.Version != paused.Version+1 || st.Mode != "auto" {
		t.Fatal(st)
	}
	var n int
	var actor string
	if e := s.DB.QueryRow("SELECT count(*),actor FROM gateway_crm_receipts").Scan(&n, &actor); e != nil || n != 1 || actor != q.Actor {
		t.Fatal("missing durable audit", n, actor, e)
	}
	wrong = q
	wrong.Actor = "other"
	crmCall(t, s, c, g, wrong, 409)
	if e := s.SetGatewayMode(st.SessionID, "human", "another handoff"); e != nil {
		t.Fatal(e)
	}
	crmCall(t, s, c, g, q, 200)
	wrong = q
	wrong.RequestID = newID()
	crmCall(t, s, c, g, wrong, 409)
	gs, _ := s.gatewaySession(st.SessionID)
	if gs.Mode != "human" {
		t.Fatal("replay unpaused new handoff")
	}
	if e := s.DB.QueryRow("SELECT count(*) FROM runs").Scan(&n); e != nil || n != 0 {
		t.Fatal("resume or historical input created work", n, e)
	}
	crmInbound(t, s, c, "distinct", "external")
	q = crmRequest{V: 1, Op: "status", StaffUserID: "staff", ExternalUserID: "external", Actor: "op"}
	crmCall(t, s, c, g, q, 409)
	b.TimeoutSeconds--
	if _, e := s.SaveGatewayBinding(b); e != nil {
		t.Fatal(e)
	}
	crmCall(t, s, c, g, q, 404)
}
func TestCRMContextThroughPauseRestartAndAcceptance(t *testing.T) {
	s, c, _, g := crmRig(t)
	crmInbound(t, s, c, "one", "ext")
	q := crmRequest{V: 1, Op: "status", StaffUserID: "staff", ExternalUserID: "ext", Actor: "operator"}
	ss := crmCall(t, s, c, g, q, 200)
	resume := func() {
		st := crmCall(t, s, c, g, q, 200)
		req := q
		req.Op = "resume"
		req.RequestID = newID()
		req.Version = st.Version
		req.SessionID = st.SessionID
		req.BindingVersion = st.BindingVersion
		crmCall(t, s, c, g, req, 200)
	}
	if e := s.SetGatewayMode(ss.SessionID, "human", "manual"); e != nil {
		t.Fatal(e)
	}
	at := time.Now()
	receiveGateway(t, s, c, "account-A", "one", "message.received", "fact", "FACT-867", at)
	resume()
	if e := s.SetGatewayMode(ss.SessionID, "human", "manual"); e != nil {
		t.Fatal(e)
	}
	receiveGateway(t, s, c, "account-A", "one", "message.self_sent", "human", "", time.Now())
	resume()
	if e := s.DB.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := OpenStore(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.DB.Close()
	s = reopened
	s.crmGrants = map[string]crmGrant{g.KeyID: g}
	receiveGateway(t, s, c, "account-A", "one", "message.received", "late", "late-fact", at.Add(-time.Millisecond))
	receiveGateway(t, s, c, "account-A", "one", "message.self_sent", "late-human", "historical-reply", at.Add(-time.Millisecond))
	if got := crmCall(t, s, c, g, q, 200); got.Mode != "auto" {
		t.Fatal("delayed human webhook repaused session")
	}
	var count int
	if e = s.DB.QueryRow("SELECT count(*) FROM inbox WHERE state='gateway_pending'").Scan(&count); e != nil || count != 0 {
		t.Fatal("old questions requeued")
	}
	receiveGateway(t, s, c, "account-A", "one", "message.received", "new", "current question", time.Now())
	queueGatewayTest(t, s, "new")
	w := NewWorker(s, nil, nil)
	run, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	prompt := w.prompt(run, Project{})
	for _, fact := range []string{"FACT-867", "late-fact", "historical-reply", "人工已回复，正文不可用", "current question"} {
		if strings.Count(prompt, fact) != 1 {
			t.Fatalf("incorrect historical injection %s: %s", fact, prompt)
		}
	}
	if e = s.acceptGatewayHistory(t.Context(), run, "accepted-fixture"); e != nil {
		t.Fatal(e)
	}
	completeGatewayTest(t, s, run, `{"action":"ignore"}`)
	receiveGateway(t, s, c, "account-A", "one", "message.received", "second", "second", time.Now())
	queueGatewayTest(t, s, "second")
	run, e = w.claim()
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(w.prompt(run, Project{}), "FACT-867") {
		t.Fatal("accepted history delivered twice")
	}
}
func TestCRMProbeNotificationsAndRedirect(t *testing.T) {
	s, c, _, g := crmRig(t)
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("signing credentials followed redirect") }))
	defer other.Close()
	var received []byte
	crm := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, other.URL, 307)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-AR-Sig") != crmMAC(g, "wb-to-crm", r.Header.Get("X-AR-Time"), raw) {
			t.Error("bad outbound signature")
		}
		var q struct{ Op, Nonce string }
		_ = json.Unmarshal(raw, &q)
		if q.Op == "probe" {
			if r.URL.Path == "/bad" {
				q.Nonce = "incorrect"
			}
			json.NewEncoder(w).Encode(map[string]string{"nonce": q.Nonce})
			return
		}
		received = raw
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer crm.Close()
	g.CallbackOrigin = crm.URL
	g.CallbackURL = crm.URL + "/initial"
	s.crmGrants[g.KeyID] = g
	s.crmTransport = crm.Client().Transport
	req := crmRequest{V: 1, Op: "configure", CallbackURL: crm.URL + "/new", RequestID: newID()}
	crmCall(t, s, c, g, req, 200)
	crmCall(t, s, c, g, req, 200)
	for _, target := range []string{crm.URL + "/bad", crm.URL + "/redirect", other.URL + "/evil"} {
		r := req
		r.CallbackURL = target
		r.RequestID = newID()
		code := 503
		if strings.Contains(target, other.URL) {
			code = 403
		}
		crmCall(t, s, c, g, r, code)
	}
	var target string
	if e := s.DB.QueryRow("SELECT url FROM gateway_crm_routes").Scan(&target); e != nil || target != req.CallbackURL {
		t.Fatal("failed pairing replaced confirmed route", target, e)
	}
	crmInbound(t, s, c, "contact", "ext")
	ss, _ := s.GatewaySessions()
	if e := s.SetGatewayMode(ss[0].ID, "human", "SENSITIVE-reason"); e != nil {
		t.Fatal(e)
	}
	w := NewWorker(s, nil, nil)
	w.gatewayDeliverOne(t.Context())
	if len(received) != 0 {
		t.Fatal("CRM sent by customer worker")
	}
	w.crmDeliverOne(t.Context())
	if len(received) == 0 || bytes.Contains(received, []byte("SENSITIVE")) || bytes.Contains(received, []byte("contact")) {
		t.Fatal("invalid data minimization", string(received))
	}
	var nt crmNotice
	if e := json.Unmarshal(received, &nt); e != nil || nt.Op != "state.changed" || nt.Mode != "human" || nt.ExternalUserID != "ext" {
		t.Fatal(nt, e)
	}
	var dstate string
	if e := s.DB.QueryRow("SELECT state FROM deliveries WHERE id=?", nt.EventID).Scan(&dstate); e != nil || dstate != "sent" {
		t.Fatal("no notify receipt", e, dstate)
	}
}
func TestCRMLargeHistoryIsCompleteAndSessionScoped(t *testing.T) {
	s, c, _, _ := crmRig(t)
	crmInbound(t, s, c, "one", "ext")
	sessions, _ := s.GatewaySessions()
	id := sessions[0].ID
	if e := s.SetGatewayMode(id, "human", "pause"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		receiveGateway(t, s, c, "account-A", "one", "message.received", fmt.Sprint(i), strings.Repeat("A", 7999)+fmt.Sprint(i), time.Now())
	}
	receiveGateway(t, s, c, "account-A", "other", "message.self_sent", "other", "different-customer-only", time.Now())
	if e := s.SetGatewayMode(id, "auto", "resume"); e != nil {
		t.Fatal(e)
	}
	receiveGateway(t, s, c, "account-A", "one", "message.received", "current", "current", time.Now())
	queueGatewayTest(t, s, "current")
	w := NewWorker(s, nil, nil)
	r, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	if !r.Gateway.HistoryFile {
		t.Fatal("did not spill")
	}
	root := t.TempDir()
	if e = s.writeGatewayHistory(root, r); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(root, crmHistoryPath))
	if e != nil || len(raw) < 3*7999 || bytes.Contains(raw, []byte("different-customer")) {
		t.Fatal("incomplete or cross-session spill", len(raw), e)
	}
}
