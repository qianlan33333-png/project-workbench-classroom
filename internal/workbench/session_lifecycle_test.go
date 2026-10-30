package workbench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecycleRestoreIsolationAndDefaultReplacement(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "回收验收")
	ss, _ := s.Session(p.DefaultSessionID)
	focusInput := Incoming{Source: "web", ConnectionID: "web", ActorID: "owner", ConversationID: "web"}
	s.SetFocus(focusInput, ss.ID)
	m := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: ss.ID, Text: "绝密验收暗号 RECYCLE-123"})
	r, _ := s.Run(m.RunID)
	r.State = "completed"
	r.Result = "结论保留到语料"
	s.saveRun(r)
	m.State = "completed"
	s.saveMessage(m)
	native := filepath.Join(s.Root, "runtime", "projects", p.ID, "sessions", "--workspace--", ss.HarnessID)
	os.MkdirAll(native, 0700)
	os.WriteFile(filepath.Join(native, "session.v4.jsonl"), []byte("raw transcript"), 0600)
	if _, e := s.SaveFile(p.ID, "成果.md", []byte("项目成果"), ""); e != nil {
		t.Fatal(e)
	}
	recycled, e := s.RecycleSession(ss.ID, "delete")
	if e != nil {
		t.Fatal(e)
	}
	if recycled.ExpiresAt-recycled.RecycledAt != recycleDuration {
		t.Fatal(recycled)
	}
	if _, e = s.Session(ss.ID); e == nil {
		t.Fatal("normal read exposed recycle")
	}
	if _, e = s.Run(r.ID); e == nil {
		t.Fatal("run exposed recycle")
	}
	if _, e = os.Stat(native); !os.IsNotExist(e) {
		t.Fatal("native session remains mounted")
	}
	rows, _ := s.RunPage("", "")
	if len(rows) != 0 {
		t.Fatal(rows)
	}
	hits, _ := s.SessionSearch(p.ID, "RECYCLE-123")
	if len(hits) != 0 {
		t.Fatal(hits)
	}
	events, _ := s.Events(0, "")
	for _, event := range events {
		if strings.Contains(string(event.Data), "RECYCLE-123") {
			t.Fatal("SSE replay exposed raw message")
		}
	}
	fresh, _ := s.Project(p.ID)
	if fresh.DefaultSessionID == ss.ID {
		t.Fatal("default was not replaced")
	}
	focused, e := s.Focus(focusInput)
	if e != nil || focused.ID != fresh.DefaultSessionID {
		t.Fatal("entry did not follow replacement assistant", focused, e)
	}
	if _, e = s.Submit(Incoming{Source: "web", EventID: newID(), SessionID: ss.ID, Text: "不能进入"}); e == nil {
		t.Fatal("accepted into recycle")
	}
	restored, e := s.RestoreSession(ss.ID)
	if e != nil {
		t.Fatal(e)
	}
	if restored.ID != ss.ID || restored.HarnessID != ss.HarnessID || restored.Kind != "topic" {
		t.Fatal(restored)
	}
	if _, e = os.Stat(native); e != nil {
		t.Fatal(e)
	}
	after, _ := s.Run(r.ID)
	if after.State != "completed" {
		t.Fatal("restoring requeued task")
	}
	if _, _, e = s.ReadFile(p.ID, "成果.md"); e != nil {
		t.Fatal(e)
	}
}
func TestRecycleBusyExpiryAndPurge(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "到期验收")
	ss, _ := s.CreateSession(p.ID, "专题", "topic", "")
	m := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: ss.ID, Text: "测试"})
	if _, e := s.RecycleSession(ss.ID, "delete"); e == nil {
		t.Fatal("recycled active input")
	}
	r, _ := s.Run(m.RunID)
	r.State = "cancelled"
	s.saveRun(r)
	m.State = "cancelled"
	s.saveMessage(m)
	if _, e := s.PinSession(ss.ID, true); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RecycleSession(ss.ID, "delete"); e != nil {
		t.Fatal(e)
	}
	ss, _ = s.rawSession(ss.ID)
	ss.ExpiresAt = now() - 1
	s.saveSession(ss)
	if _, e := s.RestoreSession(ss.ID); e == nil {
		t.Fatal("restored expired session")
	}
	if e := s.PurgeExpiredSessions(); e != nil {
		t.Fatal(e)
	}
	if _, e := s.rawSession(ss.ID); e == nil {
		t.Fatal("not purged")
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM inbox WHERE id=?", m.ID).Scan(&n)
	if n != 0 {
		t.Fatal("raw input remains")
	}
	if _, e := os.Stat(filepath.Join(s.Root, "purged-sessions", ss.ID+".json")); e != nil {
		t.Fatal(e)
	}
}

