package workbench

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func gatewayRuntimeFixture(t *testing.T) (*Store, *Harness, RuntimeInput) {
	t.Helper()
	s := testStore(t)
	p := newGatewayProvider(t)
	c, _ := gatewaySetup(t, s, p)
	receiveGateway(t, s, c, "account-A", "client-A", "message.received", "runtime-message", "question", time.Now())
	r := queueGatewayTest(t, s, "runtime-message")
	project, _ := s.Project(r.ProjectID)
	ss, _ := s.Session(r.SessionID)
	home := t.TempDir()
	if e := os.WriteFile(filepath.Join(home, ".credentials.yaml"), []byte("version: 1\nrefs:\n  DEEPSEEK_API_KEY: host-only-model-secret\n"), 0600); e != nil {
		t.Fatal(e)
	}
	h := &Harness{Store: s, Config: Config{DataDir: s.Root, RuntimeDir: t.TempDir(), HarnessHome: home, RuntimeMode: "podman", Podman: "podman", RuntimeImage: "offline-test", Provider: "deepseek-official", Model: "offline-model"}}
	return s, h, RuntimeInput{Run: r, Session: ss, Project: project, Prompt: "test"}
}

// Implements only the public SDK framing. No Harness, model, or container is run.
func TestGatewayHarnessProtocolChild(t *testing.T) {
	if os.Getenv("PW_GATEWAY_PROTOCOL_CHILD") != "1" {
		return
	}
	out := json.NewEncoder(os.Stdout)
	scan := bufio.NewScanner(os.Stdin)
	var session string
	for scan.Scan() {
		var f struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &f) != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch f.Method {
		case "initialize":
			result = map[string]any{"serverInfo": map[string]string{"name": "deepseek-harness-sdk-runtime"}}
		case "session/prompt":
			session, _ = f.Params["sessionId"].(string)
			result = map[string]string{"messageId": "offline-message"}
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": map[string]any{"sessionId": session, "event": map[string]any{"seq": 1, "type": "assistant/message", "data": map[string]any{"message": map[string]any{"content": []map[string]string{{"type": "text", "text": `{"action":"reply","text":"offline success"}`}}}}}}})
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": map[string]any{"sessionId": session, "event": map[string]any{"seq": 2, "type": "turn/end", "data": map[string]any{"reason": map[string]string{"kind": "completed"}}}}})
		}
		_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": result})
		if f.Method == "shutdown" {
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func TestGatewayExecuteDoesNotRequireBrowserPort(t *testing.T) {
	s, h, in := gatewayRuntimeFixture(t)
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("PW_GATEWAY_PROTOCOL_CHILD", "1")
	t.Setenv("PW_GATEWAY_TEST_EXECUTABLE", executable)
	marker := filepath.Join(t.TempDir(), "unexpected-port-query")
	t.Setenv("PW_GATEWAY_PORT_MARKER", marker)
	shim := filepath.Join(t.TempDir(), "offline-podman")
	script := "#!/bin/sh\ncase \"$1\" in\nrun) exec \"$PW_GATEWAY_TEST_EXECUTABLE\" -test.run '^TestGatewayHarnessProtocolChild$';;\nrm) exit 0;;\n*) touch \"$PW_GATEWAY_PORT_MARKER\"; exit 1;;\nesac\n"
	if e = os.WriteFile(shim, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	h.Config.Podman = shim
	w := NewWorker(s, h, nil)
	r, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	in.Run = r
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, e := h.Execute(ctx, in, RuntimeHooks{})
	if e != nil || !strings.Contains(result.Text, "offline success") {
		t.Fatal(result, e)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("browser port queried for customer runtime")
	}
}

func TestGatewayRuntimeMountsOnlyGrantedKnowledgeAndNoModelSecrets(t *testing.T) {
	s, h, in := gatewayRuntimeFixture(t)
	tool := capabilityFixture(t, s)
	in.Run.Gateway.ToolIDs = []string{tool.ID}
	root, _ := s.ProjectRoot(in.Project.ID)
	for name, text := range map[string]string{"FAQ.md": "public answer", "对话归档/client-B.md": "other-customer-private", "internal.md": "internal-private"} {
		if e := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
	}
	in.Run.Gateway.KnowledgePaths = []string{"FAQ.md"}
	home := filepath.Join(s.Root, "runtime/projects", in.Project.ID, "sessions", in.Session.HarnessID, "gateway-home")
	if e := os.MkdirAll(home, 0700); e != nil {
		t.Fatal(e)
	}
	victim := filepath.Join(t.TempDir(), "host-file")
	if e := os.WriteFile(victim, []byte("unchanged-host-file"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(victim, filepath.Join(home, "invocation-"+in.Run.ID+".yml")); e != nil {
		t.Fatal(e)
	}
	// Upgrading must unlink a legacy credential entry, including a symlink.
	if e := os.Symlink(filepath.Join(h.Config.HarnessHome, ".credentials.yaml"), filepath.Join(home, ".credentials.yaml")); e != nil {
		t.Fatal(e)
	}
	args, _, _, e := h.prepare(in)
	if e != nil {
		t.Fatal(e)
	}
	victimData, e := os.ReadFile(victim)
	if e != nil || string(victimData) != "unchanged-host-file" {
		t.Fatal("customer symlink overwrote host invocation target")
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/tools/bin/fixture-cli:ro") || !strings.Contains(joined, "/tools/state/fixture:rw") {
		t.Fatal("explicit business CLI grant was lost")
	}
	if strings.Contains(joined, root+":") || strings.Contains(joined, h.Config.HarnessHome) || strings.Contains(joined, "host-only-model-secret") {
		t.Fatal("private project or credentials mounted", joined)
	}
	if _, e = os.Lstat(filepath.Join(home, ".credentials.yaml")); !os.IsNotExist(e) {
		t.Fatal("legacy credential remained")
	}
	for _, dir := range []string{home, filepath.Join(s.Root, "runtime/gateway-workspaces", in.Run.ID)} {
		e = filepath.WalkDir(dir, func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			for _, private := range []string{"host-only-model-secret", "other-customer-private", "internal-private"} {
				if bytes.Contains(b, []byte(private)) {
					t.Errorf("private data in customer mount %s", path)
				}
			}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	b, e := os.ReadFile(filepath.Join(s.Root, "runtime/gateway-workspaces", in.Run.ID, "FAQ.md"))
	if e != nil || string(b) != "public answer" {
		t.Fatal("authorized knowledge unavailable", e)
	}
	// Both source symlinks and reserved paths must fail closed.
	if e = os.Symlink("对话归档/client-B.md", filepath.Join(root, "alias.md")); e != nil {
		t.Fatal(e)
	}
	for _, paths := range [][]string{{"alias.md"}, {"对话归档/client-B.md"}, {".env"}, {"../FAQ.md"}} {
		in.Run.Gateway.KnowledgePaths = paths
		if _, _, _, e = h.prepare(in); e == nil {
			t.Fatal("private knowledge accepted", paths)
		}
	}
	// The host credential remains unchanged by legacy-symlink cleanup.
	b, e = os.ReadFile(filepath.Join(h.Config.HarnessHome, ".credentials.yaml"))
	if e != nil || !bytes.Contains(b, []byte("host-only-model-secret")) {
		t.Fatal("host credential modified")
	}
}

func TestGatewayModelProxyKeepsCredentialOnHostAndScopesRequests(t *testing.T) {
	s, h, in := gatewayRuntimeFixture(t)
	w := NewWorker(s, nil, nil)
	r, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	in.Run = r
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/anthropic/v1/messages" || r.Header.Get("x-api-key") != "host-only-model-secret" || r.Header.Get("Authorization") != "" {
			t.Error("proxy credential or target boundary")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: offline\n\n")
	}))
	defer upstream.Close()
	h.Config.GatewayModelBaseURL = upstream.URL + "/anthropic"
	handler, e := h.gatewayModelHandler(in)
	if e != nil {
		t.Fatal(e)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer customer-value")
		req.Header.Set("x-api-key", "customer-value")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	res := request("POST", "/gateway-model/v1/messages", `{"model":"offline-model","messages":[]}`)
	if res.Code != 200 || res.Body.String() != "data: offline\n\n" {
		t.Fatal(res.Code, res.Body.String())
	}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/gateway-model/v1/messages", `{}`},
		{"POST", "/gateway-model/v1/messages?target=https://evil.example", `{"model":"offline-model"}`},
		{"POST", "/gateway-model/v1/files", `{"model":"offline-model"}`},
		{"POST", "/gateway-model/v1/messages", `{"model":"another-model"}`},
		{"POST", "/gateway-model/v1/messages", `{"model":"another-model","MODEL":"offline-model"}`},
		{"POST", "/gateway-model/v1/messages", `{"Model":"offline-model"}`},
	} {
		if res = request(tc.method, tc.path, tc.body); res.Code == 200 {
			t.Fatal("invalid proxy request accepted", tc)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("invalid request reached upstream")
	}
	_ = s.SetGatewayMode(in.Session.ID, "human", "pause")
	_ = s.SetGatewayMode(in.Session.ID, "auto", "resume")
	res = request("POST", "/gateway-model/v1/messages", `{"model":"offline-model"}`)
	if res.Code != 409 || calls.Load() != 1 {
		t.Fatal("old run regained model access")
	}
}

func TestGatewayModelProxyCanonicalizesDuplicateModelKeys(t *testing.T) {
	s, h, in := gatewayRuntimeFixture(t)
	w := NewWorker(s, nil, nil)
	var e error
	in.Run, e = w.claim()
	if e != nil {
		t.Fatal(e)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		if e != nil || bytes.Count(body, []byte(`"model"`)) != 1 || bytes.Contains(body, []byte("another-model")) {
			t.Error("unchecked model value reached upstream")
		}
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()
	h.Config.GatewayModelBaseURL = upstream.URL
	handler, e := h.gatewayModelHandler(in)
	if e != nil {
		t.Fatal(e)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest("POST", "/gateway-model/v1/messages", strings.NewReader(`{"model":"another-model","model":"offline-model","messages":[]}`)))
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
}

// Legacy rows may already have an expired deadline when a new worker starts.
// Age alone must not create a handoff at any transition.
func TestGatewayLegacyDeadlineWaitsAndDeliversOnce(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	p.status = "queued"
	c, binding := gatewaySetup(t, s, p)
	at := time.Now().Add(-5 * time.Minute)
	receiveGateway(t, s, c, binding.AccountID, "new-client", "message.received", "old-message", "question", at)
	inject := func(table, id string) {
		t.Helper()
		if _, err := s.DB.Exec("UPDATE "+table+" SET data=json_set(data,'$.gateway.deadline',?) WHERE id=?", at.UnixMilli(), id); err != nil {
			t.Fatal(err)
		}
	}
	msg, err := getJSON[Message](s.DB, "select data from inbox where event_id='old-message'")
	if err != nil {
		t.Fatal(err)
	}
	inject("inbox", msg.ID)
	w := NewWorker(s, nil, nil)
	r := queueGatewayTest(t, s, "old-message")
	inject("runs", r.ID)
	w.stopRevokedGatewayRuns(t.Context())
	claimed, err := w.claim()
	if err != nil || claimed.ID != r.ID || !s.gatewayRunLive(claimed) {
		t.Fatal(claimed, err)
	}
	completeGatewayTest(t, s, claimed, `{"action":"reply","text":"answer"}`)
	d := gatewayOutbox(t, s)[0]
	inject("deliveries", d.ID)
	w.gatewayDeliverOne(t.Context())
	d = gatewayOutbox(t, s)[0]
	if d.State != "accepted" || d.Receipt == "" {
		t.Fatal(d)
	}
	if _, err = s.DB.Exec("UPDATE deliveries SET data=json_set(data,'$.gateway.deadline',?,'$.gateway.nextAt',0) WHERE id=?", at.UnixMilli(), d.ID); err != nil {
		t.Fatal(err)
	}
	w.gatewayDeliverOne(t.Context())
	ds := gatewayOutbox(t, s)
	g, err := s.gatewaySession(r.SessionID)
	if err != nil || len(ds) != 1 || ds[0].State != "accepted" || g.Mode != "auto" || p.sends != 1 {
		t.Fatal(ds, g, err)
	}
}

func TestGatewayModelProxyRefusesRedirectAndCancelsUpstream(t *testing.T) {
	for _, mode := range []string{"redirect", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, h, in := gatewayRuntimeFixture(t)
			w := NewWorker(s, nil, nil)
			var e error
			in.Run, e = w.claim()
			if e != nil {
				t.Fatal(e)
			}
			var escaped atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1); fmt.Fprint(w, "must not reach") }))
			defer target.Close()
			entered := make(chan struct{})
			aborted := make(chan struct{})
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "redirect" {
					w.Header().Set("Location", target.URL)
					w.WriteHeader(307)
					fmt.Fprint(w, "host-only-model-secret")
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				close(entered)
				<-r.Context().Done()
				close(aborted)
			}))
			defer up.Close()
			defer up.CloseClientConnections()
			h.Config.GatewayModelBaseURL = up.URL
			handler, e := h.gatewayModelHandler(in)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			req := httptest.NewRequest("POST", "/gateway-model/v1/messages", strings.NewReader(`{"model":"offline-model"}`)).WithContext(ctx)
			res := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { handler.ServeHTTP(res, req); close(done) }()
			if mode == "cancel" {
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("upstream not started")
				}
				cancel()
				select {
				case <-aborted:
				case <-time.After(2 * time.Second):
					t.Fatal("upstream did not abort")
				}
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("proxy request did not settle")
			}
			if res.Code != 502 || strings.Contains(res.Body.String(), "host-only-model-secret") || escaped.Load() != 0 {
				t.Fatal("proxy error/redirect leaked authority")
			}
		})
	}
}

