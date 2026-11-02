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

func TestPlannerShiftSurvivesRestartAndDoesNotDoubleFire(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "日历")
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "周报", Instruction: "整理", Cron: "0 18 * * 2", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	original := sc.NextAt
	moved := original + 7200000
	sc, e = s.editTime(sc.ID, "occurrence", TimeEdit{Original: original, At: moved, Version: sc.Version})
	if e != nil || sc.NextAt != moved {
		t.Fatal(sc, e)
	}
	restarted, e := NewStore(s.DB, s.Root)
	if e != nil {
		t.Fatal(e)
	}
	for _, at := range []int64{original, moved, moved} {
		if e = restarted.FireSchedules(time.UnixMilli(at)); e != nil {
			t.Fatal(e)
		}
	}
	rs, e := s.Runs("")
	if e != nil || len(rs) != 1 || rs[0].ScheduledFor != moved || !rs[0].ConversationOutput {
		t.Fatal(rs, e)
	}
	sc, _ = s.Schedule(sc.ID)
	next := original + 7*24*3600000
	if sc.NextAt != next {
		t.Fatal(sc)
	}
	later := next + 7*24*3600000
	sc, e = s.editTime(sc.ID, "occurrence", TimeEdit{Original: later, At: later + 3600000, Version: sc.Version})
	if e != nil {
		t.Fatal(e)
	}
	edit := TimeEdit{Original: next, Cron: "0 20 * * 3", Version: sc.Version}
	if _, e = s.editTime(sc.ID, "series", edit); e == nil {
		t.Fatal("lost later exception without confirmation")
	}
	edit.ReplaceExceptions = true
	sc, e = s.editTime(sc.ID, "series", edit)
	if e != nil {
		t.Fatal(e)
	}
	v, e := scheduleTick(sc, next-1)
	if e != nil || time.UnixMilli(v.At).In(calendarZone()).Weekday() != time.Wednesday {
		t.Fatal(v, e)
	}
}
func TestPlannerScopeAndFailureInbox(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "甲")
	q := testProject(t, s, "乙")
	sc := Schedule{ProjectID: p.ID, Name: "甲会话", Instruction: "continue", SessionID: q.DefaultSessionID, Cron: "0 10 * * *"}
	if _, e := s.SaveSchedule(sc); e == nil {
		t.Fatal("cross-project session")
	}
	ss, e := s.CreateSession(p.ID, "验收", "topic", "")
	if e != nil {
		t.Fatal(e)
	}
	sc.SessionID = ss.ID
	sc, e = s.SaveSchedule(sc)
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := s.RunSchedule(sc.ID, "first-check")
	if e != nil {
		t.Fatal(e)
	}
	run, _ := s.Run(receipt.RunID)
	if run.SessionID != ss.ID {
		t.Fatal(run)
	}
	run.State = "failed"
	run.Error = "测试失败"
	run.FinishedAt = now()
	for i := 0; i < 2; i++ {
		if e = s.saveRun(run); e != nil {
			t.Fatal(e)
		}
	}
	var id string
	var n int
	if e = s.DB.QueryRow("SELECT count(*),min(id) FROM user_notices").Scan(&n, &id); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	f, e := s.failure(id)
	if e != nil || f.Run != run.ID || f.Detail != run.Error {
		t.Fatal(f, e)
	}
	retry, e := s.RunSchedule(sc.ID, "retry-"+id)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.RunSchedule(sc.ID, "retry-"+id)
	if e != nil || retry.RunID != again.RunID {
		t.Fatal(again, e)
	}
	if _, e = s.DB.Exec("UPDATE user_notices SET cleared=1,seen=1"); e != nil {
		t.Fatal(e)
	}
	f, e = s.failure(id)
	if e != nil || f.Retry == nil || f.Retry.RunID != retry.RunID {
		t.Fatal(f, e)
	}
	latest, _ := s.Schedule(sc.ID)
	if latest.Enabled {
		t.Fatal("retry enabled rule")
	}
	rr, _ := s.Run(retry.RunID)
	rr.State = "completed"
	s.saveRun(rr)
	s.DB.Exec("UPDATE sessions SET data=json_set(data,'$.recycledAt',1) WHERE id=?", ss.ID)
	pre, e := s.RunSchedule(sc.ID, "preflight-failed")
	if e != nil || pre.Outcome != "failed" || pre.RunID != "" {
		t.Fatal(pre, e)
	}
	s.DB.QueryRow("SELECT count(*) FROM user_notices WHERE cleared=0").Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
