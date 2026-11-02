package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskDraftAndRunAreDifferent(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "任务测试")
	task, e := s.SaveTask(ProjectTask{ProjectID: p.ID, Title: "验证真实需求", Description: "为什么：先验证再开发", Operation: "create-one"})
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.SaveTask(ProjectTask{ProjectID: p.ID, Title: "重试", Operation: "create-one"})
	if e != nil || again.ID != task.ID {
		t.Fatal(e, again)
	}
	d, e := s.PrepareTask(task.ID, false, false)
	if e != nil {
		t.Fatal(e)
	}
	if !d.Session.Draft || d.Session.ExpertID != "general" {
		t.Fatal(d)
	}
	runs, _ := s.RunPage(d.Session.ID, "")
	if len(runs) != 0 {
		t.Fatal("prepare executed")
	}
	if e = s.SaveTaskDraft(task.ID, "手动删改", d.Task.DraftVersion); e != nil {
		t.Fatal(e)
	}
	d2, e := s.PrepareTask(task.ID, false, false)
	if e != nil || d2.Session.ID != d.Session.ID || d2.Text != "手动删改" {
		t.Fatal(e, d2)
	}
	task, _ = s.Task(task.ID)
	if task.State != "pending" {
		t.Fatal(task)
	}
	task.Description = "新背景"
	task, e = s.SaveTask(task)
	if e != nil {
		t.Fatal(e)
	}
	d3, _ := s.PrepareTask(task.ID, false, false)
	if !d3.Changed || d3.Text != "手动删改" {
		t.Fatal(d3)
	}
	m := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: d.Session.ID, Text: "执行"})
	task, _ = s.Task(task.ID)
	if task.State != "in_progress" {
		t.Fatal(task)
	}
	r, _ := s.Run(m.RunID)
	r.State = "completed"
	_ = s.saveRun(r)
	task, _ = s.Task(task.ID)
	if task.State != "in_progress" {
		t.Fatal("run finished task")
	}
	if e = s.DeleteTask(task.ID, task.Version-1); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.DeleteTask(task.ID, task.Version); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Session(d.Session.ID); e != nil {
		t.Fatal("task deletion removed conversation", e)
	}
}
func TestDecisionDurabilityIdempotencyAndScope(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "决定测试")
	personal, _ := s.PersonalSpace()
	ss, _ := s.Session(personal.DefaultSessionID)
	in := RuntimeInput{Project: personal, Session: ss, Run: Run{ID: newID(), Text: "考虑一下"}}
	if _, e := s.appTool(in, "read_run", json.RawMessage(`{"runId":"x"}`)); e == nil {
		t.Fatal("raw project read allowed")
	}
	if _, e := s.appTool(in, "create_decided_task", json.RawMessage(`{"task":{"projectId":"`+p.ID+`","title":"建议"}}`)); e == nil {
		t.Fatal("discussion created task")
	}
	d, e := s.ProposeDecision(in, PersonalDecision{Question: "先验证哪件事", Reason: "先获得反馈", Options: []DecisionOption{{Label: "试用", Tradeoff: "花30分钟", Recommended: true}, {Label: "暂时观察", Tradeoff: "延迟反馈"}}})
	if e != nil {
		t.Fatal(e)
	}
	d, e = s.AnswerDecision(d.ID, d.Version, "A", "")
	if e != nil || d.TaskID != "" {
		t.Fatal(e, d)
	}
	again, e := s.AnswerDecision(d.ID, 1, "A", "")
	if e != nil || again.TaskID != d.TaskID {
		t.Fatal(e)
	}
	tasks, _ := s.Tasks(p.ID)
	if len(tasks) != 0 {
		t.Fatal(tasks)
	}
	// A decision is personal history only, including after retry.
	ms, _ := s.Memories()
	if len(ms) != 0 {
		t.Fatal("choice became preference")
	}
	s.dispatchDecisions()
	s.dispatchDecisions()
	var count int
	_ = s.DB.QueryRow("SELECT count(*) FROM inbox WHERE event_id=?", "decision-answer:"+d.ID).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	in.Run.ID = newID()
	d, e = s.ProposeDecision(in, PersonalDecision{Question: "继续吗", Reason: "验证", Options: []DecisionOption{{Label: "做", Tradeoff: "成本", Recommended: true}, {Label: "等", Tradeoff: "延迟"}}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SaveMemory(PersonalMemory{Text: "近期只有半小时", Kind: "temporary", ExpiresAt: now() + 3600000})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AnswerDecision(d.ID, 1, "A", ""); e == nil {
		t.Fatal("stale card accepted")
	}
}

type summaryExecutor struct {
	calls   int
	hook    func()
	prompts []string
	result  *RuntimeResult
	err     error
}

func (f *summaryExecutor) Execute(_ context.Context, in RuntimeInput, _ RuntimeHooks) (RuntimeResult, error) {
	f.calls++
	f.prompts = append(f.prompts, in.Prompt)
	if f.hook != nil {
		f.hook()
		f.hook = nil
	}
	if f.err != nil {
		return RuntimeResult{}, f.err
	}
	if f.result != nil {
		return *f.result, nil
	}
	return RuntimeResult{Text: `{"description":"已核对：保持长期目标，尚未完成的验证继续保留。","change":"记录一项实质变化"}`}, nil
}
func (f *summaryExecutor) Cleanup(context.Context, string) error { return nil }
func TestProjectContextWindowAndOwnerCAS(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "说明")
	at := now()
	v := rollingChanges([]ContextChange{{At: at - recentWindow}, {At: at - recentWindow + 1}, {At: at + 1}}, at)
	if len(v) != 1 {
		t.Fatal(v)
	}
	c, e := s.ProjectContext(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CorrectContext(p.ID, "人工结论", c.Version); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CorrectContext(p.ID, "旧草稿", c.Version); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	s.markContext(p.ID, true)
	cat, _ := s.ContextCatalog()
	if strings.Contains(cat[0].Description, "人工结论") {
		t.Fatal("invalid source leaked")
	}
}
func TestPersonalRuntimeIsRestricted(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	ss, _ := s.Session(p.DefaultSessionID)
	h := &Harness{Store: s, Config: Config{DataDir: s.Root, RuntimeMode: "podman", Podman: "podman", RuntimeImage: "test", RuntimeDir: "/runtime-code"}}
	args, _, _, e := h.prepare(RuntimeInput{Project: p, Session: ss, Run: Run{ID: "restricted"}})
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(args, " ")
	for _, deny := range []string{"/tools:", "6081:", "/capabilities/"} {
		if strings.Contains(joined, deny) {
			t.Fatal("unsafe mount", joined)
		}
	}
	b, e := os.ReadFile(filepath.Join(s.Root, "runtime/projects", personalProjectID, "invocation-restricted.yml"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "project-browser") || strings.Contains(string(b), "global-mcp-") {
		t.Fatal(string(b))
	}
	if !strings.Contains(string(b), "PW_PERSONAL: \"true\"") {
		t.Fatal(string(b))
	}
	ps, _ := s.Projects()
	if len(ps) != 0 {
		t.Fatal("personal project leaked")
	}
}
func TestForgetSurvivesRestoredMemoryAndNegativeConsent(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "授权测试")
	personal, _ := s.PersonalSpace()
	ss, _ := s.Session(personal.DefaultSessionID)
	in := RuntimeInput{Project: personal, Session: ss, Run: Run{ID: newID(), Text: "不要创建任务，只讨论"}}
	if _, e := s.personalTool(in, "create_decided_task", json.RawMessage(`{"task":{"projectId":"`+p.ID+`","title":"未授权"}}`)); e == nil {
		t.Fatal("negation authorized task")
	}
	m, e := s.SaveMemory(PersonalMemory{Text: "旧目标", Kind: "goal"})
	if e != nil {
		t.Fatal(e)
	}
	old := m
	if e = s.ForgetMemory(m.ID, m.Version); e != nil {
		t.Fatal(e)
	}
	_, _ = s.DB.Exec("UPDATE personal_memories SET data=? WHERE id=?", encode(old), old.ID)
	ms, _ := s.Memories()
	if len(ms) != 0 {
		t.Fatal("restore resurrected memory")
	}
	if e = s.ApplyForgottenMemories(); e != nil {
		t.Fatal(e)
	}
	var raw string
	_ = s.DB.QueryRow("SELECT data FROM personal_memories WHERE id=?", m.ID).Scan(&raw)
	if strings.Contains(raw, "旧目标") {
		t.Fatal("ledger did not scrub restored body")
	}
}
func TestTaskChangeEntersRecentWithoutModel(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "直接动态")
	task, e := s.SaveTask(ProjectTask{ProjectID: p.ID, Title: "观察", Operation: newID()})
	if e != nil {
		t.Fatal(e)
	}
	c, _ := s.ProjectContext(p.ID)
	if len(c.Recent) != 1 || !strings.Contains(c.Recent[0].Text, "观察") {
		t.Fatal(c)
	}
	d, _ := s.PrepareTask(task.ID, false, false)
	submitRoute(t, s, Incoming{Source: "web", SessionID: d.Session.ID, EventID: newID(), Text: "开始"})
	c, _ = s.ProjectContext(p.ID)
	if !strings.Contains(c.Recent[len(c.Recent)-1].Text, "in_progress") {
		t.Fatal(c.Recent)
	}
}

