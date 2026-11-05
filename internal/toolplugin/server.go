// Package toolplugin is an independently deployed MCP service. The workbench
// binary never imports this package or connects to its business database.
package toolplugin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
)

type Operation struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
	OwnerOnly   bool   `json:"ownerOnly"`
	Effect      string `json:"effect"`
}
type Grant struct {
	ID         string   `json:"id"`
	Token      string   `json:"token"`
	ProjectID  string   `json:"projectId"`
	Actions    []string `json:"actions"`
	Operations []string `json:"operations"`
}
type Model struct {
	Endpoint string `json:"endpoint"`
	Key      string `json:"key"`
	Name     string `json:"name"`
}
type Config struct {
	GenerationConcurrency int                  `json:"generationConcurrency"`
	Listen                string               `json:"listen"`
	DSN                   string               `json:"mysqlDSN"`
	SourceBase            string               `json:"sourceBase"`
	SourceAuth            string               `json:"sourceAuth"`
	SourceUser            string               `json:"sourceUser"`
	SourcePassword        string               `json:"sourcePassword"`
	SourcePath            string               `json:"sourcePath"`
	RequiredFields        []string             `json:"requiredFields"`
	TrimFields            []string             `json:"trimFields"`
	IDField               string               `json:"idField"`
	Model                 Model                `json:"model"`
	Grants                []Grant              `json:"grants"`
	Operations            map[string]Operation `json:"operations"`
	AssetsDir             string               `json:"assetsDir"`
}
type Server struct {
	Config    Config
	DB        *sql.DB
	HTTP      *http.Client
	wake      chan struct{}
	epoch     int64
	namespace string
	syncMu    sync.Mutex
	authMu    sync.Mutex
	authUntil time.Time
	active    atomic.Bool
}

func encode(v any) string  { b, _ := json.Marshal(v); return string(b) }
func hash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func randomID() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Open(c Config) (*Server, error) {
	if c.SourceAuth != "" && c.SourceAuth != "basic" && c.SourceAuth != "browser_session" {
		return nil, errors.New("unknown source authentication mode")
	}
	u, e := url.Parse(c.SourceBase)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, errors.New("sourceBase requires HTTPS")
	}
	if c.SourcePath == "" {
		c.SourcePath = "/data/canonical_users_live.json"
	}
	if c.IDField == "" {
		c.IDField = "safe_user_ref"
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:3123"
	}
	for _, g := range c.Grants {
		if len(g.Token) < 32 || g.ProjectID == "" {
			return nil, errors.New("invalid project grant")
		}
	}
	for _, op := range c.Operations {
		if !strings.HasPrefix(op.Path, "/api/local/") || strings.ContainsAny(op.Path, "?#") || (op.Method != "GET" && op.Method != "POST") {
			return nil, errors.New("invalid upstream operation")
		}
		if !op.OwnerOnly && !(op.Method == "GET" && op.Effect == "read") && !(op.Method == "POST" && op.Effect == "prepare") {
			return nil, errors.New("upstream writes require owner")
		}
	}
	dc, e := mysql.ParseDSN(c.DSN)
	if e != nil {
		return nil, e
	}
	dc.Timeout = 5 * time.Second
	dc.ReadTimeout = 15 * time.Second
	dc.WriteTimeout = 15 * time.Second
	dc.MultiStatements = false
	db, e := sql.Open("mysql", dc.FormatDSN())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(8)
	db.SetConnMaxLifetime(3 * time.Minute)
	jar, _ := cookiejar.New(nil)
	s := &Server{Config: c, DB: db, HTTP: &http.Client{Jar: jar, Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, wake: make(chan struct{}, 1), namespace: hash(dc.DBName)[:16]}
	if c.GenerationConcurrency < 1 {
		c.GenerationConcurrency = 2
	}
	if c.GenerationConcurrency > 4 {
		db.Close()
		return nil, errors.New("generation concurrency must be1 to4")
	}
	s.Config.GenerationConcurrency = c.GenerationConcurrency
	if e = s.init(); e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Server) grant(r *http.Request) (Grant, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	for _, g := range s.Config.Grants {
		if subtle.ConstantTimeCompare([]byte(token), []byte(g.Token)) == 1 {
			return g, true
		}
	}
	return Grant{}, false
}
func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/health" {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if s.DB.PingContext(ctx) != nil || !s.active.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		fmt.Fprint(w, `{"status":"ok"}`)
		return
	}
	g, ok := s.grant(r)
	if !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	var f struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&f) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if len(f.ID) == 0 {
		w.WriteHeader(202)
		return
	}
	var result any
	var e error
	switch f.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "workspace-tool-plugin", "version": "1.0.0"}}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		tools := []any{}
		for _, name := range g.Actions {
			tools = append(tools, map[string]any{"name": name, "description": descriptions[name], "inputSchema": map[string]any{"type": "object", "additionalProperties": true}})
		}
		result = map[string]any{"tools": tools}
	case "tools/call":
		var q struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(f.Params, &q) != nil {
			e = errors.New("invalid arguments")
		} else if !contains(g.Actions, q.Name) {
			e = errors.New("action not granted")
		} else if str(q.Arguments, "project_id") != g.ProjectID || str(q.Arguments, "session_id") == "" {
			e = errors.New("scope mismatch")
		} else {
			result, e = s.call(r.Context(), g, q.Name, q.Arguments, r.Header.Get("X-Workbench-Owner-Confirmed") == "true")
		}
		if e != nil {
			if _, unknown := e.(acceptanceUnknown); unknown {
				result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "受理结果不明，请按原编号查回"}}}
			} else {
				payload := map[string]any{"status": "failed", "summary": "工具未完成：" + e.Error(), "result": map[string]any{"accepted": false}}
				result = map[string]any{"structuredContent": payload, "content": []any{map[string]any{"type": "text", "text": encode(payload)}}}
			}
		} else {
			result = map[string]any{"structuredContent": result, "content": []any{map[string]any{"type": "text", "text": "工具结果已返回"}}}
		}
	default:
		e = errors.New("unknown method")
	}
	frame := map[string]any{"jsonrpc": "2.0", "id": f.ID, "result": s.redact(result)}
	if e != nil && f.Method != "tools/call" {
		delete(frame, "result")
		frame["error"] = map[string]any{"code": -32601, "message": "unsupported method"}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(frame)
}

