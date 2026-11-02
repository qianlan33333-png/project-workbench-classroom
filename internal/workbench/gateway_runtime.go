package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	gw "projectworkbench/pkg/wecomgateway"
)

func (h *Harness) gatewayInput(in RuntimeInput) bool {
	if in.Run.Source == gatewaySource {
		return true
	}
	_, e := h.Store.gatewaySession(in.Session.ID)
	return e == nil
}

func validateGatewayKnowledge(paths []string) error {
	if len(paths) > 32 {
		return errors.New("at most 32 explicit knowledge files may be shared")
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if validPath(p) != nil || seen[p] || strings.Contains(p, "\\") {
			return errors.New("invalid knowledge file path")
		}
		for _, part := range strings.Split(p, "/") {
			if strings.HasPrefix(part, ".") || part == "对话归档" {
				return errors.New("private and archive paths cannot be shared")
			}
		}
		seen[p] = true
	}
	return nil
}

func (h *Harness) gatewayWorkspace(in RuntimeInput) (string, error) {
	root := filepath.Join(h.Config.DataDir, "runtime", "gateway-workspaces", in.Run.ID)
	if !toolIdentifier.MatchString(in.Run.ID) {
		return "", errors.New("invalid run identity")
	}
	if in.Run.Gateway == nil {
		return "", errors.New("external run grants missing")
	}
	if e := validateGatewayKnowledge(in.Run.Gateway.KnowledgePaths); e != nil {
		return "", e
	}
	// Never reuse customer-writable files or symlinks from a prior invocation.
	if e := os.RemoveAll(root); e != nil {
		return "", e
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return "", e
	}
	for _, p := range in.Run.Gateway.KnowledgePaths {
		data, e := h.gatewayKnowledgeFile(in.Project.ID, p)
		if e != nil {
			return "", errors.New("configured knowledge file unavailable")
		}
		if len(data) > 1<<20 {
			return "", errors.New("knowledge file exceeds 1 MiB")
		}
		if e = gw.WritePrivate(filepath.Join(root, p), data); e != nil {
			return "", e
		}
	}
	if e := h.Store.writeGatewayHistory(root, in.Run); e != nil {
		return "", e
	}
	return root, nil
}

func (h *Harness) gatewayKnowledgeFile(project, p string) ([]byte, error) {
	r, e := h.Store.openRoot(project)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	parts := strings.Split(p, "/")
	for i := range parts {
		st, e := r.Lstat(strings.Join(parts[:i+1], "/"))
		if e != nil {
			return nil, e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("knowledge symlinks are not allowed")
		}
		if i == len(parts)-1 && (!st.Mode().IsRegular() || st.Size() > 1<<20) {
			return nil, errors.New("knowledge file unavailable or too large")
		}
	}
	f, e := r.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 1<<20+1))
}

// Only the host reads the real model key. Customer containers receive neither
// this file nor the host environment, only a fixed placeholder credential.
func (h *Harness) gatewayModelKey() (string, error) {
	ref := h.Config.GatewayModelAPIKeyEnv
	if ref == "" {
		ref = "DEEPSEEK_API_KEY"
	}
	if h.Config.HarnessHome != "" {
		data, e := os.ReadFile(filepath.Join(h.Config.HarnessHome, ".credentials.yaml"))
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return "", errors.New("model credential unavailable")
		}
		if e == nil {
			var doc struct {
				Refs map[string]string `yaml:"refs"`
			}
			if yaml.Unmarshal(data, &doc) != nil {
				return "", errors.New("invalid model credential store")
			}
			if v := strings.TrimSpace(doc.Refs[ref]); v != "" {
				return v, nil
			}
		}
	}
	if v := strings.TrimSpace(os.Getenv(ref)); v != "" {
		return v, nil
	}
	return "", errors.New("model credential unavailable")
}

