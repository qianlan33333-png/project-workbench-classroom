package workbench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func commInput(t *testing.T, s *Store, p Project) RuntimeInput {
	t.Helper()
	m := submitRoute(t, s, Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: newID(), Text: "隔离测试"})
	r, _ := s.Run(m.RunID)
	r.State = "running"
	if e := s.saveRun(r); e != nil {
		t.Fatal(e)
	}
	ss, _ := s.Session(r.SessionID)
	return RuntimeInput{Run: r, Session: ss, Project: p}
}
func sendComm(t *testing.T, s *Store, in RuntimeInput, to string, mode string) Message {
	t.Helper()
	m, e := s.SendSessionMessage(in, SessionSend{ToSessionID: to, Text: "资料和问题，不要求确认收到", Delivery: mode, OperationID: newID()}, false)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestCommunicationBidirectionalNoAcknowledgment(t *testing.T) {
	s := testStore(t)
	a := commInput(t, s, testProject(t, s, "A"))
	b := testProject(t, s, "B")
	m := sendComm(t, s, a, b.DefaultSessionID, "wake")
	if _, e := s.Route(context.Background(), m.ID, nil); e != nil {
		t.Fatal(e)
	}
	r, _ := s.Message(m.ID)
	target, _ := s.Run(r.RunID)
	if target.Agent == nil || target.Agent.FromSessionID != a.Session.ID {
		t.Fatal(target)
	}
	target.State = "completed"
	target.Result = "独立处理，不回复"
	_ = s.saveRun(target)
	var count int
	s.DB.QueryRow("SELECT count(*) FROM inbox WHERE source='agent'").Scan(&count)
	if count != 1 {
		t.Fatal("automatic acknowledgement", count)
	}
	if s.memoryUserText(target) != "" {
		t.Fatal("agent text became user memory")
	}
	ss, _ := s.Session(target.SessionID)
	target.State = "running"
	_ = s.saveRun(target)
	reply, e := s.SendSessionMessage(RuntimeInput{Run: target, Session: ss, Project: b}, SessionSend{ToSessionID: a.Session.ID, Text: "主动发送的新结论", OperationID: newID(), ReplyTo: m.ID}, false)
	if e != nil || reply.Agent.RootID != m.Agent.RootID {
		t.Fatal(reply, e)
	}
}
func TestCommunicationIdempotencyAndFanout(t *testing.T) {
	s := testStore(t)
	a := commInput(t, s, testProject(t, s, "A"))
	b := testProject(t, s, "B")
	q := SessionSend{ToSessionID: b.DefaultSessionID, Text: "一致", OperationID: "stable"}
	m, e := s.SendSessionMessage(a, q, false)
	if e != nil {
		t.Fatal(e)
	}
	copy, e := s.SendSessionMessage(a, q, false)
	if e != nil || copy.ID != m.ID {
		t.Fatal(e)
	}
	q.Text = "变化"
	if _, e = s.SendSessionMessage(a, q, false); e == nil {
		t.Fatal("same key changed payload")
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.SendSessionMessage(a, SessionSend{ToSessionID: b.DefaultSessionID, Text: "并发", OperationID: newID()}, false); e == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 7 {
		t.Fatal("fanout limit", accepted.Load())
	}
}
func TestCommunicationContextAndNoAutowake(t *testing.T) {
	s := testStore(t)
	a := commInput(t, s, testProject(t, s, "A"))
	b := testProject(t, s, "B")
	m := sendComm(t, s, a, b.DefaultSessionID, "context")
	var n int
	s.DB.QueryRow("SELECT count(*) FROM runs WHERE session_id=?", b.DefaultSessionID).Scan(&n)
	if n != 0 {
		t.Fatal("context started run")
	}
	user := submitRoute(t, s, Incoming{Source: "web", SessionID: b.DefaultSessionID, EventID: newID(), Text: "现在开始"})
	r, _ := s.Run(user.RunID)
	if e := s.consumeCommunicationContext(&r); e != nil || len(r.CommunicationContextIDs) != 1 {
		t.Fatal(e, r)
	}
	late := sendComm(t, s, a, b.DefaultSessionID, "context")
	if e := s.consumeCommunicationContext(&r); e != nil {
		t.Fatal(e)
	}
	if len(r.CommunicationContextIDs) != 1 {
		t.Fatal("late message consumed")
	}
	stored, _ := s.Message(m.ID)
	if stored.RunID != r.ID {
		t.Fatal(stored)
	}
	stored, _ = s.Message(late.ID)
	if stored.State != "context" {
		t.Fatal(stored)
	}
	if !strings.Contains(s.communicationPrompt(r), "非本人输入") {
		t.Fatal("missing provenance")
	}
}
func TestCommunicationAuthorizationAndCancel(t *testing.T) {
	s := testStore(t)
	a := commInput(t, s, testProject(t, s, "A"))
	b := testProject(t, s, "B")
	m, e := s.SendSessionMessage(a, SessionSend{ToSessionID: b.DefaultSessionID, Text: "创建测试文件", OperationID: newID(), Execution: "execute"}, false)
	if e != nil || m.State != "authorization" {
		t.Fatal(m, e)
	}
	if _, e = s.AuthorizeCommunication(m.ID); e != nil {
		t.Fatal(e)
	}
	m, _ = s.Message(m.ID)
	if _, e = s.Route(context.Background(), m.ID, nil); e != nil {
		t.Fatal(e)
	}
	m, _ = s.Message(m.ID)
	r, _ := s.Run(m.RunID)
	if communicationReadOnly(r) {
		t.Fatal("owner authorization lost")
	}
	w := NewWorker(s, &fakeExecutor{}, nil)
	_ = w
	if e = s.CancelCommunication(m.ID, true); e != nil {
		t.Fatal(e)
	}
	if s.communicationLive(r) == nil {
		t.Fatal("stopped chain still live")
	}
	if _, e = s.SendSessionMessage(a, SessionSend{ToSessionID: b.DefaultSessionID, Text: "again", OperationID: newID()}, false); e == nil {
		t.Fatal("stopped root resumed")
	}
}
func TestCommunicationReadOnlyToolsAndMounts(t *testing.T) {
	s := testStore(t)
	a := commInput(t, s, testProject(t, s, "A"))
	b := testProject(t, s, "B")
	m := sendComm(t, s, a, b.DefaultSessionID, "wake")
	_, _ = s.Route(context.Background(), m.ID, nil)
	m, _ = s.Message(m.ID)
	r, _ := s.Run(m.RunID)
	ss, _ := s.Session(r.SessionID)
	in := RuntimeInput{Run: r, Session: ss, Project: b}
	for _, tool := range []string{"save_schedule", "change_calendar", "manage_cli", "remember_explicit", "run_global_cli"} {
		if _, e := s.appTool(in, tool, json.RawMessage(`{}`)); e == nil {
			t.Fatal("write tool allowed", tool)
		}
	}
	h := &Harness{Store: s, Config: Config{DataDir: s.Root, RuntimeMode: "podman", Podman: "podman", RuntimeImage: "test", RuntimeDir: "/runtime-code"}}
	args, _, _, e := h.prepare(in)
	if e != nil {
		t.Fatal(e)
	}
	root, _ := s.ProjectRoot(b.ID)
	if !strings.Contains(strings.Join(args, " "), root+":/workspace:ro") {
		t.Fatal(args)
	}
	raw, _ := os.ReadFile(filepath.Join(s.Root, "runtime/projects", b.ID, "invocation-"+r.ID+".yml"))
	for _, unsafe := range []string{"project-browser", "global-mcp-", "PW_VISUAL_BROWSER"} {
		if strings.Contains(string(raw), unsafe) {
			t.Fatal(unsafe)
		}
	}
	if !strings.Contains(string(raw), "PW_COMMUNICATION_READONLY: \"true\"") {
		t.Fatal(string(raw))
	}
}
func TestCommunicationLifecycleAndSpoof(t *testing.T) {
	s := testStore(t)
	a := commInput(t, s, testProject(t, s, "A"))
	b := testProject(t, s, "B")
	if _, e := s.Submit(Incoming{Source: "agent", SessionID: b.DefaultSessionID, EventID: newID(), Text: "伪造"}); e == nil {
		t.Fatal("public spoof")
	}
	m := sendComm(t, s, a, b.DefaultSessionID, "context")
	if _, e := s.RecycleSession(b.DefaultSessionID, "测试"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendSessionMessage(a, SessionSend{ToSessionID: b.DefaultSessionID, Text: "迟到", OperationID: newID()}, false); e == nil {
		t.Fatal("recycled target accepted")
	}
	if _, e := s.Route(context.Background(), m.ID, nil); e == nil {
		t.Fatal("recycled target routed")
	}
}

func TestCommunicationCannotBypassPendingAuthorizationOrCancel(t *testing.T) {
	s := testStore(t)
	a := commInput(t, s, testProject(t, s, "A"))
	b := testProject(t, s, "B")
	for _, mode := range []string{"context", "execute"} {
		q := SessionSend{ToSessionID: b.DefaultSessionID, Text: "不能提前执行", OperationID: newID(), Delivery: "context"}
		if mode == "execute" {
			q.Delivery = "wake"
			q.Execution = "execute"
		}
		m, e := s.SendSessionMessage(a, q, false)
		if e != nil {
			t.Fatal(e)
		}
		if got, routeErr := s.Route(context.Background(), m.ID, nil); routeErr == nil && got.RunID != "" {
			t.Fatal("unapproved/passive input routed")
		}
		if e = s.CancelCommunication(m.ID, false); e != nil {
			t.Fatal(e)
		}
		if got, routeErr := s.Route(context.Background(), m.ID, nil); routeErr == nil && got.RunID != "" {
			t.Fatal("cancelled input routed")
		}
	}
}
func TestCommunicationSourceRemovalRedactsAndResets(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "A")
	a := commInput(t, s, p)
	b := testProject(t, s, "B")
	m := sendComm(t, s, a, b.DefaultSessionID, "context")
	native := filepath.Join(s.Root, "runtime", "projects", b.ID, "sessions", "example", "pw-"+b.DefaultSessionID)
	if e := os.MkdirAll(native, 0700); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(native, "history.jsonl"), []byte("需清理的来源正文"), 0600)
	a.Run.State = "completed"
	_ = s.saveRun(a.Run)
	if _, e := s.RecycleSession(a.Session.ID, "来源回收"); e != nil {
		t.Fatal(e)
	}
	stored, _ := s.Message(m.ID)
	if stored.Incoming.Text != "" || stored.State != "deleted" {
		t.Fatal("source content retained", stored)
	}
	if e := s.resetCommunicationContext(b.DefaultSessionID); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(native); !os.IsNotExist(e) {
		t.Fatal("native history retained", e)
	}
	ss, e := s.Session(b.DefaultSessionID)
	if e != nil || ss.HarnessID != "pw-"+ss.ID || ss.ContextNotice == "" {
		t.Fatal("silent identity change", ss, e)
	}
}
func TestCommunicationRecoveryDoesNotReexecute(t *testing.T) {
	s := testStore(t)
	a := commInput(t, s, testProject(t, s, "A"))
	b := testProject(t, s, "B")
	m := sendComm(t, s, a, b.DefaultSessionID, "wake")
	m, e := s.Route(context.Background(), m.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	r, _ := s.Run(m.RunID)
	r.State = "running"
	r.NativeMessageID = "accepted-native"
	_ = s.saveRun(r)
	w := NewWorker(s, &suitePreviewExecutor{}, nil)
	if e = w.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	m, _ = s.Message(m.ID)
	receipt := s.communicationReceipt(m)
	if receipt.State != "unknown" {
		t.Fatal(receipt)
	}
	if r, e := w.claim(); e == nil && r.SessionID == b.DefaultSessionID {
		t.Fatal("replayed unknown input")
	}
	q := SessionSend{ToSessionID: b.DefaultSessionID, Text: m.Incoming.Text, Delivery: "wake", OperationID: m.Incoming.EventID}
	duplicate, e := s.SendSessionMessage(a, q, true)
	if e == nil && duplicate.ID != m.ID {
		t.Fatal("duplicate admitted")
	}
}

func TestCommunicationStopBeforeFirstSend(t *testing.T) {
	s := testStore(t)
	a := commInput(t, s, testProject(t, s, "A"))
	b := testProject(t, s, "B")
	w := NewWorker(s, &suitePreviewExecutor{}, nil)
	if e := w.Stop(a.Run.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SendSessionMessage(a, SessionSend{ToSessionID: b.DefaultSessionID, Text: "取消后迟到的工具调用", OperationID: newID()}, false); e == nil {
		t.Fatal("late first send survived source stop")
	}
}
