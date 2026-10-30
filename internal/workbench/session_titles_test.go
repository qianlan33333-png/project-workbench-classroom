package workbench

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDraftSessionFirstReceiptAndRename(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "标题生命周期")
	ss, err := s.CreateSession(p.ID, "", "topic", "general")
	if err != nil {
		t.Fatal(err)
	}
	if !ss.Draft || ss.Title != "" || ss.TitleSource != "pending" {
		t.Fatal(ss)
	}
	list, _ := s.Sessions(p.ID)
	if len(list) != 1 {
		t.Fatal("empty session leaked into list", list)
	}
	in := Incoming{Source: "web", SessionID: ss.ID, EventID: newID(), Text: "请帮我整理本周客户回访计划，并且把所有步骤列出来"}
	m, err := s.Submit(in)
	if err != nil {
		t.Fatal(err)
	}
	activated, _ := s.Session(ss.ID)
	if activated.Draft || activated.Title != initialSessionTitle(in.Text) || activated.TitleSource != "fallback" || activated.HarnessID != ss.HarnessID {
		t.Fatal(activated)
	}
	list, _ = s.Sessions(p.ID)
	if len(list) != 2 {
		t.Fatal("first receipt must publish before routing", list)
	}
	again, err := s.Submit(in)
	if err != nil || again.ID != m.ID {
		t.Fatal("duplicate receipt", err)
	}
	fallback := json.RawMessage(`{"title":"runtime wrapper","source":{"kind":"fallback"}}`)
	if err = s.ApplySessionTitle(ss.ID, fallback); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := s.Session(ss.ID)
	if unchanged.Title != activated.Title {
		t.Fatal("native fallback replaced first user text")
	}
	ai := json.RawMessage(`{"title":"本周客户回访计划","source":{"kind":"provider"}}`)
	if err = s.ApplySessionTitle(ss.ID, ai); err != nil {
		t.Fatal(err)
	}
	titled, _ := s.Session(ss.ID)
	if titled.TitleSource != "ai" || titled.Title != "本周客户回访计划" {
		t.Fatal(titled)
	}
	manual := "我自己的回访计划"
	renamed, err := s.UpdateSession(ss.ID, &manual, nil)
	if err != nil || renamed.ExpertID != "general" || renamed.HarnessID != ss.HarnessID {
		t.Fatal("rename changed identity or expert", renamed, err)
	}
	if err = s.ApplySessionTitle(ss.ID, ai); err != nil {
		t.Fatal(err)
	}
	updated, _ := s.Session(ss.ID)
	if updated.Title != manual || updated.TitleSource != "manual" {
		t.Fatal("automatic title overrode rename", updated)
	}
	defaultBefore, _ := s.Session(p.DefaultSessionID)
	if err = s.ApplySessionTitle(p.DefaultSessionID, ai); err != nil {
		t.Fatal(err)
	}
	defaultAfter, _ := s.Session(p.DefaultSessionID)
	if defaultBefore.Title != defaultAfter.Title {
		t.Fatal("renamed default assistant")
	}
}

func TestSessionRenameWinsConcurrentProvider(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "并发命名")
	for i := 0; i < 15; i++ {
		ss, _ := s.CreateSession(p.ID, "", "topic", "")
		if _, err := s.Submit(Incoming{Source: "web", SessionID: ss.ID, EventID: newID(), Text: "第一条问题"}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errors := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			errors <- s.ApplySessionTitle(ss.ID, json.RawMessage(`{"title":"自动总结","source":{"kind":"provider"}}`))
		}()
		go func() {
			defer wg.Done()
			title := "人工标题"
			_, e := s.UpdateSession(ss.ID, &title, nil)
			errors <- e
		}()
		wg.Wait()
		close(errors)
		for e := range errors {
			if e != nil {
				t.Fatal(e)
			}
		}
		current, _ := s.Session(ss.ID)
		if current.Title != "人工标题" {
			t.Fatal(current)
		}
	}
}

func TestDraftReceiptFailureIsAtomicAndFirstInputWins(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "首消息")
	ss, _ := s.CreateSession(p.ID, "", "topic", "")
	if _, e := s.Submit(Incoming{Source: "web", SessionID: ss.ID, EventID: newID()}); e == nil {
		t.Fatal("accepted empty input")
	}
	current, _ := s.Session(ss.ID)
	if !current.Draft {
		t.Fatal("rejected message published session")
	}
	if _, e := s.Submit(Incoming{Source: "web", SessionID: "missing", EventID: "missing-session", Text: "hello"}); e == nil {
		t.Fatal("accepted missing session")
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM inbox").Scan(&count)
	if count != 0 {
		t.Fatal("partial receipt persisted")
	}
	for _, text := range []string{"首次提问", "第二次提问"} {
		if _, e := s.Submit(Incoming{Source: "web", SessionID: ss.ID, EventID: newID(), Text: text}); e != nil {
			t.Fatal(e)
		}
	}
	current, _ = s.Session(ss.ID)
	if current.Title != "首次提问" {
		t.Fatal(current)
	}
	title := strings.Repeat("长", 81)
	if _, e := s.UpdateSession(ss.ID, &title, nil); e == nil {
		t.Fatal("accepted long title")
	}
	expert := "general"
	updated, e := s.UpdateSession(ss.ID, nil, &expert)
	if e != nil || updated.TitleSource != "fallback" || updated.Title != "首次提问" {
		t.Fatal("expert update pinned title", updated, e)
	}
}

func TestRealHarnessSessionTitle(t *testing.T) {
	path := os.Getenv("PW_REAL_CONFIG")
	if path == "" {
		t.Skip("credentialed isolated Harness config not set")
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
	p := testProject(t, s, "自动标题真实验收")
	ss, e := s.CreateSession(p.ID, "", "topic", "")
	if e != nil {
		t.Fatal(e)
	}
	m := submitRoute(t, s, Incoming{Source: "web", SessionID: ss.ID, EventID: newID(), Text: "请帮我设计一份新员工入职第一周的培训计划，分三点简短回答，不使用工具。"})
	h := &Harness{Config: cfg, Store: s}
	w := NewWorker(s, h, nil)
	r, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	w.execute(ctx, r)
	done, _ := s.Run(m.RunID)
	named, _ := s.Session(ss.ID)
	if done.State != "completed" {
		t.Fatal(done.State, done.Error)
	}
	t.Logf("title=%q source=%s elapsed=%dms", named.Title, named.TitleSource, done.FinishedAt-done.StartedAt)
	if named.TitleSource != "ai" {
		t.Fatal("native title did not complete", named)
	}
	if named.HarnessID != ss.HarnessID || named.Draft {
		t.Fatal("session identity changed", named)
	}
}
