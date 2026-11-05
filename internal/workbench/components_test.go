package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func componentFixture(t *testing.T, s *Store, p Project) Plugin {
	t.Helper()
	plugin, e := s.SavePlugin(Plugin{Name: "测试组件", ProjectID: p.ID, Transport: "http", URL: "https://component.invalid/mcp?token=secret-query", Headers: map[string]string{"Authorization": "Bearer test-secret"}, Component: &ComponentSpec{Version: "1", Actions: []ComponentAction{{Name: "start", Description: "开始"}}, StatusAction: "status", LookupAction: "lookup", CancelAction: "cancel"}})
	if e != nil {
		t.Fatal(e)
	}
	plugin, e = s.Plugin(plugin.ID)
	if e != nil {
		t.Fatal(e)
	}
	return plugin
}
func TestComponentIdempotencyScopeAndPersistence(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "组件A")
	other := testProject(t, s, "组件B")
	plugin := componentFixture(t, s, p)
	var calls atomic.Int64
	s.componentRunner = func(_ context.Context, p Plugin, action string, args map[string]any) (json.RawMessage, error) {
		calls.Add(1)
		if args["project_id"] != p.ProjectID {
			t.Error("unbound project")
		}
		return json.RawMessage(`{"job_ref":"j1","status":"running","summary":"执行中"}`), nil
	}
	q := ComponentRequest{PluginID: plugin.ID, Action: "start", Arguments: map[string]any{"value": 1}, OperationKey: "stable-key-1"}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			v, e := s.SubmitComponent(context.Background(), p.DefaultSessionID, q)
			if e != nil {
				t.Error(e)
			}
			ids <- v.ID
		})
	}
	wg.Wait()
	close(ids)
	id := ""
	for v := range ids {
		if id != "" && v != id {
			t.Fatal("duplicate invocation")
		}
		id = v
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate external execution", calls.Load())
	}
	q.Arguments = map[string]any{"value": 2}
	if _, e := s.SubmitComponent(context.Background(), p.DefaultSessionID, q); !errors.Is(e, ErrConflict) {
		t.Fatal("key reuse", e)
	}
	if _, e := s.SubmitComponent(context.Background(), other.DefaultSessionID, q); e == nil {
		t.Fatal("cross-project execution")
	}
	q.Action = "forbidden"
	if _, e := s.SubmitComponent(context.Background(), p.DefaultSessionID, q); e == nil {
		t.Fatal("unregistered action")
	}
	ps, _ := s.Components(p.ID)
	catalog := encode(ps)
	if strings.Contains(catalog, "secret-query") || strings.Contains(catalog, "test-secret") || strings.Contains(catalog, "component.invalid") {
		t.Fatal("catalog secret")
	}
	direct, _ := s.enabledProjectPlugins(p.ID)
	for _, d := range direct {
		if d.ID == plugin.ID {
			t.Fatal("component bypasses host in Harness catalog")
		}
	}
	if e := s.DeletePlugin(plugin.ID); e == nil {
		t.Fatal("removed in-flight connection")
	}
	// A separate Store instance can recover the same opaque reference; no model state required.
	v, e := getJSON[ComponentCall](s.DB, "SELECT data FROM component_calls WHERE id=?", id)
	if e != nil || v.JobRef != "j1" {
		t.Fatal(v, e)
	}
}
func TestComponentLateSubmitCannotUndoCancellation(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "race")
	plugin := componentFixture(t, s, p)
	started := make(chan struct{})
	release := make(chan struct{})
	s.componentRunner = func(_ context.Context, _ Plugin, action string, _ map[string]any) (json.RawMessage, error) {
		if action == "start" {
			close(started)
			<-release
			return json.RawMessage(`{"job_ref":"j","status":"running"}`), nil
		}
		if action == "lookup" {
			return json.RawMessage(`{"job_ref":"j","status":"running"}`), nil
		}
		return json.RawMessage(`{"job_ref":"j","status":"cancelled"}`), nil
	}
	done := make(chan ComponentCall)
	go func() {
		v, e := s.SubmitComponent(context.Background(), p.DefaultSessionID, ComponentRequest{PluginID: plugin.ID, Action: "start", OperationKey: "race-key-1"})
		if e != nil {
			t.Error(e)
		}
		done <- v
	}()
	<-started
	rows, _ := s.ComponentCalls(p.DefaultSessionID)
	v, e := s.RefreshComponent(context.Background(), rows[0].ID, false)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.RefreshComponent(context.Background(), v.ID, true)
	if e != nil || v.State != "cancelled" {
		t.Fatal(v, e)
	}
	close(release)
	if v = <-done; v.State != "cancelled" {
		t.Fatal("late response overwrote terminal", v)
	}
}
func TestComponentUnknownDisableAndDeletionGuard(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "recover")
	plugin := componentFixture(t, s, p)
	s.componentRunner = func(_ context.Context, _ Plugin, action string, _ map[string]any) (json.RawMessage, error) {
		if action == "start" {
			return nil, errors.New("lost response")
		}
		return json.RawMessage(`{"job_ref":"j","status":"cancelled"}`), nil
	}
	v, e := s.SubmitComponent(context.Background(), p.DefaultSessionID, ComponentRequest{PluginID: plugin.ID, Action: "start", OperationKey: "unknown-key"})
	if e != nil || v.State != "unknown" {
		t.Fatal(v, e)
	}
	if _, e = s.DB.Exec("DELETE FROM sessions WHERE id=?", p.DefaultSessionID); e == nil {
		t.Fatal("lost in-flight reference")
	}
	no := false
	plugin.Enabled = &no
	if _, e = s.SavePlugin(plugin); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SubmitComponent(context.Background(), p.DefaultSessionID, ComponentRequest{PluginID: plugin.ID, Action: "start", OperationKey: "new-key-123"}); e == nil {
		t.Fatal("disabled submission")
	}
	v, e = s.RefreshComponent(context.Background(), v.ID, false)
	if e != nil || v.State != "cancelled" {
		t.Fatal("cannot manage accepted task", v, e)
	}
	if e = s.DeletePlugin(plugin.ID); e != nil {
		t.Fatal(e)
	}
}
func TestComponentProtocolSSEAndTLS(t *testing.T) {
	var tools atomic.Int64
	var pongs atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		var f struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&f)
		switch f.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
		case "notifications/initialized":
			w.WriteHeader(202)
		case "":
			pongs.Add(1)
			w.WriteHeader(202)
		case "tools/call":
			tools.Add(1)
			if r.Header.Get("Mcp-Session-Id") != "session" {
				t.Error("lost MCP session")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"id\":2,\"method\":\"ping\"}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\n"+"data: \"result\":{\"structuredContent\":{\"status\":\"succeeded\",\"summary\":\"完成\"}}}\n\n")
		}
	}))
	defer srv.Close()
	raw, e := componentMCPClient(context.Background(), Plugin{URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer secret"}}, "start", map[string]any{}, srv.Client())
	if e != nil || !strings.Contains(string(raw), "完成") || tools.Load() != 1 || pongs.Load() != 1 {
		t.Fatal(string(raw), e)
	}
	if _, e = componentRPCResult(strings.NewReader(`{"id":4,"result":{}}`), false, 2); e == nil {
		t.Fatal("wrong rpc id accepted")
	}
}
func TestComponentRejectsModelScopeAndStoppedRuns(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "tools")
	plugin := componentFixture(t, s, p)
	in := commInput(t, s, p)
	q := ComponentRequest{PluginID: plugin.ID, Action: "start", Arguments: map[string]any{"project_id": "other"}, OperationKey: "reserved-key"}
	if _, e := s.componentTool(context.Background(), in, "call_component", json.RawMessage(encode(q))); e == nil {
		t.Fatal("accepted reserved identity")
	}
	in.Run.State = "cancelled"
	s.saveRun(in.Run)
	if _, e := s.componentTool(context.Background(), in, "list_components", nil); e == nil {
		t.Fatal("stopped source called tool")
	}
}

