package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The caller's writable tool leaf never owns a mount source's parent. In
// particular we must not resolve a symlink planted in a legacy customer HOME.
func (h *Harness) gatewayDir(in RuntimeInput, suffix ...string) (string, error) {
	path := h.Config.DataDir
	parts := append([]string{"runtime", "projects", in.Project.ID, "sessions", in.Session.HarnessID}, suffix...)
	for _, part := range parts {
		if !toolIdentifier.MatchString(part) {
			return "", errors.New("invalid gateway runtime identity")
		}
		path = filepath.Join(path, part)
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		st, err := os.Lstat(path)
		if err != nil || !st.IsDir() {
			return "", errors.New("unsafe gateway runtime path")
		}
	}
	return path, nil
}

func scrubGatewayHome(home string) error {
	files, err := os.ReadDir(home)
	if err != nil {
		return err
	}
	for _, f := range files {
		n := f.Name()
		if n == ".credentials.yaml" || (strings.HasPrefix(n, "invocation-") && strings.HasSuffix(n, ".yml")) {
			// Remove the entry itself, including a symlink. Never open its target.
			if err := os.RemoveAll(filepath.Join(home, n)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Retired/recycled sessions must also lose the invocation files written by
// earlier versions. Only the managed directory shapes are traversed, never links.
func (h *Harness) scrubLegacyGatewayHomes() error {
	if h.Config.DataDir == "" {
		return errors.New("缺少运行目录")
	}
	for _, spec := range []struct {
		base  string
		shape []string
	}{
		{"runtime/projects", []string{"*", "sessions", "*", "gateway-home"}},
		{"trash", []string{"*", "*", "gateway-home"}},
	} {
		root := filepath.Join(h.Config.DataDir, spec.base)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if p == root && errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			}
			if !d.IsDir() || p == root {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			parts := strings.Split(rel, string(os.PathSeparator))
			at := len(parts) - 1
			expected := spec.shape[at]
			if expected != "*" && expected != parts[at] {
				return filepath.SkipDir
			}
			if at == len(spec.shape)-1 {
				if err := scrubGatewayHome(p); err != nil {
					return err
				}
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *Harness) gatewayPlugin(in RuntimeInput, id, transport string) (Plugin, bool) {
	var empty Plugin
	if in.Routing || in.Run.Gateway == nil || !contains(in.Run.Gateway.PluginIDs, id) || !h.Store.gatewayRunLive(in.Run) {
		return empty, false
	}
	p, err := h.Store.Plugin(id)
	return p, err == nil && p.Transport == transport && (p.ProjectID == "" || p.ProjectID == in.Project.ID) && (p.Enabled == nil || *p.Enabled)
}

// Resolve the owner-configured HTTPS endpoint on the host. Only the run's
// granted plugin can receive its headers. Session handles cannot cross runs.
func (h *Harness) gatewayMCPHandler(in RuntimeInput, transport http.RoundTripper) http.Handler {
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	type session struct{ external, local string }
	var sessions sync.Map
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/gateway-mcp/")
		if !toolIdentifier.MatchString(id) || r.URL.RawQuery != "" || (r.Method != "GET" && r.Method != "POST" && r.Method != "DELETE") {
			http.Error(w, "invalid MCP request", 400)
			return
		}
		p, ok := h.gatewayPlugin(in, id, "http")
		if !ok {
			http.Error(w, "MCP unavailable", 403)
			return
		}
		var body io.Reader
		if r.Method == "POST" {
			raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
			if e != nil || !json.Valid(raw) {
				http.Error(w, "invalid MCP request", 400)
				return
			}
			body = bytes.NewReader(raw)
		}
		ctx, cancel := context.WithDeadline(r.Context(), time.UnixMilli(in.Run.Gateway.Deadline))
		defer cancel()
		up, e := http.NewRequestWithContext(ctx, r.Method, p.URL, body)
		if e != nil {
			http.Error(w, "MCP unavailable", 502)
			return
		}
		if !strings.EqualFold(up.URL.Scheme, "https") || up.URL.User != nil {
			http.Error(w, "MCP unavailable", 403)
			return
		}
		for _, name := range []string{"Accept", "Mcp-Protocol-Version", "Last-Event-ID"} {
			if v := r.Header.Get(name); v != "" {
				up.Header.Set(name, v)
			}
		}
		if r.Method == "POST" {
			up.Header.Set("Content-Type", "application/json")
		}
		if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
			cur, ok := sessions.Load(id)
			if !ok || cur.(session).local != sid {
				http.Error(w, "unknown MCP session", 404)
				return
			}
			up.Header.Set("Mcp-Session-Id", cur.(session).external)
		}
		for name, v := range p.Headers {
			up.Header.Set(name, v)
		}
		if !h.Store.gatewayRunLive(in.Run) {
			http.Error(w, "inactive gateway run", 409)
			return
		}
		res, e := client.Do(up)
		if e != nil {
			http.Error(w, "MCP unavailable", 502)
			return
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			status := res.StatusCode
			if status < 400 {
				status = 502
			}
			http.Error(w, "MCP unavailable", status)
			return
		}
		if sid := res.Header.Get("Mcp-Session-Id"); sid != "" {
			cur, ok := sessions.Load(id)
			if !ok || cur.(session).external != sid {
				cur = session{external: sid, local: newID()}
				sessions.Store(id, cur)
			}
			w.Header().Set("Mcp-Session-Id", cur.(session).local)
		}
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		w.WriteHeader(res.StatusCode)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = io.Copy(gatewayFlusher{w}, res.Body)
	})
}
