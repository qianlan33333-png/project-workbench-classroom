package workbench

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Store) Tasks(project string) ([]ProjectTask, error) {
	if project == "" {
		return listJSON[ProjectTask](s.DB, "SELECT t.data FROM project_tasks t JOIN projects p ON p.id=t.project_id WHERE p.archived=0 ORDER BY json_extract(t.data,'$.updatedAt') DESC")
	}
	return listJSON[ProjectTask](s.DB, "SELECT data FROM project_tasks WHERE project_id=? ORDER BY json_extract(data,'$.updatedAt') DESC", project)
}
func (s *Store) Task(id string) (ProjectTask, error) {
	return getJSON[ProjectTask](s.DB, "SELECT data FROM project_tasks WHERE id=?", id)
}
func validateTask(t ProjectTask) error {
	if strings.TrimSpace(t.Title) == "" || len([]rune(t.Title)) > 200 || len(t.Description) > 100000 {
		return errors.New("任务需要 1–200 字标题，说明不超过 100KB")
	}
	if !contains([]string{"pending", "in_progress", "completed", "cancelled"}, t.State) {
		return errors.New("任务状态无效")
	}
	for _, d := range []string{t.ScheduledDate, t.DueDate} {
		if d != "" {
			if _, e := time.Parse("2006-01-02", d); e != nil {
				return errors.New("日期应为 YYYY-MM-DD")
			}
		}
	}
	return nil
}
func (s *Store) SaveTask(t ProjectTask) (ProjectTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.Project(t.ProjectID)
	if e != nil {
		return t, e
	}
	if p.Archived || p.ID == personalProjectID {
		return t, errors.New("请选择有效业务项目")
	}
	if t.ID == "" {
		if t.Operation == "" {
			return t, errors.New("需要 operation 幂等标识")
		}
		if old, e := getJSON[ProjectTask](s.DB, "SELECT data FROM project_tasks WHERE operation=?", t.Operation); e == nil {
			return old, nil
		}
		t.ID = newID()
		t.Version = 1
		if t.State == "" {
			t.State = "pending"
		}
	} else {
		old, e := s.Task(t.ID)
		if e != nil {
			return t, e
		}
		if old.ProjectID != t.ProjectID || old.Version != t.Version {
			return t, ErrConflict
		}
		t.Operation = old.Operation
		t.SessionID = old.SessionID
		t.DraftVersion = old.DraftVersion
		t.DraftText = old.DraftText
		t.Source = old.Source
		t.Version++
	}
	if e = validateTask(t); e != nil {
		return t, e
	}
	t.UpdatedAt = now()
	_, e = s.DB.Exec("INSERT INTO project_tasks VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", t.ID, t.ProjectID, t.Operation, encode(t))
	if e == nil {
		e = s.Emit("", "", "task.updated", t)
	}
	return t, e
}
func (s *Store) DeleteTask(id string, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.Task(id)
	if e != nil {
		return e
	}
	if t.Version != version {
		return ErrConflict
	}
	_, e = s.DB.Exec("DELETE FROM project_tasks WHERE id=?", id)
	if e == nil {
		e = s.Emit("", "", "task.deleted", t)
	}
	return e
}

type TaskDraft struct {
	Task    ProjectTask `json:"task"`
	Session Session     `json:"session"`
	Text    string      `json:"text"`
	Changed bool        `json:"changed"`
}

func (s *Store) PrepareTask(id string, another, refill bool) (TaskDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.Task(id)
	if e != nil {
		return TaskDraft{}, e
	}
	if t.State == "completed" || t.State == "cancelled" {
		return TaskDraft{}, errors.New("请先恢复任务状态")
	}
	ss, e := s.Session(t.SessionID)
	if e != nil || another {
		ss, e = s.CreateSession(t.ProjectID, "", "topic", "general")
		if e != nil {
			return TaskDraft{}, e
		}
		t.SessionID = ss.ID
		t.DraftVersion = 0
	}
	if t.DraftVersion == 0 || refill && ss.Draft {
		t.DraftText = "# " + t.Title + "\n\n" + t.Description
		t.DraftVersion = t.Version
	}
	_, e = s.DB.Exec("UPDATE project_tasks SET data=? WHERE id=?", encode(t), t.ID)
	return TaskDraft{t, ss, t.DraftText, t.DraftVersion != t.Version}, e
}

// This update shares the inbox/run transaction: opening a draft is not starting work.
func startTaskForSession(tx *sql.Tx, session string) error {
	rows, e := tx.Query("SELECT id,data FROM project_tasks WHERE json_extract(data,'$.sessionId')=?", session)
	if e != nil {
		return e
	}
	var ts []ProjectTask
	for rows.Next() {
		var id, raw string
		if e = rows.Scan(&id, &raw); e != nil {
			rows.Close()
			return e
		}
		t, e := decodeJSON[ProjectTask](raw)
		if e != nil {
			rows.Close()
			return e
		}
		ts = append(ts, t)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	for _, t := range ts {
		if t.State == "pending" {
			t.State = "in_progress"
			t.Version++
			t.UpdatedAt = now()
			if _, e = tx.Exec("UPDATE project_tasks SET data=? WHERE id=?", encode(t), t.ID); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Store) SaveTaskDraft(id, text string, version int64) error {
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRow("SELECT data FROM project_tasks WHERE id=?", id).Scan(&raw); e != nil {
			return e
		}
		t, e := decodeJSON[ProjectTask](raw)
		if e != nil {
			return e
		}
		if t.DraftVersion != version {
			return ErrConflict
		}
		t.DraftText = text
		_, e = tx.Exec("UPDATE project_tasks SET data=? WHERE id=?", encode(t), id)
		return e
	})
}
