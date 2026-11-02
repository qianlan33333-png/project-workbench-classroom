package workbench

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestChangedScheduleManualFailureRecovery(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "验收项目")
	sc, e := s.SaveSchedule(Schedule{Name: "说明", ProjectID: p.ID, Cron: "0 7 * * *", Instruction: "核对来源", Condition: "changed", Output: "project_context", SourceScope: "资料", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.RunSchedule(sc.ID, "empty-first")
	if e != nil || first.Outcome != "skipped" || first.RunID != "" {
		t.Fatal(first, e)
	}
	f := &summaryExecutor{}
	w := NewWorker(s, f, nil)
	_, e = s.SaveFile(p.ID, "资料/README.md", []byte("交付仍待验收"), "")
	if e != nil {
		t.Fatal(e)
	}
	text := `{"catalog":["` + strings.Repeat("x", 80000) + `"]}`
	_, e = s.SaveFile(p.ID, "资料/catalog.json", []byte(text), "")
	if e != nil {
		t.Fatal(e)
	}
	trigger := func(key string) ScheduleReceipt {
		r, e := s.RunSchedule(sc.ID, key)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	runNext := func() Run {
		r, e := w.claim()
		if e != nil {
			t.Fatal(e)
		}
		w.execute(context.Background(), r)
		r, e = s.Run(r.ID)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	q := trigger("first-change")
	if q.RunID == "" || trigger("first-change").RunID != q.RunID || trigger("while-active").RunID != q.RunID {
		t.Fatal("duplicate admission")
	}
	r := runNext()
	if r.State != "completed" || f.calls != 1 || !strings.Contains(f.prompts[0], "JSON 结构") || strings.Contains(f.prompts[0], strings.Repeat("x", 100)) {
		t.Fatal(r.State, r.Error)
	}
	c, _ := s.ProjectContext(p.ID)
	if c.Version != 1 || len(c.Sources) != 2 {
		t.Fatal(c)
	}
	if x := trigger("no-changes"); x.Outcome != "skipped" || x.RunID != "" {
		t.Fatal(x)
	}
	_, e = s.SaveFile(p.ID, "资料/新.md", []byte("新增"), "")
	if e != nil {
		t.Fatal(e)
	}
	f.err = errors.New("模拟供应商失败")
	q = trigger("try-once")
	r = runNext()
	sc, _ = s.Schedule(sc.ID)
	if r.State != "failed" || sc.Enabled || !strings.Contains(sc.PausedReason, "供应商") || f.calls != 2 {
		t.Fatal(r, sc)
	}
	if trigger("try-once").RunID != q.RunID {
		t.Fatal("replayed failed operation")
	}
	sc.Instruction = "已经修订"
	sc, e = s.SaveSchedule(sc)
	if e != nil || sc.Enabled {
		t.Fatal(e, sc)
	}
	if e = w.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.FireSchedules(time.Now().AddDate(0, 0, 2)); e != nil {
		t.Fatal(e)
	}
	if _, e = w.claim(); !errors.Is(e, ErrNotFound) {
		t.Fatal("automatic replay", e)
	}
	f.err = nil
	trigger("manual-retry")
	r = runNext()
	sc, _ = s.Schedule(sc.ID)
	if sc.Enabled || r.State != "completed" || f.calls != 3 {
		t.Fatal(sc, r.State, r.Error)
	}
}
func TestChangedSchedulePreservesOwnerCorrection(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "竞态")
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "说明", Instruction: "整理", Cron: "0 7 * * *", Enabled: true, Output: "project_context", Condition: "changed"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveFile(p.ID, "input.md", []byte("需核对"), ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RunSchedule(sc.ID, "test-conflict"); e != nil {
		t.Fatal(e)
	}
	w := NewWorker(s, &summaryExecutor{hook: func() {
		c, _ := s.ProjectContext(p.ID)
		if _, e := s.CorrectContext(p.ID, "本人新说明", c.Version); e != nil {
			t.Fatal(e)
		}
	}}, nil)
	r, _ := w.claim()
	w.execute(context.Background(), r)
	updated, _ := s.Project(p.ID)
	sc, _ = s.Schedule(sc.ID)
	r, _ = s.Run(r.ID)
	if updated.Description != "本人新说明" || sc.Enabled || r.State != "failed" {
		t.Fatal(updated.Description, sc.Enabled, r.State)
	}
}

func TestConditionalCronUsesSameCheckpointAndInterruptPauses(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "定时检查")
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "摘要", Cron: "0 7 * * *", Enabled: true, Condition: "changed", Output: "project_context", SourceScope: "input.md", Instruction: "整理"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveFile(p.ID, "input.md", []byte("有新资料"), ""); e != nil {
		t.Fatal(e)
	}
	at := time.UnixMilli(sc.NextAt)
	w := NewWorker(s, &summaryExecutor{}, nil)
	for i := 0; i < 2; i++ {
		if e = s.FireSchedules(at); e != nil {
			t.Fatal(e)
		}
	}
	r, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	w.execute(context.Background(), r)
	if e = s.FireSchedules(at.Add(24 * time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e = w.claim(); !errors.Is(e, ErrNotFound) {
		t.Fatal("unchanged cron created run", e)
	}
	original, hash, e := s.ReadFile(p.ID, "input.md")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveFile(p.ID, "input.md", append(original, []byte("，再次修改")...), hash); e != nil {
		t.Fatal(e)
	}
	if e = s.FireSchedules(at.Add(48 * time.Hour)); e != nil {
		t.Fatal(e)
	}
	interrupted, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	stop, e := s.Schedule(sc.ID)
	if e != nil || stop.Enabled || stop.PausedReason == "" {
		t.Fatal(stop, e)
	}
	r, e = s.Run(interrupted.ID)
	if e != nil || r.State != "interrupted" {
		t.Fatal(r.State, e)
	}
	if e = s.FireSchedules(at.Add(72 * time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e = w.claim(); !errors.Is(e, ErrNotFound) {
		t.Fatal("interrupted run replayed", e)
	}
}