type archiveExecutor struct {
	prompts []string
	fail    bool
}

func (e *archiveExecutor) Cleanup(context.Context, string) error { return nil }
func (e *archiveExecutor) Execute(_ context.Context, in RuntimeInput, _ RuntimeHooks) (RuntimeResult, error) {
	e.prompts = append(e.prompts, in.Prompt)
	if e.fail {
		return RuntimeResult{}, os.ErrPermission
	}
	return RuntimeResult{Text: "## 已确认事实\n客户代号 松果314。\n\n## 待办\n完成培训计划。", StopReason: "completed"}, nil
}
func TestArchiveWritesMDAndPreservesOriginalOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			s := testStore(t)
			p := testProject(t, s, "归档验收")
			ss, _ := s.CreateSession(p.ID, "培训计划", "topic", "")
			m := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: ss.ID, Text: "客户代号 松果314，请做培训计划"})
			r, _ := s.Run(m.RunID)
			r.State = "completed"
			r.Result = "待办：完成培训计划"
			s.saveRun(r)
			m.State = "completed"
			s.saveMessage(m)
			e := &archiveExecutor{fail: fail}
			w := NewWorker(s, e, nil)
			if _, err := s.QueueArchive(ss.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Submit(Incoming{Source: "web", EventID: newID(), SessionID: ss.ID, Text: "归档中不可写"}); err == nil {
				t.Fatal("freeze ignored")
			}
			job, err := w.claim()
			if err != nil {
				t.Fatal(err)
			}
			w.execute(context.Background(), job)
			after, _ := s.rawSession(ss.ID)
			if fail {
				if after.RecycledAt != 0 || after.Lifecycle != "" || after.LifecycleError == "" {
					t.Fatal(after)
				}
			} else {
				if after.RecycledAt == 0 || after.ArchivePath == "" {
					t.Fatal(after)
				}
				b, _, err := s.ReadFile(p.ID, after.ArchivePath)
				if err != nil || !strings.Contains(string(b), "松果314") {
					t.Fatal(string(b), err)
				}
				if len(e.prompts) != 1 || !strings.Contains(e.prompts[0], "客户代号 松果314") {
					t.Fatal(e.prompts)
				}
				restored, err := s.RestoreSession(ss.ID)
				if err != nil || restored.ArchivePath == "" {
					t.Fatal(restored, err)
				}
			}
			if err = s.CheckUnlocked(p.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRealArchiveHarness(t *testing.T) {
	p := os.Getenv("PW_REAL_CONFIG")
	if p == "" {
		t.Skip("credentialed isolated runtime required")
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	var cfg Config
	json.Unmarshal(raw, &cfg)
	s := testStore(t)
	cfg.DataDir = s.Root
	project := testProject(t, s, "真实归档")
	ss, _ := s.CreateSession(project.ID, "客户培训", "topic", "")
	m := submitRoute(t, s, Incoming{Source: "web", SessionID: ss.ID, EventID: newID(), Text: "已确认事实：客户代号松果314；预算8000元。待办：周五完成培训计划。"})
	r, _ := s.Run(m.RunID)
	r.State = "completed"
	r.Result = "已记录以上已确认事实，培训计划尚未完成。"
	s.saveRun(r)
	m.State = "completed"
	s.saveMessage(m)
	h := &Harness{Config: cfg, Store: s}
	w := NewWorker(s, h, nil)
	s.QueueArchive(ss.ID)
	job, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	w.execute(ctx, job)
	after, _ := s.rawSession(ss.ID)
	if after.RecycledAt == 0 {
		t.Fatal(after)
	}
	b, _, e := s.ReadFile(project.ID, after.ArchivePath)
	if e != nil || !strings.Contains(string(b), "松果314") || !strings.Contains(string(b), "8000") {
		t.Fatal(string(b), e)
	}
	t.Log(after.ArchivePath)
}

func TestRecycleFailedInputHidesGlobalEvents(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "错误消息回收")
	ss, _ := s.CreateSession(p.ID, "失败输入", "topic", "")
	m, e := s.Submit(Incoming{Source: "web", ConnectionID: "web", SessionID: ss.ID, EventID: newID(), Text: "FAILED-PRIVATE-314"})
	if e != nil {
		t.Fatal(e)
	}
	m.State = "error"
	m.Question = "路由失败"
	s.saveMessage(m)
	s.Emit("", "", "message.error", m)
	if _, e = s.RecycleSession(ss.ID, "delete"); e != nil {
		t.Fatal(e)
	}
	events, e := s.Events(0, "")
	if e != nil {
		t.Fatal(e)
	}
	for _, event := range events {
		if strings.Contains(string(event.Data), "FAILED-PRIVATE-314") {
			t.Fatal("global error exposed original")
		}
	}
	if _, e = s.Message(m.ID); e == nil {
		t.Fatal("receipt exposed original")
	}
	if e = s.DeleteProject(p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(s.Root, "trash", ss.ID)); !os.IsNotExist(e) {
		t.Fatal("project deletion left raw recycle files", e)
	}
}

func TestPurgeLedgerRestoreRepairsDefaultWithoutRestoringHistory(t *testing.T) {
	for _, alreadyScrubbed := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-live-snapshot", true: "scrubbed-backup"}[alreadyScrubbed], func(t *testing.T) {
			s := testStore(t)
			p := testProject(t, s, "旧快照恢复")
			ss, _ := s.Session(p.DefaultSessionID)
			if _, e := s.SaveFile(p.ID, "保留成果.md", []byte("keep"), ""); e != nil {
				t.Fatal(e)
			}
			if e := os.MkdirAll(filepath.Join(s.Root, "purged-sessions"), 0700); e != nil {
				t.Fatal(e)
			}
			marker := map[string]any{"id": ss.ID, "projectId": p.ID, "harnessId": ss.HarnessID, "messageHashes": []string{}}
			if e := os.WriteFile(filepath.Join(s.Root, "purged-sessions", ss.ID+".json"), []byte(encode(marker)), 0600); e != nil {
				t.Fatal(e)
			}
			if alreadyScrubbed {
				if _, e := s.DB.Exec("DELETE FROM sessions WHERE id=?", ss.ID); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.ApplyPurgeLedger(); e != nil {
				t.Fatal(e)
			}
			current, e := s.Project(p.ID)
			if e != nil || current.DefaultSessionID == ss.ID {
				t.Fatal(current, e)
			}
			fresh, e := s.Session(current.DefaultSessionID)
			if e != nil || fresh.HarnessID == ss.HarnessID {
				t.Fatal(fresh, e)
			}
			if _, e = s.rawSession(ss.ID); e == nil {
				t.Fatal("purged original resurrected")
			}
			if _, _, e = s.ReadFile(p.ID, "保留成果.md"); e != nil {
				t.Fatal(e)
			}
			if e = s.ApplyPurgeLedger(); e != nil {
				t.Fatal(e)
			}
			after, _ := s.Project(p.ID)
			if after.DefaultSessionID != fresh.ID {
				t.Fatal("non-idempotent repair")
			}
		})
	}
}