func TestGatewayOldDeliveryCannotPauseResumedConversation(t *testing.T) {
	for _, outcome := range []string{"failed", "unknown", "recovery"} {
		t.Run(outcome, func(t *testing.T) {
			s := testStore(t)
			p := newGatewayProvider(t)
			p.status = "queued"
			c, _ := gatewaySetup(t, s, p)
			receiveGateway(t, s, c, "account-A", "client", "message.received", "old", "question", time.Now())
			r := queueGatewayTest(t, s, "old")
			completeGatewayTest(t, s, r, `{"action":"reply","text":"old answer"}`)
			w := NewWorker(s, nil, nil)
			w.gatewayDeliverOne(t.Context())
			d := gatewayOutbox(t, s)[0]
			_ = s.SetGatewayMode(r.SessionID, "human", "pause")
			_ = s.SetGatewayMode(r.SessionID, "auto", "resume")
			receiveGateway(t, s, c, "account-A", "client", "message.received", "fresh", "fresh question", time.Now())
			fresh := queueGatewayTest(t, s, "fresh")
			var e error
			if outcome == "recovery" {
				d.Gateway.FirstAttemptAt = now() - (3 * time.Minute).Milliseconds()
				d.Gateway.NextAt = 0
				d.State = "sending"
				_, e = s.DB.Exec("UPDATE deliveries SET state=?,data=? WHERE id=?", d.State, encode(d), d.ID)
				if e != nil {
					t.Fatal(e)
				}
				e = s.recoverGatewayDeliveries()
			} else {
				raw, _ := json.Marshal(map[string]string{"messageId": d.Receipt, "status": outcome})
				e = s.Transaction(t.Context(), func(tx *sql.Tx) error { return s.gatewayStatusTx(tx, c.ID, raw) })
			}
			if e != nil {
				t.Fatal(e)
			}
			g, _ := s.gatewaySession(r.SessionID)
			if g.Mode != "auto" || g.Version != fresh.Gateway.Version {
				t.Fatal("stale result paused resumed session", g)
			}
			completeGatewayTest(t, s, fresh, `{"action":"reply","text":"fresh answer"}`)
			for _, d := range gatewayOutbox(t, s) {
				if d.Kind == "gateway.handoff" {
					t.Fatal("stale delivery emitted a new notification")
				}
			}
		})
	}
}

