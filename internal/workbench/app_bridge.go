package workbench

import (
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
func (h *Harness) startBridge(in RuntimeInput) (func(), error) {
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
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		value, err := h.Store.appTool(in, req.Tool, req.Args)
		if err != nil {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(value)
	})
	go server.Serve(listener)
	return func() { server.Close(); os.Remove(socket) }, nil
}

func (s *Store) appTool(in RuntimeInput, tool string, args json.RawMessage) (any, error) {
	var a struct {
		Query        string   `json:"query"`
		RunID        string   `json:"runId"`
		Index        int      `json:"index"`
		Schedule     Schedule `json:"schedule"`
		NotifySource bool     `json:"notifySource"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
	}
	switch tool {
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
	case "list_schedules":
		return s.Schedules(in.Project.ID)
	case "save_schedule":
		v := a.Schedule
		if in.Run.Source == "automation" {
			return nil, errors.New("自动任务不能自行创建或修改定时任务")
		}
		v.ProjectID = in.Project.ID
		v.NotifyConnectionID = ""
		v.NotifyConversationID = ""
		if a.NotifySource {
			m, e := s.Message(in.Run.MessageID)
			if e != nil {
				return nil, e
			}
			if m.Incoming.Source != "wechat" && m.Incoming.Source != "feishu" {
				return nil, errors.New("本次入口没有可用的私聊通知目标，请在网页选择")
			}
			v.NotifyConnectionID = m.Incoming.ConnectionID
			v.NotifyConversationID = m.Incoming.ConversationID
		}
		creationID := ""
		if v.ID == "" {
			creationID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(in.Run.ID+":"+encode(v))).String()
			if old, e := s.Schedule(creationID); e == nil {
				return old, nil
			}
		}
		result, e := s.saveSchedule(v, creationID)
		if e == nil {
			e = s.Emit(in.Session.ID, in.Run.ID, "schedule.updated", result)
		}
		return result, e
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
