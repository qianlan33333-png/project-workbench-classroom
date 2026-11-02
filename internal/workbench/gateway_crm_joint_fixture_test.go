package workbench

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// The loopback fixture starts the real store, worker, public webhook and outbox.
// External customer delivery, Feishu, and model inference are deterministic doubles.
type jointProbeExecutor struct{}

func (jointProbeExecutor) Cleanup(context.Context, string) error { return nil }
func (jointProbeExecutor) Execute(_ context.Context, in RuntimeInput, h RuntimeHooks) (RuntimeResult, error) {
	if err := h.Accepted(in.Run.ID); err != nil {
		return RuntimeResult{}, err
	}
	switch {
	case in.Run.Text == "joint-handoff":
		return RuntimeResult{Text: `{"action":"handoff","reason":"joint handoff","summary":"test customer"}`}, nil
	case in.Run.Text == "joint-next" && strings.Contains(in.Prompt, "violet-paper-713"):
		return RuntimeResult{Text: `{"action":"reply","text":"violet-paper-713"}`}, nil
	default:
		return RuntimeResult{}, errors.New("unexpected fixture input or missing pause history")
	}
}

func TestAutoReplyJointWorkbenchFixture(t *testing.T) {
	dir := os.Getenv("PW_AR_FIXTURE_DIR")
	if dir == "" {
		t.Skip("opt-in loopback CRM joint acceptance")
	}
	st, err := os.Stat(dir)
	if err != nil || !filepath.IsAbs(dir) || !st.IsDir() || st.Mode().Perm()&077 != 0 {
		t.Fatal("private fixture directory required")
	}
	t.Setenv("WORKBENCH_AR_GRANTS_FILE", "")
	s := testStore(t)
	p := testProject(t, s, "joint fixture")
	provider := newGatewayProvider(t)
	c, err := s.SaveGatewayConnection(GatewayRegistration{GatewayConnection: GatewayConnection{ID: "joint", TenantID: "fixture-tenant", Accounts: []string{"account-a"}, BaseURL: provider.server.URL, Enabled: true}, APIKey: "fixture-api", WebhookSecret: "joint-fixture-hook"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.SaveGatewayBinding(GatewayBinding{ConnectionID: c.ID, AccountID: "account-a", ProjectID: p.ID, Enabled: true, NotifyChatID: "fixture-chat"})
	if err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, "wb-meta.json")
	f, err := os.OpenFile(meta, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(encode(map[string]string{"bindingId": b.ID, "store": s.Root}))
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		t.Fatal(err)
	}
	grants := filepath.Join(dir, "bundle", "workbench", "grants.json")
	deadline := time.Now().Add(20 * time.Minute)
	for {
		if _, err = os.Stat(grants); err == nil {
			break
		}
		if !os.IsNotExist(err) || time.Now().After(deadline) {
			t.Fatal("awaiting paired installation")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Setenv("WORKBENCH_AR_GRANTS_FILE", grants)
	if s.crmGrants, err = loadCRMGrants(); err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid fixture CA")
	}
	s.crmTransport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	w := NewWorker(s, jointProbeExecutor{}, nil)
	w.GatewayNotify = func(context.Context, string, string, string) (string, error) { return "fixture-feishu-receipt", nil }
	ctx, cancel := context.WithCancel(t.Context())
	w.Start(ctx)
	defer func() { cancel(); w.Wait() }()
	router := gin.New()
	api := API{Store: s, Worker: w, Config: Config{DataDir: s.Root, WebDir: t.TempDir()}, Shutdown: ctx.Done()}
	api.Mount(router)
	ln, err := net.Listen("tcp", "127.0.0.1:18443")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: router, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(dir, "wb.stop")); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("joint acceptance fixture timed out")
}
