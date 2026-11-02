package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const recentWindow = int64(72 * time.Hour / time.Millisecond)

func rollingChanges(v []ContextChange, at int64) []ContextChange {
	out := []ContextChange{}
	for _, c := range v {
		if c.At > at-recentWindow && c.At <= at {
			out = append(out, c)
		}
	}
	return out
}
func (s *Store) ProjectContext(id string) (ProjectContext, error) {
	var projectRaw, contextRaw string
	if e := s.DB.QueryRow("SELECT p.data,coalesce(c.data,'') FROM projects p LEFT JOIN project_contexts c ON c.project_id=p.id WHERE p.id=?", id).Scan(&projectRaw, &contextRaw); e != nil {
		if e == sql.ErrNoRows {
			return ProjectContext{}, ErrNotFound
		}
		return ProjectContext{}, e
	}
	p, e := decodeJSON[Project](projectRaw)
	if e != nil {
		return ProjectContext{}, e
	}
	c := ProjectContext{ProjectID: id, State: "pending", Manual: p.Description, ManualAt: p.CreatedAt, Sources: []ContextSource{}}
	if contextRaw != "" {
		c, e = decodeJSON[ProjectContext](contextRaw)
		if e != nil {
			return c, e
		}
	}
	c.Coverage = map[string]int{"total": len(c.Sources), "read": 0, "unparsed": 0, "failed": 0}
	for _, src := range c.Sources {
		c.Coverage[src.Status]++
	}
	c.Name = p.Name
	c.Description = p.Description
	c.Recent = rollingChanges(c.Recent, now())
	return c, e
}
func (s *Store) ContextCatalog() ([]ProjectContext, error) {
	ps, e := s.Projects()
	if e != nil {
		return nil, e
	}
	out := []ProjectContext{}
	for _, p := range ps {
		if p.Archived {
			continue
		}
		c, e := s.ProjectContext(p.ID)
		if e != nil {
			return nil, e
		}
		c.Sources = nil
		c.Manual = ""
		if c.State == "invalid" || c.Fingerprint == "" && c.Version > 0 && c.Description != c.Manual {
			c.Description = "资料已被删除或纠正，等待重新整理"
			c.Recent = nil
		}
		out = append(out, c)
	}
	return out, nil
}
func (s *Store) contextVersion() string {
	c, _ := s.ContextCatalog()
	m, _ := s.Memories()
	return digest([]byte(encode(c) + encode(m)))
}
func (s *Store) markContext(id string, invalid bool) {
	if id == "" || id == personalProjectID {
		return
	}
	c, e := s.ProjectContext(id)
	if e != nil {
		return
	}
	if invalid {
		c.State = "invalid"
		_ = s.SetSetting("personal_context_reset", "true")
		c.Sources = nil
		c.Fingerprint = ""
	} else if c.State != "invalid" {
		c.State = "pending"
	}
	_, _ = s.DB.Exec("INSERT INTO project_contexts VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET data=excluded.data", id, encode(c))

}

// Hook only durable business events, never model tool/reasoning events.
func (s *Store) contextEvent(session, kind string, data any) {
	if kind == "project.deleted" {
		_ = s.SetSetting("personal_context_reset", "true")
		return
	}
	switch kind {
	case "project.created", "project.updated", "task.updated", "task.deleted", "file.changed", "file.updated", "file.deleted", "files.imported", "project.imported", "files.recorded", "session.recycled", "session.purged", "run.updated":
	default:
		return
	}
	var a struct {
		ID        string `json:"id"`
		ProjectID string `json:"projectId"`
		State     string `json:"state"`
		Path      string `json:"path"`
		After     string `json:"after"`
		Before    string `json:"before"`
	}
	_ = json.Unmarshal([]byte(encode(data)), &a)
	if kind == "run.updated" && !contains([]string{"completed", "failed", "cancelled", "interrupted"}, a.State) {
		return
	}
	id := a.ProjectID
	if kind == "project.created" || kind == "project.updated" {
		id = a.ID
	}
	if id == "" && session != "" {
		ss, e := s.rawSession(session)
		if e == nil {
			id = ss.ProjectID
		}
	}
	if kind == "run.updated" {
		if r, ok := data.(Run); ok && r.ScheduleConfig != nil && r.ScheduleConfig.Output == "project_context" {
			return
		}
	}
	invalid := (kind == "file.changed" && a.Before != "" && a.Before != a.After) || strings.Contains(kind, "deleted") || kind == "session.recycled" || kind == "session.purged"
	if invalid {
		// A raw source may also have been read by the personal session.
		_ = s.SetSetting("personal_context_reset", "true")
		if c, e := s.ProjectContext(id); e == nil && (c.PublishedBy != "" || len(c.Sources) > 0 || c.Description == c.Manual) && c.State != "invalid" {
			relevant := false
			for _, source := range c.Sources {
				switch kind {
				case "file.changed", "file.deleted":
					relevant = source.ID == "file:"+a.Path
				case "task.deleted":
					relevant = source.ID == "task:"+a.ID
				case "session.recycled", "session.purged":
					if strings.HasPrefix(source.ID, "run:") {
						var sid string
						e := s.DB.QueryRow("SELECT session_id FROM runs WHERE id=?", strings.TrimPrefix(source.ID, "run:")).Scan(&sid)
						relevant = sid == a.ID || (kind == "session.purged" && errors.Is(e, sql.ErrNoRows))
					}
				}
				if relevant {
					break
				}
			}
			if !relevant {
				if kind != "task.deleted" {
					return
				}
				invalid = false
			}
		}
	}
	s.markContext(id, invalid)
	if kind == "task.updated" || kind == "task.deleted" {
		var task ProjectTask
		_ = json.Unmarshal([]byte(encode(data)), &task)
		c, e := s.ProjectContext(id)
		if e == nil {
			label := "任务状态变更"
			if kind == "task.deleted" {
				label = "任务已删除"
			}
			c.Recent = append(rollingChanges(c.Recent, now()), ContextChange{Text: label + "：" + task.Title + "（" + task.State + "）", At: now(), Source: "task:" + task.ID})
			_, _ = s.DB.Exec("UPDATE project_contexts SET data=? WHERE project_id=?", encode(c), id)
		}
	}

}
func (s *Store) CorrectContext(id, text string, version int64) (ProjectContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, e := s.ProjectContext(id)
	if e != nil {
		return c, e
	}
	if version != c.Version {
		return c, ErrConflict
	}
	if len(text) > 100000 {
		return c, errors.New("说明超过 100KB")
	}
	p, e := s.Project(id)
	if e != nil {
		return c, e
	}
	previous := c
	c.Description = text
	c.Manual = text
	c.ManualAt = now()
	c.Version++
	c.UpdatedAt = now()
	c.State = "pending"
	p.Description = text
	e = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if _, e := tx.Exec("INSERT OR IGNORE INTO context_versions VALUES(?,?,?)", id, previous.Version, encode(previous)); e != nil {
			return e
		}
		if _, e := tx.Exec("UPDATE projects SET data=? WHERE id=?", encode(p), id); e != nil {
			return e
		}
		if _, e := tx.Exec("INSERT INTO project_contexts VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET data=excluded.data", id, encode(c)); e != nil {
			return e
		}
		return nil
	})
	if e == nil {
		_ = s.SetSetting("personal_context_reset", "true")
	}
	return c, e
}
func jsonPayload(t string) string {
	t = strings.TrimSpace(t)
	if strings.HasPrefix(t, "```") {
		if i := strings.Index(t, "\n"); i >= 0 {
			t = t[i+1:]
		}
		t = strings.TrimSuffix(strings.TrimSpace(t), "```")
	}
	return strings.TrimSpace(t)
}