// Opt-in browser fixture; no real customers, model keys or external plugin calls.
func TestComponentsPreview(t *testing.T) {
	dir := os.Getenv("PW_COMPONENT_PREVIEW")
	if dir == "" {
		t.Skip("opt-in component UI preview")
	}
	os.MkdirAll(dir, 0700)
	s := testStore(t)
	p := testProject(t, s, "组件接入验证")
	plugin := componentFixture(t, s, p)
	plugin.Component.Actions = append(plugin.Component.Actions, ComponentAction{Name: "read", Description: "读取样例", ReadOnly: true}, ComponentAction{Name: "approve", Description: "确认样例操作", RequiresOwner: true})
	if _, e := s.SavePlugin(plugin); e != nil {
		t.Fatal(e)
	}

	ex, err := s.CreateExpert("组件样例专家", "虚构验收", "只使用当前项目工具读取虚构样例。")
	if err != nil {
		t.Fatal(err)
	}
	p.ExpertIDs = []string{ex.ID}
	p.DefaultExpertID = ex.ID
	p, err = s.UpdateProject(p.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	s.componentRunner = func(_ context.Context, _ Plugin, action string, args map[string]any) (json.RawMessage, error) {
		if action == "start" {
			return json.RawMessage(`{"job_ref":"preview-job","status":"running","summary":"后台正在处理样例"}`), nil
		}
		if action == "cancel" {
			return json.RawMessage(`{"job_ref":"preview-job","status":"cancelled","summary":"已停止样例任务"}`), nil
		}
		return json.RawMessage(`{"job_ref":"preview-job","status":"succeeded","summary":"样例已完成","result":{"processed":12,"source":"fictional"}}`), nil
	}
	hash, _ := PasswordHash("isolated-components-preview")
	g := gin.New()
	web, _ := filepath.Abs("../../web/dist")
	(&API{Store: s, Config: Config{DataDir: s.Root, WebDir: web, PasswordHash: hash}}).Mount(g)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetBasicAuth("owner", "isolated-components-preview")
		g.ServeHTTP(w, r)
	}))
	defer server.Close()
	os.WriteFile(filepath.Join(dir, "preview.json"), []byte(encode(map[string]any{"url": server.URL, "project": p, "plugin": plugin.ID})), 0600)
	for {
		if _, e := os.Stat(filepath.Join(dir, "stop")); e == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestComponentRealHarness(t *testing.T) {
	path := os.Getenv("PW_REAL_CONFIG")
	if path == "" {
		t.Skip("credentialed isolated Harness acceptance")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var cfg Config
	if e = json.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	s := testStore(t)
	cfg.DataDir = s.Root
	p := testProject(t, s, "组件真实Harness验收")
	plugin := componentFixture(t, s, p)
	var called atomic.Int64
	s.componentRunner = func(_ context.Context, p Plugin, action string, args map[string]any) (json.RawMessage, error) {
		called.Add(1)
		return json.RawMessage(`{"status":"succeeded","summary":"COMPONENT_HARNESS_ACCEPTED","result":{"value":314}}`), nil
	}
	in := commInput(t, s, p)
	in.Prompt = "完成一次隔离验收：只调用workbench的list_components确认组件，再用call_component调用pluginId=" + plugin.ID + " action=start arguments={} operationKey=component-live-001。不要使用其他工具，不要改文件或创建定时，不对外发送消息。最后原样回复COMPONENT_HARNESS_ACCEPTED和结果值。"
	cfg.RuntimeDir = os.Getenv("PW_COMPONENT_RUNTIME")
	if cfg.RuntimeDir == "" {
		t.Fatal("set PW_COMPONENT_RUNTIME to isolated candidate runtime")
	}
	h := &Harness{Config: cfg, Store: s}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, e := h.Execute(ctx, in, RuntimeHooks{Interaction: func(context.Context, string, json.RawMessage) (any, error) {
		return map[string]string{"outcome": "allowed-once"}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(result.Text, "COMPONENT_HARNESS_ACCEPTED") || called.Load() != 1 {
		t.Fatalf("real tool not used: calls=%d result=%s", called.Load(), result.Text)
	}
	rows, e := s.ComponentCalls(p.DefaultSessionID)
	if e != nil || len(rows) != 1 || rows[0].State != "succeeded" {
		t.Fatal(rows, e)
	}
}

func TestComponentPollingErrorsDoNotStarveLaterTasks(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "poll")
	plugin := componentFixture(t, s, p)
	s.componentRunner = func(_ context.Context, _ Plugin, action string, _ map[string]any) (json.RawMessage, error) {
		if action == "start" {
			return nil, errors.New("lost")
		}
		return json.RawMessage(`{"status":"succeeded","summary":"recovered"}`), nil
	}
	for i := 0; i < 5; i++ {
		_, e := s.SubmitComponent(context.Background(), p.DefaultSessionID, ComponentRequest{PluginID: plugin.ID, Action: "start", OperationKey: fmt.Sprintf("poll-key-%d", i)})
		if e != nil {
			t.Fatal(e)
		}
	}
	// Four invalid bindings must not monopolize the polling batch forever.
	rows, _ := s.ComponentCalls(p.DefaultSessionID)
	for _, v := range rows[:4] {
		v.Binding = "changed"
		s.DB.Exec("UPDATE component_calls SET data=? WHERE id=?", encode(v), v.ID)
	}
	s.pollComponents(context.Background())
	s.pollComponents(context.Background())
	rows, _ = s.ComponentCalls(p.DefaultSessionID)
	n := 0
	for _, v := range rows {
		if v.State == "succeeded" {
			n++
		}
	}
	if n != 1 {
		t.Fatal("healthy task starved", rows)
	}
}

func TestComponentPurgeGuardBeforeTombstoneAndRecovery(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "lifecycle")
	plugin := componentFixture(t, s, p)
	s.componentRunner = func(context.Context, Plugin, string, map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"job_ref":"j","status":"running"}`), nil
	}
	// A recovered/older snapshot can contain a recycled session with an accepted job.
	if _, e := s.RecycleSession(p.DefaultSessionID, "test"); e != nil {
		t.Fatal(e)
	}
	ss, _ := s.rawSession(p.DefaultSessionID)
	ss.RecycledAt = 0
	s.saveSession(ss)
	v, e := s.SubmitComponent(context.Background(), ss.ID, ComponentRequest{PluginID: plugin.ID, Action: "start", OperationKey: "purge-guard-1"})
	if e != nil {
		t.Fatal(e)
	}
	ss.RecycledAt = now()
	ss.ExpiresAt = now() - 1
	s.saveSession(ss)
	if e = s.PurgeSession(ss.ID); e == nil {
		t.Fatal("accepted purge")
	}
	ss, _ = s.rawSession(ss.ID)
	if ss.Lifecycle == "purging" {
		t.Fatal("left destructive lifecycle")
	}
	if _, e = os.Stat(filepath.Join(s.Root, "purged-sessions", ss.ID+".json")); !os.IsNotExist(e) {
		t.Fatal("tombstone written before guard", e)
	}
	w := NewWorker(s, &suitePreviewExecutor{}, nil)
	if e = w.Recover(context.Background()); e != nil {
		t.Fatal("startup blocked", e)
	}
	// Even an independently restored old purge marker must not prevent recovery/polling.
	os.MkdirAll(filepath.Join(s.Root, "purged-sessions"), 0700)
	os.WriteFile(filepath.Join(s.Root, "purged-sessions", ss.ID+".json"), []byte(encode(map[string]any{"id": ss.ID})), 0600)
	if e = w.Recover(context.Background()); e != nil {
		t.Fatal("restore blocked", e)
	}
	if _, e = s.ComponentCall(v.ID); e != nil {
		t.Fatal("lost reference", e)
	}
}

func TestComponentFixedSessionConfigAndDeterministicSchedule(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "fixed-config")
	plugin := componentFixture(t, s, p)
	expert, e := s.CreateExpert("数据方法", "固定版本", "固定规则：只读事实，不推断权益。")
	if e != nil {
		t.Fatal(e)
	}
	p.ExpertIDs = []string{expert.ID}
	if _, e = s.UpdateProject(p.ID, p); e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int64
	s.componentRunner = func(_ context.Context, _ Plugin, _ string, a map[string]any) (json.RawMessage, error) {
		calls.Add(1)
		m, ok := a["workbench_method"].(map[string]any)
		if !ok || m["expert_version"] != expert.Digest || !strings.Contains(m["instructions"].(string), "固定规则") {
			t.Error("frozen expert method not executed", m)
		}
		return json.RawMessage(`{"status":"succeeded","summary":"fixed"}`), nil
	}
	cfg, e := s.SaveComponentConfig(ComponentRunConfig{SessionID: p.DefaultSessionID, PluginID: plugin.ID, PluginVersion: "1", Action: "start", Arguments: map[string]any{}, ExpertID: expert.ID, ExpertVersion: expert.Digest, RuleVersion: "canonical.clean.v1"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveComponentConfig(ComponentRunConfig{SessionID: p.DefaultSessionID, PluginID: plugin.ID, PluginVersion: "1", Action: "start"}); !errors.Is(e, ErrConflict) {
		t.Fatal("missing config CAS", e)
	}
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, SessionID: p.DefaultSessionID, ExpertID: expert.ID, Component: &cfg, Name: "固定组件任务", Instruction: "按保存配置执行", Cron: "* * * * *", Timezone: "Asia/Shanghai", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	at := time.UnixMilli(sc.NextAt)
	if _, e = s.fireSchedule(context.Background(), sc.ID, at, ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.fireSchedule(context.Background(), sc.ID, at, ""); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.ComponentCalls(p.DefaultSessionID)
	if len(rows) != 1 || rows[0].State != "ready" {
		t.Fatal("occurrence not deduplicated", rows)
	}
	if calls.Load() != 0 {
		t.Fatal("scheduler executed inline")
	}
	s.pollComponents(context.Background())
	if calls.Load() != 1 {
		t.Fatal("intent not dispatched")
	}
	if _, e = s.ChangeExpert(expert.ID, ExpertChanges{BaseDigest: expert.Digest, Metadata: map[string]string{"description": "专家已有新版本"}}, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RunComponentConfig(context.Background(), p.DefaultSessionID, "saved-config-after-expert-upgrade"); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal("saved method did not run")
	}
}
func TestComponent2000ReferencesAndOwnerConfirmation(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "2000")
	plugin := componentFixture(t, s, p)
	count := 0
	s.componentRunner = func(_ context.Context, p Plugin, _ string, a map[string]any) (json.RawMessage, error) {
		if items, ok := a["items"].([]string); ok {
			count = len(items)
		}
		return json.RawMessage(`{"status":"succeeded","summary":"accepted"}`), nil
	}
	items := []string{}
	for i := 0; i < 2000; i++ {
		items = append(items, newID())
	}
	q := ComponentRequest{PluginID: plugin.ID, Action: "start", Arguments: map[string]any{"items": items}, OperationKey: "two-thousand-uuid-references"}
	if len(encode(q.Arguments)) <= 64<<10 {
		t.Fatal("fixture does not cross old bound")
	}
	if _, e := s.SubmitComponent(context.Background(), p.DefaultSessionID, q); e != nil || count != 2000 {
		t.Fatal("2000 references rejected", count, e)
	}
	plugin.Component.Actions = append(plugin.Component.Actions, ComponentAction{Name: "approve", RequiresOwner: true})
	if _, e := s.SavePlugin(plugin); e != nil {
		t.Fatal(e)
	}
	in := commInput(t, s, p)
	q.Action = "approve"
	q.OperationKey = "forged-owner-confirmation"
	q.OwnerConfirmed = true
	before := count
	v, e := s.componentTool(context.Background(), in, "call_component", json.RawMessage(encode(q)))
	if e != nil {
		t.Fatal(e)
	}
	pending := v.(ComponentCall)
	if pending.State != "awaiting_owner" || pending.OwnerConfirmed || count != before {
		t.Fatal("model bypassed owner confirmation", pending.State)
	}
	if _, e = s.componentTool(context.Background(), in, "confirm_component", nil); e == nil {
		t.Fatal("model inferred an owner confirmation")
	}
	if _, e = s.RefreshComponent(context.Background(), pending.ID, true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfirmComponent(context.Background(), pending.ID); e == nil {
		t.Fatal("cancelled action confirmed")
	}
	q.OperationKey = "actual-owner-confirmation"
	v, e = s.componentTool(context.Background(), in, "call_component", json.RawMessage(encode(q)))
	if e != nil {
		t.Fatal(e)
	}
	pending = v.(ComponentCall)
	m, e := s.Submit(Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: newID(), Text: "确认组件 " + pending.ID[:8]})
	if e != nil {
		t.Fatal(e)
	}
	in.Run.MessageID = m.ID
	s.saveRun(in.Run)
	v, e = s.componentTool(context.Background(), in, "confirm_component", nil)
	if e != nil || v.(ComponentCall).State != "ready" {
		t.Fatal(v, e)
	}
	s.pollComponents(context.Background())
	confirmed, e := s.ComponentCall(pending.ID)
	if e != nil || confirmed.State != "succeeded" {
		t.Fatal(confirmed, e)
	}
	if _, e := s.SaveComponentConfig(ComponentRunConfig{SessionID: p.DefaultSessionID, PluginID: plugin.ID, PluginVersion: "1", Action: "approve"}); e == nil {
		t.Fatal("owner-only action became unattended")
	}
}

func TestComponentExpertReadOnlyRunBoundary(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "expert-only")
	in := commInput(t, s, p)
	in.Run.ComponentConfig = &ComponentRunConfig{Mode: "expert"}
	s.saveRun(in.Run)
	if !communicationReadOnly(in.Run) {
		t.Fatal("expert mode not read only")
	}
	for _, tool := range []string{"save_schedule", "change_calendar", "manage_cli", "run_global_cli", "confirm_component", "send_session_message"} {
		if _, e := s.appTool(in, tool, json.RawMessage(`{}`)); e == nil {
			t.Fatal("expert mode wrote through", tool)
		}
	}
	h := &Harness{Store: s, Config: Config{DataDir: s.Root, RuntimeMode: "podman", Podman: "podman", RuntimeImage: "test", RuntimeDir: "/runtime-code"}}
	args, _, _, e := h.prepare(in)
	if e != nil {
		t.Fatal(e)
	}
	root, _ := s.ProjectRoot(p.ID)
	if !strings.Contains(strings.Join(args, " "), root+":/workspace:ro") {
		t.Fatal("writable project mount")
	}
	raw, e := os.ReadFile(filepath.Join(s.Root, "runtime/projects", p.ID, "invocation-"+in.Run.ID+".yml"))
	if e != nil {
		t.Fatal(e)
	}
	for _, unsafe := range []string{"project-browser", "global-mcp-", "PW_VISUAL_BROWSER"} {
		if strings.Contains(string(raw), unsafe) {
			t.Fatal(unsafe)
		}
	}
	if !strings.Contains(string(raw), "PW_COMMUNICATION_READONLY: \"true\"") {
		t.Fatal("runtime tool filtering missing")
	}
}

func TestComponentExpertAdmissionAndSchedulePinsMethod(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "expert-snapshot")
	plugin := componentFixture(t, s, p)
	plugin.Component.Actions = []ComponentAction{{Name: "read", ReadOnly: true}}
	if _, e := s.SavePlugin(plugin); e != nil {
		t.Fatal(e)
	}
	ex, e := s.CreateExpert("只读方法", "固定方法", "EXPERT_METHOD_FIXED_729")
	if e != nil {
		t.Fatal(e)
	}
	p.ExpertIDs = []string{ex.ID}
	if _, e = s.UpdateProject(p.ID, p); e != nil {
		t.Fatal(e)
	}
	cfg, e := s.SaveComponentConfig(ComponentRunConfig{Mode: "expert", Goal: "解释读取结果", SessionID: p.DefaultSessionID, PluginID: plugin.ID, PluginVersion: "1", Action: "read", Arguments: map[string]any{}, ExpertID: ex.ID, ExpertVersion: ex.Digest})
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.RunComponentConfig(context.Background(), p.DefaultSessionID, "expert-manual-729")
	if e != nil {
		t.Fatal(e)
	}
	m := v.(Message)
	r, e := s.Run(m.RunID)
	if e != nil {
		t.Fatal(e)
	}
	if !communicationReadOnly(r) || r.ExpertDigest != ex.Digest || !strings.Contains(m.Incoming.Text, "EXPERT_METHOD_FIXED_729") || m.ComponentConfig == nil {
		t.Fatal("manual expert snapshot lost")
	}
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, SessionID: p.DefaultSessionID, Component: &cfg, Name: "expert schedule", Cron: "* * * * *", Timezone: "Asia/Shanghai", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := s.fireSchedule(context.Background(), sc.ID, time.UnixMilli(sc.NextAt), "")
	if e != nil {
		t.Fatal(e)
	}
	r, e = s.Run(receipt.RunID)
	if e != nil {
		t.Fatal(e)
	}
	m, e = s.Message(r.MessageID)
	if e != nil {
		t.Fatal(e)
	}
	if !communicationReadOnly(r) || m.ComponentConfig == nil || r.ExpertDigest != ex.Digest || !strings.Contains(r.Text, "EXPERT_METHOD_FIXED_729") {
		t.Fatal("scheduled expert snapshot lost")
	}
}

func TestComponentSubmissionIsNotPolledBeforeFirstResponse(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "首次提交窗口")
	plugin := componentFixture(t, s, p)
	entered := make(chan struct{})
	release := make(chan struct{})
	var lookups atomic.Int64
	s.componentRunner = func(_ context.Context, _ Plugin, action string, _ map[string]any) (json.RawMessage, error) {
		if action == "start" {
			close(entered)
			<-release
			return json.RawMessage(`{"status":"succeeded","summary":"慢但有效的首次结果"}`), nil
		}
		lookups.Add(1)
		return json.RawMessage(`{"status":"not_found"}`), nil
	}
	done := make(chan ComponentCall, 1)
	go func() {
		v, e := s.SubmitComponent(context.Background(), p.DefaultSessionID, ComponentRequest{PluginID: plugin.ID, Action: "start", OperationKey: "first-response-reservation"})
		if e != nil {
			t.Error(e)
		}
		done <- v
	}()
	<-entered
	s.pollComponents(context.Background())
	if lookups.Load() != 0 {
		t.Fatal("background lookup raced in-flight initial response")
	}
	close(release)
	v := <-done
	if v.State != "succeeded" {
		t.Fatal("valid initial result discarded", v.State)
	}
}
