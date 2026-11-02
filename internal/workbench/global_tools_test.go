package workbench

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGlobalToolWrapperAndPluginScope(t *testing.T) {
	s := testStore(t)
	v := CLITool{ID: "demo", Name: "演示CLI", Command: "demo-cli", Version: "1", URL: "https://example.com/demo.tar.gz", Enabled: true}
	dir := filepath.Dir(s.toolBinary(v))
	os.MkdirAll(dir, 0700)
	os.WriteFile(s.toolBinary(v), []byte("#!/bin/sh\nprintf 'global-ok'\n"), 0755)
	if _, e := s.SaveCLITool(v); e != nil {
		t.Fatal(e)
	}
	wrapper, e := os.ReadFile(filepath.Join(s.Root, "tools", "bin", "demo-cli"))
	if e != nil || !strings.Contains(string(wrapper), "packages/demo/1/demo-cli") {
		t.Fatal(string(wrapper), e)
	}
	p, e := s.SavePlugin(Plugin{Name: "全局", Transport: "stdio", Command: "node", Args: []string{"server.mjs"}})
	if e != nil {
		t.Fatal(e)
	}
	a := testProject(t, s, "A")
	b := testProject(t, s, "B")
	if len(a.PluginIDs) != 0 || len(b.PluginIDs) != 0 {
		t.Fatal("unexpected project binding")
	}
	plugins, e := s.EnabledPlugins()
	if e != nil || len(plugins) != 1 || plugins[0].ID != p.ID {
		t.Fatal(plugins, e)
	}
	enabled := false
	p.Enabled = &enabled
	if _, e = s.SavePlugin(p); e != nil {
		t.Fatal(e)
	}
	plugins, _ = s.EnabledPlugins()
	if len(plugins) != 0 {
		t.Fatal(plugins)
	}
	if e = s.DeletePlugin(p.ID); e != nil {
		t.Fatal(e)
	}
}
func TestToolOutputRedactsCredentials(t *testing.T) {
	var text string
	o := toolOutput{update: func(s string) { text = s }}
	o.Write([]byte(`{"access_token":"not-for-logs","refresh_token":"also-secret","verification_url":"https://auth.example/verify?code=123"}`))
	if strings.Contains(text, "not-for-logs") || strings.Contains(text, "also-secret") || !strings.Contains(text, "https://auth.example") {
		t.Fatal(text)
	}
}
func TestRealGlobalMCPAndFailureDeadline(t *testing.T) {
	path := os.Getenv("PW_REAL_CONFIG")
	if path == "" {
		t.Skip("credentialed isolated runtime required")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var cfg Config
	json.Unmarshal(raw, &cfg)
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	cfg.RuntimeDir = filepath.Join(root, "runtime")
	s := testStore(t)
	cfg.DataDir = s.Root
	p := testProject(t, s, "全局MCP A")
	ss, _ := s.Session(p.DefaultSessionID)
	fixture := filepath.Join(root, "runtime", "test-mcp.mjs")
	node := cfg.HarnessCommand[0]
	if _, e = s.SavePlugin(Plugin{Name: "echo", Transport: "stdio", Command: node, Args: []string{fixture}}); e != nil {
		t.Fatal(e)
	}
	h := Harness{Config: cfg, Store: s}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	run := func(p Project, ss Session, prompt string) (RuntimeResult, error) {
		return h.Execute(ctx, RuntimeInput{Run: Run{ID: newID()}, Session: ss, Project: p, Prompt: prompt}, RuntimeHooks{})
	}
	result, e := run(p, ss, "请调用全局 MCP 的 echo 工具，传 text=ALPHA314，然后原样回复工具返回的结果。不要调用其他工具。")
	if e != nil || !strings.Contains(result.Text, "GLOBAL_MCP:ALPHA314") {
		t.Fatal(result, e)
	}
	q := testProject(t, s, "全局MCP B")
	qs, _ := s.Session(q.DefaultSessionID)
	result, e = run(q, qs, "请调用全局 MCP 的 echo 工具，传 text=BETA271，然后原样回复工具返回的结果。不要调用其他工具。")
	if e != nil || !strings.Contains(result.Text, "GLOBAL_MCP:BETA271") {
		t.Fatal(result, e)
	}
	_, e = s.SavePlugin(Plugin{Name: "hang", Transport: "stdio", Command: node, Args: []string{"-e", "setInterval(()=>{},1000)"}})
	if e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	result, e = run(q, qs, "本轮不调用工具，只回复：普通聊天继续。")
	if e != nil || !strings.Contains(result.Text, "普通聊天继续") {
		t.Fatal(result, e)
	}
	if time.Since(started) > 25*time.Second {
		t.Fatal("broken global MCP stalls chat", time.Since(started))
	}
	t.Log("native MCP across projects and unavailable-server fallback", time.Since(started))
}

func TestChatToolInstallDoesNotWaitOnItsOwnRun(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "工具维护")
	q := testProject(t, s, "其他任务")
	if _, e := s.DB.Exec("INSERT INTO project_locks VALUES(?,?,?)", p.ID, "requesting-run", now()); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckNoRunningTools(); e == nil {
		t.Fatal("web maintenance ignored active task")
	}
	if e := s.checkNoRunningToolsExcept("requesting-run"); e != nil {
		t.Fatal("chat install waits for itself", e)
	}
	if _, e := s.DB.Exec("INSERT INTO project_locks VALUES(?,?,?)", q.ID, "other-run", now()); e != nil {
		t.Fatal(e)
	}
	if e := s.checkNoRunningToolsExcept("requesting-run"); e == nil {
		t.Fatal("chat install ignored unrelated task")
	}
	s.toolManager = func(id, action string, args []string) (ToolJob, error) {
		if id != "lark" || action != "install" || len(args) != 2 || args[1] != "requesting-run" {
			t.Fatal(id, action, args)
		}
		return ToolJob{State: "queued"}, nil
	}
	if _, e := s.appTool(RuntimeInput{Run: Run{ID: "requesting-run", Source: "web"}}, "manage_cli", json.RawMessage(`{"action":"install","toolId":"lark"}`)); e != nil {
		t.Fatal(e)
	}
}