func TestProjectChangesDoNotEnqueueHiddenSummaries(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "增量")
	rev, e := s.SaveFile(p.ID, "资料.md", []byte("初始资料"), "")
	if e != nil {
		t.Fatal(e)
	}
	if flag, _ := s.Setting("personal_context_reset"); flag == "true" {
		t.Fatal("new evidence reset history")
	}
	if _, e = s.SaveFile(p.ID, "资料.md", []byte("人工修正"), rev.After); e != nil {
		t.Fatal(e)
	}
	if flag, _ := s.Setting("personal_context_reset"); flag != "true" {
		t.Fatal("stale native context")
	}
	var n int
	if e = s.DB.QueryRow("SELECT count(*) FROM context_jobs").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
func TestDailyFailureKeepsPreviousAndRejectsChangedEvidence(t *testing.T) {
	s := testStore(t)
	_ = s.SetSetting("today_enabled", "true")
	prior := DailyContent{Day: "2000-01-01", Version: 1, Text: "保留已有建议", State: "ready", UpdatedAt: 1}
	_, _ = s.DB.Exec("INSERT INTO daily_contents VALUES(?,?)", prior.Day, encode(prior))
	_ = s.RequestToday()
	f := &summaryExecutor{err: errors.New("provider unavailable")}
	w := NewWorker(s, f, nil)
	if !w.maintainToday(context.Background()) {
		t.Fatal("did not attempt daily")
	}
	d, _ := getJSON[DailyContent](s.DB, "SELECT data FROM daily_contents WHERE day=?", shanghaiDay())
	if d.State != "failed" || d.Text != prior.Text {
		t.Fatal(d)
	}
	f.err = nil
	f.result = &RuntimeResult{Text: `{"text":"基于过期依据的建议","decision":null}`}
	f.hook = func() {
		_, e := s.SaveMemory(PersonalMemory{Kind: "goal", Text: "现在的目标已经改变"})
		if e != nil {
			t.Fatal(e)
		}
	}
	_ = s.RequestToday()
	w.maintainToday(context.Background())
	d, _ = getJSON[DailyContent](s.DB, "SELECT data FROM daily_contents WHERE day=?", shanghaiDay())
	if d.State != "failed" || d.Text != prior.Text || !strings.Contains(d.Error, "依据已变化") {
		t.Fatal(d)
	}
}

