package workbench

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Deletion removes project data, but retains input digests to stop delayed
// channel retries from executing the same event in a different project.
func (s *Store) DeleteProject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Project(id); err != nil {
		marker, _ := s.Setting("project_delete:" + id)
		if marker != "" {
			return s.finishProjectDeletion(id)
		}
		return err
	}
	err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var busy int
		if err := tx.QueryRow(`SELECT
    (SELECT count(*) FROM project_locks WHERE project_id=?)+
    (SELECT count(*) FROM runs WHERE project_id=? AND state IN ('running','waiting','stopping'))+
    (SELECT count(*) FROM deliveries WHERE state='sending' AND json_extract(data,'$.sessionId') IN (SELECT id FROM sessions WHERE project_id=?))`, id, id, id).Scan(&busy); err != nil {
			return err
		}
		if busy > 0 {
			return ErrLocked
		}
		rows, err := tx.Query(`SELECT data FROM inbox WHERE session_id IN (SELECT id FROM sessions WHERE project_id=?) OR json_extract(data,'$.incoming.sessionId') IN (SELECT id FROM sessions WHERE project_id=?)`, id, id)
		if err != nil {
			return err
		}
		var messages []Message
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			m, e := decodeJSON[Message](raw)
			if e != nil {
				rows.Close()
				return e
			}
			messages = append(messages, m)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT OR REPLACE INTO settings(key,value) SELECT ?,json_group_array(id) FROM sessions WHERE project_id=?", "project_delete_sessions:"+id, id); err != nil {
			return err
		}
		statements := []string{
			"DELETE FROM gateway_crm_routes WHERE json_extract(grant,'$[1]') IN (SELECT id FROM gateway_bindings WHERE json_extract(data,'$.projectId')=?)",
			"DELETE FROM gateway_crm_receipts WHERE session_id IN (SELECT id FROM sessions WHERE project_id=?)",
			"DELETE FROM deliveries WHERE json_extract(data,'$.sessionId') IN (SELECT id FROM sessions WHERE project_id=?)",
			"DELETE FROM schedule_occurrences WHERE schedule_id IN (SELECT id FROM schedules WHERE project_id=?)",
			"DELETE FROM schedules WHERE project_id=?",
			"DELETE FROM interactions WHERE run_id IN (SELECT id FROM runs WHERE project_id=?)",
			"DELETE FROM baselines WHERE run_id IN (SELECT id FROM runs WHERE project_id=?)",
			"DELETE FROM events WHERE session_id IN (SELECT id FROM sessions WHERE project_id=?)",
			"DELETE FROM events WHERE json_extract(data,'$.projectId')=? OR json_extract(data,'$.id')=?",
			"DELETE FROM runs WHERE project_id=?",
			"DELETE FROM revisions WHERE project_id=?",
			"DELETE FROM document_search WHERE project_id=?",
			"DELETE FROM documents WHERE project_id=?",
			"DELETE FROM entry_focus WHERE session_id IN (SELECT id FROM sessions WHERE project_id=?)",
			"DELETE FROM harness_events WHERE session_id IN (SELECT id FROM sessions WHERE project_id=?)",
			"DELETE FROM runtime_sessions WHERE session_id IN (SELECT id FROM sessions WHERE project_id=?)",
			"DELETE FROM sessions WHERE project_id=?",
			"DELETE FROM gateway_bindings WHERE json_extract(data,'$.projectId')=? AND NOT EXISTS(SELECT 1 FROM gateway_sessions WHERE binding_id=gateway_bindings.id)",
			// A legacy session in another project can still refer to the binding. Retain
			// its audit ID but release the live account slot for a fresh ownership epoch.
			"UPDATE gateway_bindings SET account_id='retired:'||id,data=json_set(data,'$.enabled',json('false'),'$.projectId','','$.version',json_extract(data,'$.version')+1) WHERE json_extract(data,'$.projectId')=?",
			"DELETE FROM projects WHERE id=?",
		}
		for _, q := range statements {
			args := []any{id}
			if strings.Count(q, "?") == 2 {
				args = append(args, id)
			}
			if _, err = tx.Exec(q, args...); err != nil {
				return err
			}
		}
		for _, m := range messages {
			tombstone := Message{ID: m.ID, State: "deleted", IncomingHash: digest([]byte(encode(m.Incoming))), CreatedAt: m.CreatedAt}
			if _, err = tx.Exec("UPDATE inbox SET state='deleted',session_id='',data=? WHERE id=?", encode(tombstone), m.ID); err != nil {
				return err
			}
			if _, err = tx.Exec("DELETE FROM events WHERE json_extract(data,'$.id')=? OR json_extract(data,'$.messageId')=?", m.ID, m.ID); err != nil {
				return err
			}
		}
		_, err = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "project_delete:"+id, id)
		return err
	})
	if err != nil {
		return err
	}
	if err = s.finishProjectDeletion(id); err != nil {
		return err
	}
	return s.Emit("", "", "project.deleted", map[string]string{"id": id})
}
func (s *Store) finishProjectDeletion(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		return errors.New("项目删除标识无效")
	}
	if raw, e := s.Setting("project_delete_sessions:" + id); e != nil {
		return e
	} else if raw != "" {
		ids, e := decodeJSON[[]string](raw)
		if e != nil {
			return e
		}
		for _, sid := range ids {
			if !toolIdentifier.MatchString(sid) {
				return errors.New("回收记录标识无效")
			}
			if e = os.RemoveAll(filepath.Join(s.Root, "trash", sid)); e != nil {
				return e
			}
		}
	}
	for _, dir := range []string{filepath.Join(s.Root, "projects", id), filepath.Join(s.Root, "runtime", "projects", id)} {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	_, err := s.DB.Exec("DELETE FROM settings WHERE key IN (?,?)", "project_delete:"+id, "project_delete_sessions:"+id)
	return err
}
func (s *Store) RecoverProjectDeletions() error {
	rows, err := s.DB.Query("SELECT value FROM settings WHERE key LIKE 'project_delete:%'")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.finishProjectDeletion(id); err != nil {
			return err
		}
	}
	return nil
}
