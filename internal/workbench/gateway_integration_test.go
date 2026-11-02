package workbench

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// This opt-in check uses a real isolated Harness and host model credentials.
// Ingress and sends remain fixtures: it never claims real gateway acceptance.
func TestRealGatewayHarness(t *testing.T) {
	file := os.Getenv("PW_REAL_GATEWAY_CONFIG")
	if file == "" {
		t.Skip("set PW_REAL_GATEWAY_CONFIG for an isolated, credentialed Podman runtime")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.RuntimeMode != "podman" {
		t.Fatal("customer acceptance requires Podman")
	}
	s := testStore(t)
	cfg.DataDir = s.Root
	provider := newGatewayProvider(t)
	c, b := gatewaySetup(t, s, provider)
	if dir := os.Getenv("PW_REAL_GATEWAY_EXPERT"); dir != "" {
		ex, e := s.installExpertDir(dir, "")
		if e != nil {
			t.Fatal(e)
		}
		p, _ := s.Project(b.ProjectID)
		p.ExpertIDs, p.DefaultExpertID = []string{ex.ID}, ex.ID
		if _, e = s.UpdateProject(p.ID, p); e != nil {
			t.Fatal(e)
		}
		b.ExpertID = ex.ID
	}
	if file := os.Getenv("PW_REAL_GATEWAY_PLUGIN"); file != "" {
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		var plugin Plugin
		if e = json.Unmarshal(raw, &plugin); e != nil {
			t.Fatal(e)
		}
		plugin.ID = ""
		plugin, e = s.SavePlugin(plugin)
		if e != nil {
			t.Fatal(e)
		}
		b.PluginIDs = []string{plugin.ID}
	}
	if _, err = s.SaveGatewayBinding(b); err != nil {
		t.Fatal(err)
	}
	question := os.Getenv("PW_REAL_GATEWAY_QUESTION")
	if question == "" {
		question = "你好"
	}
	receiveGateway(t, s, c, b.AccountID, "fixture-customer", "message.received", "fixture-"+newID(), question, time.Now())
	w := NewWorker(s, nil, nil)
	// Queue the one persisted fixture before claiming it; never start delivery.
	msg, err := getJSON[Message](s.DB, "SELECT data FROM inbox WHERE state='gateway_pending' LIMIT 1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.queueGateway(msg); err != nil {
		t.Fatal(err)
	}
	run, err := w.claim()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Project(b.ProjectID)
	ss, _ := s.Session(run.SessionID)
	h := &Harness{Config: cfg, Store: s}
	t.Cleanup(func() { _ = h.Cleanup(context.Background(), run.ID) })
	ctx, cancel := context.WithDeadline(t.Context(), time.UnixMilli(run.Gateway.Deadline))
	defer cancel()
	started := time.Now()
	result, err := h.Execute(ctx, RuntimeInput{Run: run, Session: ss, Project: p, Prompt: w.gatewayPrompt(run, p)}, RuntimeHooks{})
	if err != nil {
		t.Fatal("real external Harness:", err)
	}
	var decision struct {
		Action string `json:"action"`
		Text   string `json:"text"`
		Reason string `json:"reason"`
	}
	want := os.Getenv("PW_REAL_GATEWAY_EXPECT")
	if want == "" {
		want = "reply"
	}
	if want != "reply" && want != "handoff" {
		t.Fatal("PW_REAL_GATEWAY_EXPECT must be reply or handoff")
	}
	if json.Unmarshal([]byte(result.Text), &decision) != nil || decision.Action != want || want == "reply" && strings.TrimSpace(decision.Text) == "" || want == "handoff" && strings.TrimSpace(decision.Reason) == "" {
		t.Fatal("expected a structured "+want+" from real Harness:", result.Text)
	}
	t.Logf("real isolated Harness %s in %s: %s%s", decision.Action, time.Since(started).Round(time.Millisecond), decision.Text, decision.Reason)
	if provider.sends != 0 {
		t.Fatal("Harness test sent an external message")
	}
}
