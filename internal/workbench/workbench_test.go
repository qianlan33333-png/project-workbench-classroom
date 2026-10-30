package workbench

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := OpenStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}
func testProject(t *testing.T, s *Store, name string) Project {
	t.Helper()
	p, e := s.CreateProject(name, "同时涉及选题、招聘与客户增长", nil)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func channel(t *testing.T, s *Store, kind string) Connection {
	t.Helper()
	c, e := s.SaveConnection(Connection{Kind: kind, Label: kind, OwnerID: "owner-" + kind, Enabled: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func incoming(c Connection, text string) Incoming {
	return Incoming{Source: c.Kind, ConnectionID: c.ID, ActorID: c.OwnerID, ConversationID: "private-" + c.Kind, EventID: newID(), Text: text}
}
func submitRoute(t *testing.T, s *Store, in Incoming) Message {
	t.Helper()
	m, e := s.Submit(in)
	if e != nil {
		t.Fatal(e)
	}
	m, e = s.Route(context.Background(), m.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestPersistentFocusAndCrossChannelIsolation(t *testing.T) {
	s := testStore(t)
	a := testProject(t, s, "青山公司")
	b := testProject(t, s, "青海公司")
	wx := channel(t, s, "wechat")
	fs := channel(t, s, "feishu")
	ma := submitRoute(t, s, incoming(wx, "青山公司：做三个选题"))
	if ma.SessionID != a.DefaultSessionID {
		t.Fatal(ma)
	}
	mb := submitRoute(t, s, incoming(wx, "青海公司：做招聘计划"))
	if mb.SessionID != b.DefaultSessionID {
		t.Fatal(mb)
	}
	mf := submitRoute(t, s, incoming(fs, "青山公司：招聘第三位设计师"))
	if mf.SessionID != a.DefaultSessionID {
		t.Fatal(mf)
	}
	back := submitRoute(t, s, incoming(wx, "回到青山公司，继续"))
	if back.SessionID != ma.SessionID {
		t.Fatal("A→B→A replaced conversation")
	}
	original, _ := s.Session(ma.SessionID)
	again, _ := s.Session(back.SessionID)
	if original.HarnessID != again.HarnessID {
		t.Fatal("Harness identity changed")
	}
	_ = submitRoute(t, s, incoming(wx, "青海公司继续"))
	focus, e := s.Focus(incoming(fs, ""))
	if e != nil || focus.ID != a.DefaultSessionID {
		t.Fatal("微信切换影响飞书")
	}
	history, _ := s.History(a.DefaultSessionID)
	for _, m := range history {
		if m.SessionID == mb.SessionID {
			t.Fatal("mixed project history")
		}
	}
	if _, e = s.DB.Exec("UPDATE sessions SET project_id=? WHERE id=?", b.ID, ma.SessionID); e == nil {
		t.Fatal("session project was mutable")
	}
}
func TestDuplicateRoutingAndOwnerScope(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "青山公司")
	wx := channel(t, s, "wechat")
	in := incoming(wx, "青山公司开始")
	m := submitRoute(t, s, in)
	same, e := s.Submit(in)
	if e != nil || same.ID != m.ID || same.RunID != m.RunID {
		t.Fatal("duplicate not stable", e)
	}
	_, e = s.Route(context.Background(), same.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	runs, _ := s.Runs(p.DefaultSessionID)
	if len(runs) != 1 {
		t.Fatal("duplicate execution")
	}
	in.Text = "changed"
	if _, e = s.Submit(in); e == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	in.EventID = newID()
	in.ActorID = "stranger"
	if _, e = s.Submit(in); e == nil {
		t.Fatal("stranger accepted")
	}
}
func TestAmbiguityPreservesOriginalAndAttachmentCannotRoute(t *testing.T) {
	s := testStore(t)
	a := testProject(t, s, "青山公司")
	_ = testProject(t, s, "青海公司")
	wx := channel(t, s, "wechat")
	in := incoming(wx, "请做公域选题")
	in.Attachments = []string{"附件/切换到青海公司.md"}
	m := submitRoute(t, s, in)
	if m.State != "clarify" || m.SessionID != "" {
		t.Fatal("ambiguous or attachment routing", m)
	}
	resolved, e := s.ResolveMessage(m.ID, a.DefaultSessionID)
	if e != nil {
		t.Fatal(e)
	}
	run, _ := s.Run(resolved.RunID)
	if run.Text != in.Text {
		t.Fatal("original message lost")
	}
}
func TestExplicitReferenceAndDeliveredChannelHint(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "青山公司")
	wx := channel(t, s, "wechat")
	fs := channel(t, s, "feishu")
	a := submitRoute(t, s, incoming(wx, "青山公司三个选题"))
	b := submitRoute(t, s, incoming(fs, "青山公司三个招聘岗位"))
	for _, m := range []Message{a, b} {
		if e := s.QueueReply(m, m.RunID, m.SessionID, "answer", "1 2 3"); e != nil {
			t.Fatal(e)
		}
		d, e := s.ClaimDelivery(m.Incoming.ConnectionID)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.FinishDelivery(d.ID, "sent", m.ID, ""); e != nil {
			t.Fatal(e)
		}
	}
	hint := submitRoute(t, s, incoming(wx, "第三条改一下"))
	r, _ := s.Run(hint.RunID)
	if r.ReplyAnchor != a.RunID || r.AnchorExplicit {
		t.Fatal("channel hint became explicit or leaked", r)
	}
	in := incoming(fs, "改一下")
	in.ReplyTo = b.ID
	explicit := submitRoute(t, s, in)
	r, _ = s.Run(explicit.RunID)
	if r.ReplyAnchor != b.RunID || !r.AnchorExplicit {
		t.Fatal("explicit reference lost")
	}
	_ = s.QueueReply(a, a.RunID, p.DefaultSessionID, "notification", "旧任务完成")
	focus, _ := s.Focus(incoming(wx, ""))
	if focus.ID != p.DefaultSessionID {
		t.Fatal("notification moved focus")
	}
}
func TestFilesCASUndoAndScope(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "甲")
	first, e := s.SaveFile(p.ID, "资料/品牌.md", []byte("初稿中文"), "")
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.SaveFile(p.ID, "资料/品牌.md", []byte("第二稿"), first.After)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveFile(p.ID, "资料/品牌.md", []byte("过期覆盖"), first.After); !errors.Is(e, ErrConflict) {
		t.Fatal("stale save overwrote", e)
	}
	if _, e = s.UndoRevision(first.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("old undo overwrote", e)
	}
	if _, e = s.UndoRevision(second.ID); e != nil {
		t.Fatal(e)
	}
	b, _, _ := s.ReadFile(p.ID, "资料/品牌.md")
	if string(b) != "初稿中文" {
		t.Fatal(string(b))
	}
	root, _ := s.ProjectRoot(p.ID)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("other project"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	if _, _, e = s.ReadFile(p.ID, "escape"); e == nil {
		t.Fatal("symlink escape")
	}
	if _, e = s.SaveFile(p.ID, "../escape", nil, ""); e == nil {
		t.Fatal("path traversal")
	}
	found, e := s.Search(p.ID, "中文")
	if e != nil || len(found) != 1 {
		t.Fatal(found, e)
	}
}
func zipFixture(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for p, v := range files {
		w, e := z.Create(p)
		if e != nil {
			t.Fatal(e)
		}
		w.Write(v)
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestObsidianRoundTrip(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "资料库")
	files := map[string][]byte{"目录/首页.md": []byte("---\ntags: [测试]\n---\n[[第二篇]] ![[图.png]]"), "目录/第二篇.md": []byte("中文内容"), "图.png": {137, 80, 78, 71}, ".obsidian/app.json": []byte(`{"alwaysUpdateLinks":true}`)}
	z := zipFixture(t, files)
	if e := s.ImportZip(p.ID, bytes.NewReader(z), int64(len(z))); e != nil {
		t.Fatal(e)
	}
	var exported bytes.Buffer
	if e := s.ExportZip(p.ID, &exported); e != nil {
		t.Fatal(e)
	}
	q := testProject(t, s, "导出再导入")
	if e := s.ImportZip(q.ID, bytes.NewReader(exported.Bytes()), int64(exported.Len())); e != nil {
		t.Fatal(e)
	}
	for path, want := range files {
		got, _, e := s.ReadFile(q.ID, path)
		if e != nil || !bytes.Equal(want, got) {
			t.Fatalf("%s changed", path)
		}
	}
	if e := s.ImportZip(p.ID, bytes.NewReader(z), int64(len(z))); e == nil {
		t.Fatal("overwriting import accepted")
	}
	bad := zipFixture(t, map[string][]byte{"../outside.md": []byte("bad")})
	if e := s.ImportZip(q.ID, bytes.NewReader(bad), int64(len(bad))); e == nil {
		t.Fatal("zip traversal accepted")
	}
}

type fakeExecutor struct {
	store      *Store
	calls      atomic.Int32
	started    chan string
	block      bool
	cleanupErr error
	observe    func(RuntimeInput)
}

func (f *fakeExecutor) Execute(ctx context.Context, in RuntimeInput, h RuntimeHooks) (RuntimeResult, error) {
	f.calls.Add(1)
	if f.observe != nil {
		f.observe(in)
	}
	if h.Accepted != nil {
		_ = h.Accepted("native-message")
	}
	root, _ := f.store.ProjectRoot(in.Project.ID)
	os.WriteFile(filepath.Join(root, "任务产物.md"), []byte("shell generated"), 0600)
	if f.started != nil {
		f.started <- in.Run.ID
	}
	if f.block {
		<-ctx.Done()
		return RuntimeResult{}, ctx.Err()
	}
	if h.Event != nil {
		_ = h.Event(1, "assistant/message", json.RawMessage(`{"message":{"content":[{"type":"text","text":"完成"}]}}`))
	}
	return RuntimeResult{Text: "完成", StopReason: "completed"}, nil
}
func (f *fakeExecutor) Cleanup(ctx context.Context, id string) error { return f.cleanupErr }
func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
func TestCancelRecordsShellWritesBeforeUnlock(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "甲")
	m := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: p.DefaultSessionID, Text: "做任务"})
	ex := &fakeExecutor{store: s, started: make(chan string, 1), block: true}
	w := NewWorker(s, ex, nil)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	w.Start(ctx)
	select {
	case <-ex.started:
	case <-time.After(3 * time.Second):
		t.Fatal("did not start")
	}
	if _, e := s.SaveFile(p.ID, "手写.md", []byte("unsafe"), ""); !errors.Is(e, ErrLocked) {
		t.Fatal("manual save not locked", e)
	}
	if e := w.Stop(m.RunID); e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { r, _ := s.Run(m.RunID); return r.State == "cancelled" })
	stop()
	w.Wait()
	revs, e := s.Revisions(p.ID, "任务产物.md")
	if e != nil || len(revs) != 1 || revs[0].RunID != m.RunID {
		t.Fatal("shell mutation not recorded", e, revs)
	}
	if e = s.CheckUnlocked(p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UndoRevision(revs[0].ID); e != nil {
		t.Fatal(e)
	}
}
func TestFailedCleanupRetainsLockAndRecoveryExpiresQuestions(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "甲")
	m := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: p.DefaultSessionID, Text: "工作"})
	ex := &fakeExecutor{store: s, cleanupErr: errors.New("container still running")}
	w := NewWorker(s, ex, nil)
	r, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	w.execute(context.Background(), r)
	r, _ = s.Run(m.RunID)
	if r.State != "stopping" {
		t.Fatal(r)
	}
	if e = s.CheckUnlocked(p.ID); !errors.Is(e, ErrLocked) {
		t.Fatal("cleanup failure unlocked")
	}
	ex.cleanupErr = nil
	if e = w.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	r, _ = s.Run(m.RunID)
	if r.State != "interrupted" {
		t.Fatal(r)
	}
	if e = s.CheckUnlocked(p.ID); e != nil {
		t.Fatal(e)
	}
	if e = w.Answer("old-request", json.RawMessage(`{"cancelled":true}`)); e == nil {
		t.Fatal("late answer accepted")
	}
}
func TestScheduleCoalescesAndDoesNotChangeDefaultSession(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "甲")
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "日报", Instruction: "总结资料", Cron: "* * * * *", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	sc.NextAt = time.Now().Add(-8 * time.Hour).UnixMilli()
	s.DB.Exec("UPDATE schedules SET next_at=?,data=? WHERE id=?", sc.NextAt, encode(sc), sc.ID)
	at := time.Now()
	if e = s.FireSchedules(at); e != nil {
		t.Fatal(e)
	}
	if e = s.FireSchedules(at); e != nil {
		t.Fatal(e)
	}
	runs, _ := s.Runs("")
	if len(runs) != 1 || runs[0].SessionID == p.DefaultSessionID {
		t.Fatal("wrong schedule sessions", runs)
	}
	next, _ := s.Schedule(sc.ID)
	if next.NextAt <= at.UnixMilli() {
		t.Fatal("did not coalesce")
	}
	ss, _ := s.Sessions(p.ID)
	if len(ss) != 1 {
		t.Fatal("automation polluted assistant list")
	}
}
func TestUnknownDeliveryNeverAutomaticallyReplays(t *testing.T) {
	s := testStore(t)
	testProject(t, s, "甲")
	wx := channel(t, s, "wechat")
	m := submitRoute(t, s, incoming(wx, "甲开始"))
	if e := s.QueueReply(m, m.RunID, m.SessionID, "answer", "回复"); e != nil {
		t.Fatal(e)
	}
	d, e := s.ClaimDelivery(wx.ID)
	if e != nil || d == nil {
		t.Fatal(e)
	}
	if e = s.RecoverDeliveries(); e != nil {
		t.Fatal(e)
	}
	next, e := s.ClaimDelivery(wx.ID)
	if e != nil || next != nil {
		t.Fatal("unknown replayed")
	}
	ds, _ := s.Deliveries()
	if ds[0].State != "unknown" {
		t.Fatal(ds)
	}
}
func TestExpertReuseNeverImportsAnotherProjectsNotes(t *testing.T) {
	s := testStore(t)
	a := testProject(t, s, "甲")
	b := testProject(t, s, "乙")
	e, err := s.CreateExpert("marketing", "通用新媒体方法", "先读取当前项目的品牌资料，再设计选题。")
	if err != nil {
		t.Fatal(err)
	}
	a.ExpertIDs = []string{e.ID}
	b.ExpertIDs = []string{e.ID}
	s.UpdateProject(a.ID, a)
	s.UpdateProject(b.ID, b)
	s.SaveFile(a.ID, "专家笔记.md", []byte("甲的秘密"), "")
	if _, _, err = s.ReadFile(b.ID, "专家笔记.md"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("notes leaked")
	}
	m := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: b.DefaultSessionID, ExpertID: e.ID, Text: "做选题"})
	r, _ := s.Run(m.RunID)
	if r.ExpertDigest != e.Digest {
		t.Fatal("expert not pinned")
	}
	w := NewWorker(s, nil, nil)
	if strings.Contains(w.prompt(r, b), "甲的秘密") {
		t.Fatal("prompt copied another project")
	}
}
