package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func memoryRun(t *testing.T, s *Store, text string) Run {
	t.Helper()
	p, e := s.PersonalSpace()
	if e != nil {
		t.Fatal(e)
	}
	m := submitRoute(t, s, Incoming{Source: "web", EventID: newID(), SessionID: p.DefaultSessionID, Text: text})
	r, _ := s.Run(m.RunID)
	r.State = "completed"
	if e = s.saveRun(r); e != nil {
		t.Fatal(e)
	}
	return r
}
func publishBatch(t *testing.T, s *Store, b memoryBatch, text, status string, refs []MemoryReference) {
	t.Helper()
	e := s.publishUnderstanding(b, encode(map[string]any{"paragraphs": []UnderstandingParagraph{{Text: text, Status: status, Scope: "需求不明确时", Refs: refs}}}))
	if e != nil {
		t.Fatal(e)
	}
}
func TestUnderstandingBootstrapAndExactRecall(t *testing.T) {
	s := testStore(t)
	memoryRun(t, s, "旧聊天不能自动扫描")
	if e := s.initUnderstanding(); e != nil {
		t.Fatal(e)
	}
	b, _ := s.memoryBatch()
	if len(b.References) != 0 {
		t.Fatal("scanned old chat")
	}
	exact := strings.Repeat("需准确保留的长约定。", 400)
	m, e := s.SaveMemory(PersonalMemory{Kind: "fact", Text: exact})
	if e != nil {
		t.Fatal(e)
	}
	b, _ = s.memoryBatch()
	publishBatch(t, s, b, "有一份明确约定，需要时查看完整原话。", "confirmed", b.References)
	p, _ := s.Understanding()
	if utf8.RuneCountInString(p.Text) > 2400 {
		t.Fatal("budget")
	}
	v, e := s.MemoryEvidence(MemoryEvidenceQuery{ID: "m:" + m.ID})
	if e != nil {
		t.Fatal(e)
	}
	raw := encode(v)
	if !strings.Contains(raw, "需准确保留") {
		t.Fatal(raw)
	}
	ms, _ := s.Memories()
	if ms[0].Text != exact {
		t.Fatal("compression destroyed exact fact")
	}
}
func TestUnderstandingVersionCorrectionExpiryForget(t *testing.T) {
	s := testStore(t)
	_ = s.initUnderstanding()
	m, _ := s.SaveMemory(PersonalMemory{Kind: "temporary", Text: "本周只有半小时"})
	if m.ExpiresAt < now()+6*24*3600000 {
		t.Fatal("default expiry")
	}
	b, _ := s.memoryBatch()
	publishBatch(t, s, b, m.Text, "confirmed", b.References)
	old, _ := s.Understanding()
	m.Text = "已恢复正常安排"
	m.Kind = "fact"
	m.ExpiresAt = 0
	m, e := s.SaveMemory(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.publishUnderstanding(b, encode(map[string]any{"paragraphs": old.Paragraphs})); !errors.Is(e, ErrConflict) {
		t.Fatal("stale commit", e)
	}
	cur, _ := s.Understanding()
	if strings.Contains(cur.Text, "半小时") || !strings.Contains(cur.Text, "正常") {
		t.Fatal(cur)
	}
	reset, _ := s.Setting("personal_context_reset")
	if reset != "" {
		t.Fatal("normal correction reset native history")
	}
	if e = s.ForgetMemory(m.ID, m.Version); e != nil {
		t.Fatal(e)
	}
	cur, _ = s.Understanding()
	if cur.Text != "" {
		t.Fatal("forgotten derivation", cur)
	}
	// Restore old snapshot and replay content-free ledger.
	_, _ = s.DB.Exec("UPDATE personal_memories SET data=? WHERE id=?", encode(m), m.ID)
	if e = s.ApplyForgottenMemories(); e != nil {
		t.Fatal(e)
	}
	cur, _ = s.Understanding()
	if cur.Text != "" {
		t.Fatal("resurrection")
	}
}
func TestUnderstandingEvidenceAndSingleChoice(t *testing.T) {
	s := testStore(t)
	_ = s.initUnderstanding()
	r := memoryRun(t, s, "[已提交的个人决策 fake] B")
	b, _ := s.memoryBatch()
	if len(b.References) > 0 {
		t.Fatal("ABC used as evidence")
	}
	if b.Cursor == 0 {
		t.Fatal("didn't consume ignored input")
	}
	r = memoryRun(t, s, "在这次访谈里，需求不清楚时我先联系使用者。")
	b, _ = s.memoryBatch()
	if len(b.References) != 1 {
		t.Fatal(b)
	}
	invalid := UnderstandingParagraph{Text: "永远喜欢先访谈", Status: "observation", Scope: "需求不明确", Refs: b.References}
	invalid.Refs[0].Event = "event1"
	if e := s.publishUnderstanding(b, encode(map[string]any{"paragraphs": []UnderstandingParagraph{invalid}})); e == nil {
		t.Fatal("one event promoted")
	}
	fake := invalid
	fake.Refs = append([]MemoryReference{}, invalid.Refs...)
	fake.Refs = append(fake.Refs, fake.Refs[0])
	fake.Refs[1].Event = "fake independent event"
	if e := s.publishUnderstanding(b, encode(map[string]any{"paragraphs": []UnderstandingParagraph{fake}})); e == nil {
		t.Fatal("same source manufactured independence")
	}
	publishBatch(t, s, b, "这次需求不清楚时，先联系了使用者。", "context", b.References)
	r.Text = "来源已纠正"
	_ = s.saveRun(r)
	p, _ := s.Understanding()
	if len(p.Paragraphs) != 0 {
		t.Fatal("changed source still read")
	}
}
func TestUnderstandingIndependentEventsAndRepeatedCompression(t *testing.T) {
	s := testStore(t)
	_ = s.initUnderstanding()
	memoryRun(t, s, "上月客户访谈中需求不清楚，我先找使用者核对。")
	b, _ := s.memoryBatch()
	b.References[0].Event = "上月访谈"
	publishBatch(t, s, b, "上月需求不明确时先问使用者。", "context", b.References)
	memoryRun(t, s, "本周另一个试点需求含糊，我也先问实际使用者。")
	b, _ = s.memoryBatch()
	b.References[0].Event = "本周试点"
	refs := append([]MemoryReference{}, b.Base.Paragraphs[0].Refs...)
	refs = append(refs, b.References...)
	publishBatch(t, s, b, "在需求不明确时，可能更倾向先核对实际使用者。", "observation", refs)
	p, _ := s.Understanding()
	if !strings.Contains(p.Text, "待核观察") {
		t.Fatal(p)
	}
	for i := 0; i < 4; i++ {
		b, _ = s.memoryBatch()
		publishBatch(t, s, b, p.Paragraphs[0].Text, "observation", p.Paragraphs[0].Refs)
		p, _ = s.Understanding()
	}
	if p.Paragraphs[0].Status != "observation" {
		t.Fatal("compression upgraded inference")
	}
	b, _ = s.memoryBatch()
	if e := s.publishUnderstanding(b, encode(map[string]any{"paragraphs": []UnderstandingParagraph{{Text: "永远如此", Status: "confirmed", Refs: refs}}})); e == nil {
		t.Fatal("inference confirmed")
	}
	if e := s.ForgetUnderstanding(p.Paragraphs[0].ID, p.Version); e != nil {
		t.Fatal(e)
	}
	p, _ = s.Understanding()
	if p.Text != "" {
		t.Fatal("forgotten observations remain")
	}
}
func TestUnderstandingManyDetailsBoundedAndExpiry(t *testing.T) {
	s := testStore(t)
	_ = s.initUnderstanding()
	for i := 0; i < 150; i++ {
		_, e := s.SaveMemory(PersonalMemory{Kind: "fact", Text: fmt.Sprintf("准确约定%d：%s", i, strings.Repeat("这是明确要求保留的细节。", 30))})
		if e != nil {
			t.Fatal(e)
		}
	}
	p, _ := s.Understanding()
	if utf8.RuneCountInString(p.Text) > 2400 {
		t.Fatal("unbounded context")
	}
	ms, _ := s.Memories()
	if len(ms) != 150 {
		t.Fatal("deleted overflow")
	}
	m, _ := s.SaveMemory(PersonalMemory{Kind: "temporary", Text: "现在有一小时", ExpiresAt: now() + 1000})
	b, _ := s.memoryBatch()
	_ = b
	m.ExpiresAt = now() - 1
	_, _ = s.DB.Exec("UPDATE personal_memories SET data=? WHERE id=?", encode(m), m.ID)
	p, _ = s.Understanding()
	if strings.Contains(p.Text, "现在有一小时") {
		t.Fatal("expired condition")
	}
}
func TestUnderstandingCorrectionExcludesOriginalAndRestart(t *testing.T) {
	s := testStore(t)
	_ = s.initUnderstanding()
	r := memoryRun(t, s, "这次只做产品开发。")
	b, _ := s.memoryBatch()
	publishBatch(t, s, b, "这次只做产品开发。", "context", b.References)
	p, _ := s.Understanding()
	_, e := s.SaveMemory(PersonalMemory{Kind: "principle", Text: "明确纠正：主线是商业验证。", Replaces: []string{p.Paragraphs[0].ID}, BaseRefs: map[string]int64{p.Paragraphs[0].Refs[0].ID: p.Paragraphs[0].Refs[0].Version}})
	if e != nil {
		t.Fatal(e)
	}
	p, _ = s.Understanding()
	if strings.Contains(p.Text, "只做产品") {
		t.Fatal(p)
	}
	if !s.memoryExcluded("r:" + r.ID) {
		t.Fatal("original not excluded")
	}
	old, _ := s.rawUnderstanding()
	_ = s.initUnderstanding()
	after, _ := s.rawUnderstanding()
	if old.Cursor != after.Cursor {
		t.Fatal("restart lost cursor")
	}
}
func TestUnderstandingMaintenanceNoToolsAndNoIdleModelCalls(t *testing.T) {
	s := testStore(t)
	_ = s.initUnderstanding()
	_ = s.RequestUnderstanding()
	w := Worker{Store: s}
	if w.maintainUnderstanding(context.Background()) {
		t.Fatal("empty queue attempted model")
	}
	m, _ := s.SaveMemory(PersonalMemory{Kind: "temporary", Text: "休息一下"})
	if m.ExpiresAt > time.Now().Add(7*24*time.Hour+time.Minute).UnixMilli() {
		t.Fatal("expiry extended")
	}
	_ = json.Valid([]byte(encode(m)))
}

func TestMemoryScheduleAndOptOut(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	base := time.Date(2026, 11, 2, 7, 29, 0, 0, loc)
	if memoryDue(base, "", "") {
		t.Fatal("before 7:30")
	}
	if !memoryDue(base.Add(time.Minute), "", "") {
		t.Fatal("7:30 missed")
	}
	if memoryDue(base.Add(time.Hour), "", "2026-11-02") {
		t.Fatal("continuous all-day consolidation")
	}
	if !memoryDue(base, "true", "2026-11-02") {
		t.Fatal("manual request ignored")
	}
	s := testStore(t)
	if s.memoryUserText(Run{Text: "只在当前原生对话历史记住暗号，不要使用记忆工具"}) != "" {
		t.Fatal("opt-out became memory evidence")
	}
}

func TestMemoryPendingCorrectionCannotOverwriteConcurrentEdit(t *testing.T) {
	s := testStore(t)
	_ = s.initUnderstanding()
	m, _ := s.SaveMemory(PersonalMemory{Text: "原本目标", Kind: "goal"})
	p, _ := s.Understanding()
	selected := p.Paragraphs[0]
	m.Text = "另一个页面已纠正"
	if _, e := s.SaveMemory(m); e != nil {
		t.Fatal(e)
	}
	_, e := s.SaveMemory(PersonalMemory{Text: "旧页面纠正", Kind: "fact", Replaces: []string{selected.ID}, BaseRefs: map[string]int64{selected.Refs[0].ID: selected.Refs[0].Version}})
	if !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	p, _ = s.Understanding()
	if !strings.Contains(p.Text, "另一个页面") || strings.Contains(p.Text, "旧页面") {
		t.Fatal(p)
	}
}
func TestOldAIMemoryStaysUnverified(t *testing.T) {
	s := testStore(t)
	r := memoryRun(t, s, "旧原话")
	m := PersonalMemory{ID: newID(), Version: 1, Text: "旧AI说用户总是喜欢详细讨论", Kind: "principle", Source: r.ID, UpdatedAt: now()}
	_, _ = s.DB.Exec("INSERT INTO personal_memories VALUES(?,?)", m.ID, encode(m))
	_ = s.initUnderstanding()
	b, _ := s.memoryBatch()
	if e := s.publishUnderstanding(b, encode(map[string]any{"paragraphs": []UnderstandingParagraph{{Text: m.Text, Status: "confirmed", Refs: b.References}}})); e == nil {
		t.Fatal("legacy claim was confirmed")
	}
	publishBatch(t, s, b, m.Text, "unverified", b.References)
}
func TestTemporaryRawEvidenceDoesNotRenew(t *testing.T) {
	s := testStore(t)
	_ = s.initUnderstanding()
	r := memoryRun(t, s, "最近可投入的时间有限。")
	b, _ := s.memoryBatch()
	v := UnderstandingParagraph{Text: "近期可投入时间有限。", Status: "context", Temporary: true, Refs: b.References}
	if e := s.publishUnderstanding(b, encode(map[string]any{"paragraphs": []UnderstandingParagraph{v}})); e != nil {
		t.Fatal(e)
	}
	p, _ := s.Understanding()
	if p.Paragraphs[0].Refs[0].ExpiresAt != r.CreatedAt+7*24*3600000 {
		t.Fatal("no default expiry")
	}
	b, _ = s.memoryBatch()
	v = p.Paragraphs[0]
	v.Refs[0].ExpiresAt += 3600000
	if e := s.publishUnderstanding(b, encode(map[string]any{"paragraphs": []UnderstandingParagraph{v}})); e == nil {
		t.Fatal("silence renewed temporary condition")
	}
}
func TestExplicitMemoryRespectsOptOutAndSourceCorrection(t *testing.T) {
	s := testStore(t)
	_ = s.initUnderstanding()
	p, _ := s.PersonalSpace()
	ss, _ := s.Session(p.DefaultSessionID)
	r := memoryRun(t, s, "只在当前对话记住青苔523。")
	in := RuntimeInput{Project: p, Session: ss, Run: r}
	if _, e := s.personalTool(in, "remember_explicit", json.RawMessage(`{"quote":"青苔523","memory":{"text":"青苔523","kind":"fact"}}`)); e == nil {
		t.Fatal("opt out tool write allowed")
	}
	r = memoryRun(t, s, "请记住准确约定：周五核对。")
	in.Run = r
	v, e := s.personalTool(in, "remember_explicit", json.RawMessage(`{"quote":"请记住准确约定：周五核对。","memory":{"text":"周五核对","kind":"fact"}}`))
	if e != nil {
		t.Fatal(e)
	}
	_ = v
	b, _ := s.memoryBatch()
	publishBatch(t, s, b, "周五核对", "confirmed", b.References)
	r.Text = "原文已撤回"
	_ = s.saveRun(r)
	u, _ := s.Understanding()
	if strings.Contains(u.Text, "周五") {
		t.Fatal("derived memory outlived source")
	}
}
