package workbench

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Uses a disposable store and the actual credentialed Harness. No production
// data, channel listeners, or external writes are part of this test.
func TestRealTodayFollowupLifecycle(t *testing.T) {
	path := os.Getenv("PW_REAL_FOLLOWUP_CONFIG")
	if path == "" {
		t.Skip("set isolated credentialed followup config")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var cfg Config
	if e = json.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	s := testStore(t)
	cfg.DataDir = s.Root
	p := testProject(t, s, "持续跟进隔离验收")
	token := "FOLLOWUP-" + newID()
	original := "本次测试校验短语为 " + token + "，事实状态为已解决。"
	if _, e = s.SaveFile(p.ID, "事实.md", []byte(original), ""); e != nil {
		t.Fatal(e)
	}
	personal, e := s.PersonalSpace()
	if e != nil {
		t.Fatal(e)
	}
	prompt := `请帮我持续跟进“持续跟进隔离验收”项目，严格走本次隔离验收流程，只读资料，不写项目，不调用外部工具。
 第一轮：用 manage_followup 创建 key=live-check，title=核对事实，goal=两次只读核对事实.md中的校验短语和已解决状态，action=ask。问题问先核对还是暂缓，2个选项，唯一推荐先核对并说明理由。保存后结束本轮。
 本人回答后续办轮：用 read_project_source 读取该项目事实.md，保存 about 为“已完成第一次核对，等待到期第二次核对”，用 action=review 设下次复查在本轮启动时间之后90秒，随后结束本轮，不结案。
 到期自动复查轮（reviewCount=1）：再次只读该原文，确认校验短语和已解决状态后，用 action=complete 将文件来源和校验短语记入 evidence。保存完成依据后结束。
 任何一步不能完成就如实报告，不宣称通过。`
	m := submitRoute(t, s, Incoming{Source: "web", SessionID: personal.DefaultSessionID, EventID: newID(), Text: prompt})
	h := &Harness{Config: cfg, Store: s}
	w := NewWorker(s, h, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	execute := func() Run {
		t.Helper()
		r, e := w.claim()
		if e != nil {
			t.Fatal(e)
		}
		w.execute(ctx, r)
		r, e = s.Run(r.ID)
		if e != nil || r.State != "completed" {
			t.Fatalf("real round %s: %s %s", r.ID, r.State, r.Error)
		}
		t.Log("real round completed", r.ID)
		return r
	}
	first := execute()
	if first.ID != m.RunID {
		t.Fatal("wrong run")
	}
	fs, e := s.Followups()
	if e != nil || len(fs) != 1 || fs[0].State != "waiting_decision" {
		t.Fatalf("missing durable wait: %+v %v", fs, e)
	}
	f := fs[0]
	var locks int
	_ = s.DB.QueryRow("SELECT count(*) FROM project_locks").Scan(&locks)
	if locks != 0 {
		t.Fatal("wait retained lease")
	}
	if _, e = s.AnswerDecision(f.DecisionID, 1, "A", ""); e != nil {
		t.Fatal(e)
	}
	s.dispatchDecisions()
	second := execute()
	f, _ = s.Followup(f.ID)
	if f.State != "scheduled" || f.NextAt <= now() || second.SessionID != first.SessionID {
		t.Fatalf("missing same-session scheduled review: %+v", f)
	}
	t.Log("durable once review scheduled")
	for now() < f.NextAt {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if e = s.FireSchedules(time.Now()); e != nil {
		t.Fatal(e)
	}
	third := execute()
	f, _ = s.Followup(f.ID)
	if f.State != "completed" || f.ReviewCount != 1 || !strings.Contains(f.Evidence, token) || third.SessionID != first.SessionID {
		t.Fatalf("no evidence-backed completion: %+v", f)
	}
	b, _, e := s.ReadFile(p.ID, "事实.md")
	if e != nil || string(b) != original {
		t.Fatal("project body changed")
	}
	var toolCalls int
	_ = s.DB.QueryRow("SELECT count(*) FROM harness_events WHERE data LIKE '%tool/call%'").Scan(&toolCalls)
	if toolCalls < 3 {
		t.Fatal("missing real MCP calls")
	}
	t.Log("PASS: real model, durable ask, answer resume, real due review, same Harness identity, evidence completion, unchanged project body", toolCalls)
}
