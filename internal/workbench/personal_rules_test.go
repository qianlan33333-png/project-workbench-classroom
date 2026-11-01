package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

func TestPersonalRulesRevisionConflictAndSessionContinuity(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	session, _ := s.Session(p.DefaultSessionID)
	before, e := s.PersonalRules()
	if e != nil {
		t.Fatal(e)
	}
	changed := before
	changed.Soul += "\n人手修改"
	changed.Agent += "\n只在有证据时提一句"
	next, e := s.SavePersonalRules(changed)
	if e != nil || next.Version != 2 {
		t.Fatal(e, next)
	}
	if _, e = s.SavePersonalRules(changed); !errors.Is(e, ErrConflict) {
		t.Fatal("stale overwrite", e)
	}
	empty := next
	empty.Agent = " "
	if _, e = s.SavePersonalRules(empty); e == nil {
		t.Fatal("empty rules accepted")
	}
	large := next
	large.Soul = strings.Repeat("x", 65537)
	if _, e = s.SavePersonalRules(large); e == nil {
		t.Fatal("oversized rules accepted")
	}
	current, _ := s.PersonalRules()
	if current != next {
		t.Fatal("invalid save changed persisted rules")
	}
	old, _ := s.Setting("personal_rules_revision:1")
	var r PersonalRules
	json.Unmarshal([]byte(old), &r)
	if r != before {
		t.Fatal("old revision overwritten")
	}
	again, _ := s.Session(p.DefaultSessionID)
	if again.HarnessID != session.HarnessID || again.ID != session.ID {
		t.Fatal("session reset")
	}
	if flag, _ := s.Setting("personal_context_reset"); flag != "" {
		t.Fatal("rules change reset context")
	}
}

func TestPersonalRulesOwnerOnlyAndRuntimeSnapshot(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	ss, _ := s.Session(p.DefaultSessionID)
	initial, _ := s.PersonalRules()
	edited := initial
	edited.Agent += "\nTEST-NEW-RULE"
	edited, _ = s.SavePersonalRules(edited)
	hash, _ := PasswordHash("rules-test-password")
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	(&API{Store: s, Config: Config{PasswordHash: hash}}).Mount(router)
	req := httptest.NewRequest("PUT", "/api/v1/personal/rules", strings.NewReader(encode(edited)))
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 401 {
		t.Fatal("unauthenticated rules write", res.Code)
	}
	req = httptest.NewRequest("GET", "/api/v1/personal/rules", nil)
	req.SetBasicAuth("owner", "rules-test-password")
	res = httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "TEST-NEW-RULE") {
		t.Fatal(res.Code, res.Body.String())
	}
	in := RuntimeInput{Project: p, Session: ss, Run: Run{ID: "snapshot"}, PersonalRules: &initial}
	for _, tool := range []string{"save_personal_rules", "write_file", "create_decided_task"} {
		if _, e := s.appTool(in, tool, json.RawMessage(`{}`)); e == nil {
			t.Fatal("agent write allowed", tool)
		}
	}
	h := &Harness{Store: s, Config: Config{DataDir: s.Root, RuntimeMode: "podman", Podman: "podman", RuntimeImage: "test", RuntimeDir: "/runtime-code"}}
	for _, daily := range []bool{false, true} {
		input := in
		home := filepath.Join(s.Root, "runtime/projects", p.ID)
		if daily {
			input.Routing = true
			input.Purpose = "daily"
			input.Project = Project{ID: "router"}
			home = filepath.Join(s.Root, "runtime/router-home", input.Run.ID)
		}
		if _, _, _, e := h.prepare(input); e != nil {
			t.Fatal(e)
		}
		body, e := os.ReadFile(filepath.Join(home, "invocation-snapshot.yml"))
		if e != nil {
			t.Fatal(e)
		}
		var patch []map[string]any
		if e = yaml.Unmarshal(body, &patch); e != nil {
			t.Fatal(e)
		}
		var persona, ownerContext string
		for _, item := range patch {
			if inserted, ok := item["insert"].([]any); ok {
				for _, v := range inserted {
					entry := v.(map[string]any)
					if entry["id"] == "personal-owner-rules" {
						ownerContext = entry["config"].(map[string]any)["text"].(string)
					}
				}
			}
			if item["id"] == "system-prompt" {
				persona = item["config"].(map[string]any)["personaPrefix"].(string)
			}
		}
		if ownerContext != initial.Prompt() || !strings.Contains(persona, "workbench-owner-rules") || strings.Contains(ownerContext, "TEST-NEW-RULE") {
			t.Fatal("running snapshot changed", daily)
		}
	}
}

type rulesExecutor struct {
	inputs []RuntimeInput
	hook   func()
	result string
}

func (f *rulesExecutor) Execute(_ context.Context, in RuntimeInput, _ RuntimeHooks) (RuntimeResult, error) {
	f.inputs = append(f.inputs, in)
	if f.hook != nil {
		f.hook()
		f.hook = nil
	}
	return RuntimeResult{Text: f.result}, nil
}
func (f *rulesExecutor) Cleanup(context.Context, string) error { return nil }

func TestAllPersonalEntrypointsUseRulesAndDailyRejectsStaleRules(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	r, _ := s.PersonalRules()
	r.Agent += "\n共用验收规则"
	r, _ = s.SavePersonalRules(r)
	f := &rulesExecutor{result: "收到"}
	w := NewWorker(s, f, nil)
	for _, source := range []string{"web", "wechat", "feishu"} {
		in := Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: newID(), Text: "不必追问"}
		if source != "web" {
			c := channel(t, s, source)
			in = incoming(c, "不必追问")
		}
		m := submitRoute(t, s, in)
		run, e := w.claim()
		if e != nil {
			t.Fatal(e)
		}
		w.execute(context.Background(), run)
		actual, _ := s.Run(m.RunID)
		if actual.PersonalRulesVersion != r.Version || actual.State != "completed" {
			t.Fatal(actual)
		}
		last := f.inputs[len(f.inputs)-1]
		if last.PersonalRules == nil || *last.PersonalRules != r || last.Session.ID != p.DefaultSessionID {
			t.Fatal("rules/session diverged", source)
		}
	}
	_ = s.SetSetting("today_enabled", "true")
	_ = s.RequestToday()
	f.result = `{"text":"旧规则生成的建议","decision":null}`
	f.hook = func() {
		r.Agent += "\n改用新规则"
		var e error
		r, e = s.SavePersonalRules(r)
		if e != nil {
			t.Fatal(e)
		}
	}
	w.maintainToday(context.Background())
	var n int
	s.DB.QueryRow("SELECT count(*) FROM daily_contents").Scan(&n)
	if n != 0 {
		t.Fatal("stale daily published")
	}
	if pending, _ := s.Setting("today_refresh"); pending == "" {
		t.Fatal("lost refresh request")
	}
	f.result = `{"text":"新规则的建议","decision":null}`
	w.maintainToday(context.Background())
	d, e := getJSON[DailyContent](s.DB, "SELECT data FROM daily_contents WHERE day=?", shanghaiDay())
	if e != nil || d.PersonalRulesVersion != r.Version {
		t.Fatal(e, d)
	}
	last := f.inputs[len(f.inputs)-1]
	if last.PersonalRules == nil || *last.PersonalRules != r {
		t.Fatal("daily rules diverged")
	}
}
