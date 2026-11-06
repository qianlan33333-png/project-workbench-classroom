package workbench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestWorkflowRealHarness(t *testing.T) {
	path := os.Getenv("PW_REAL_CONFIG")
	if path == "" {
		t.Skip("credentialed isolated Harness acceptance")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	cfg.DataDir, cfg.RuntimeDir = s.Root, os.Getenv("PW_COMPONENT_RUNTIME")
	if cfg.RuntimeDir == "" {
		t.Fatal("set PW_COMPONENT_RUNTIME to candidate runtime")
	}
	p := testProject(t, s, "工作轮次真实模型验收")
	c := ComponentRunConfig{SessionID: p.DefaultSessionID}
	r := WorkflowRun{ID: newID(), SessionID: c.SessionID, ProjectID: p.ID, Config: c, Name: "隔离数据核对",
		State: "succeeded", At: now(), FinishedAt: now(), Steps: []WorkflowStep{{Name: "核对发布", State: "succeeded", Detail: "已发布 314159 人，验收标识 WORKFLOW-CHECK-2718"}}}
	if _, err := s.DB.Exec("INSERT INTO workflow_runs VALUES(?,?,?,?,?,?,?)", r.ID, r.SessionID, "real-test", nil, 0, r.At, encode(r)); err != nil {
		t.Fatal(err)
	}
	if err := s.queueWorkflowSummary(r); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(s, &Harness{Config: cfg, Store: s}, nil)
	ctx, stop := context.WithTimeout(context.Background(), 4*time.Minute)
	defer stop()
	run, err := w.claim()
	if err != nil || run.Kind != workflowSummaryKind {
		t.Fatalf("could not claim summary: %+v %v", run, err)
	}
	w.execute(ctx, run)
	final, err := s.Run(run.ID)
	if err != nil || final.State != "completed" || (!strings.Contains(final.Result, "314159") && !strings.Contains(final.Result, "314,159")) {
		t.Fatalf("real feedback not grounded: %s %s %v", final.State, final.Result, err)
	}
	var events int
	if err := s.DB.QueryRow("SELECT count(*) FROM harness_events WHERE session_id=?", r.SessionID).Scan(&events); err != nil || events != 0 {
		t.Fatal("summary polluted native conversation events", events, err)
	}
	m, err := s.Submit(Incoming{Source: "web", ActorID: "owner", SessionID: r.SessionID, WorkflowID: r.ID,
		EventID: "real-workflow-question", Text: "只读验收：先调用 workbench 的 read_workflow 工具核对我引用的轮次。然后原样回答其发布人数和验收标识。只使用这一个工具。"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Route(ctx, m.ID, nil); err != nil {
		t.Fatal(err)
	}
	run, err = w.claim()
	if err != nil {
		t.Fatal(err)
	}
	w.execute(ctx, run)
	final, err = s.Run(run.ID)
	if err != nil || final.State != "completed" || !strings.Contains(final.Result, "WORKFLOW-CHECK-2718") {
		t.Fatalf("real question failed: %s %s %v", final.State, final.Result, err)
	}
	if err := s.DB.QueryRow("SELECT count(*) FROM events WHERE run_id=? AND type='harness.tool/call' AND data LIKE '%read_workflow%'", run.ID).Scan(&events); err != nil || events == 0 {
		t.Fatal("no real workflow tool call", events, err)
	}
	t.Log("Real Harness returned receipt-grounded summary and read the quoted workflow through the actual workbench tool.")
}

func TestWorkflowBrowserPreview(t *testing.T) {
	dir := os.Getenv("PW_WORKFLOW_PREVIEW")
	if dir == "" {
		t.Skip("opt-in isolated browser fixture")
	}
	s := testStore(t)
	p := testProject(t, s, "工作轮次浏览器验收")
	plugin := workflowFixture(t, s, p)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	session := p.DefaultSessionID
	c := ComponentRunConfig{SessionID: session, PluginID: plugin.ID, PluginVersion: plugin.Component.Version, Action: "sync_contexts", Mode: "program"}
	var err error
	if c, err = s.SaveComponentConfig(c); err != nil {
		t.Fatal(err)
	}
	var retries atomic.Int64
	s.componentRunner = func(_ context.Context, _ Plugin, action string, _ map[string]any) (json.RawMessage, error) {
		switch action {
		case "sync_contexts":
			return json.RawMessage(`{"status":"queued","job_ref":"browser-job"}`), nil
		case "retry":
			retries.Add(1)
			return json.RawMessage(`{"status":"queued","job_ref":"browser-job"}`), nil
		case "status":
			if retries.Load() == 0 {
				return json.RawMessage(`{"status":"failed","job_ref":"browser-job","summary":"源暂时不可读"}`), nil
			}
			return json.RawMessage(`{"status":"succeeded","job_ref":"browser-job","result":{"published":true,"generation":"browser-job","users":25126,"published_at":1793938896564}}`), nil
		}
		return nil, ErrNotFound
	}
	r, _, err := s.beginWorkflow(ctx, c, "browser-schedule-123", "schedule", now()-10000)
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorker(s, &workflowPreviewExecutor{store: s}, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(180 * time.Millisecond)
		defer ticker.Stop()
		for ctx.Err() == nil {
			_, _ = s.DB.Exec("UPDATE workflow_runs SET next_at=1 WHERE next_at>0")
			_, _ = s.DB.Exec("UPDATE component_calls SET next_poll=0")
			s.pollWorkflows(ctx)
			s.pollComponents(ctx)
			rows, _ := listJSON[Message](s.DB, "SELECT data FROM inbox WHERE state='unrouted'")
			for _, m := range rows {
				_, _ = s.Route(ctx, m.ID, nil)
			}
			if run, e := w.claim(); e == nil {
				w.execute(ctx, run)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { stop(); <-done }()
	hash, _ := PasswordHash("workflow-browser-fixture")
	web, _ := filepath.Abs("../../web/dist")
	g := gin.New()
	(&API{Store: s, Config: Config{DataDir: s.Root, WebDir: web, PasswordHash: hash}}).Mount(g)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetBasicAuth("owner", "workflow-browser-fixture")
		g.ServeHTTP(w, r)
	}))
	defer server.Close()
	if err := os.WriteFile(filepath.Join(dir, "preview.json"), []byte(encode(map[string]any{"url": server.URL, "project": p.ID, "session": session, "workflow": r.ID})), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

type workflowPreviewExecutor struct{ store *Store }

func (e *workflowPreviewExecutor) Execute(_ context.Context, in RuntimeInput, _ RuntimeHooks) (RuntimeResult, error) {
	if in.Run.Kind == workflowSummaryKind {
		if !in.Routing {
			return RuntimeResult{}, ErrConflict
		}
		if strings.Contains(in.Prompt, "25126") {
			return RuntimeResult{Text: "本轮已发布 25,126 人，已经核对发布回执。"}, nil
		}
		return RuntimeResult{Text: "这轮源暂时不可读，可以继续原同步任务。"}, nil
	}
	if in.Run.WorkflowID != "" && strings.Contains(in.Prompt, "本会话工作轮次证据") {
		return RuntimeResult{Text: "已读取所引用的轮次。"}, nil
	}
	return RuntimeResult{Text: "普通聊天回应。"}, nil
}
func (e *workflowPreviewExecutor) Cleanup(context.Context, string) error { return nil }
