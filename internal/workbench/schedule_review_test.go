package workbench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestChatPausePreservesConditionalSchedule(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "对话暂停")
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "周报", Cron: "0 9 * * *", Enabled: true, SourceScope: "资料", Condition: "changed", Output: "project_context", Instruction: "更新说明"})
	if e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]any{"schedule": map[string]any{"id": sc.ID, "enabled": false}})
	if _, e = s.appTool(RuntimeInput{Project: p, Run: Run{Source: "web", ID: newID()}}, "save_schedule", body); e != nil {
		t.Fatal(e)
	}
	got, _ := s.Schedule(sc.ID)
	if got.Enabled || got.Condition != sc.Condition || got.Output != sc.Output || got.SourceScope != sc.SourceScope || got.Name != sc.Name {
		t.Fatalf("pause changed policy: %+v", got)
	}
}
func TestScheduleOwnFileDoesNotTriggerReplay(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "自己的输出")
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "日报", Cron: "0 9 * * *", SourceScope: "资料", Condition: "changed", Instruction: "写入日报"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveFile(p.ID, "资料/输入.md", []byte("变化"), ""); e != nil {
		t.Fatal(e)
	}
	root, _ := s.ProjectRoot(p.ID)
	f := &summaryExecutor{hook: func() {
		if e := os.WriteFile(filepath.Join(root, "资料", "日报.md"), []byte("任务自行写入"), 0600); e != nil {
			t.Fatal(e)
		}
	}}
	w := NewWorker(s, f, nil)
	q, e := s.RunSchedule(sc.ID, "own-output-first")
	if e != nil {
		t.Fatal(e)
	}
	r, e := w.claim()
	if e != nil || r.ID != q.RunID {
		t.Fatal(r, e)
	}
	w.execute(context.Background(), r)
	after, e := s.RunSchedule(sc.ID, "no-new-work")
	if e != nil || after.Outcome != "skipped" {
		t.Fatalf("self-triggered: %+v %v", after, e)
	}
	_, h, _ := s.ReadFile(p.ID, "资料/日报.md")
	if _, e = s.SaveFile(p.ID, "资料/日报.md", []byte("人工改了同一份文件"), h); e != nil {
		t.Fatal(e)
	}
	after, e = s.RunSchedule(sc.ID, "human-edited")
	if e != nil || after.Outcome != "queued" {
		t.Fatal(after, e)
	}
}