// Reuses the per-run private bridge. This is a single fixed Messages operation,
// not an arbitrary authenticated HTTP proxy.
func (h *Harness) gatewayModelHandler(in RuntimeInput) (http.Handler, error) {
	base := strings.TrimRight(h.Config.GatewayModelBaseURL, "/")
	if base == "" {
		base = "https://api.deepseek.com/anthropic"
	}
	if e := gw.ValidateURL(base); e != nil {
		return nil, e
	}
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	key, e := h.gatewayModelKey()
	if e != nil {
		return nil, e
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deny := func(code int) { http.Error(w, "model request refused", code) }
		if r.Method != "POST" || r.URL.Path != "/gateway-model/v1/messages" || r.URL.RawQuery != "" {
			deny(403)
			return
		}
		meta := in.Run.Gateway
		if meta == nil || meta.Deadline <= now() {
			deny(409)
			return
		}
		if !h.Store.gatewayRunLive(in.Run) {
			deny(409)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
		var request map[string]json.RawMessage
		var model string
		if err != nil || json.Unmarshal(body, &request) != nil || json.Unmarshal(request["model"], &model) != nil || model == "" || model != h.Config.Model {
			deny(400)
			return
		}
		for name := range request {
			if strings.EqualFold(name, "model") && name != "model" {
				deny(400)
				return
			}
		}
		// Forward exactly the model value we checked. Re-encoding the parsed
		// top-level map removes duplicate keys and parser precedence ambiguity.
		body, err = json.Marshal(request)
		if err != nil {
			deny(400)
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), time.UnixMilli(meta.Deadline))
		defer cancel()
		up, err := http.NewRequestWithContext(ctx, "POST", base+"/messages", bytes.NewReader(body))
		if err != nil {
			deny(502)
			return
		}
		up.Header.Set("Content-Type", "application/json")
		up.Header.Set("x-api-key", key)
		for _, name := range []string{"anthropic-version", "anthropic-beta"} {
			if v := r.Header.Get(name); v != "" {
				up.Header.Set(name, v)
			}
		}
		// Reading the caller's HTTP body may have taken time; recheck at dispatch.
		if !h.Store.gatewayRunLive(in.Run) {
			deny(409)
			return
		}
		res, err := client.Do(up)
		if err != nil {
			deny(502)
			return
		}
		defer res.Body.Close()
		// Never forward credential-bearing redirect/error prose or headers.
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			deny(502)
			return
		}
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(gatewayFlusher{w}, res.Body)
	}), nil
}

type gatewayFlusher struct{ http.ResponseWriter }

func (w gatewayFlusher) Write(b []byte) (int, error) {
	n, e := w.ResponseWriter.Write(b)
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	return n, e
}

// Use one database snapshot at each irreversible boundary. An in-flight provider
// request cannot be revoked, so version is also checked before any answer is sent.
func (s *Store) gatewayRunLive(r Run) bool {
	if r.Gateway == nil || r.Gateway.Deadline <= now() {
		return false
	}
	var active int
	e := s.DB.QueryRow(`SELECT count(*) FROM runs r JOIN gateway_sessions g ON r.session_id=g.id JOIN sessions ss ON ss.id=g.id JOIN projects p ON p.id=ss.project_id JOIN gateway_bindings b ON b.id=g.binding_id JOIN connections c ON c.id=b.connection_id
 WHERE r.id=? AND r.state='running' AND g.id=? AND p.archived=0 AND coalesce(json_extract(ss.data,'$.recycledAt'),0)=0
 AND json_extract(g.data,'$.mode')='auto' AND json_extract(g.data,'$.version')=? AND json_extract(g.data,'$.bindingVersion')=?
 AND json_extract(b.data,'$.version')=? AND json_extract(b.data,'$.enabled')=1 AND json_extract(c.data,'$.enabled')=1`, r.ID, r.SessionID, r.Gateway.Version, r.Gateway.BindingVersion, r.Gateway.BindingVersion).Scan(&active)
	return e == nil && active == 1
}
