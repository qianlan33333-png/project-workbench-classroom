package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// A private Unix socket lives in the current project's runtime mount. The
// closure fixes the project/run; tools cannot supply another authority scope.
func bridgeSocket(in RuntimeInput) string {
	return filepath.Join(os.TempDir(), "pw-"+in.Run.ID+".sock")
}
func (h *Harness) startBridge(parent context.Context, in RuntimeInput) (func(), error) {
	var model, gatewayMCP http.Handler
	stdio := gatewayStdioStarts{started: make(map[string]bool)}
	if h.gatewayInput(in) {
		var e error
		gatewayMCP = boundGatewayHTTP(h.gatewayMCPHandler(in, nil), 8, 256)
		model, e = h.gatewayModelHandler(in)
		if e != nil {
			return nil, e
		}
		model = boundGatewayHTTP(model, 2, 64)
	}
	socket := bridgeSocket(in)
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	lifetime, cancel := context.WithCancel(parent)
	server := &http.Server{BaseContext: func(net.Listener) context.Context { return lifetime }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second}
	if model != nil {
		server.WriteTimeout = 0
	} // Streaming is bounded by the run deadline.
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gatewayMCP != nil && strings.HasPrefix(r.URL.Path, "/gateway-mcp/") {
			gatewayMCP.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/gateway-stdio/") {
			id := strings.TrimPrefix(r.URL.Path, "/gateway-stdio/")
			if gatewayMCP == nil || in.Run.Gateway == nil || !contains(in.Run.Gateway.PluginIDs, id) || r.URL.RawQuery != "" || r.Method != "GET" || r.Header.Get("Upgrade") != "pwb-stdio" {
				http.Error(w, "MCP unavailable", 403)
				return
			}
			if !stdio.claim(id) {
				http.Error(w, "MCP startup budget exhausted", http.StatusTooManyRequests)
				return
			}
			defer stdio.work.Done()
			p, ok := h.gatewayPlugin(in, id, "stdio")
			if !ok {
				http.Error(w, "MCP unavailable", 403)
				return
			}
			live, stop := h.gatewayPluginContext(lifetime, in, p)
			defer stop()
			h.serveIsolatedStdio(live, in, p, true, w)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/global-mcp/") {
			if model != nil {
				http.NotFound(w, r)
				return
			}
			h.servePersonalMCP(lifetime, in, w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/gateway-model") {
			if model == nil {
				http.NotFound(w, r)
				return
			}
			model.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		var req struct {
			Tool string          `json:"tool"`
			Args json.RawMessage `json:"args"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		var value any
		var err error
		if req.Tool == "run_global_cli" && in.Project.ID == personalProjectID && !in.Routing {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(95 * time.Second))
			value, err = h.runPersonalCLI(r.Context(), in, req.Args)
		} else if model == nil && (req.Tool == "query_calendar" || req.Tool == "change_calendar") {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(95 * time.Second))
			value, err = h.Store.plannerTool(r.Context(), in, req.Tool, req.Args)
		} else {
			value, err = h.Store.appTool(in, req.Tool, req.Args)
		}
		if err != nil {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(value)
	})
	go server.Serve(listener)
	return func() {
		stdio.mu.Lock()
		stdio.closed = true
		stdio.mu.Unlock()
		cancel()
		server.Close()
		stdio.work.Wait()
		os.Remove(socket)
	}, nil
}

func (s *Store) appTool(in RuntimeInput, tool string, args json.RawMessage) (any, error) {
	if _, e := s.gatewaySession(in.Session.ID); e == nil || in.Run.Source == gatewaySource {
		var a struct {
			Offset int    `json:"offset"`
			Query  string `json:"query"`
			RunID  string `json:"runId"`
		}
		if len(args) > 0 {
			if e = json.Unmarshal(args, &a); e != nil {
				return nil, e
			}
		}
		switch tool {
		case "read_paused_history":
			return s.pausedHistoryPage(in.Session.ID, a.RunID, a.Offset)
		case "search_history":
			return listJSON[Run](s.DB, "SELECT data FROM runs WHERE session_id=? AND (data->>'$.text' LIKE ? OR data->>'$.result' LIKE ?) ORDER BY created_at DESC LIMIT 20", in.Session.ID, "%"+a.Query+"%", "%"+a.Query+"%")
		case "read_run":
			r, e := s.Run(a.RunID)
			if e != nil {
				return nil, e
			}
			if r.SessionID != in.Session.ID {
				return nil, errors.New("只能读取当前客户会话")
			}
			return r, nil
		default:
			return nil, errors.New("外部会话没有工作台管理或跨会话权限")
		}
	}
	switch tool {
	case "query_calendar", "change_calendar", "update_occurrence", "update_series", "delete_schedule", "save_schedule", "list_schedules":
		return s.plannerTool(context.Background(), in, tool, args)
	}
	if in.Project.ID == personalProjectID {
		return s.personalTool(in, tool, args)
	}
	var a struct {
		Query        string          `json:"query"`
		RunID        string          `json:"runId"`
		Index        int             `json:"index"`
		Schedule     json.RawMessage `json:"schedule"`
		NotifySource bool            `json:"notifySource"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
	}
	switch tool {
	case "manage_cli":
		if in.Run.Source == "automation" {
			return nil, errors.New("定时任务不能安装或授权全局工具")
		}
		var req struct {
			ToolID string  `json:"toolId"`
			Action string  `json:"action"`
			Tool   CLITool `json:"tool"`
		}
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, err
		}
		switch req.Action {
		case "list":
			return s.CLITools()
		case "jobs":
			return s.ToolJobs()
		case "save":
			return s.SaveCLITool(req.Tool)
		case "remove":
			return nil, s.removeCLITool(req.ToolID, in.Run.ID)
		case "install", "auth", "check", "configure":
			if s.toolManager == nil {
				return nil, errors.New("工具管理服务未启动")
			}
			return s.toolManager(req.ToolID, req.Action, []string{"requesting-run", in.Run.ID})
		}
		return nil, errors.New("不支持的工具管理动作")
	case "search_history":
		return s.SessionSearch(in.Project.ID, a.Query)
	case "read_run":
		r, e := s.Run(a.RunID)
		if e != nil {
			return nil, e
		}
		if r.ProjectID != in.Project.ID {
			return nil, errors.New("只能读取当前项目的执行历史")
		}
		return r, nil
	case "list_references", "read_reference":
		m, e := s.Message(in.Run.MessageID)
		if e != nil {
			return nil, e
		}
		if tool == "list_references" {
			return m.References, nil
		}
		if a.Index < 0 || a.Index >= len(m.References) {
			return nil, errors.New("没有这份明确授权的引用资料")
		}
		ref := m.References[a.Index]
		b, hash, e := s.ReadFile(ref.ProjectID, ref.Path)
		if e != nil {
			return nil, e
		}
		if len(b) > 2<<20 || !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
			return nil, errors.New("跨项目引用限 2MB 内的文本资料，请先明确导出所需内容")
		}
		p, e := s.Project(ref.ProjectID)
		if e != nil {
			return nil, e
		}
		value := map[string]string{"projectId": p.ID, "projectName": p.Name, "path": ref.Path, "sha256": hash, "text": string(b)}
		if e = s.Emit(in.Session.ID, in.Run.ID, "reference.read", map[string]string{"projectId": p.ID, "path": ref.Path, "sha256": hash}); e != nil {
			return nil, e
		}
		return value, nil
	default:
		return nil, fmt.Errorf("未知工作台工具：%s", tool)
	}
}

