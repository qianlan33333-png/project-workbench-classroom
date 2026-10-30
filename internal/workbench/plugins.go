package workbench

import (
	"errors"
	"net/url"
	"strings"
)

// MCP config remains the native Harness shape; credentials are stored outside
// project files and never included in the owner-facing list.
type Plugin struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Transport   string            `json:"transport"`
	URL         string            `json:"url"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	HasSecrets  bool              `json:"hasSecrets"`
}

func (s *Store) Plugins() ([]Plugin, error) {
	vs, e := listJSON[Plugin](s.DB, "SELECT data FROM plugins ORDER BY id")
	for i := range vs {
		vs[i].HasSecrets = len(vs[i].Env) > 0 || len(vs[i].Headers) > 0
		vs[i].Env = nil
		vs[i].Headers = nil
	}
	return vs, e
}
func (s *Store) Plugin(id string) (Plugin, error) {
	return getJSON[Plugin](s.DB, "SELECT data FROM plugins WHERE id=?", id)
}
func (s *Store) SavePlugin(p Plugin) (Plugin, error) {
	if strings.TrimSpace(p.Name) == "" {
		return p, errors.New("请填写插件名称")
	}
	if p.Transport != "stdio" && p.Transport != "http" {
		return p, errors.New("MCP 传输只支持 stdio 或 http")
	}
	if p.Transport == "http" {
		u, e := url.Parse(p.URL)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return p, errors.New("MCP 地址需要 HTTPS")
		}
	} else if p.Command == "" {
		return p, errors.New("请填写执行环境内的命令")
	}
	if p.ID == "" {
		p.ID = newID()
	} else if old, e := s.Plugin(p.ID); e == nil {
		if p.Env == nil {
			p.Env = old.Env
		}
		if p.Headers == nil {
			p.Headers = old.Headers
		}
	} else {
		return p, ErrNotFound
	}
	_, e := s.DB.Exec("INSERT INTO plugins VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", p.ID, encode(p))
	p.HasSecrets = len(p.Env) > 0 || len(p.Headers) > 0
	p.Env = nil
	p.Headers = nil
	return p, e
}
func (s *Store) DeletePlugin(id string) error {
	ps, e := s.Projects()
	if e != nil {
		return e
	}
	for _, p := range ps {
		if contains(p.PluginIDs, id) {
			return errors.New("请先从项目中停用这个插件")
		}
	}
	_, e = s.DB.Exec("DELETE FROM plugins WHERE id=?", id)
	return e
}
