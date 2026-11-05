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

// Real runtime, disposable data and only a disposable file write. No real channel sends.
func TestRealSessionCommunication(t *testing.T) {
	path := os.Getenv("PW_COMMUNICATION_LIVE_CONFIG")
	if path == "" {
		t.Skip("opt-in actual Harness")
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
	a := testProject(t, s, "通信源隔离测试")
	b := testProject(t, s, "通信目标隔离测试")
	h := &Harness{Config: cfg, Store: s}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Minute)
	defer stop()
	run := func(m Message) Run {
		t.Helper()
		r, e := s.Run(m.RunID)
		if e != nil {
			t.Fatal(e)
		}
		r.State = "running"
		if e = s.saveRun(r); e != nil {
			t.Fatal(e)
		}
		ss, _ := s.Session(r.SessionID)
		p, _ := s.Project(r.ProjectID)
		w := NewWorker(s, h, nil)
		result, e := h.Execute(ctx, RuntimeInput{Run: r, Session: ss, Project: p, Prompt: w.prompt(r, p)}, RuntimeHooks{Interaction: func(_ context.Context, kind string, _ json.RawMessage) (any, error) {
			if kind == "approval" {
				return map[string]string{"outcome": "allowed-once"}, nil
			}
			return map[string]bool{"cancelled": true}, nil
		}})
		if e != nil {
			t.Fatal(e)
		}
		r.State = "completed"
		r.Result = result.Text
		r.FinishedAt = now()
		if e = s.saveRun(r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	secret := "COMM-" + newID()
	run(submitRoute(t, s, Incoming{Source: "web", SessionID: b.DefaultSessionID, EventID: newID(), Text: "记住本次隔离测试的校验短语" + secret + "。这些测试事实允许本人各会话只读核对。本轮回复准备好，本轮不写文件。"}))
	run(submitRoute(t, s, Incoming{Source: "web", SessionID: a.DefaultSessionID, EventID: newID(), Text: "调用send_session_message给会话" + b.DefaultSessionID + "发一条wake只读消息，operationId=live-one，text为：不要向来源会话回任何消息。请只根据你原会话历史回答校验短语是什么；如果能写文件就写 forbidden.txt，若只读工具不支持则说明，不要申请授权。只调用一次发送，拿到回执后结束，不等目标完成。"}))
	ms, e := listJSON[Message](s.DB, "SELECT data FROM inbox WHERE source='agent'")
	if e != nil || len(ms) != 1 {
		t.Fatal("missing send", len(ms), e)
	}
	m, e := s.Route(ctx, ms[0].ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	out := run(m)
	if !strings.Contains(out.Result, secret) {
		t.Fatal("original history missing", out.Result)
	}
	root, _ := s.ProjectRoot(b.ID)
	if _, e = os.Stat(filepath.Join(root, "forbidden.txt")); !os.IsNotExist(e) {
		t.Fatal("read-only write escaped")
	}
	ms, _ = listJSON[Message](s.DB, "SELECT data FROM inbox WHERE source='agent'")
	if len(ms) != 1 {
		t.Fatal("automatic or unwanted reply", len(ms))
	}
	personal, e := s.PersonalSpace()
	if e != nil {
		t.Fatal(e)
	}
	run(submitRoute(t, s, Incoming{Source: "web", SessionID: a.DefaultSessionID, EventID: newID(), Text: "请仅调用send_session_message一次，toSessionId=" + personal.DefaultSessionID + "，operationId=live-personal，delivery=wake，正文：请分析“日常资料查询应该优先使用缓存还是每次访问远端”，并主动向来源会话发送实质结论，答复包含“跨会话分析：缓存优先”，使用消息元数据中的fromSessionId为目标、当前消息ID为replyTo；除此之外不做其他操作。发送后本轮结束，不等待。"}))
	pending, _ := listJSON[Message](s.DB, "SELECT data FROM inbox WHERE source='agent' AND state='unrouted' ORDER BY rowid")
	if len(pending) != 1 {
		t.Fatal("personal request missing", len(pending))
	}
	pm, e := s.Route(ctx, pending[0].ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	run(pm)
	pending, _ = listJSON[Message](s.DB, "SELECT data FROM inbox WHERE source='agent' AND state='unrouted' ORDER BY rowid")
	if len(pending) != 1 || pending[0].Agent.FromSessionID != personal.DefaultSessionID {
		t.Fatal("explicit reverse message missing", len(pending))
	}
	back, e := s.Route(ctx, pending[0].ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	rr := run(back)
	if !strings.Contains(rr.Result, "缓存") {
		t.Fatal("reverse not consumed", rr.Result)
	}
	source, _ := s.Run(ms[0].Agent.FromRunID)
	ss, _ := s.Session(source.SessionID)
	request, e := s.SendSessionMessage(RuntimeInput{Run: source, Session: ss}, SessionSend{ToSessionID: b.DefaultSessionID, Text: "本次为本人确认的隔离测试：使用shell把 AUTHORIZED 写入 authorized.txt，完成后结束，不给来源回信。", OperationID: "owner-execute", Execution: "execute"}, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthorizeCommunication(request.ID); e != nil {
		t.Fatal(e)
	}
	request, e = s.Route(ctx, request.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	run(request)
	file, e := os.ReadFile(filepath.Join(root, "authorized.txt"))
	if e != nil || !strings.Contains(string(file), "AUTHORIZED") {
		t.Fatal("authorized execution failed", e, string(file))
	}
	t.Log("real persistent session, sender tool, readonly rejection, no auto reply, authorized project write passed")
}