func TestScopedPublicationAndConcurrentRecent(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "项目说明")
	save := func(name, text string) {
		_, h, _ := s.ReadFile(p.ID, name)
		if _, e := s.SaveFile(p.ID, name, []byte(text), h); e != nil {
			t.Fatal(e)
		}
	}
	save("A.md", "A的证据")
	save("B.md", "B的证据")
	save("B2.md", "B2必须保留")
	save("无关.md", "旧")
	makeSchedule := func(scope string) Schedule {
		v, e := s.SaveSchedule(Schedule{Name: "说明", ProjectID: p.ID, Condition: "changed", Output: "project_context", Cron: "0 7 * * *", SourceScope: scope, Instruction: "整理"})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	a, b := makeSchedule("A.md"), makeSchedule("B.md\nB2.md")
	f := &summaryExecutor{}
	w := NewWorker(s, f, nil)
	run := func(sc Schedule, result string) {
		f.result = &RuntimeResult{Text: encode(map[string]any{"description": result, "recent": []ContextChange{}})}
		q, e := s.RunSchedule(sc.ID, newID())
		if e != nil || q.RunID == "" {
			t.Fatal(q, e)
		}
		r, e := w.claim()
		if e != nil {
			t.Fatal(e)
		}
		w.execute(context.Background(), r)
		r, _ = s.Run(r.ID)
		if r.State != "completed" {
			t.Fatal(r.Error)
		}
	}
	run(b, "B完成")
	run(a, "含A与B的共同说明")
	save("无关.md", "人工改了范围外资料")
	cat, _ := s.ContextCatalog()
	if cat[0].Description != "含A与B的共同说明" {
		t.Fatal("out-of-scope invalidation", cat[0].Description)
	}
	q, e := s.RunSchedule(a.ID, newID())
	if e != nil || q.Outcome != "skipped" {
		t.Fatal(q, e)
	}
	save("B新.md", "补充")
	b.SourceScope += "\nB新.md"
	b, e = s.SaveSchedule(b)
	if e != nil {
		t.Fatal(e)
	}
	f.hook = func() {
		_, e := s.SaveTask(ProjectTask{ProjectID: p.ID, Title: "执行期间新增", Operation: newID()})
		if e != nil {
			t.Fatal(e)
		}
	}
	run(b, "新的联合说明")
	if prompt := f.prompts[len(f.prompts)-1]; !strings.Contains(prompt, "B2必须保留") || !strings.Contains(prompt, "含A与B的共同说明") {
		t.Fatal("did not rebase evidence", prompt)
	}
	c, _ := s.ProjectContext(p.ID)
	if len(c.Recent) != 1 || !strings.Contains(c.Recent[0].Text, "执行期间新增") {
		t.Fatal(c.Recent)
	}
	save("B.md", "B发生实质纠正")
	q, e = s.RunSchedule(b.ID, newID())
	if e != nil || q.RunID == "" {
		t.Fatal(q, e)
	}
	rr, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	w.execute(context.Background(), rr)
	rr, _ = s.Run(rr.ID)
	c, _ = s.ProjectContext(p.ID)
	if rr.State != "failed" || c.Description != "新的联合说明" {
		t.Fatal("partial rebuild lost other writer", rr.State, c.Description)
	}

}
func TestProjectedTaskDoesNotChangeWhenPreparingDraft(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "草稿")
	v, e := s.SaveTask(ProjectTask{ProjectID: p.ID, Title: "意图", Operation: newID()})
	if e != nil {
		t.Fatal(e)
	}
	sc := Schedule{ProjectID: p.ID, ID: newID(), SourceScope: "@任务"}
	a, e := s.scheduleSources(p, sc)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PrepareTask(v.ID, false, false); e != nil {
		t.Fatal(e)
	}
	b, e := s.scheduleSources(p, sc)
	if e != nil || scheduleFingerprint(a) != scheduleFingerprint(b) {
		t.Fatal("draft changed task fingerprint", e)
	}
}
func TestReviewExpertResourcesAndPartialUTF8(t *testing.T) {
	d := t.TempDir()
	if e := os.MkdirAll(filepath.Join(d, "references"), 0700); e != nil {
		t.Fatal(e)
	}
	for k, v := range map[string]string{"口径.md": "正式口径。详见 同级.md", "同级.md": "附录资料", "其他.md": "不应带入"} {
		if e := os.WriteFile(filepath.Join(d, "references", k), []byte(v), 0600); e != nil {
			t.Fatal(e)
		}
	}
	ref, e := expertReferences(d, "依据 references/口径.md")
	if e != nil || !strings.Contains(ref, "附录资料") || strings.Contains(ref, "不应带入") {
		t.Fatal(ref, e)
	}
	data := []byte(strings.Repeat("汉", 1400000))
	v, status := briefExcerpt("file:说明.md", data[:4<<20], false)
	if status != "partial" || !strings.Contains(v, "汉汉") {
		t.Fatal(status)
	}
}
func TestExpertGateAppliesToEnableAndManual(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "专家")
	ex, e := s.CreateExpert("expert-review", "方法", "处理")
	if e != nil {
		t.Fatal(e)
	}
	p.ExpertIDs = []string{ex.ID}
	p, e = s.updateProject(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "整理", ExpertID: ex.ID, Cron: "0 7 * * *", Instruction: "处理", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	p.ExpertIDs = nil
	if _, e = s.updateProject(p.ID, p); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RunSchedule(sc.ID, newID()); e == nil {
		t.Fatal("manual bypassed missing expert")
	}
	if _, _, e = s.toggleSchedule(sc.ID, true); e == nil {
		t.Fatal("enable bypassed missing expert")
	}
	if _, _, e = s.toggleSchedule(sc.ID, false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RunSchedule(sc.ID, newID()); e == nil {
		t.Fatal("pause cleared expert gate")
	}
}
func TestReviewScratchCleanup(t *testing.T) {
	root := t.TempDir()
	h := Harness{Config: Config{DataDir: root, RuntimeMode: "native"}}
	mk := func(dir, id string) {
		p := filepath.Join(root, "runtime", dir, id)
		if e := os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(p, "transcript"), []byte("private"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, d := range []string{"router", "router-home", "projects"} {
		mk(d, "run-a")
		mk(d, "run-b")
	}
	if e := h.Cleanup(context.Background(), "run-a"); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, "runtime/router-home/run-a")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, "runtime/router-home/run-b")); e != nil {
		t.Fatal(e)
	}
	if e := h.CleanupOrphans(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, "runtime/router-home")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, "runtime/projects/run-a/transcript")); e != nil {
		t.Fatal(e)
	}
}
func TestSelectedFilesIgnoreNamedPipes(t *testing.T) {
	d := t.TempDir()
	if e := syscall.Mkfifo(filepath.Join(d, "never-read"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(d, "ok.md"), []byte("ok"), 0600); e != nil {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(d)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	for _, scope := range []string{"", "@任务", "ok.md"} {
		items, e := scheduleFiles(context.Background(), root, scope)
		if e != nil || len(items) > 1 || (scope == "@任务" && len(items) != 0) {
			t.Fatal(items, e)
		}
	}
}

func TestRepairInvalidLegacyPublicationWithoutModel(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "历史发布")
	_, e := s.SaveFile(p.ID, "范围.md", []byte("内容"), "")
	if e != nil {
		t.Fatal(e)
	}
	sc, e := s.SaveSchedule(Schedule{Name: "说明", ProjectID: p.ID, Condition: "changed", Output: "project_context", SourceScope: "范围.md", Cron: "0 7 * * *", Instruction: "整理"})
	if e != nil {
		t.Fatal(e)
	}
	w := NewWorker(s, &summaryExecutor{}, nil)
	_, e = s.RunSchedule(sc.ID, newID())
	if e != nil {
		t.Fatal(e)
	}
	r, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	w.execute(context.Background(), r)
	s.markContext(p.ID, true)
	q, e := s.RunSchedule(sc.ID, newID())
	if e != nil || q.Outcome != "skipped" {
		t.Fatal(q, e)
	}
	c, _ := s.ProjectContext(p.ID)
	if c.State != "ready" || len(c.Sources) != 1 {
		t.Fatal(c.State)
	}
}