func (s *Store) saveChatSchedule(in RuntimeInput, raw json.RawMessage, notify bool) (Schedule, []string, error) {
	var patch map[string]json.RawMessage
	var requested struct {
		ID      string `json:"id"`
		Enabled *bool  `json:"enabled"`
	}
	var v Schedule
	if err := json.Unmarshal(raw, &patch); err != nil {
		return v, nil, err
	}
	if err := json.Unmarshal(raw, &requested); err != nil {
		return v, nil, err
	}
	for key := range patch {
		switch key {
		case "id", "name", "instruction", "cron", "timezone", "expertId", "enabled", "condition", "sourceScope", "output", "sessionId", "onceAt", "capabilities":
		default:
			return v, nil, fmt.Errorf("不支持修改 %s", key)
		}
	}
	if requested.ID != "" && requested.Enabled != nil && !*requested.Enabled {
		old, e := s.Schedule(requested.ID)
		if e != nil {
			return v, nil, e
		}
		if old.ProjectID != in.Project.ID {
			return v, nil, ErrNotFound
		}
		return s.toggleSchedule(requested.ID, false)
	}
	s.expertMu.RLock()
	defer s.expertMu.RUnlock()
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	if requested.ID != "" {
		old, e := s.Schedule(requested.ID)
		if e != nil {
			return v, nil, e
		}
		if old.ProjectID != in.Project.ID {
			return v, nil, ErrNotFound
		}
		v = old
	}

	if e := json.Unmarshal(raw, &v); e != nil {
		return v, nil, e
	}
	v.ProjectID = in.Project.ID
	if v.SessionID == "current" {
		v.SessionID = in.Session.ID
		if v.SessionID == "" {
			return v, nil, errors.New("没有可续接的当前对话")
		}
	}
	if notify {
		m, e := s.Message(in.Run.MessageID)
		if e != nil {
			return v, nil, e
		}
		if !isChatChannel(m.Incoming.Source) {
			return v, nil, errors.New("本次入口没有可用的私聊通知目标，请在网页选择")
		}
		v.NotifyConnectionID = m.Incoming.ConnectionID
		v.NotifyConversationID = m.Incoming.ConversationID
	}
	creationID := ""
	if v.ID == "" {
		creationID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(in.Run.ID+":"+encode(v))).String()
		if old, e := s.Schedule(creationID); e == nil {
			return old, nil, nil
		}
	}
	var ids []string
	if requested.ID != "" && requested.Enabled != nil && !*requested.Enabled {
		var err error
		ids, err = s.activeScheduleRuns(v.ID)
		if err != nil {
			return v, nil, err
		}
	}
	v, e := s.saveScheduleLocked(v, creationID, requested.Enabled != nil)
	return v, ids, e
}
