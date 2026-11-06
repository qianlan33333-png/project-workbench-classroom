package workbench

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTodayCreatesRealTasksFromOwnerTurns(t *testing.T) {
	s := testStore(t)
	personal, _ := s.PersonalSpace()
	a := testProject(t, s, "青山")
	b := testProject(t, s, "青海")
	old := testProject(t, s, "归档")
	old.Archived = true
	if _, e := s.DB.Exec("UPDATE projects SET archived=1,data=? WHERE id=?", encode(old), old.ID); e != nil {
		t.Fatal(e)
	}
	const text = "请把跟进记为待处理任务"
	inputs := []Incoming{
		{Source: "web", ActorID: "owner", ConnectionID: "web", EventID: newID(), SessionID: personal.DefaultSessionID, Text: text},
		incoming(channel(t, s, "wechat"), text),
		incoming(channel(t, s, "feishu"), text),
	}
	worker := &Worker{Store: s}
	for i, p := range []Project{personal, a, b} {
		m := submitRoute(t, s, inputs[i])
		r, e := worker.claim()
		if e != nil || r.ID != m.RunID {
			t.Fatal(r.ID, e)
		}
		session, _ := s.Session(r.SessionID)
		rt := RuntimeInput{Project: personal, Session: session, Run: r}
		request := func(target, quote string) json.RawMessage {
			return json.RawMessage(encode(map[string]string{
				"projectId": target, "title": "跟进", "description": "核对记录", "quote": quote,
			}))
		}
		for _, invalid := range []json.RawMessage{request("", text), request(old.ID, text), request(p.ID, "不属于本轮的授权")} {
			if _, e = s.appTool(rt, "create_task", invalid); e == nil {
				t.Fatal("accepted invalid request", string(invalid))
			}
		}
		v, e := s.appTool(rt, "create_task", request(p.ID, text))
		if e != nil {
			t.Fatal(e)
		}
		got := v.(taskCreation)
		retry, e := s.appTool(rt, "create_task", request(p.ID, "跟进记为待处理任务"))
		if e != nil || retry != got {
			t.Fatal("retry changed result", retry, e)
		}
		rows, e := s.Tasks(p.ID)
		if e != nil || len(rows) != 1 || rows[0].ID != got.ID || got.State != "pending" || !strings.Contains(got.URL, "task="+got.ID) {
			t.Fatal(rows, got, e)
		}
		for _, edit := range []func(*RuntimeInput){
			func(in *RuntimeInput) { in.Run.Agent = &AgentMessage{} },
			func(in *RuntimeInput) { in.Run.ComponentConfig = &ComponentRunConfig{} },
			func(in *RuntimeInput) { in.Run.Source = "automation" },
			func(in *RuntimeInput) { in.Run.FollowupID = "followup" },
			func(in *RuntimeInput) { in.Run.ScheduleID = "schedule" },
			func(in *RuntimeInput) { in.Run.EventID = "decision-answer:one" },
			func(in *RuntimeInput) { in.Routing = true },
		} {
			other := rt
			edit(&other)
			if _, e = s.appTool(other, "create_task", request(p.ID, text)); e == nil {
				t.Fatal("accepted a non-owner round")
			}
		}
		if e = worker.Stop(r.ID); e != nil {
			t.Fatal(e)
		}
		late := json.RawMessage(strings.Replace(string(request(p.ID, text)), "核对记录", "停止后的请求", 1))
		if _, e = s.appTool(rt, "create_task", late); e == nil {
			t.Fatal("accepted a write after Stop")
		}
		r.State = "completed"
		if e = s.saveRun(r); e != nil {
			t.Fatal(e)
		}
		if _, e = s.DB.Exec("DELETE FROM project_locks WHERE run_id=?", r.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = s.appTool(rt, "create_task", request(p.ID, text)); e == nil {
			t.Fatal("accepted a stale runtime")
		}
	}
}
