package workbench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTodayGlobalToolsAcrossEntrances(t *testing.T) {
	s := testStore(t)
	p, _ := s.PersonalSpace()
	tool := capabilityFixture(t, s)
	plugin, e := s.SavePlugin(Plugin{Name: "Echo", Transport: "stdio", Command: "node", Env: map[string]string{"EXAMPLE": "PRIVATE_STDIO_ENV"}})
	if e != nil {
		t.Fatal(e)
	}
	h := Harness{Store: s, Config: Config{RuntimeMode: "podman", Podman: "podman", RuntimeImage: "image", RuntimeDir: "/runtime-code", DataDir: s.Root}}
	w := Worker{Store: s}
	ss, _ := s.Session(p.DefaultSessionID)
	for _, source := range []string{"web", "wechat", "feishu"} {
		in := Incoming{EventID: newID(), Text: "使用工具查询", SessionID: ss.ID, Source: "web", Capabilities: []CapabilityRef{{"cli", tool.ID}, {"mcp", plugin.ID}}}
		if source != "web" {
			in = incoming(channel(t, s, source), "使用工具查询")
		}
		m := submitRoute(t, s, in)
		r, e := s.Run(m.RunID)
		if e != nil || r.SessionID != ss.ID {
			t.Fatal(r, e)
		}
		prompt := w.personalPrompt(r)
		if !strings.Contains(prompt, tool.Command) || !strings.Contains(prompt, plugin.Name) || !strings.Contains(prompt, "run_global_cli") {
			t.Fatal(prompt)
		}
		if source == "web" && (len(r.Capabilities) != 2 || !strings.Contains(prompt, "指定 CLI")) {
			t.Fatal("selection lost")
		}
		input := RuntimeInput{Project: p, Run: r, Session: ss}
		args, _, _, e := h.prepare(input)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := os.ReadFile(filepath.Join(s.Root, "runtime/projects", p.ID, "invocation-"+r.ID+".yml"))
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(raw), "global-mcp-"+plugin.ID) || !strings.Contains(string(raw), "personal-mcp-relay") || strings.Contains(string(raw), "PRIVATE_STDIO_ENV") || strings.Contains(strings.Join(args, " "), "/tools:") {
			t.Fatal("missing isolated MCP or leaked global credentials")
		}
		if _, e = s.appTool(input, "save_schedule", json.RawMessage(`{}`)); e == nil {
			t.Fatal("personal project write accepted")
		}
	}
	disabled := false
	plugin.Enabled = &disabled
	s.SavePlugin(plugin)
	tool.Enabled = false
	s.SaveCLITool(tool)
	caps, e := s.Capabilities(p.ID)
	if e != nil || len(caps) != 0 {
		t.Fatal(caps, e)
	}
}
func TestExternalCLIIsolationAndLiteralArgs(t *testing.T) {
	s := testStore(t)
	cli := capabilityFixture(t, s)
	p, _ := s.PersonalSpace()
	in := RuntimeInput{Project: p}
	fake := filepath.Join(t.TempDir(), "podman")
	os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700)
	h := Harness{Store: s, Config: Config{Podman: fake, RuntimeMode: "podman", DataDir: s.Root, RuntimeImage: "image"}}
	payload := json.RawMessage(`{"id":"fixture","args":["lookup","x; echo SHOULD_NOT_EXECUTE","--volume","/root:/workspace"]}`)
	v, e := h.runPersonalCLI(context.Background(), in, payload)
	if e != nil {
		t.Fatal(e)
	}
	out := v.(map[string]any)["output"].(string)
	parts := strings.Split(out, "\n--\nimage\n")
	if len(parts) != 2 || parts[1] != "lookup\nx; echo SHOULD_NOT_EXECUTE\n--volume\n/root:/workspace\n" {
		t.Fatal(out)
	}
	for _, denied := range []string{"/var/lib/project-workbench", "bridge.sock", "/runtime:", "/projects/", ".credentials.yaml", "/tools/state:rw"} {
		if strings.Contains(parts[0], denied) {
			t.Fatal(out)
		}
	}
	for _, dir := range []string{"/tools/bin/fixture-cli:ro", "/tools/packages/fixture/1:ro", "/tools/state/fixture:rw"} {
		if !strings.Contains(parts[0], dir) {
			t.Fatal(out)
		}
	}
	cli.Enabled = false
	s.SaveCLITool(cli)
	if _, e = h.runPersonalCLI(context.Background(), in, payload); e == nil {
		t.Fatal("disabled CLI ran")
	}
	cli.Enabled = true
	s.SaveCLITool(cli)
	in.Routing = true
	if _, e = h.runPersonalCLI(context.Background(), in, payload); e == nil {
		t.Fatal("automatic run got tools")
	}
	in.Routing = false
	h.Config.RuntimeMode = "native"
	if _, e = h.runPersonalCLI(context.Background(), in, payload); e == nil {
		t.Fatal("unsandboxed host exec")
	}
}
