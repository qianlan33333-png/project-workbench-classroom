package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func followupInput(t *testing.T, s *Store) RuntimeInput {
	t.Helper()
	p, e := s.PersonalSpace()
	if e != nil {
		t.Fatal(e)
	}
	m := submitRoute(t, s, Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: newID(), Text: "请持续跟进，只读核对项目情况"})
	r, _ := s.Run(m.RunID)
	ss, _ := s.Session(r.SessionID)
	return RuntimeInput{Project: p, Session: ss, Run: r}
}
func followupRequest() FollowupChange {
	return FollowupChange{Key: "feedback", Title: "核对反馈进展", Goal: "确认原反馈已经解决", About: "只读查询，保留结果依据", Action: "save"}
}
func followupDecision() *PersonalDecision {
	return &PersonalDecision{Question: "先核对哪一项？", Reason: "先验证主要问题", Options: []DecisionOption{{Label: "导出", Tradeoff: "先确认主要问题", Recommended: true}, {Label: "显示", Tradeoff: "主要问题稍后确认"}}}
}
func TestFollowupCreateCASAndScope(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	f, e := s.SaveFollowup(in, followupRequest())
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.SaveFollowup(in, followupRequest())
	if e != nil || again.ID != f.ID {
		t.Fatal(again, e)
	}
	q := followupRequest()
	q.ID = f.ID
	q.Version = f.Version - 1
	if _, e = s.SaveFollowup(in, q); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	p := testProject(t, s, "只读边界")
	in.Project = p
	if _, e = s.SaveFollowup(in, q); e == nil {
		t.Fatal("project run obtained personal followup access")
	}
}
func TestFollowupDecisionResumeExactlyOnceAndCancel(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	q := followupRequest()
	q.Action = "ask"
	q.Decision = followupDecision()
	f, e := s.SaveFollowup(in, q)
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.AnswerDecision(f.DecisionID, 1, "A", "")
	if e != nil {
		t.Fatal(e)
	}
	in.Run.State = "completed"
	if e = s.saveRun(in.Run); e != nil {
		t.Fatal(e)
	}
	s.dispatchDecisions()
	s.dispatchDecisions()
	f, _ = s.Followup(f.ID)
	if f.State != "queued" {
		t.Fatal(f)
	}
	r, e := s.Run(f.LastRunID)
	if e != nil || r.FollowupID != f.ID || r.SessionID != in.Session.ID {
		t.Fatal(r, e)
	}
	if _, e = s.AnswerDecision(d.ID, 1, "A", ""); e != nil {
		t.Fatal(e)
	}
	var n int
	_ = s.DB.QueryRow("SELECT count(*) FROM runs WHERE data->>'$.followupId'=?", f.ID).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	// A later successful completion retains the consumed decision receipt.
	in.Run = r
	if _, e = s.SaveFollowup(in, FollowupChange{ID: f.ID, Version: f.Version, Action: "complete", Evidence: "已核对当前事实"}); e != nil {
		t.Fatal(e)
	}
	r.State = "completed"
	if e = s.saveRun(r); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AnswerDecision(d.ID, 1, "A", ""); e != nil {
		t.Fatal("consumed answer lost idempotence", e)
	}
	// Use another waiting item to exercise cancellation independently of completion.
	in = followupInput(t, s)
	q = followupRequest()
	q.Key = "cancel-target"
	f, e = s.SaveFollowup(in, q)
	if e != nil {
		t.Fatal(e)
	}
	f, e = s.CancelFollowup(f.ID, f.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ResumeFollowup(f.ID, f.Version); e == nil {
		t.Fatal("cancelled followup revived")
	}
}
func TestFollowupDueOnceSurvivesStoreAndBounds(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	q := followupRequest()
	q.Action = "review"
	q.At = now() + 120000
	f, e := s.SaveFollowup(in, q)
	if e != nil {
		t.Fatal(e)
	}
	in.Run.State = "completed"
	if e = s.saveRun(in.Run); e != nil {
		t.Fatal(e)
	}
	// Reopen against the same durable data without rebuilding in-memory tasks.
	s2, e := OpenStore(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	defer s2.DB.Close()
	if e = s2.FireSchedules(time.UnixMilli(q.At + 1)); e != nil {
		t.Fatal(e)
	}
	if e = s2.FireSchedules(time.UnixMilli(q.At + 1)); e != nil {
		t.Fatal(e)
	}
	f, _ = s2.Followup(f.ID)
	if f.ReviewCount != 1 || f.State != "queued" {
		t.Fatal(f)
	}
	r, e := s2.Run(f.LastRunID)
	if e != nil || r.FollowupID != f.ID {
		t.Fatal(r, e)
	}
	in.Run = r
	q.ID = f.ID
	q.Version = f.Version
	q.At = now() + 120000
	_, _ = s2.DB.Exec("UPDATE personal_followups SET data=json_set(data,'$.reviewCount',3) WHERE id=?", f.ID)
	if _, e = s2.SaveFollowup(in, q); e == nil {
		t.Fatal("review cap bypassed")
	}
}
func TestFollowupCompletionFailureAndNoImplicitCompletion(t *testing.T) {
	for _, state := range []string{"completed", "failed", "interrupted"} {
		t.Run(state, func(t *testing.T) {
			s := testStore(t)
			in := followupInput(t, s)
			f, e := s.SaveFollowup(in, followupRequest())
			if e != nil {
				t.Fatal(e)
			}
			q := FollowupChange{ID: f.ID, Version: f.Version, Action: "complete", Evidence: "只读接口确认原反馈状态已解决"}
			if _, e = s.SaveFollowup(in, q); e != nil {
				t.Fatal(e)
			}
			in.Run.State = state
			if e = s.saveRun(in.Run); e != nil {
				t.Fatal(e)
			}
			f, _ = s.Followup(f.ID)
			want := "needs_review"
			if state == "completed" {
				want = "completed"
			}
			if f.State != want {
				t.Fatal(f)
			}
		})
	}
}
func TestFollowupReadOnlyToolsAndStaleRun(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	f, _ := s.SaveFollowup(in, followupRequest())
	in.Run.State = "completed"
	_ = s.saveRun(in.Run)
	f, _ = s.Followup(f.ID)
	f, e := s.ResumeFollowup(f.ID, f.Version)
	if e != nil {
		t.Fatal(e)
	}
	r, _ := s.Run(f.LastRunID)
	in.Run = r
	for _, tool := range []string{"run_global_cli", "remember_explicit", "change_calendar", "save_schedule", "manage_cli"} {
		if _, e = s.appTool(in, tool, json.RawMessage(`{}`)); e == nil {
			t.Fatal(tool, "allowed")
		}
	}
	if _, e = s.appTool(in, "project_contexts", json.RawMessage(`{}`)); e != nil {
		t.Fatal(e)
	}
	f, _ = s.CancelFollowup(f.ID, f.Version)
	if _, e = s.appTool(in, "read_project_source", json.RawMessage(`{}`)); e == nil {
		t.Fatal("cancelled run still has access")
	}
}
func TestFollowupRuntimeRestrictionsAndWaitRelease(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	q := followupRequest()
	q.Action = "ask"
	q.Decision = followupDecision()
	f, e := s.SaveFollowup(in, q)
	if e != nil {
		t.Fatal(e)
	}
	w := NewWorker(s, &summaryExecutor{}, nil)
	r, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	w.execute(context.Background(), r)
	var locks int
	_ = s.DB.QueryRow("SELECT count(*) FROM project_locks").Scan(&locks)
	if locks != 0 {
		t.Fatal("waiting decision retained execution lock")
	}
	f, _ = s.Followup(f.ID)
	if f.State != "waiting_decision" {
		t.Fatal(f)
	}
	_, _ = s.AnswerDecision(f.DecisionID, 1, "A", "")
	s.dispatchDecisions()
	f, _ = s.Followup(f.ID)
	in.Run, _ = s.Run(f.LastRunID)
	h := &Harness{Store: s, Config: Config{DataDir: s.Root, RuntimeMode: "podman", Podman: "podman", RuntimeImage: "test", RuntimeDir: "/runtime-code"}}
	capabilityFixture(t, s)
	if _, e = s.SavePlugin(Plugin{Name: "Must not enter autonomous review", Transport: "http", URL: "https://example.invalid/mcp"}); e != nil {
		t.Fatal(e)
	}
	args, _, _, e := h.prepare(in)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(strings.Join(args, " "), "/tools/state") {
		t.Fatal("tools leaked")
	}
	raw, e := os.ReadFile(filepath.Join(s.Root, "runtime/projects", personalProjectID, "invocation-"+in.Run.ID+".yml"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "global-mcp-") || !strings.Contains(string(raw), `PW_FOLLOWUP: "true"`) {
		t.Fatal(string(raw))
	}
	if _, e = h.runPersonalCLI(context.Background(), in, json.RawMessage(`{}`)); e == nil {
		t.Fatal("direct CLI path allowed")
	}
}

func TestFollowupMutationAndManualResumeAreIdempotent(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	f, e := s.SaveFollowup(in, followupRequest())
	if e != nil {
		t.Fatal(e)
	}
	q := FollowupChange{ID: f.ID, Version: f.Version, Action: "review", At: now() + 120000, About: "核对新增资料"}
	f, e = s.SaveFollowup(in, q)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.SaveFollowup(in, q)
	if e != nil || again.Version != f.Version || again.ScheduleID != f.ScheduleID {
		t.Fatal(again, e)
	}
	if _, e = s.ResumeFollowup(f.ID, 0); !errors.Is(e, ErrConflict) {
		t.Fatal("zero version accepted", e)
	}
	in.Run.State = "completed"
	if e = s.saveRun(in.Run); e != nil {
		t.Fatal(e)
	}
	f, _ = s.Followup(f.ID)
	version := f.Version
	f, e = s.ResumeFollowup(f.ID, version)
	if e != nil {
		t.Fatal(e)
	}
	again, e = s.ResumeFollowup(f.ID, version)
	if e != nil || again.LastRunID != f.LastRunID {
		t.Fatal(again, e)
	}
	var n int
	_ = s.DB.QueryRow("SELECT count(*) FROM runs WHERE data->>'$.followupId'=?", f.ID).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
func TestFollowupDeferredAndStaleDecisionsRemainActionable(t *testing.T) {
	for _, answer := range []string{"defer", "stale"} {
		t.Run(answer, func(t *testing.T) {
			s := testStore(t)
			in := followupInput(t, s)
			q := followupRequest()
			q.Action = "ask"
			q.Decision = followupDecision()
			f, e := s.SaveFollowup(in, q)
			if e != nil {
				t.Fatal(e)
			}
			if answer == "stale" {
				if _, e = s.SaveMemory(PersonalMemory{Text: "本人更新了临时情况", Kind: "temporary"}); e != nil {
					t.Fatal(e)
				}
				_, e = s.AnswerDecision(f.DecisionID, 1, "A", "")
				if e == nil {
					t.Fatal("old evidence accepted")
				}
			} else {
				if _, e = s.AnswerDecision(f.DecisionID, 1, answer, ""); e != nil {
					t.Fatal(e)
				}
			}
			s.dispatchDecisions()
			f, _ = s.Followup(f.ID)
			if f.State != "needs_review" || f.LastRunID != in.Run.ID {
				t.Fatal(f)
			}
		})
	}
}
func TestFollowupChannelAnswerDedupAndOwnerScope(t *testing.T) {
	s := testStore(t)
	c := channel(t, s, "feishu")
	m := submitRoute(t, s, incoming(c, "请跟进反馈，需要决定时问我"))
	r, _ := s.Run(m.RunID)
	p, _ := s.PersonalSpace()
	ss, _ := s.Session(r.SessionID)
	in := RuntimeInput{Project: p, Session: ss, Run: r}
	q := followupRequest()
	q.Action = "ask"
	q.Decision = followupDecision()
	f, e := s.SaveFollowup(in, q)
	if e != nil {
		t.Fatal(e)
	}
	r.State = "completed"
	if e = s.saveRun(r); e != nil {
		t.Fatal(e)
	}
	w := NewWorker(s, &summaryExecutor{}, nil)
	text := "回答 #D" + f.DecisionID[:8] + " A"
	for range 2 {
		a, e := s.Submit(incoming(c, text))
		if e != nil {
			t.Fatal(e)
		}
		handled, e := w.handleControl(a)
		if e != nil || !handled {
			t.Fatal(handled, e)
		}
	}
	f, _ = s.Followup(f.ID)
	var n int
	_ = s.DB.QueryRow("SELECT count(*) FROM runs WHERE data->>'$.followupId'=?", f.ID).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	forged := Message{Incoming: incoming(c, text)}
	forged.Incoming.ActorID = "someone-else"
	if _, e = w.handleControl(forged); e == nil {
		t.Fatal("unbound actor answered")
	}
	if !strings.Contains(s.followupReply(in.Run, "进展"), "回答 #D") { // answered questions are not re-sent
		f2q := followupRequest()
		f2q.Key = "second"
		f2q.Action = "ask"
		f2q.Decision = followupDecision()
		in = followupInput(t, s)
		f2, e := s.SaveFollowup(in, f2q)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(s.followupReply(in.Run, "进展"), f2.DecisionID[:8]) {
			t.Fatal("missing stable channel answer command")
		}
	}
}
func TestFollowupCancellationRacesDueDispatch(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	q := followupRequest()
	q.Action = "review"
	q.At = now() + 120000
	f, e := s.SaveFollowup(in, q)
	if e != nil {
		t.Fatal(e)
	}
	in.Run.State = "completed"
	if e = s.saveRun(in.Run); e != nil {
		t.Fatal(e)
	}
	f, _ = s.Followup(f.ID)
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() { defer wg.Done(); _, e := s.CancelFollowup(f.ID, f.Version); errs <- e }()
	go func() { defer wg.Done(); errs <- s.FireSchedules(time.UnixMilli(q.At + 1)) }()
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil && !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	f, _ = s.Followup(f.ID)
	if f.State != "cancelled" {
		if _, e = s.CancelFollowup(f.ID, f.Version); e != nil {
			t.Fatal(e)
		}
	}
	var live int
	_ = s.DB.QueryRow("SELECT count(*) FROM runs WHERE data->>'$.followupId'=? AND state='queued'", f.ID).Scan(&live)
	if live != 0 {
		t.Fatal("cancelled review remains runnable", live)
	}
	if e = s.FireSchedules(time.UnixMilli(q.At + 2000)); e != nil {
		t.Fatal(e)
	}
}

type followupStopExecutor struct{ started, cleanup, release chan struct{} }

func (e *followupStopExecutor) Execute(ctx context.Context, _ RuntimeInput, _ RuntimeHooks) (RuntimeResult, error) {
	close(e.started)
	<-ctx.Done()
	return RuntimeResult{}, ctx.Err()
}
func (e *followupStopExecutor) Cleanup(context.Context, string) error {
	close(e.cleanup)
	<-e.release
	return nil
}
func TestFollowupCancelRunningRetainsLockUntilCleanup(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	f, e := s.SaveFollowup(in, followupRequest())
	if e != nil {
		t.Fatal(e)
	}
	in.Run.State = "completed"
	if e = s.saveRun(in.Run); e != nil {
		t.Fatal(e)
	}
	f, _ = s.Followup(f.ID)
	f, e = s.ResumeFollowup(f.ID, f.Version)
	if e != nil {
		t.Fatal(e)
	}
	ex := &followupStopExecutor{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	w := NewWorker(s, ex, nil)
	r, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { defer close(done); w.execute(context.Background(), r) }()
	select {
	case <-ex.started:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not start")
	}
	if _, e = s.CancelFollowup(f.ID, f.Version); e != nil {
		t.Fatal(e)
	}
	select {
	case <-ex.cleanup:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup not requested")
	}
	var locks int
	_ = s.DB.QueryRow("SELECT count(*) FROM project_locks WHERE run_id=?", r.ID).Scan(&locks)
	if locks != 1 {
		t.Fatal("lock released before cleanup")
	}
	if s.followupRunLive(r) {
		t.Fatal("revoked run still live")
	}
	close(ex.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not finish")
	}
	_ = s.DB.QueryRow("SELECT count(*) FROM project_locks WHERE run_id=?", r.ID).Scan(&locks)
	if locks != 0 {
		t.Fatal("lock retained after cleanup")
	}
	f, _ = s.Followup(f.ID)
	if f.State != "cancelled" {
		t.Fatal(f)
	}
}
func TestFollowupNotificationCannotFollowReboundConnection(t *testing.T) {
	s := testStore(t)
	c := channel(t, s, "wechat")
	m := submitRoute(t, s, incoming(c, "持续跟进"))
	r, _ := s.Run(m.RunID)
	p, _ := s.PersonalSpace()
	ss, _ := s.Session(r.SessionID)
	in := RuntimeInput{Project: p, Session: ss, Run: r}
	f, e := s.SaveFollowup(in, followupRequest())
	if e != nil {
		t.Fatal(e)
	}
	r.State = "completed"
	_ = s.saveRun(r)
	f, _ = s.Followup(f.ID)
	f, e = s.ResumeFollowup(f.ID, f.Version)
	if e != nil {
		t.Fatal(e)
	}
	r, _ = s.Run(f.LastRunID)
	r.State = "completed"
	_ = s.saveRun(r)
	c.OwnerID = "a-new-owner"
	if _, e = s.SaveConnection(c, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.queueFollowupNotification(r); e != nil {
		t.Fatal(e)
	}
	var n int
	_ = s.DB.QueryRow("SELECT count(*) FROM deliveries WHERE data->>'$.runId'=?", r.ID).Scan(&n)
	if n != 0 {
		t.Fatal("notification followed changed recipient")
	}
}

func TestFollowupCancelWithdrawsOnlyUnsentNotifications(t *testing.T) {
	s := testStore(t)
	c := channel(t, s, "feishu")
	m := submitRoute(t, s, incoming(c, "持续跟进"))
	r, _ := s.Run(m.RunID)
	p, _ := s.PersonalSpace()
	ss, _ := s.Session(r.SessionID)
	in := RuntimeInput{Project: p, Session: ss, Run: r}
	f, e := s.SaveFollowup(in, followupRequest())
	if e != nil {
		t.Fatal(e)
	}
	r.State = "completed"
	_ = s.saveRun(r)
	f, _ = s.Followup(f.ID)
	f, e = s.ResumeFollowup(f.ID, f.Version)
	if e != nil {
		t.Fatal(e)
	}
	r, _ = s.Run(f.LastRunID)
	r.State = "completed"
	_ = s.saveRun(r)
	if e = s.queueFollowupNotification(r); e != nil {
		t.Fatal(e)
	}
	f, _ = s.Followup(f.ID)
	if _, e = s.CancelFollowup(f.ID, f.Version); e != nil {
		t.Fatal(e)
	}
	var state string
	_ = s.DB.QueryRow("SELECT state FROM deliveries WHERE data->>'$.runId'=?", r.ID).Scan(&state)
	if state != "cancelled" {
		t.Fatal(state)
	}
	if e = s.QueueReply(m, r.ID, r.SessionID, "notification", "迟到的通知"); e != nil {
		t.Fatal(e)
	}
	var n int
	_ = s.DB.QueryRow("SELECT count(*) FROM deliveries WHERE data->>'$.runId'=? AND state='pending'", r.ID).Scan(&n)
	if n != 0 {
		t.Fatal("late notification revived")
	}
}

func TestFollowupAnsweredDecisionRecoversWithoutOpenToday(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	q := followupRequest()
	q.Action = "ask"
	q.Decision = followupDecision()
	f, e := s.SaveFollowup(in, q)
	if e != nil {
		t.Fatal(e)
	}
	in.Run.State = "completed"
	if e = s.saveRun(in.Run); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AnswerDecision(f.DecisionID, 1, "A", ""); e != nil {
		t.Fatal(e)
	}
	// Simulate a crash after committing the answer but before API dispatch.
	if e = s.SetSetting("today_enabled", ""); e != nil {
		t.Fatal(e)
	}
	reopened, e := OpenStore(s.Root)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.DB.Close()
	w := NewWorker(reopened, &summaryExecutor{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); w.Wait() }()
	w.Start(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, e := reopened.Followup(f.ID)
		if e != nil {
			t.Fatal(e)
		}
		if current.LastRunID != in.Run.ID {
			r, e := reopened.Run(current.LastRunID)
			if e != nil || r.FollowupID != f.ID || r.SessionID != in.Session.ID {
				t.Fatal(r, e)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("committed answer depended on opening Today again")
}

func TestFollowupContextResetReleasesStaleDecision(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	q := followupRequest()
	q.Action = "ask"
	q.Decision = followupDecision()
	f, e := s.SaveFollowup(in, q)
	if e != nil {
		t.Fatal(e)
	}
	in.Run.State = "completed"
	if e = s.saveRun(in.Run); e != nil {
		t.Fatal(e)
	}
	if e = s.resetPersonalContext(); e != nil {
		t.Fatal(e)
	}
	f, e = s.Followup(f.ID)
	if e != nil || f.State != "needs_review" {
		t.Fatal(f, e)
	}
	if _, e = s.ResumeFollowup(f.ID, f.Version); e != nil {
		t.Fatal("stale decision trapped followup", e)
	}
}

func TestFollowupFastAnswerWaitsForOriginalOutcome(t *testing.T) {
	for _, outcome := range []string{"completed", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			s := testStore(t)
			in := followupInput(t, s)
			q := followupRequest()
			q.Action = "ask"
			q.Decision = followupDecision()
			f, e := s.SaveFollowup(in, q)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.AnswerDecision(f.DecisionID, 1, "A", ""); e != nil {
				t.Fatal(e)
			}
			s.dispatchDecisions()
			current, _ := s.Followup(f.ID)
			if current.LastRunID != in.Run.ID {
				t.Fatal("answer replaced active round")
			}
			in.Run.State = outcome
			if e = s.saveRun(in.Run); e != nil {
				t.Fatal(e)
			}
			s.dispatchDecisions()
			current, _ = s.Followup(f.ID)
			if outcome == "completed" && current.LastRunID == in.Run.ID {
				t.Fatal("answer not recovered after cleanup")
			}
			if outcome == "failed" && (current.LastRunID != in.Run.ID || current.State != "needs_review") {
				t.Fatal("failed round resumed automatically", current)
			}
		})
	}
}
func TestFollowupDueWaitDoesNotConsumeOccurrence(t *testing.T) {
	s := testStore(t)
	in := followupInput(t, s)
	q := followupRequest()
	q.Action = "review"
	q.At = now() + 120000
	f, e := s.SaveFollowup(in, q)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FireSchedules(time.UnixMilli(q.At + 1)); !errors.Is(e, ErrLocked) {
		t.Fatal(e)
	}
	sc, _ := s.Schedule(f.ScheduleID)
	if !sc.Enabled || sc.NextAt != q.At {
		t.Fatal("active round consumed due occurrence", sc)
	}
	in.Run.State = "completed"
	if e = s.saveRun(in.Run); e != nil {
		t.Fatal(e)
	}
	if e = s.FireSchedules(time.UnixMilli(q.At + 1)); e != nil {
		t.Fatal(e)
	}
	f, _ = s.Followup(f.ID)
	if f.LastRunID == in.Run.ID || f.ReviewCount != 1 {
		t.Fatal(f)
	}
}
