package workbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func capabilityFixture(t *testing.T, s *Store) CLITool {
	t.Helper()
	c := CLITool{ID: "fixture", Name: "示例 CLI", Command: "fixture-cli", Version: "1", URL: "https://example.com/fixture.tar.gz", Enabled: true}
	if e := os.MkdirAll(filepath.Dir(s.toolBinary(c)), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(s.toolBinary(c), []byte("#!/bin/sh\nexit 0\n"), 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SaveCLITool(c); e != nil {
		t.Fatal(e)
	}
	return c
}
func TestCapabilityCatalogAndProjectBoundary(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "选择能力A")
	q := testProject(t, s, "选择能力B")
	cli := capabilityFixture(t, s)
	expert, e := s.CreateExpert("方法测试", "项目专属方法", "使用指定方法")
	if e != nil {
		t.Fatal(e)
	}
	p.ExpertIDs = []string{expert.ID}
	if _, e = s.UpdateProject(p.ID, p); e != nil {
		t.Fatal(e)
	}
	plugin, e := s.SavePlugin(Plugin{Name: "示例 MCP", Transport: "stdio", Command: "node"})
	if e != nil {
		t.Fatal(e)
	}
	rows, e := s.Capabilities(p.ID)
	if e != nil || len(rows) != 3 {
		t.Fatal(rows, e)
	}
	rows, e = s.Capabilities(q.ID)
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	if _, e = s.Submit(Incoming{Source: "web", EventID: newID(), SessionID: q.DefaultSessionID, Text: "不能越界", Capabilities: []CapabilityRef{{Kind: "skill", ID: expert.ID}}}); e == nil {
		t.Fatal("foreign skill accepted")
	}
	cli.Enabled = false
	s.SaveCLITool(cli)
	enabled := false
	plugin.Enabled = &enabled
	s.SavePlugin(plugin)
	rows, e = s.Capabilities(q.ID)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	if _, e = s.selectCapabilities(p.ID, []CapabilityRef{{Kind: "cli", ID: cli.ID}}); e == nil {
		t.Fatal("disabled CLI selected")
	}
}
func TestSelectedCapabilitiesPersistPinSkillAndKeepSession(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "能力记录")
	cli := capabilityFixture(t, s)
	ex, e := s.CreateExpert("固定方法", "保持原版本", "SELECTED_OLD_METHOD")
	if e != nil {
		t.Fatal(e)
	}
	p.ExpertIDs = []string{ex.ID}
	if _, e = s.UpdateProject(p.ID, p); e != nil {
		t.Fatal(e)
	}
	original, _ := s.Session(p.DefaultSessionID)
	in := Incoming{Source: "web", EventID: newID(), SessionID: original.ID, Text: "按所选方法处理", Capabilities: []CapabilityRef{{Kind: "cli", ID: cli.ID}, {Kind: "skill", ID: ex.ID}, {Kind: "cli", ID: cli.ID}}}
	m := submitRoute(t, s, in)
	r, e := s.Run(m.RunID)
	if e != nil || len(r.Capabilities) != 2 {
		t.Fatal(r, e)
	}
	if r.Capabilities[0].Command != cli.Command || r.ExpertVersions[ex.ID] != ex.Digest {
		t.Fatal(r)
	}
	if e = s.DeleteExpert(ex.ID, ex.Digest); e != nil {
		t.Fatal(e)
	}
	prompt := s.capabilityPrompt(r)
	if !strings.Contains(prompt, "指定 Skill") || !strings.Contains(prompt, ex.SkillName) || !strings.Contains(prompt, ex.Digest) || !strings.Contains(prompt, cli.Command) {
		t.Fatal(prompt)
	}
	if e = s.RemoveCLITool(cli.ID); e != nil {
		t.Fatal(e)
	}
	repeated, e := s.Submit(in)
	if e != nil || repeated.ID != m.ID || repeated.RunID != r.ID {
		t.Fatal(repeated, e)
	}
	if !strings.Contains(s.capabilityPrompt(r), "本轮不可用") {
		t.Fatal("deleted CLI pretended available")
	}
	after, _ := s.Session(original.ID)
	if after.HarnessID != original.HarnessID || after.ExpertID != original.ExpertID {
		t.Fatal("selection changed session", after)
	}
	// Missing tools fail before an input is persisted, and no forged display name is trusted.
	if _, e = s.Submit(Incoming{Source: "web", EventID: newID(), SessionID: original.ID, Text: "无效选择", Capabilities: []CapabilityRef{{Kind: "cli", ID: cli.ID}}}); e == nil {
		t.Fatal("missing CLI accepted")
	}
}