func TestToolArchiveUsesVerifiedDigestCache(t *testing.T) {
	s := testStore(t)
	body := []byte("offline release fixture")
	sha := digest(body)
	dir := filepath.Join(s.Root, "tools", "downloads")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, sha+".tar.gz"), body, 0600); e != nil {
		t.Fatal(e)
	}
	f, e := s.toolArchive(t.Context(), CLITool{URL: "https://unreachable.invalid/release", SHA256: sha})
	if e != nil {
		t.Fatal(e)
	}
	var b strings.Builder
	_, e = io.Copy(&b, f)
	f.Close()
	if e != nil || b.String() != string(body) {
		t.Fatal(b.String(), e)
	}
	if _, e = s.toolArchive(t.Context(), CLITool{SHA256: strings.Repeat("z", 64)}); e == nil {
		t.Fatal("invalid cache digest accepted")
	}
}

func TestCustomerRetrievalPluginStaysInItsProject(t *testing.T) {
	s := testStore(t)
	a := testProject(t, s, "A")
	b := testProject(t, s, "B")
	plugin, err := s.SavePlugin(Plugin{Name: "private evidence", Transport: "http", URL: "https://evidence.example/mcp", ProjectID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	var legacyVisible int
	if err = s.DB.QueryRow("SELECT count(*) FROM plugins WHERE id=?", plugin.ID).Scan(&legacyVisible); err != nil || legacyVisible != 0 {
		t.Fatalf("rollback leaks project plugin: %d %v", legacyVisible, err)
	}
	for _, project := range []string{a.ID, b.ID, personalProjectID} {
		visible, err := s.enabledProjectPlugins(project)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, p := range visible {
			found = found || p.ID == plugin.ID
		}
		if found != (project == a.ID) {
			t.Fatalf("wrong visibility for %s", project)
		}
	}
	b.PluginIDs = []string{plugin.ID}
	if _, err = s.UpdateProject(b.ID, b); err == nil {
		t.Fatal("accepted another project's tool")
	}
}

func TestDeleteGatewayProjectReleasesIdentityAndCredentials(t *testing.T) {
	s := testStore(t)
	c, b := gatewaySetup(t, s, newGatewayProvider(t))
	receiveGateway(t, s, c, b.AccountID, "fixture", "message.received", "event", "hello", time.Now())
	sessions, err := s.GatewaySessions()
	if err != nil || len(sessions) != 1 {
		t.Fatal(sessions, err)
	}
	plugin, err := s.SavePlugin(Plugin{ProjectID: b.ProjectID, Name: "private", Transport: "http", URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "private"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO gateway_crm_receipts VALUES('test','request','hash','actor',?,'result',1)", sessions[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteProject(b.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Plugin(plugin.ID); err != ErrNotFound {
		t.Fatal(err)
	}
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM gateway_crm_receipts").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	b.ProjectID = testProject(t, s, "replacement").ID
	old := b.ID
	b.ID = ""
	next, err := s.SaveGatewayBinding(b)
	if err != nil || next.ID == old {
		t.Fatal(next, err)
	}
}

func TestOldProjectPluginMigrationDropsDeletedOwners(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "existing")
	for _, project := range []string{p.ID, "deleted-project"} {
		plugin := Plugin{ID: "legacy-" + project, ProjectID: project, Name: "project secret", Headers: map[string]string{"Authorization": "retired-key"}}
		if _, err := s.DB.Exec("INSERT INTO plugins VALUES(?,?)", plugin.ID, encode(plugin)); err != nil {
			t.Fatal(err)
		}
	}
	next, err := NewStore(s.DB, s.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = next.Plugin("legacy-deleted-project"); err != ErrNotFound {
		t.Fatal(err)
	}
	if kept, err := next.Plugin("legacy-" + p.ID); err != nil || kept.ProjectID != p.ID {
		t.Fatal(err)
	}
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM plugins WHERE id LIKE 'legacy-%'").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