func TestCalendarUnknownCreateReconcilesOnceAndExternalScope(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "测试")
	ctx := context.Background()
	start := now() + 3600000
	cli := toolPresets()[0]
	if e := os.MkdirAll(filepath.Dir(s.toolBinary(cli)), 0700); e != nil {
		t.Fatal(e)
	}
	state := filepath.Join(s.Root, "provider-key")
	body := "#!/bin/sh\ncase \"$3\" in\ncreate) while [ \"$1\" != '--idempotency-key' ]; do shift; done; shift; if ! test -f '" + state + "'; then echo \"$1\" > '" + state + "'; exit 1; fi; test \"$(cat '" + state + "')\" = \"$1\" || exit 3; echo '{\"ok\":true,\"data\":{\"event\":{\"event_id\":\"ext-1\"}}}';;\ninstance_view) echo '{\"ok\":true,\"data\":{\"items\":[]}}';;\nesac\n"
	if e := os.WriteFile(s.toolBinary(cli), []byte(body), 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SaveCLITool(cli); e != nil {
		t.Fatal(e)
	}
	draft := CalendarEvent{Operation: "stable-write", Title: "验收", Start: start, End: start + 3600000, Destination: "both", ProjectID: p.ID}
	v, e := s.changeEvent(ctx, "create", draft, p.ID)
	if e != nil || v.Sync != "unknown" {
		t.Fatal(v, e)
	}
	same, e := s.changeEvent(ctx, "create", draft, p.ID)
	if e != nil || same.ID != v.ID {
		t.Fatal(same, e)
	}
	v, e = s.changeEvent(ctx, "reconcile", v, p.ID)
	if e != nil || v.RemoteID != "ext-1" || v.Pending != "" {
		t.Fatal(v, e)
	}
	bad := v
	bad.Version--
	if _, e = s.changeEvent(ctx, "edit", bad, p.ID); e != ErrConflict {
		t.Fatal(e)
	}
	if _, e = s.changeEvent(ctx, "delete", v, "another"); e != ErrNotFound {
		t.Fatal(e)
	}
}
func TestPersonalTodoStartsOnlyFromOwnDraft(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	task, e := s.SaveTask(ProjectTask{ProjectID: p.ID, Title: "个人任务", Operation: newID()})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.PrepareTask(task.ID, false, false)
	if e != nil || d.Session.ID != p.DefaultSessionID || !strings.Contains(d.Text, task.Title) {
		t.Fatal(d, e)
	}
	in := Incoming{Source: "web", EventID: newID(), SessionID: d.Session.ID, Text: "无关消息"}
	submitRoute(t, s, in)
	task, _ = s.Task(task.ID)
	if task.State != "pending" {
		t.Fatal(task)
	}
	in.EventID = newID()
	in.TaskID = task.ID
	submitRoute(t, s, in)
	task, _ = s.Task(task.ID)
	if task.State != "in_progress" {
		t.Fatal(task)
	}
	raw, _ := json.Marshal(map[string]any{"projectId": p.ID, "schedule": map[string]any{"name": "固定归属", "instruction": "执行", "cron": "0 9 * * *"}})
	if _, e = s.appTool(RuntimeInput{Project: p, Run: Run{ID: newID(), Source: "web"}}, "save_schedule", raw); e != nil {
		t.Fatal(e)
	}
}
