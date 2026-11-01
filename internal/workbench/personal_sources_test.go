package workbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTodayReadsOnlySelectedVisibleSources(t *testing.T) {
	s := testStore(t)
	a := testProject(t, s, "甲")
	b := testProject(t, s, "乙")
	personal, _ := s.PersonalSpace()
	rt := RuntimeInput{Project: personal, Run: Run{ID: newID(), Text: "给甲项目创建一个任务"}}
	const name = "记录.md"
	for _, p := range []Project{a, b} {
		if _, e := s.SaveFile(p.ID, name, []byte(strings.Repeat("甲", 6001)+p.Name+"专属"), ""); e != nil {
			t.Fatal(e)
		}
	}
	task, e := s.SaveTask(ProjectTask{ProjectID: a.ID, Title: "任务原件", Operation: newID(), Description: "先验证意向"})
	if e != nil {
		t.Fatal(e)
	}
	session, e := s.CreateSession(a.ID, "项目访谈", "topic", "")
	if e != nil {
		t.Fatal(e)
	}
	m := submitRoute(t, s, Incoming{Source: "web", SessionID: session.ID, Text: "原始访谈记录", EventID: newID()})
	r, _ := s.Run(m.RunID)
	r.State = "completed"
	r.Result = "反馈已记录"
	_ = s.saveRun(r)
	call := func(tool string, args any) (string, error) {
		v, e := s.appTool(rt, tool, json.RawMessage(encode(args)))
		return encode(v), e
	}
	for _, tc := range []struct{ kind, key, query, want string }{{"file", name, "专属", "甲专属"}, {"task", task.ID, "意向", "先验证意向"}, {"conversation", r.ID, "访谈", "反馈已记录"}} {
		q := sourceQuery{ProjectID: a.ID, Kind: tc.kind, Key: tc.key, Query: tc.query}
		found, e := call("find_project_sources", q)
		if e != nil || !strings.Contains(found, tc.key) {
			t.Fatal(found, e)
		}
		if tc.kind == "file" {
			q.Offset = 6000
		}
		v, e := call("read_project_source", q)
		if e != nil || !strings.Contains(v, tc.want) {
			t.Fatal(v, e)
		}
		if tc.kind != "file" {
			q.ProjectID = b.ID
			if _, e = call("read_project_source", q); e == nil {
				t.Fatal("crossed project boundary")
			}
		}
	}
	root, _ := s.ProjectRoot(a.ID)
	outside := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(outside, []byte("private"), 0600)
	_ = os.Symlink(outside, filepath.Join(root, "escape"))
	for _, path := range []string{"../secret", "escape"} {
		if _, e := call("read_project_source", sourceQuery{ProjectID: a.ID, Kind: "file", Key: path}); e == nil {
			t.Fatal("path escape", path)
		}
	}
	if _, e = s.RecycleSession(session.ID, "delete"); e != nil {
		t.Fatal(e)
	}
	if _, e = call("read_project_source", sourceQuery{ProjectID: a.ID, Kind: "conversation", Key: r.ID}); e == nil {
		t.Fatal("read recycled chat")
	}
	for _, tool := range []string{"create_decided_task", "refresh_project_context", "save_schedule", "manage_cli", "write_file", "delete_task"} {
		if _, e := call(tool, map[string]any{"projectId": a.ID, "task": map[string]string{"projectId": a.ID, "title": "不要写入"}}); e == nil {
			t.Fatal(tool)
		}
	}
	old := PersonalDecision{ID: newID(), State: "pending", Version: 1, ContextVersion: s.contextVersion(), Options: []DecisionOption{{ID: "A", Task: &ProjectTask{ProjectID: a.ID, Title: "历史选项"}}}}
	_, e = s.DB.Exec("INSERT INTO personal_decisions VALUES(?,?,?)", old.ID, newID(), encode(old))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AnswerDecision(old.ID, 1, "A", ""); e == nil {
		t.Fatal("legacy card could write a project")
	}
	tasks, _ := s.Tasks(a.ID)
	if len(tasks) != 1 {
		t.Fatal("unexpected task")
	}
}

func TestBotCannotControlOrResolveBusinessRun(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "测试")
	w := NewWorker(s, &fakeExecutor{store: s}, nil)
	original := submitRoute(t, s, Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: newID(), Text: "工作"})
	wx := channel(t, s, "wechat")
	m, e := s.Submit(incoming(wx, "停止 #T"+original.RunID[:8]))
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := w.handleControl(m); e != nil || ok {
		t.Fatal("external control bypass", e)
	}
	if _, e = s.ResolveMessage(m.ID, p.DefaultSessionID); e == nil {
		t.Fatal("resolved outside Today")
	}
	target, e := s.ResolveMessage(m.ID, "")
	if e != nil {
		t.Fatal(e)
	}
	task, _ := s.Run(original.RunID)
	if task.State != "queued" || target.SessionID == p.DefaultSessionID {
		t.Fatal("project task affected")
	}
}
