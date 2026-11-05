package workbench

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectMergeRetainsIdentityAndReadScope(t *testing.T) {
	s := testStore(t)
	old := testProject(t, s, "旧能力")
	target := testProject(t, s, "私域运营")
	other := testProject(t, s, "其他项目")
	original, _ := s.Session(old.DefaultSessionID)
	run := Run{ID: newID(), SessionID: original.ID, ProjectID: old.ID, State: "completed", Result: "UNIQUE_OLD_HISTORY", CreatedAt: now()}
	m, e := s.Submit(Incoming{Source: "web", EventID: "history-fixture", SessionID: original.ID, Text: "历史输入"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("INSERT INTO runs VALUES(?,?,?,?,?,?,?,?)", run.ID, old.ID, original.ID, m.ID, "completed", 0, now(), encode(run)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CommitProjectMerge(target.ID, ProjectMerge{SourceIDs: []string{old.ID}}); e != nil {
		t.Fatal(e)
	}
	ss, _ := s.rawSession(original.ID)
	if ss.ProjectID != old.ID || ss.HarnessID != original.HarnessID {
		t.Fatal("historical session identity changed")
	}
	sources, _ := s.ProjectHistorySources(target.ID)
	if len(sources) != 1 {
		t.Fatal(sources)
	}
	hits, e := s.SessionSearch(target.ID, "UNIQUE_OLD_HISTORY")
	if e != nil || len(hits) != 1 {
		t.Fatal(hits, e)
	}
	_, e = s.appTool(RuntimeInput{Project: target, Session: Session{ProjectID: target.ID}}, "read_run", json.RawMessage(encode(map[string]string{"runId": run.ID})))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ProjectHistoryRuns(other.ID, original.ID); e == nil {
		t.Fatal("unrelated project read retained history")
	}
	if _, e = s.SubmitComponent(context.Background(), original.ID, ComponentRequest{}); e == nil {
		t.Fatal("archived project accepted write")
	}
}
func TestProjectMergeScheduleSnapshotTimingAndBusyLock(t *testing.T) {
	s := testStore(t)
	source := testProject(t, s, "原项目")
	target := testProject(t, s, "统一项目")
	from, e := s.SaveSchedule(Schedule{ProjectID: source.ID, SessionID: source.DefaultSessionID, Name: "原规则", Instruction: "保留目标", Cron: "10 9 * * *", Timezone: "Asia/Shanghai", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	to, e := s.SaveSchedule(Schedule{ProjectID: target.ID, SessionID: target.DefaultSessionID, Name: "原规则", Instruction: "保留目标", Cron: from.Cron, Timezone: from.Timezone, Enabled: false})
	if e != nil {
		t.Fatal(e)
	}
	q := ProjectMerge{SourceIDs: []string{source.ID}}
	q.ScheduleMoves = append(q.ScheduleMoves, struct {
		FromID string   `json:"fromId"`
		ToID   string   `json:"toId"`
		Source Schedule `json:"source"`
		Target Schedule `json:"target"`
	}{from.ID, to.ID, from, to})
	s.scheduleMu.Lock()
	_, e = s.CommitProjectMerge(target.ID, q)
	s.scheduleMu.Unlock()
	if e != ErrLocked {
		t.Fatal("merge raced scheduler", e)
	}
	originalNext := from.NextAt
	changed := from
	changed.Instruction = "用户新目标"
	if _, e = s.SaveSchedule(changed); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CommitProjectMerge(target.ID, q); e != ErrConflict {
		t.Fatal("source drift accepted", e)
	}
	from, _ = s.Schedule(from.ID)
	q.ScheduleMoves[0].Source = from
	if _, e = s.CommitProjectMerge(target.ID, q); e != nil {
		t.Fatal(e)
	}
	current, _ := s.Schedule(to.ID)
	old, _ := s.Schedule(from.ID)
	if current.Instruction != "用户新目标" || current.NextAt != originalNext || !current.Enabled || old.Enabled {
		t.Fatal("timing or latest exact definition lost", current, old)
	}
}
func TestProjectMergeRebindsExpertSchedulePrompt(t *testing.T) {
	s := testStore(t)
	source := testProject(t, s, "原推荐")
	target := testProject(t, s, "统一运营")
	ex, e := s.CreateExpert("推荐方法", "只读候选", "只依据现有事实推荐，不发送")
	if e != nil {
		t.Fatal(e)
	}
	source.ExpertIDs = []string{ex.ID}
	source, _ = s.UpdateProject(source.ID, source)
	target.ExpertIDs = []string{ex.ID}
	target, _ = s.UpdateProject(target.ID, target)
	oldPlugin := componentFixture(t, s, source)
	oldPlugin.Component.Actions[0].ReadOnly = true
	s.SavePlugin(oldPlugin)
	newPlugin := componentFixture(t, s, target)
	newPlugin.Component.Actions[0].ReadOnly = true
	s.SavePlugin(newPlugin)
	oldConfig := ComponentRunConfig{SessionID: source.DefaultSessionID, PluginID: oldPlugin.ID, PluginVersion: "1", Action: "start", Mode: "expert", ExpertID: ex.ID, ExpertVersion: ex.Digest, Goal: "保留固定推荐目标", Arguments: map[string]any{"limit": 5}}
	newConfig := oldConfig
	newConfig.SessionID = target.DefaultSessionID
	newConfig.PluginID = newPlugin.ID
	from, e := s.SaveSchedule(Schedule{ProjectID: source.ID, SessionID: source.DefaultSessionID, Name: "推荐", Instruction: "专家配置", Cron: "10 9 * * *", Timezone: "Asia/Shanghai", Enabled: true, Component: &oldConfig})
	if e != nil {
		t.Fatal(e)
	}
	to, e := s.SaveSchedule(Schedule{ProjectID: target.ID, SessionID: target.DefaultSessionID, Name: "推荐", Instruction: "专家配置", Cron: from.Cron, Timezone: from.Timezone, Enabled: false, Component: &newConfig})
	if e != nil {
		t.Fatal(e)
	}
	q := ProjectMerge{SourceIDs: []string{source.ID}}
	q.ScheduleMoves = append(q.ScheduleMoves, struct {
		FromID string   `json:"fromId"`
		ToID   string   `json:"toId"`
		Source Schedule `json:"source"`
		Target Schedule `json:"target"`
	}{from.ID, to.ID, from, to})
	if _, e = s.CommitProjectMerge(target.ID, q); e != nil {
		t.Fatal(e)
	}
	rebound, _ := s.Schedule(to.ID)
	if strings.Contains(rebound.Instruction, oldPlugin.ID) || !strings.Contains(rebound.Instruction, newPlugin.ID) || rebound.Component.Goal != oldConfig.Goal {
		t.Fatal("expert schedule retained wrong binding")
	}
	receipt, e := s.RunSchedule(to.ID, "merge-expert-once")
	if e != nil {
		t.Fatal(e)
	}
	run, e := s.Run(receipt.RunID)
	if e != nil || !strings.Contains(run.Text, newPlugin.ID) || strings.Contains(run.Text, oldPlugin.ID) {
		t.Fatal("migrated actual Run prompt uses old binding", e)
	}
}