var descriptions = map[string]string{"sync_contexts": "拉取并清洗源上下文，校验后发布到插件MySQL", "query_contexts": "读取已发布用户上下文与来源", "batch_generate": "按固定方案批量生成，最多2000条", "batch_results": "分页读取批次条目", "recommend_users": "读取原候选与证据，不自动触达", "legacy_read": "按登记操作读取原中心能力", "legacy_action": "本人确认后调用已登记原中心动作", "legacy_prepare": "沿原方法准备内容，保留原校验，不发送", "status": "查询任务状态", "lookup": "按原调用编号查回", "cancel": "停止任务", "configuration": "查看已迁移配置及依赖摘要"}

func str(m map[string]any, k string) string { x, _ := m[k].(string); return x }
func number(m map[string]any, k string, def int) int {
	if v, ok := m[k].(float64); ok {
		return int(v)
	}
	return def
}
func success(summary string, result any) any {
	return map[string]any{"status": "succeeded", "summary": summary, "result": result}
}
func (s *Server) call(ctx context.Context, g Grant, name string, a map[string]any, owner bool) (any, error) {
	switch name {
	case "sync_contexts", "batch_generate":
		return s.enqueue(ctx, g, name, a)
	case "status", "lookup", "cancel", "retry":
		return s.jobControl(ctx, g, name, a)
	case "query_contexts":
		return s.query(ctx, a)
	case "list_user_refs":
		return s.listUserRefs(ctx, a)
	case "batch_results":
		return s.batchResults(ctx, g, a)
	case "recommend_users":
		a["operation"] = "opportunity_pool"
		return s.upstream(ctx, g, a, false, owner)
	case "legacy_read":
		return s.upstream(ctx, g, a, false, owner)
	case "legacy_prepare":
		return s.enqueue(ctx, g, name, a)
	case "legacy_action":
		if !owner {
			return nil, errors.New("owner confirmation required")
		}
		op, ok := s.Config.Operations[str(a, "operation")]
		if !ok || !op.OwnerOnly || !contains(g.Operations, str(a, "operation")) {
			return nil, errors.New("operation not granted")
		}
		return s.enqueue(ctx, g, name, a)
	case "configuration":
		return s.configuration(g), nil
	case "read_configuration":
		return s.readConfiguration(a)
	}
	return nil, errors.New("unknown action")
}
func (s *Server) sourceRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	if e := s.authenticateSource(ctx); e != nil {
		return nil, e
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.Config.SourceBase, "/")+path, body)
	if e != nil {
		return nil, e
	}
	req.SetBasicAuth(s.Config.SourceUser, s.Config.SourcePassword)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", strings.TrimRight(s.Config.SourceBase, "/"))
	res, e := s.HTTP.Do(req)
	if e != nil {
		return nil, errors.New("source unavailable")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		if s.Config.SourceAuth == "browser_session" && (res.StatusCode == 401 || res.StatusCode == 303) {
			s.authMu.Lock()
			s.authUntil = time.Time{}
			s.authMu.Unlock()
		}
		return nil, fmt.Errorf("source returned HTTP %d; no business request was replayed", res.StatusCode)
	}
	return res, nil
}
func (s *Server) upstream(ctx context.Context, g Grant, a map[string]any, write, owner bool) (any, error) {
	key := str(a, "operation")
	op, ok := s.Config.Operations[key]
	if !ok || !contains(g.Operations, key) {
		return nil, errors.New("operation not granted")
	}
	if write {
		if !op.OwnerOnly || op.Effect != "execute" || !owner {
			return nil, errors.New("owner confirmation required")
		}
	} else if op.Method != "GET" || op.Effect != "read" || op.OwnerOnly {
		return nil, errors.New("read action cannot write")
	}
	path := op.Path
	params, _ := a["parameters"].(map[string]any)
	var body io.Reader
	if op.Method == "GET" {
		q := url.Values{}
		for k, v := range params {
			q.Set(k, fmt.Sprint(v))
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
	} else {
		body = strings.NewReader(encode(params))
	}
	res, e := s.sourceRequest(ctx, op.Method, path, body)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	bound := 512 << 10
	if str(a, "projection") == "user_refs" {
		bound = 32 << 20
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, int64(bound)+1))
	if e != nil {
		return nil, e
	}
	if len(raw) > bound {
		return nil, errors.New("result too large; use narrower parameters or pagination")
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("upstream response was not JSON")
	}
	if m, ok := value.(map[string]any); ok {
		if x, exists := m["ok"]; exists && x == false {
			return nil, errors.New("upstream rejected operation; check source state")
		}
	}
	if str(a, "projection") == "user_refs" {
		refs := []string{}
		seen := map[string]bool{}
		var collect func(any)
		collect = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for _, k := range []string{"safe_user_ref", "user_ref", "subject_ref"} {
					if ref, ok := x[k].(string); ok && strings.HasPrefix(ref, "usr_") && !seen[ref] {
						seen[ref] = true
						refs = append(refs, ref)
					}
				}
				for _, n := range x {
					collect(n)
				}
			case []any:
				for _, n := range x {
					collect(n)
				}
			}
		}
		collect(value)
		start := number(a, "offset", 0)
		if start < 0 {
			start = 0
		}
		limit := number(a, "limit", 2000)
		if limit < 1 || limit > 2000 {
			limit = 2000
		}
		if start > len(refs) {
			start = len(refs)
		}
		end := min(start+limit, len(refs))
		value = map[string]any{"user_refs": refs[start:end], "total": len(refs), "next_offset": end, "scope_note": "仅投影原结果中的身份引用，不另行判定资格"}
	}
	return success("已调用原中心兼容能力；原服务仍为执行方", map[string]any{"operation": key, "dependency": "original-context-center", "data": value}), nil
}
func (s *Server) configuration(g Grant) any {
	ops := []any{}
	for _, k := range g.Operations {
		if op, ok := s.Config.Operations[k]; ok {
			ops = append(ops, map[string]any{"key": k, "description": op.Description, "method": op.Method, "ownerOnly": op.OwnerOnly, "effect": op.Effect})
		}
	}
	var manifest any
	raw, e := os.ReadFile(s.Config.AssetsDir + "/migration-manifest.json")
	if e == nil {
		json.Unmarshal(raw, &manifest)
	}
	return success("配置已导入；原能力依赖及启用状态见清单", map[string]any{"operations": ops, "migration": manifest, "mysql": "plugin-owned", "sourceDependency": s.Config.SourceBase})
}

