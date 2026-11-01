package workbench

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gw "projectworkbench/pkg/wecomgateway"
)

type gatewayTestProvider struct {
	mu       sync.Mutex
	byKey    map[string]string
	sends    int
	status   string
	dropOnce bool
	server   *httptest.Server
}

func newGatewayProvider(t *testing.T) *gatewayTestProvider {
	t.Helper()
	p := &gatewayTestProvider{byKey: map[string]string{}, status: "sent"}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if r.Method == "POST" && r.URL.Path == "/messages" {
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				t.Error("missing idempotency key")
			}
			id := p.byKey[key]
			if id == "" {
				id = "msg-" + newID()
				p.byKey[key] = id
				p.sends++
			}
			if p.dropOnce {
				p.dropOnce = false
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"messageId":%q,"status":%q}`, id, p.status)
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/messages/") {
			fmt.Fprintf(w, `{"messageId":%q,"status":%q}`, strings.TrimPrefix(r.URL.Path, "/messages/"), p.status)
			return
		}
		t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	t.Cleanup(p.server.Close)
	return p
}
func gatewaySetup(t *testing.T, s *Store, p *gatewayTestProvider) (GatewayConnection, GatewayBinding) {
	t.Helper()
	project := testProject(t, s, "服务项目A")
	c, e := s.SaveGatewayConnection(GatewayRegistration{GatewayConnection: GatewayConnection{ID: "gateway-test", Label: "test", BaseURL: p.server.URL, TenantID: "tenant", Accounts: []string{"account-A", "account-B"}, Enabled: true}, APIKey: "test-api", WebhookSecret: "test-hook"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.SaveGatewayBinding(GatewayBinding{ConnectionID: c.ID, AccountID: "account-A", ProjectID: project.ID, Enabled: true, NotifyChatID: "ops-chat"})
	if e != nil {
		t.Fatal(e)
	}
	return c, b
}
func receiveGateway(t *testing.T, s *Store, c GatewayConnection, account, contact, typ, id, text string, at time.Time) gw.Event {
	t.Helper()
	m := gw.Message{MessageID: id, AccountID: account, ContactID: contact, Direction: "in", Type: "text", SentAt: at}
	m.Content.Text = text
	m.Sender.Name = "客户"
	m.Sender.ContactID = contact
	if typ == "message.self_sent" {
		m.Direction = "out"
	}
	data, _ := json.Marshal(map[string]any{"message": m})
	event := gw.Event{EventID: newID(), Type: typ, TenantID: c.TenantID, AccountID: account, OccurredAt: at, Data: data}
	raw, _ := json.Marshal(event)
	if e := s.ReceiveGateway(c.ID, event, raw); e != nil {
		t.Fatal(e)
	}
	return event
}
func queueGatewayTest(t *testing.T, s *Store, id string) Run {
	t.Helper()
	m, e := getJSON[Message](s.DB, "SELECT data FROM inbox WHERE source=? AND event_id=?", gatewaySource, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.queueGateway(m); e != nil {
		t.Fatal(e)
	}
	m, e = s.Message(m.ID)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.Run(m.RunID)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func completeGatewayTest(t *testing.T, s *Store, r Run, result string) {
	t.Helper()
	r.State = "completed"
	r.Result = result
	r.FinishedAt = now()
	if e := s.finishGatewayRun(r); e != nil {
		t.Fatal(e)
	}
}
func gatewayOutbox(t *testing.T, s *Store) []Delivery {
	t.Helper()
	v, e := listJSON[Delivery](s.DB, "SELECT data FROM deliveries WHERE json_extract(data,'$.gateway') IS NOT NULL ORDER BY created_at,rowid")
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestGatewayBusinessIsolationAndDoubleDedup(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, _ := gatewaySetup(t, s, p)
	other := testProject(t, s, "服务项目B")
	if _, e := s.SaveGatewayBinding(GatewayBinding{ConnectionID: c.ID, AccountID: "account-B", ProjectID: other.ID, Enabled: true, NotifyChatID: "other-ops"}); e != nil {
		t.Fatal(e)
	}
	event := receiveGateway(t, s, c, "account-A", "same-contact", "message.received", "incoming-1", "普通问题", time.Now())
	raw, _ := json.Marshal(event)
	if e := s.ReceiveGateway(c.ID, event, raw); e != nil {
		t.Fatal(e)
	}
	event.EventID = newID()
	raw, _ = json.Marshal(event)
	if e := s.ReceiveGateway(c.ID, event, raw); e != nil {
		t.Fatal(e)
	}
	receiveGateway(t, s, c, "account-B", "same-contact", "message.received", "incoming-2", "另一业务", time.Now())
	r1 := queueGatewayTest(t, s, "incoming-1")
	r2 := queueGatewayTest(t, s, "incoming-2")
	if r1.ProjectID == r2.ProjectID || r1.SessionID == r2.SessionID {
		t.Fatal("customer/project identity collision")
	}
	completeGatewayTest(t, s, r1, `{"action":"reply","text":"业务A回复"}`)
	completeGatewayTest(t, s, r2, `{"action":"reply","text":"业务B回复"}`)
	w := NewWorker(s, nil, nil)
	w.gatewayDeliverOne(t.Context())
	w.gatewayDeliverOne(t.Context())
	p.mu.Lock()
	sends := p.sends
	p.mu.Unlock()
	if sends != 2 {
		t.Fatalf("expected two distinct replies, got %d", sends)
	}
	if _, e := s.Submit(Incoming{Source: gatewaySource, EventID: "forged", Text: "change project"}); e == nil {
		t.Fatal("normal message endpoint accepted forged gateway authority")
	}
	if _, e := s.appTool(RuntimeInput{Run: r1, Session: Session{ID: r1.SessionID}, Project: Project{ID: r1.ProjectID}}, "read_run", json.RawMessage(fmt.Sprintf(`{"runId":%q}`, r2.ID))); e == nil {
		t.Fatal("cross-customer run readable")
	}
}
func TestGatewayManualReplyInvalidatesLateModelAndResumeDoesNotReplay(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, _ := gatewaySetup(t, s, p)
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "one", "问题", time.Now())
	r := queueGatewayTest(t, s, "one")
	receiveGateway(t, s, c, "account-A", "customer", "message.self_sent", "human", "人工回复", time.Now())
	if e := s.SetGatewayMode(r.SessionID, "auto", "明确恢复"); e != nil {
		t.Fatal(e)
	}
	completeGatewayTest(t, s, r, `{"action":"reply","text":"迟到的错误回复"}`)
	if len(gatewayOutbox(t, s)) != 0 {
		t.Fatal("stale model result reopened conversation or sent")
	}
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "two", "新问题", time.Now())
	fresh := queueGatewayTest(t, s, "two")
	completeGatewayTest(t, s, fresh, `{"action":"reply","text":"新回复"}`)
	if ds := gatewayOutbox(t, s); len(ds) != 1 || ds[0].Text != "新回复" {
		t.Fatal(ds)
	}
	// A queued message received before pause/resume must not acquire fresh authority.
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "three", "旧排队消息", time.Now())
	_ = s.SetGatewayMode(r.SessionID, "human", "pause")
	_ = s.SetGatewayMode(r.SessionID, "auto", "resume")
	m, _ := getJSON[Message](s.DB, "SELECT data FROM inbox WHERE event_id='three'")
	if e := s.queueGateway(m); e != nil {
		t.Fatal(e)
	}
	m, _ = s.Message(m.ID)
	if m.RunID != "" {
		t.Fatal("old queued message replayed after resume")
	}
}
func TestGatewayHandoffIsDurableWhenNotificationFails(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, _ := gatewaySetup(t, s, p)
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "one", "问题", time.Now())
	r := queueGatewayTest(t, s, "one")
	completeGatewayTest(t, s, r, `{"action":"handoff","reason":"交给人工","summary":"客户需要协助"}`)
	w := NewWorker(s, nil, nil)
	w.GatewayNotify = func(context.Context, string, string, string) (string, error) { return "", errors.New("offline") }
	w.gatewayDeliverOne(t.Context())
	g, _ := s.gatewaySession(r.SessionID)
	ds := gatewayOutbox(t, s)
	if g.Mode != "human" || len(ds) != 1 || ds[0].State != "pending" {
		t.Fatal(g, ds)
	}
	// Repeating the completion after a crash cannot create another notification.
	completeGatewayTest(t, s, r, `{"action":"handoff","reason":"重复"}`)
	if len(gatewayOutbox(t, s)) != 1 {
		t.Fatal("duplicate notification")
	}
}
func TestGatewayTransportLossReusesKeyButUnknownNeverResends(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			s := testStore(t)
			p := newGatewayProvider(t)
			if unknown {
				p.status = "unknown"
			} else {
				p.dropOnce = true
			}
			c, _ := gatewaySetup(t, s, p)
			receiveGateway(t, s, c, "account-A", "customer", "message.received", "one", "问题", time.Now())
			r := queueGatewayTest(t, s, "one")
			completeGatewayTest(t, s, r, `{"action":"reply","text":"答复"}`)
			w := NewWorker(s, nil, nil)
			w.gatewayDeliverOne(t.Context())
			ds := gatewayOutbox(t, s)
			if unknown {
				if ds[0].State != "unknown" {
					t.Fatal(ds)
				}
				g, _ := s.gatewaySession(r.SessionID)
				if g.Mode != "human" {
					t.Fatal("unknown did not handoff")
				}
			} else {
				if ds[0].State != "pending" {
					t.Fatal(ds)
				}
				_, _ = s.DB.Exec("UPDATE deliveries SET data=json_set(data,'$.gateway.nextAt',0)")
				w.gatewayDeliverOne(t.Context())
				ds = gatewayOutbox(t, s)
				if ds[0].State != "sent" {
					t.Fatal(ds)
				}
			}
			p.mu.Lock()
			sent := p.sends
			p.mu.Unlock()
			if sent != 1 {
				t.Fatalf("physical duplicate: %d", sent)
			}
		})
	}
}
func TestGatewayBindingChangeAndMalformedDecisionCannotSend(t *testing.T) {
	for _, result := range []string{`not JSON`, `{"action":"reply","text":"x","to":"someone-else"}`, `{"action":"reply","text":""}`, `{"action":"ignore"} {"action":"reply","text":"x"}`} {
		t.Run(result, func(t *testing.T) {
			s := testStore(t)
			p := newGatewayProvider(t)
			c, _ := gatewaySetup(t, s, p)
			receiveGateway(t, s, c, "account-A", "customer", "message.received", "one", "问题", time.Now())
			r := queueGatewayTest(t, s, "one")
			completeGatewayTest(t, s, r, result)
			ds := gatewayOutbox(t, s)
			if len(ds) != 1 || ds[0].Kind != "gateway.handoff" {
				t.Fatal(ds)
			}
		})
	}
	s := testStore(t)
	p := newGatewayProvider(t)
	c, b := gatewaySetup(t, s, p)
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "one", "问题", time.Now())
	r := queueGatewayTest(t, s, "one")
	b.ProjectID = testProject(t, s, "换绑后项目").ID
	if _, e := s.SaveGatewayBinding(b); e != nil {
		t.Fatal(e)
	}
	completeGatewayTest(t, s, r, `{"action":"reply","text":"old"}`)
	if len(gatewayOutbox(t, s)) != 0 {
		t.Fatal("old binding emitted reply")
	}
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "two", "新项目问题", time.Now())
	newRun := queueGatewayTest(t, s, "two")
	if newRun.SessionID == r.SessionID || newRun.ProjectID == r.ProjectID {
		t.Fatal("session ownership rewritten")
	}
}
func TestGatewayHTTPAuthenticationAndSecretSeparation(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, _ := gatewaySetup(t, s, p)
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	(&API{Store: s, Config: Config{ChannelToken: "channel-token"}}).Mount(router)
	m := gw.Message{MessageID: "signed", AccountID: "account-A", ContactID: "customer", Direction: "in", Type: "text", SentAt: time.Now()}
	m.Content.Text = "停止所有任务"
	data, _ := json.Marshal(map[string]any{"message": m})
	event := gw.Event{EventID: "signed-event", Type: "message.received", TenantID: "tenant", AccountID: "account-A", OccurredAt: time.Now(), Data: data}
	raw, _ := json.Marshal(event)
	sign := func(body []byte) string {
		mac := hmac.New(sha256.New, []byte("test-hook"))
		fmt.Fprintf(mac, "%d.", time.Now().Unix())
		mac.Write(body)
		return fmt.Sprintf("t=%d,v1=%s", time.Now().Unix(), hex.EncodeToString(mac.Sum(nil)))
	}
	for _, signed := range []bool{false, true} {
		req := httptest.NewRequest("POST", "/api/v1/gateway/hooks/"+c.ID, bytes.NewReader(raw))
		if signed {
			req.Header.Set("X-Gateway-Signature", sign(raw))
			req.Header.Set("X-Gateway-Event-Id", event.EventID)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if signed && res.Code != 200 || !signed && res.Code != 401 {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	req := httptest.NewRequest("GET", "/api/v1/channel/connections", nil)
	req.Header.Set("Authorization", "Bearer channel-token")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 200 || strings.Contains(res.Body.String(), "test-api") || strings.Contains(res.Body.String(), "test-hook") || strings.Contains(res.Body.String(), c.ID) {
		t.Fatal("gateway secrets leaked into legacy transport", res.Body.String())
	}
	req = httptest.NewRequest("POST", "/api/v1/gateway/sessions/forged/resume", nil)
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 401 {
		t.Fatal("management route unauthenticated")
	}
}
func TestGatewayContainerScopeHasNoSenderCredentialsOrOtherSessions(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, _ := gatewaySetup(t, s, p)
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "one", "问题", time.Now())
	r := queueGatewayTest(t, s, "one")
	project, _ := s.Project(r.ProjectID)
	ss, _ := s.Session(r.SessionID)
	h := Harness{Store: s, Config: Config{DataDir: s.Root, RuntimeMode: "native"}}
	if _, _, _, e := h.prepare(RuntimeInput{Run: r, Session: ss, Project: project}); e == nil {
		t.Fatal("customer was allowed native host access")
	}
	h.Config.RuntimeMode = "podman"
	h.Config.Podman = "podman"
	h.Config.RuntimeImage = "test"
	h.Config.RuntimeDir = t.TempDir()
	args, _, _, e := h.prepare(RuntimeInput{Run: r, Session: ss, Project: project})
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "/tools/state:rw") || strings.Contains(joined, "/workspace:rw") || strings.Contains(joined, "test-api") || strings.Contains(joined, "--publish") {
		t.Fatal("external runtime inherited host authority", joined)
	}
	patch, e := os.ReadFile(filepath.Join(s.Root, "runtime/projects", r.ProjectID, "sessions", ss.HarnessID, "gateway-home", "invocation-"+r.ID+".yml"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(patch), "test-api") {
		t.Fatal("gateway credential in invocation")
	}
	if _, e = s.appTool(RuntimeInput{Run: r, Session: ss, Project: project}, "manage_cli", json.RawMessage(`{"action":"list"}`)); e == nil {
		t.Fatal("customer can administer global tools")
	}
}

type gatewayTestExecutor struct {
	calls         atomic.Int32
	blockInternal bool
}

func (e *gatewayTestExecutor) Execute(ctx context.Context, in RuntimeInput, _ RuntimeHooks) (RuntimeResult, error) {
	e.calls.Add(1)
	if e.blockInternal && in.Run.Source == "web" {
		<-ctx.Done()
		return RuntimeResult{}, ctx.Err()
	}
	return RuntimeResult{Text: `{"action":"reply","text":"测试回复"}`}, nil
}
func (e *gatewayTestExecutor) Cleanup(context.Context, string) error { return nil }
func TestGatewayFiveClientsRespectProjectLeaseAndQueueDeadline(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, b := gatewaySetup(t, s, p)
	b.TimeoutSeconds = 1
	if _, e := s.SaveGatewayBinding(b); e != nil {
		t.Fatal(e)
	}
	project, _ := s.Project(b.ProjectID)
	internal := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: project.DefaultSessionID, Text: "长任务"})
	ex := &gatewayTestExecutor{blockInternal: true}
	w := NewWorker(s, ex, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w.GatewayNotify = func(context.Context, string, string, string) (string, error) { return "notification-receipt", nil }
	w.Start(ctx)
	waitFor(t, func() bool { r, _ := s.Run(internal.RunID); return r.State == "running" })
	for i := range 5 {
		receiveGateway(t, s, c, "account-A", fmt.Sprintf("customer-%d", i), "message.received", fmt.Sprintf("incoming-%d", i), "问题", time.Now())
	}
	waitFor(t, func() bool {
		gs, _ := s.GatewaySessions()
		if len(gs) != 5 {
			return false
		}
		for _, g := range gs {
			if g.Mode != "human" {
				return false
			}
		}
		return true
	})
	if ex.calls.Load() != 1 {
		t.Fatal("customer bypassed project write lease")
	}
	p.mu.Lock()
	sent := p.sends
	p.mu.Unlock()
	if sent != 0 {
		t.Fatal("expired customer reply sent")
	}
	cancel()
	w.Wait()
}

func TestGatewayRecoveryCommitsHandoffAndRecoversAmbiguousSend(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, _ := gatewaySetup(t, s, p)
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "one", "问题", time.Now())
	r := queueGatewayTest(t, s, "one")
	// Simulate a crash after the worker claims a run, before its result is saved.
	w := NewWorker(s, &gatewayTestExecutor{}, nil)
	claimed, e := w.claim()
	if e != nil || claimed.ID != r.ID {
		t.Fatal(claimed, e)
	}
	root := s.Root
	if e = s.DB.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	w = NewWorker(s, &gatewayTestExecutor{}, nil)
	if e = w.Recover(t.Context()); e != nil {
		t.Fatal(e)
	}
	g, _ := s.gatewaySession(r.SessionID)
	ds := gatewayOutbox(t, s)
	if g.Mode != "human" || len(ds) != 1 || ds[0].Kind != "gateway.handoff" {
		t.Fatal(g, ds)
	}
	if e = w.Recover(t.Context()); e != nil {
		t.Fatal(e)
	}
	if len(gatewayOutbox(t, s)) != 1 {
		t.Fatal("recovery duplicated handoff")
	}
	// Crash after recording a notification claim: same stable key can resume.
	d := ds[0]
	d.State = "sending"
	_, _ = s.DB.Exec("UPDATE deliveries SET state='sending',data=? WHERE id=?", encode(d), d.ID)
	if e = s.recoverGatewayDeliveries(); e != nil {
		t.Fatal(e)
	}
	ds = gatewayOutbox(t, s)
	if ds[0].State != "pending" {
		t.Fatal(ds)
	}
	var calls int
	w.GatewayNotify = func(_ context.Context, _ string, _ string, key string) (string, error) {
		calls++
		if key != "gw-"+digest([]byte(d.ID))[:40] {
			t.Error("notification key changed")
		}
		return "receipt", nil
	}
	w.gatewayDeliverOne(t.Context())
	w.gatewayDeliverOne(t.Context())
	if calls != 1 || gatewayOutbox(t, s)[0].State != "sent" {
		t.Fatal("notification recovery failed")
	}
}
func TestGatewayAcceptedStateIsNotDeliveryAndFinalStatusDoesNotRegress(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	p.status = "queued"
	c, _ := gatewaySetup(t, s, p)
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "one", "问题", time.Now())
	r := queueGatewayTest(t, s, "one")
	completeGatewayTest(t, s, r, `{"action":"reply","text":"答复"}`)
	w := NewWorker(s, nil, nil)
	w.gatewayDeliverOne(t.Context())
	d := gatewayOutbox(t, s)[0]
	if d.State != "accepted" || d.Receipt == "" {
		t.Fatal("202 was treated as delivery", d)
	}
	for _, status := range []string{"sent", "queued", "unknown"} {
		raw, _ := json.Marshal(map[string]string{"messageId": d.Receipt, "status": status})
		e := s.Transaction(t.Context(), func(tx *sql.Tx) error { return s.gatewayStatusTx(tx, c.ID, raw) })
		if e != nil {
			t.Fatal(e)
		}
	}
	d.Gateway.NextAt = 0
	_ = s.saveGatewayDelivery(d)
	if current := gatewayOutbox(t, s)[0]; current.State != "sent" {
		t.Fatal("late poll overwrote final callback", current)
	}
}
func TestGatewayBindingOwnershipTenantLockAndContactAllowlist(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, b := gatewaySetup(t, s, p)
	dup := b
	dup.ID = ""
	dup.ProjectID = testProject(t, s, "另一个项目").ID
	if _, e := s.SaveGatewayBinding(dup); e == nil {
		t.Fatal("silently replaced another project's binding")
	}
	same, e := s.SaveGatewayBinding(b)
	if e != nil || same.Version != b.Version {
		t.Fatal("same configuration created a new generation", same, e)
	}
	b.ContactIDs = []string{"allowed"}
	b, e = s.SaveGatewayBinding(b)
	if e != nil {
		t.Fatal(e)
	}
	receiveGateway(t, s, c, "account-A", "other", "message.received", "ignored", "不应触发", time.Now())
	if gs, _ := s.GatewaySessions(); len(gs) != 0 {
		t.Fatal("allowlist bypass")
	}
	receiveGateway(t, s, c, "account-A", "allowed", "message.received", "allowed-one", "问题", time.Now())
	r := queueGatewayTest(t, s, "allowed-one")
	registration := GatewayRegistration{GatewayConnection: c, APIKey: "test-api", WebhookSecret: "test-hook"}
	registration.Enabled = false
	if _, e = s.SaveGatewayConnection(registration); e != nil {
		t.Fatal(e)
	}
	registration.Enabled = true
	if _, e = s.SaveGatewayConnection(registration); e != nil {
		t.Fatal(e)
	}
	completeGatewayTest(t, s, r, `{"action":"reply","text":"失效回复"}`)
	if len(gatewayOutbox(t, s)) != 0 {
		t.Fatal("disable/enable revived old response")
	}
	// Tenant can be learned only through an authenticated event; it then freezes.
	s2 := testStore(t)
	p2 := newGatewayProvider(t)
	proj := testProject(t, s2, "首次租户")
	reg := GatewayRegistration{GatewayConnection: GatewayConnection{ID: "fresh", BaseURL: p2.server.URL, Accounts: []string{"a"}, Enabled: true}, APIKey: "a", WebhookSecret: "b"}
	fresh, e := s2.SaveGatewayConnection(reg)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s2.SaveGatewayBinding(GatewayBinding{ConnectionID: fresh.ID, AccountID: "a", ProjectID: proj.ID, Enabled: true, NotifyChatID: "ops"})
	if e != nil {
		t.Fatal(e)
	}
	fresh.TenantID = "learned"
	ev := receiveGateway(t, s2, fresh, "a", "c", "message.received", "m", "问题", time.Now())
	stored, _, _ := s2.gatewayConnection(fresh.ID)
	if stored.TenantID != "learned" {
		t.Fatal("tenant not locked")
	}
	ev.EventID = newID()
	ev.TenantID = "other"
	raw, _ := json.Marshal(ev)
	if e = s2.ReceiveGateway(fresh.ID, ev, raw); e == nil {
		t.Fatal("tenant changed after first event")
	}
}
func TestGatewayCustomerCommandsAndManualContext(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, _ := gatewaySetup(t, s, p)
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "one", "/compact", time.Now())
	r := queueGatewayTest(t, s, "one")
	var native bool
	ex := &gatewayObserveExecutor{observe: func(in RuntimeInput) { native = in.NativeCommand }}
	w := NewWorker(s, ex, nil)
	claimed, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	w.execute(t.Context(), claimed)
	if native {
		t.Fatal("customer executed native control")
	}
	receiveGateway(t, s, c, "account-A", "customer", "message.self_sent", "human", "人工已经确认处理方案", time.Now())
	if e = s.SetGatewayMode(r.SessionID, "auto", "resume"); e != nil {
		t.Fatal(e)
	}
	receiveGateway(t, s, c, "account-A", "customer", "message.received", "two", "继续", time.Now())
	fresh := queueGatewayTest(t, s, "two")
	project, _ := s.Project(fresh.ProjectID)
	if !strings.Contains(w.prompt(fresh, project), "人工已经确认处理方案") {
		t.Fatal("human reply was lost on resume")
	}
}

type gatewayObserveExecutor struct{ observe func(RuntimeInput) }

func TestGatewayCallbackDuringConfigurationIsNotAcknowledgedThenLost(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	project := testProject(t, s, "配置窗口")
	c, e := s.SaveGatewayConnection(GatewayRegistration{GatewayConnection: GatewayConnection{ID: "setup-window", BaseURL: p.server.URL, TenantID: "tenant", Accounts: []string{"a"}, Enabled: true}, APIKey: "key", WebhookSecret: "secret"})
	if e != nil {
		t.Fatal(e)
	}
	m := gw.Message{MessageID: "message-before-binding", AccountID: "a", ContactID: "c", Direction: "in", Type: "text", SentAt: time.Now()}
	m.Content.Text = "配置期间的问题"
	data, _ := json.Marshal(map[string]any{"message": m})
	event := gw.Event{EventID: "event-before-binding", Type: "message.received", TenantID: "tenant", AccountID: "a", OccurredAt: time.Now(), Data: data}
	raw, _ := json.Marshal(event)
	if e = s.ReceiveGateway(c.ID, event, raw); e == nil {
		t.Fatal("acknowledged a message without a business binding")
	}
	var n int
	_ = s.DB.QueryRow("SELECT count(*) FROM gateway_events WHERE event_id=?", event.EventID).Scan(&n)
	if n != 0 {
		t.Fatal("failed acceptance committed a dedup tombstone")
	}
	if _, e = s.SaveGatewayBinding(GatewayBinding{ConnectionID: c.ID, AccountID: "a", ProjectID: project.ID, Enabled: true, NotifyChatID: "ops"}); e != nil {
		t.Fatal(e)
	}
	if e = s.ReceiveGateway(c.ID, event, raw); e != nil {
		t.Fatal(e)
	}
	r := queueGatewayTest(t, s, m.MessageID)
	if r.ProjectID != project.ID {
		t.Fatal("retry did not recover the original message")
	}
}

func (e *gatewayObserveExecutor) Execute(_ context.Context, in RuntimeInput, _ RuntimeHooks) (RuntimeResult, error) {
	e.observe(in)
	return RuntimeResult{Text: `{"action":"ignore"}`}, nil
}
func (e *gatewayObserveExecutor) Cleanup(context.Context, string) error { return nil }
