package workbench

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CapabilityRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Capability struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command,omitempty"`
}

// Only catalog metadata is returned. Choosing a capability changes one input,
// never the project's scope, tool permissions, or persistent Harness identity.
func (s *Store) Capabilities(project string) ([]Capability, error) {
	p, e := s.Project(project)
	if e != nil {
		return nil, e
	}
	out := []Capability{}
	tools, e := s.CLITools()
	if e != nil {
		return nil, e
	}
	for _, t := range tools {
		if t.Installed && t.Enabled {
			out = append(out, Capability{Kind: "cli", ID: t.ID, Name: t.Name, Command: t.Command, Description: t.Command + " · 全局 CLI"})
		}
	}
	if project == personalProjectID {
		p.ExpertIDs = nil
	}
	for _, id := range p.ExpertIDs {
		ex, e := s.Expert(id)
		if e != nil {
			return nil, e
		}
		out = append(out, Capability{Kind: "skill", ID: ex.ID, Name: ex.Name, Command: ex.SkillName, Description: ex.Description})
	}
	plugins, e := s.enabledProjectPlugins(project)
	if e != nil {
		return nil, e
	}
	for _, plugin := range plugins {
		out = append(out, Capability{Kind: "mcp", ID: plugin.ID, Name: plugin.Name, Description: plugin.Description})
	}
	return out, nil
}
func (s *Store) selectCapabilities(project string, refs []CapabilityRef) ([]Capability, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if len(refs) > 8 {
		return nil, errors.New("一条消息最多选择 8 项能力")
	}
	available, e := s.Capabilities(project)
	if e != nil {
		return nil, e
	}
	selected := []Capability{}
	seen := map[string]bool{}
	for _, ref := range refs {
		key := ref.Kind + ":" + ref.ID
		if seen[key] {
			continue
		}
		found := false
		for _, c := range available {
			if c.Kind == ref.Kind && c.ID == ref.ID {
				selected = append(selected, c)
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("所选能力不可用、已停用或尚未加入当前项目，请重新选择")
		}
		seen[key] = true
	}
	return selected, nil
}
func (s *Store) capabilityPrompt(r Run) string {
	if len(r.Capabilities) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n[用户为本条消息明确选择的能力]\n优先使用以下能力完成本次任务；这不改变后续消息的默认专家或会话。选择不是原始 shell 命令，也不代表额外的业务操作授权。若所选能力不可用，请说明原因，不擅自换成其他同类服务。\n")
	for _, c := range r.Capabilities {
		switch c.Kind {
		case "cli":
			t, e := s.CLITool(c.ID)
			if e != nil || !t.Installed || !t.Enabled {
				fmt.Fprintf(&b, "CLI %q 本轮不可用。\n", c.Name)
				continue
			}
			if r.ProjectID == personalProjectID {
				fmt.Fprintf(&b, "指定 CLI：%q，调用 run_global_cli，id=%q。\n", t.Name, t.ID)
				continue
			}
			fmt.Fprintf(&b, "指定 CLI：%q，入口命令 %q；通过命令工具调用，先按需查看 --help 或 schema。\n", t.Name, t.Command)
		case "skill":
			version := r.ExpertVersions[c.ID]
			raw, e := os.ReadFile(filepath.Join(s.Root, "experts", c.ID, version, "SKILL.md"))
			if e != nil || version == "" {
				fmt.Fprintf(&b, "Skill %q 固定版本不可用。\n", c.Name)
				continue
			}
			ex, e := expertManifest(raw)
			if e != nil {
				fmt.Fprintf(&b, "Skill %q 入口无效。\n", c.Name)
				continue
			}
			fmt.Fprintf(&b, "指定 Skill：%q，Skill 标识 %q，固定版本 %s；本轮请读取其 SKILL.md 并使用方法。\n", ex.Name, ex.SkillName, version)
		case "mcp":
			p, e := s.Plugin(c.ID)
			if e != nil || (p.Enabled != nil && !*p.Enabled) {
				fmt.Fprintf(&b, "MCP %q 本轮不可用。\n", c.Name)
				continue
			}
			fmt.Fprintf(&b, "指定 MCP：%q，原生 serverName 为 p_%s；使用该服务对应的工具。\n", p.Name, strings.ReplaceAll(p.ID, "-", "")[:20])
		}
	}
	b.WriteString("[指定能力结束]\n")
	return b.String()
}