func object(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }

func grantIdentity(g Grant) string {
	if g.ID != "" {
		return g.ID
	}
	return hash(g.Token)
}

type acceptanceUnknown struct{ error }

func (s *Server) readConfiguration(a map[string]any) (any, error) {
	raw, e := os.ReadFile(filepath.Join(s.Config.AssetsDir, "migration-manifest.json"))
	if e != nil {
		return nil, errors.New("migration manifest unavailable")
	}
	var manifest struct{ Entries []struct{ Path, Kind string } }
	if json.Unmarshal(raw, &manifest) != nil {
		return nil, errors.New("manifest invalid")
	}
	name := str(a, "name")
	allowed := false
	for _, entry := range manifest.Entries {
		if entry.Path == name && !strings.HasPrefix(entry.Kind, "private") {
			allowed = true
		}
	}
	if !allowed || filepath.Clean(name) != name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "..") {
		return nil, errors.New("configuration not in public migration manifest")
	}
	base, e := filepath.EvalSymlinks(s.Config.AssetsDir)
	if e != nil {
		return nil, e
	}
	target, e := filepath.EvalSymlinks(filepath.Join(base, name))
	if e != nil {
		return nil, e
	}
	rel, e := filepath.Rel(base, target)
	if e != nil || strings.HasPrefix(rel, "..") {
		return nil, errors.New("configuration escaped asset root")
	}
	content, e := os.ReadFile(target)
	if e != nil || len(content) > 2<<20 {
		return nil, errors.New("configuration missing or too large")
	}
	chars := []rune(string(content))
	start := number(a, "offset", 0)
	if start < 0 || start > len(chars) {
		return nil, errors.New("invalid offset")
	}
	end := min(start+16000, len(chars))
	return success("读取原配置快照；发布/草稿状态见迁移清单", map[string]any{"name": name, "sha256": hash(string(content)), "content": string(chars[start:end]), "next_offset": end, "complete": start == 0 && end == len(chars)}), nil
}