func TestGatewayNotificationWindowCheckedBeforeExternalEffect(t *testing.T) {
	for _, state := range []string{"pending", "sending"} {
		t.Run(state, func(t *testing.T) {
			s := testStore(t)
			p := newGatewayProvider(t)
			c, _ := gatewaySetup(t, s, p)
			receiveGateway(t, s, c, "account-A", "client", "message.received", "one", "question", time.Now())
			r := queueGatewayTest(t, s, "one")
			completeGatewayTest(t, s, r, `{"action":"handoff","reason":"help"}`)
			d := gatewayOutbox(t, s)[0]
			d.State = state
			d.Gateway.Attempts = 1
			d.Gateway.FirstAttemptAt = now() - int64(2*time.Hour/time.Millisecond)
			_, e := s.DB.Exec("UPDATE deliveries SET state=?,data=? WHERE id=?", state, encode(d), d.ID)
			if e != nil {
				t.Fatal(e)
			}
			w := NewWorker(s, nil, nil)
			calls := 0
			w.GatewayNotify = func(context.Context, string, string, string) (string, error) {
				calls++
				return "duplicate-receipt", nil
			}
			if e = s.recoverGatewayDeliveries(); e != nil {
				t.Fatal(e)
			}
			w.gatewayDeliverOne(t.Context())
			if calls != 0 || gatewayOutbox(t, s)[0].State != "unknown" {
				t.Fatal("expired notification re-sent")
			}
		})
	}
}

