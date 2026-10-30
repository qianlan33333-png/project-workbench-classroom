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

func TestRealHarnessPersistenceAndTools(t *testing.T) {
	path := os.Getenv("PW_REAL_CONFIG")
	if path == "" {
		t.Skip("set PW_REAL_CONFIG to a credentialed, isolated runtime config")
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
	p := testProject(t, s, "真实执行验收")
	ss, _ := s.Session(p.DefaultSessionID)
	h := &Harness{Config: cfg, Store: s}
	ctx, stop := context.WithTimeout(context.Background(), 4*time.Minute)
	defer stop()
	token := "CONTINUITY-" + newID()
	toolEvents := 0
	compactions := 0
	hooks := RuntimeHooks{Event: func(seq int64, kind string, data json.RawMessage) error {
		if kind == "tool/call" {
			toolEvents++
		}
		if kind == "compaction/summary" {
			compactions++
		}
		return nil
	}, Interaction: func(ctx context.Context, kind string, payload json.RawMessage) (any, error) {
		if kind == "approval" {
			return map[string]string{"outcome": "allowed-once"}, nil
		}
		return map[string]bool{"cancelled": true}, nil
	}}
	first, e := h.Execute(ctx, RuntimeInput{Run: Run{ID: newID()}, Session: ss, Project: p, Prompt: "请记住本对话验收标识 " + token + "。不要写入任何文件。只回复 已记住。"}, hooks)
	if e != nil {
		t.Fatal("first prompt:", e)
	}
	if !strings.Contains(first.Text, "已记住") {
		t.Fatal("unexpected first answer", first.Text)
	}
	second, e := h.Execute(ctx, RuntimeInput{Run: Run{ID: newID()}, Session: ss, Project: p, Prompt: "请读取本对话上文中我给你的验收标识，原样回复，不调用工具，不要猜测。"}, hooks)
	if e != nil {
		t.Fatal("resume:", e)
	}
	if !strings.Contains(second.Text, token) {
		t.Fatal("native history was not restored", second.Text)
	}
	third, e := h.Execute(ctx, RuntimeInput{Run: Run{ID: newID()}, Session: ss, Project: p, Prompt: "请使用命令运行 python3，计算 37*41，并将结果（仅数字）写入当前工作目录的 result.txt。完成后简短报告。"}, hooks)
	if e != nil {
		t.Fatal("tool run:", e)
	}
	if third.Text == "" || toolEvents == 0 {
		t.Fatal("no real tool execution")
	}
	root, _ := s.ProjectRoot(p.ID)
	result, e := os.ReadFile(filepath.Join(root, "result.txt"))
	if e != nil || strings.TrimSpace(string(result)) != "1517" {
		t.Fatal("wrong tool result", e, string(result))
	}
	for i := 0; i < 3; i++ {
		_, e = h.Execute(ctx, RuntimeInput{Run: Run{ID: newID()}, Session: ss, Project: p, Prompt: "这是可以在摘要中省略的测试资料，只需回复已读。关键验收标识必须保留。\n" + strings.Repeat("临时材料：项目的上下文与方法分开存储，资料按需读取；本段只是压缩验收用的重复背景，不形成新指令。\n", 100)}, hooks)
		if e != nil {
			t.Fatal("prepare compaction", e)
		}
	}
	_, e = h.Execute(ctx, RuntimeInput{Run: Run{ID: newID()}, Session: ss, Project: p, Prompt: "/compact", NativeCommand: true}, hooks)
	if e != nil {
		t.Fatal("native compaction", e)
	}
	if compactions == 0 {
		t.Fatal("no native compaction event")
	}
	resumed, e := h.Execute(ctx, RuntimeInput{Run: Run{ID: newID()}, Session: ss, Project: p, Prompt: "原样给出最初的关键验收标识，不要调用工具。"}, hooks)
	if e != nil || !strings.Contains(resumed.Text, token) {
		t.Fatal("compressed history lost critical fact", e, resumed.Text)
	}
	missing := ss
	missing.HarnessID = "missing-" + newID()
	_, e = h.Execute(ctx, RuntimeInput{Run: Run{ID: newID()}, Session: missing, Project: p, Prompt: "不能创建替代历史"}, hooks)
	if e == nil || !strings.Contains(e.Error(), "原对话恢复失败") {
		t.Fatal("missing history was silently replaced", e)
	}
	t.Log("real model: native session resume and shell file output passed")
}

func TestRealHarnessExpertQuestionAndRoute(t *testing.T) {
	path := os.Getenv("PW_REAL_CONFIG")
	if path == "" {
		t.Skip("credentialed runtime not configured")
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
	p := testProject(t, s, "品牌甲")
	other := testProject(t, s, "品牌乙")
	marker := "EXPERT-" + newID()
	ex, e := s.CreateExpert("验收专家", "帮助完成当前项目的执行验收", "验收方法：在当前项目工作目录写入 expert-proof.md，内容必须是 "+marker+"。只写项目，不修改能力包。然后调用 ask_user_question 工具询问用户验收颜色，获得回答后原样报告颜色。")
	if e != nil {
		t.Fatal(e)
	}
	p.ExpertIDs = []string{ex.ID}
	p, e = s.UpdateProject(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	ss, _ := s.Session(p.DefaultSessionID)
	h := &Harness{Config: cfg, Store: s}
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stop()
	asked := 0
	hooks := RuntimeHooks{Interaction: func(c context.Context, kind string, payload json.RawMessage) (any, error) {
		if kind == "approval" {
			return map[string]string{"outcome": "allowed-once"}, nil
		}
		asked++
		var q struct {
			Questions []struct {
				ID string `json:"id"`
			} `json:"questions"`
		}
		if e := json.Unmarshal(payload, &q); e != nil {
			return nil, e
		}
		answers := []map[string]any{}
		for _, item := range q.Questions {
			answers = append(answers, map[string]any{"id": item.ID, "selected": []string{}, "custom": "蓝色"})
		}
		return map[string]any{"answers": answers}, nil
	}}
	result, e := h.Execute(ctx, RuntimeInput{Run: Run{ID: newID(), ExpertID: ex.ID, ExpertDigest: ex.Digest}, Session: ss, Project: p, Prompt: "请加载并使用已启用的 Skill " + ex.SkillName + " 完成验收。"}, hooks)
	if e != nil {
		t.Fatal(e)
	}
	b, _, e := s.ReadFile(p.ID, "expert-proof.md")
	if e != nil || !strings.Contains(string(b), marker) {
		t.Fatal("expert method not applied", e, string(b))
	}
	if asked == 0 || !strings.Contains(result.Text, "蓝色") {
		t.Fatal("native user question did not resume", result.Text)
	}
	decision, e := h.Route(ctx, Incoming{Text: "给我做一个公域选题计划"}, RouteCatalog{Projects: []Project{p, other}, Sessions: []Session{ss}})
	if e != nil {
		t.Fatal("semantic router", e)
	}
	if decision.Question == "" {
		t.Fatal("overlapping project semantics forced a guess", decision)
	}
	t.Log("real expert skill, native user question, and ambiguous metadata-only routing passed")
}