type yieldingSummaryExecutor struct {
	started  chan struct{}
	cleaning chan struct{}
	release  chan struct{}
}

func (f *yieldingSummaryExecutor) Execute(ctx context.Context, _ RuntimeInput, _ RuntimeHooks) (RuntimeResult, error) {
	close(f.started)
	<-ctx.Done()
	return RuntimeResult{}, ctx.Err()
}
func (f *yieldingSummaryExecutor) Cleanup(ctx context.Context, _ string) error {
	close(f.cleaning)
	select {
	case <-f.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("maintenance did not yield promptly")
	}
}
func TestDailyYieldRetainsRefreshRequest(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	_ = s.SetSetting("today_enabled", "true")
	_ = s.RequestToday()
	f := &yieldingSummaryExecutor{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	w := NewWorker(s, f, nil)
	done := make(chan struct{})
	go func() { w.maintainToday(context.Background()); close(done) }()
	waitSignal(t, f.started)
	submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: p.DefaultSessionID, Text: "先聊一下"})
	waitSignal(t, f.cleaning)
	close(f.release)
	waitSignal(t, done)
	req, _ := s.Setting("today_refresh")
	if req == "" {
		t.Fatal("lost refresh request")
	}
	var n int
	_ = s.DB.QueryRow("SELECT count(*) FROM daily_contents").Scan(&n)
	if n != 0 {
		t.Fatal("cancelled daily published as failure")
	}
}