func TestGatewayDisabledIngressCanBeReplacedWithoutRevivingOldWork(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	c, _ := gatewaySetup(t, s, p)
	receiveGateway(t, s, c, "account-A", "client", "message.received", "one", "old question", time.Now())
	old := queueGatewayTest(t, s, "one")
	registration := GatewayRegistration{GatewayConnection: c, APIKey: "test-api", WebhookSecret: "test-hook"}
	replacement := registration
	replacement.ID = "replacement"
	replacement.Accounts = []string{"account-A", "account-C"}
	if _, e := s.SaveGatewayConnection(replacement); e == nil {
		t.Fatal("two active ingress connections accepted")
	}
	registration.Enabled = false
	if _, e := s.SaveGatewayConnection(registration); e != nil {
		t.Fatal(e)
	}
	next, e := s.SaveGatewayConnection(replacement)
	if e != nil {
		t.Fatal("disabled ingress cannot be replaced", e)
	}
	registration.Enabled = true
	if _, e = s.SaveGatewayConnection(registration); e == nil {
		t.Fatal("old connection reactivated alongside replacement")
	}
	project := testProject(t, s, "new account project")
	if _, e = s.SaveGatewayBinding(GatewayBinding{ConnectionID: next.ID, AccountID: "account-A", ProjectID: project.ID, Enabled: true, NotifyChatID: "ops"}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveGatewayBinding(GatewayBinding{ConnectionID: next.ID, AccountID: "account-C", ProjectID: project.ID, Enabled: true, NotifyChatID: "ops"}); e != nil {
		t.Fatal(e)
	}
	receiveGateway(t, s, next, "account-C", "client", "message.received", "two", "new question", time.Now())
	fresh := queueGatewayTest(t, s, "two")
	// Webhook redelivery after changing the ingress URL retains tenant-level
	// message deduplication, even when the old binding/project was replaced.
	receiveGateway(t, s, next, "account-A", "client", "message.received", "one", "old question", time.Now())
	gs, e := s.GatewaySessions()
	if e != nil || len(gs) != 2 {
		t.Fatal("replacement replayed an old message", gs, e)
	}
	completeGatewayTest(t, s, old, `{"action":"reply","text":"stale"}`)
	if len(gatewayOutbox(t, s)) != 0 || fresh.SessionID == old.SessionID || fresh.ProjectID != project.ID {
		t.Fatal("replacement revived old work or reused ownership")
	}
	oldSession, _ := s.Session(old.SessionID)
	if oldSession.ProjectID != old.ProjectID {
		t.Fatal("old ownership changed")
	}
}

func TestGatewayReceiptPollYieldsToWaitingReply(t *testing.T) {
	s := testStore(t)
	p := newGatewayProvider(t)
	p.status = "sending"
	c, b := gatewaySetup(t, s, p)
	w := NewWorker(s, nil, nil)
	answer := func(id string) {
		receiveGateway(t, s, c, b.AccountID, id, "message.received", id, "question", time.Now())
		r := queueGatewayTest(t, s, id)
		completeGatewayTest(t, s, r, `{"action":"reply","text":"answer"}`)
	}
	// Outstanding receipts stay in sending. A fresh reply was ready before their next poll.
	for i := 0; i < 6; i++ {
		answer(fmt.Sprintf("old-%d", i))
		w.gatewayDeliverOne(t.Context())
	}
	answer("fresh")
	stamp := now()
	_, err := s.DB.Exec(`UPDATE deliveries SET created_at=?,data=json_set(data,'$.gateway.nextAt',?) WHERE state='accepted'`, stamp-90000, stamp-1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE deliveries SET created_at=? WHERE state='pending'", stamp-2000); err != nil {
		t.Fatal(err)
	}
	w.gatewayDeliverOne(t.Context())
	if p.sends != 7 {
		t.Fatal("receipt polls blocked an older waiting send", p.sends)
	}
	p.status = "invalid-state"
	old := gatewayOutbox(t, s)[0]
	w.gatewayDeliverOne(t.Context())
	polled, err := getJSON[Delivery](s.DB, "SELECT data FROM deliveries WHERE id=?", old.ID)
	if err != nil || polled.State != "accepted" || polled.Gateway.NextAt <= now() || polled.Gateway.PollErrors != 1 {
		t.Fatal(polled, err)
	}
	if polled.Gateway.LegacyDeadline <= 0 {
		t.Fatal("rollback reader would immediately expire a known receipt")
	}
}

func TestGatewaySilentUpstreamReleasesRequest(t *testing.T) {
	for _, sendHeaders := range []bool{false, true} {
		t.Run(fmt.Sprint("headers=", sendHeaders), func(t *testing.T) {
			done := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(done)
				if sendHeaders {
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer srv.Close()
			req, _ := http.NewRequestWithContext(t.Context(), "GET", srv.URL, nil)
			res, err := gatewayReadStream(srv.Client(), req, 150*time.Millisecond)
			if err == nil {
				_, err = io.ReadAll(res.Body)
				res.Body.Close()
			}
			if err == nil {
				t.Fatal("silent connection survived idle timeout")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("upstream was not cancelled")
			}
		})
	}
}
