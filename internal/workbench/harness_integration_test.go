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
	hooks := RuntimeHooks{Event: func(seq int64, kind string, data json.RawMessage) error {
		if kind == "tool/call" {
			toolEvents++
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
	missing := ss
	missing.HarnessID = "missing-" + newID()
	_, e = h.Execute(ctx, RuntimeInput{Run: Run{ID: newID()}, Session: missing, Project: p, Prompt: "不能创建替代历史"}, hooks)
	if e == nil || !strings.Contains(e.Error(), "原对话恢复失败") {
		t.Fatal("missing history was silently replaced", e)
	}
	t.Log("real model: native session resume and shell file output passed")
}
