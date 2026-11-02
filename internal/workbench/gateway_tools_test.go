package workbench

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGatewayToolSecretsAndStateMounts(t *testing.T) {
	s, h, in := gatewayRuntimeFixture(t)
	cli := capabilityFixture(t, s)
	httpPlugin, e := s.SavePlugin(Plugin{Name: "knowledge", ProjectID: in.Project.ID, Transport: "http", URL: "https://mcp.test/rpc?token=private-url", Headers: map[string]string{"Authorization": "private-bearer"}})
	if e != nil {
		t.Fatal(e)
	}
	stdio, e := s.SavePlugin(Plugin{Name: "records", Transport: "stdio", Command: "fixture-cli", Args: []string{"--token", "private-argument"}, Env: map[string]string{"MCP_TOKEN": "private-env"}})
	if e != nil {
		t.Fatal(e)
	}
	in.Run.Gateway.PluginIDs = []string{stdio.ID, httpPlugin.ID}
	in.Run.Gateway.ToolIDs = []string{cli.ID}
	mount := func(in RuntimeInput) string {
		args, env, _, err := h.prepare(in)
		if err != nil {
			t.Fatal(err)
		}
		home, _ := h.gatewayDir(in, "gateway-home")
		f, err := os.ReadFile(filepath.Join(home, "invocation-"+in.Run.ID+".yml"))
		if err != nil {
			t.Fatal(err)
		}
		all := string(f) + strings.Join(args, " ") + strings.Join(env, " ")
		for _, secret := range []string{"private-bearer", "private-argument", "private-env", "private-url"} {
			if strings.Contains(all, secret) {
				t.Fatalf("credential in customer configuration: %s", secret)
			}
		}
		if !strings.Contains(string(f), "gatewayHTTPPlugin: "+httpPlugin.ID) || !strings.Contains(string(f), "personal-mcp-relay.mjs") {
			t.Fatal("missing customer relay")
		}
		if strings.Contains(strings.Join(args, " "), home+":/runtime:rw") || strings.Contains(strings.Join(args, " "), filepath.Join(s.Root, "tools/state", cli.ID)+":") {
			t.Fatal("private data or legacy customer HOME is mounted")
		}
		for _, a := range args {
			if before, ok := strings.CutSuffix(a, ":/tools/state/"+cli.ID+":rw"); ok {
				return before
			}
		}
		t.Fatal("scoped CLI state missing")
		return ""
	}
	one := mount(in)
	if err := os.WriteFile(filepath.Join(one, "session-token"), []byte("A"), 0600); err != nil {
		t.Fatal(err)
	}
	if mount(in) != one {
		t.Fatal("same session lost its CLI state")
	}
	two := in
	two.Session.HarnessID = "pw-" + newID()
	two.Run.ID = newID()
	if second := mount(two); one == second {
		t.Fatal("shared state across sessions")
	} else if _, err := os.Stat(filepath.Join(second, "session-token")); !os.IsNotExist(err) {
		t.Fatal("other customer's file visible", err)
	}
	// A symlink planted at a mount root must never redirect Podman to a host directory.
	if e := os.RemoveAll(one); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(t.TempDir(), one); e != nil {
		t.Fatal(e)
	}
	if _, _, _, e := h.prepare(in); e == nil {
		t.Fatal("symlink accepted as customer CLI state")
	}
}