var errMaintenanceYield = errors.New("后台整理让位于交互任务")

func (w *Worker) compactCall(parent context.Context, purpose, prompt string, rules ...*PersonalRules) (RuntimeResult, error) {
	id := newID()
	p, e := w.Store.PersonalSpace()
	if e != nil {
		return RuntimeResult{}, e
	}
	e = w.Store.Transaction(parent, func(tx *sql.Tx) error {
		var n int
		if e := tx.QueryRow("SELECT (SELECT count(*) FROM workbench_run_leases)+(SELECT count(*) FROM settings WHERE key='tool_install_running')+(SELECT count(*) FROM runs WHERE state='queued')").Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return errMaintenanceYield
		}
		_, e := tx.Exec("INSERT INTO project_locks VALUES(?,?,?)", p.ID, "maintenance:"+id, now())
		return e
	})
	if e != nil {
		return RuntimeResult{}, e
	}
	timeout, stopTimeout := context.WithTimeout(parent, 90*time.Second)
	defer stopTimeout()
	ctx, cancel := context.WithCancelCause(timeout)
	defer cancel(nil)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var queued int
				if er := w.Store.DB.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE state='queued'").Scan(&queued); er == nil && queued > 0 {
					cancel(errMaintenanceYield)
					return
				}
			}
		}
	}()
	in := RuntimeInput{Purpose: purpose, Routing: true, Project: Project{ID: "router"}, Session: Session{ID: id, HarnessID: "maintenance-" + id}, Run: Run{ID: id}, Prompt: prompt}
	if len(rules) > 0 {
		in.PersonalRules = rules[0]
	}
	result, e := w.Executor.Execute(ctx, in, RuntimeHooks{})
	yielded := errors.Is(context.Cause(ctx), errMaintenanceYield)
	cancel(nil)
	<-watchDone
	clean, stop := context.WithTimeout(context.Background(), 20*time.Second)
	ce := w.Executor.Cleanup(clean, id)
	stop()
	if ce != nil {
		return result, ce
	}
	if _, er := w.Store.DB.Exec("DELETE FROM project_locks WHERE run_id=?", "maintenance:"+id); er != nil {
		return result, er
	}
	if yielded {
		_ = w.Store.Emit("", "", "maintenance.yielded", map[string]string{"purpose": purpose})
		return result, errMaintenanceYield
	}
	return result, e
}

func (s *Store) loadContextEvidence(p Project, c ProjectContext, src ContextSource) (string, error) {
	if src.Status != "read" {
		return "", nil
	}
	if src.ID == "manual" {
		if c.Manual != "" {
			return c.Manual, nil
		}
		return p.Description, nil
	}
	if strings.HasPrefix(src.ID, "file:") {
		b, _, e := s.ReadFile(p.ID, strings.TrimPrefix(src.ID, "file:"))
		return string(b), e
	}
	if strings.HasPrefix(src.ID, "run:") {
		r, e := s.Run(strings.TrimPrefix(src.ID, "run:"))
		if e != nil {
			return "", e
		}
		if r.ProjectID != p.ID {
			return "", ErrNotFound
		}
		return fmt.Sprintf("用户表达：%s\n执行状态：%s\n助手答复（建议不等于成果）：%s", r.Text, r.State, r.Result), nil
	}
	if strings.HasPrefix(src.ID, "task:") {
		t, e := s.Task(strings.TrimPrefix(src.ID, "task:"))
		if e != nil {
			return "", e
		}
		if t.ProjectID != p.ID {
			return "", ErrNotFound
		}
		t = taskEvidence(t)
		return encode(t), nil
	}
	return "", ErrNotFound
}