func TestGatewayScrubsLegacyActiveAndRecycledInvocations(t *testing.T) {
	s, h, in := gatewayRuntimeFixture(t)
	home, e := h.gatewayDir(in, "gateway-home")
	if e != nil {
		t.Fatal(e)
	}
	trash := filepath.Join(s.Root, "trash", in.Session.ID, "0", "gateway-home")
	if e = os.MkdirAll(trash, 0700); e != nil {
		t.Fatal(e)
	}
	outside := t.TempDir()
	keep := filepath.Join(outside, "invocation-keep.yml")
	if e = os.WriteFile(keep, []byte("unrelated"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, d := range []string{home, trash} {
		if e = os.Symlink(keep, filepath.Join(d, "invocation-link.yml")); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(d, "invocation-old.yml"), []byte("retired-bearer"), 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.MkdirAll(filepath.Join(d, "sessions"), 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e = os.Symlink(outside, filepath.Join(s.Root, "trash", "external")); e != nil {
		t.Fatal(e)
	}
	if e = h.scrubLegacyGatewayHomes(); e != nil {
		t.Fatal(e)
	}
	for _, d := range []string{home, trash} {
		files, e := os.ReadDir(d)
		if e != nil || len(files) != 1 || files[0].Name() != "sessions" {
			t.Fatal("legacy cleanup", e, files)
		}
	}
	if _, e = os.Stat(keep); e != nil {
		t.Fatal("followed symlink")
	}
}

func TestGatewayMCPHostUsesBoundCredentialsAndSession(t *testing.T) {
	s, h, in := gatewayRuntimeFixture(t)
	count := 0
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.Header.Get("Authorization") != "Bearer only-host" || r.Header.Get("Cookie") != "" {
			t.Error("wrong auth forwarding")
		}
		if r.Method == "POST" {
			w.Header().Set("Mcp-Session-Id", "remote-private")
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
			return
		}
		if r.Header.Get("Mcp-Session-Id") != "remote-private" {
			t.Error("remote session handle not restored")
		}
		w.WriteHeader(204)
	}))
	defer up.Close()
	p, e := s.SavePlugin(Plugin{Name: "test", Transport: "http", ProjectID: in.Project.ID, URL: up.URL, Headers: map[string]string{"Authorization": "Bearer only-host"}})
	if e != nil {
		t.Fatal(e)
	}
	in.Run = claimGatewayWithTestPlugin(t, s, in.Run, p.ID)
	proxy := h.gatewayMCPHandler(in, up.Client().Transport)
	request := func(method, path, sid string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
		r.Header.Set("Authorization", "override")
		r.Header.Set("Cookie", "private")
		r.Header.Set("Mcp-Session-Id", sid)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		return w
	}
	path := "/gateway-mcp/" + p.ID
	res := request("POST", path, "")
	sid := res.Header().Get("Mcp-Session-Id")
	if res.Code != 200 || sid == "" || sid == "remote-private" {
		t.Fatal("customer session handle", res.Code, sid)
	}
	if request("DELETE", path, "remote-private").Code != 404 || request("GET", path, sid).Code != 204 {
		t.Fatal("session scope failed")
	}
	if request("POST", path+"?target=https://bad", "").Code != 400 || request("POST", "/gateway-mcp/"+newID(), "").Code != 403 {
		t.Fatal("target selection accepted")
	}
	p.Headers = map[string]string{"Authorization": "new-owner-token"}
	if _, err := s.SavePlugin(p); err != nil {
		t.Fatal(err)
	}
	if request("GET", path, sid).Code != 404 {
		t.Fatal("previous provider session handle accepted after credential change")
	}
	if e = s.SetGatewayMode(in.Session.ID, "human", "test"); e != nil {
		t.Fatal(e)
	}
	if request("POST", path, "").Code != 403 || count != 2 {
		t.Fatal("issued request after pause", count)
	}
}

func TestGatewayStdioHasNoHostOrCustomerWritableState(t *testing.T) {
	s, h, in := gatewayRuntimeFixture(t)
	run, e := NewWorker(s, nil, nil).claim()
	if e != nil {
		t.Fatal(e)
	}
	in.Run = run
	cli, e := s.CLITool(capabilityFixture(t, s).ID)
	if e != nil {
		t.Fatal(e)
	}
	a, _, e := h.isolatedExecArgs(in, "/tools/bin/"+cli.Command, []string{"mcp"}, filepath.Join(t.TempDir(), "private-env"), &cli, true)
	if e != nil {
		t.Fatal(e)
	}
	args := strings.Join(a, " ")
	if !strings.Contains(args, "--tmpfs /tools/state/"+cli.ID+":rw") || strings.Contains(args, filepath.Join(s.Root, "tools/state")) || strings.Contains(args, "bridge.sock") || strings.Contains(args, "/runtime:rw") {
		t.Fatal("MCP process received untrusted/host state")
	}
}

func TestGatewayStdioConcurrentHandshakesLaunchOnceAndRevoke(t *testing.T) {
	s, h, in := gatewayRuntimeFixture(t)
	p, err := s.SavePlugin(Plugin{Name: "one MCP", Transport: "stdio", Command: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	in.Run = claimGatewayWithTestPlugin(t, s, in.Run, p.ID)
	log := filepath.Join(t.TempDir(), "podman-actions")
	h.Config.Podman = filepath.Join(t.TempDir(), "podman")
	// One line per actual host process operation; stay attached like a real MCP.
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> '" + log + "'\nif [ \"$1\" = run ]; then exec cat; fi\n"
	if err = os.WriteFile(h.Config.Podman, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	stop, err := h.startBridge(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	tr := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", bridgeSocket(in))
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	get := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(t.Context(), "GET", "http://bridge/gateway-stdio/"+p.ID, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "pwb-stdio")
		return client.Do(req)
	}
	type result struct {
		res *http.Response
		err error
	}
	const contenders = 12
	results := make(chan result, contenders)
	begin := make(chan struct{})
	for i := 0; i < contenders; i++ {
		go func() { <-begin; r, e := get(); results <- result{r, e} }()
	}
	close(begin)
	var stream io.ReadCloser
	accepted := 0
	for i := 0; i < contenders; i++ {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		switch result.res.StatusCode {
		case 101:
			accepted++
			stream = result.res.Body
		case 429:
			result.res.Body.Close()
		default:
			result.res.Body.Close()
			t.Fatalf("unexpected handshake status %d", result.res.StatusCode)
		}
	}
	if accepted != 1 {
		t.Fatal("actual successful handshakes", accepted)
	}
	defer stream.Close()
	waitActions := func(want string) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(log)
			if string(data) == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		data, _ := os.ReadFile(log)
		t.Fatalf("expected %q, got %q", want, data)
	}
	waitActions("run\n")
	disabled := false
	p.Enabled = &disabled
	if _, err = s.SavePlugin(p); err != nil {
		t.Fatal(err)
	}
	waitActions("run\nrm\n")
	// Re-enabling the plugin cannot multiply starts within this run.
	enabled := true
	p.Enabled = &enabled
	if _, err = s.SavePlugin(p); err != nil {
		t.Fatal(err)
	}
	retry, err := get()
	if err != nil {
		t.Fatal(err)
	}
	retry.Body.Close()
	if retry.StatusCode != 429 {
		t.Fatalf("retry status=%d", retry.StatusCode)
	}
}

func TestGatewayHTTPBoundsRequestsAndCancelsOpenMCP(t *testing.T) {
	s, h, in := gatewayRuntimeFixture(t)
	started := make(chan struct{}, 2)
	closed := make(chan struct{}, 2)
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		rctx := r.Context()
		<-rctx.Done()
		closed <- struct{}{}
	}))
	defer up.Close()
	p, e := s.SavePlugin(Plugin{Name: "stream", Transport: "http", URL: up.URL})
	if e != nil {
		t.Fatal(e)
	}
	in.Run = claimGatewayWithTestPlugin(t, s, in.Run, p.ID)
	proxy := boundGatewayHTTP(h.gatewayMCPHandler(in, up.Client().Transport), 1, 1)
	path := "/gateway-mcp/" + p.ID
	do := func() int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		proxy.ServeHTTP(w, r)
		return w.Code
	}
	done := make(chan int, 1)
	go func() { done <- do() }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not start")
	}
	if got := do(); got != 429 {
		t.Fatal("parallel call accepted", got)
	}
	if e := s.SetGatewayMode(in.Session.ID, "human", "pause MCP"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("open upstream survived pause")
	}
	if got := <-done; got != 502 {
		t.Fatal("cancelled upstream", got)
	}
	if got := do(); got != 429 {
		t.Fatal("exhausted budget was reused", got)
	}
}

// These protocol fixtures create their plugin after preparing the queued
// message. Capture the grant before the scheduler classifies that queued run.
func claimGatewayWithTestPlugin(t *testing.T, s *Store, r Run, plugin string) Run {
	t.Helper()
	r.Gateway.PluginIDs = []string{plugin}
	if _, err := s.DB.Exec("UPDATE runs SET data=? WHERE id=? AND state='queued'", encode(r), r.ID); err != nil {
		t.Fatal(err)
	}
	run, err := NewWorker(s, nil, nil).claim()
	if err != nil || run.Gateway.SharedRead {
		t.Fatal("unapproved plugin should require the writer lease", err)
	}
	return run
}
