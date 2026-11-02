package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const recycleDuration = int64(15 * 24 * time.Hour / time.Millisecond)

func (s *Store) rawSession(id string) (Session, error) {
	return getJSON[Session](s.DB, "SELECT data FROM sessions WHERE id=?", id)
}
func (s *Store) saveSession(ss Session) error {
	e := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var updated int64
		if e := tx.QueryRow("SELECT updated_at FROM sessions WHERE id=?", ss.ID).Scan(&updated); e != nil {
			return e
		}
		ss.UpdatedAt = max(now(), updated+1)
		_, e := tx.Exec("UPDATE sessions SET data=?,updated_at=?,kind=? WHERE id=?", encode(ss), ss.UpdatedAt, ss.Kind, ss.ID)
		return e
	})
	if e != nil {
		return e
	}
	return s.Emit(ss.ID, "", "session.updated", ss)
}
func (s *Store) PinSession(id string, pinned bool) (Session, error) {
	var ss Session
	e := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRow("SELECT data FROM sessions WHERE id=?", id).Scan(&raw); e != nil {
			return e
		}
		var e error
		ss, e = decodeJSON[Session](raw)
		if e != nil {
			return e
		}
		if ss.RecycledAt > 0 || ss.Lifecycle != "" {
			return ErrLocked
		}
		ss.PinnedAt = 0
		if pinned {
			ss.PinnedAt = now()
		}
		ss.UpdatedAt = max(now(), ss.UpdatedAt+1)
		_, e = tx.Exec("UPDATE sessions SET data=?,updated_at=? WHERE id=?", encode(ss), ss.UpdatedAt, id)
		return e
	})
	if e != nil {
		return ss, e
	}
	return ss, s.Emit(ss.ID, "", "session.updated", ss)
}
func (s *Store) RecycleBin() ([]Session, error) {
	return listJSON[Session](s.DB, "SELECT data FROM sessions WHERE coalesce(json_extract(data,'$.recycledAt'),0)>0 ORDER BY json_extract(data,'$.recycledAt') DESC")
}
func sessionIdle(tx *sql.Tx, id string) error {
	var n int
	e := tx.QueryRow(`SELECT
 (SELECT count(*) FROM runs WHERE session_id=? AND state IN ('queued','running','waiting','stopping'))+
 (SELECT count(*) FROM inbox WHERE (session_id=? OR json_extract(data,'$.incoming.sessionId')=?) AND state IN ('unrouted','clarify'))+
 (SELECT count(*) FROM deliveries WHERE json_extract(data,'$.sessionId')=? AND state='sending')`, id, id, id, id).Scan(&n)
	if e != nil {
		return e
	}
	if n > 0 {
		return errors.New("对话还有未处理消息或活动任务，请先完成或停止")
	}
	return nil
}
func (s *Store) QueueArchive(id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, e := s.Session(id)
	if e != nil {
		return ss, e
	}
	if ss.Lifecycle == "archiving" {
		return ss, nil
	}
	e = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if e := sessionIdle(tx, id); e != nil {
			return e
		}
		ss.Lifecycle = "archiving"
		ss.LifecycleError = ""
		ss.UpdatedAt = max(now(), ss.UpdatedAt+1)
		m := Message{ID: newID(), SessionID: id, State: "queued", CreatedAt: now(), Incoming: Incoming{Source: "web", ConnectionID: "maintenance", EventID: newID(), SessionID: id, Text: "归档对话"}}
		r := Run{ID: newID(), ProjectID: ss.ProjectID, SessionID: id, MessageID: m.ID, Kind: "archive", Source: "web", Text: "归档对话", State: "queued", CreatedAt: now()}
		m.RunID = r.ID
		ss.ArchivePath = "对话归档/" + time.Now().In(shanghai()).Format("2006-01-02") + "-" + safeArchiveTitle(ss.Title) + "-" + r.ID[:8] + ".md"
		if _, e := tx.Exec("INSERT INTO inbox VALUES(?,?,?,?,?,?,?,?,?)", m.ID, "web", "maintenance", m.Incoming.EventID, "maintenance", m.State, id, m.CreatedAt, encode(m)); e != nil {
			return e
		}
		if _, e := tx.Exec("INSERT INTO runs VALUES(?,?,?,?,?,?,?,?)", r.ID, ss.ProjectID, id, m.ID, "queued", 0, r.CreatedAt, encode(r)); e != nil {
			return e
		}
		_, e := tx.Exec("UPDATE sessions SET data=?,updated_at=? WHERE id=?", encode(ss), ss.UpdatedAt, id)
		return e
	})
	if e != nil {
		return ss, e
	}
	return ss, s.Emit(id, "", "session.updated", ss)
}
func (s *Store) RecycleSession(id, reason string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recycleSession(id, reason, "")
}
func (s *Store) recycleSession(id, reason, archiveRun string) (Session, error) {
	ss, e := s.rawSession(id)
	if e != nil {
		return ss, e
	}
	if ss.RecycledAt > 0 {
		return ss, s.moveSessionFiles(ss, false)
	}
	e = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if archiveRun == "" {
			if e := sessionIdle(tx, id); e != nil {
				return e
			}
		}
		ss.RecycledAt = now()
		ss.ExpiresAt = ss.RecycledAt + recycleDuration
		ss.RecycleReason = reason
		ss.Lifecycle = "moving"
		ss.UpdatedAt = max(now(), ss.UpdatedAt+1)
		if _, e := tx.Exec("UPDATE sessions SET data=?,updated_at=? WHERE id=?", encode(ss), ss.UpdatedAt, id); e != nil {
			return e
		}
		if _, e := tx.Exec("UPDATE inbox SET state='cancelled',session_id=?,data=json_set(data,'$.state','cancelled','$.sessionId',?) WHERE (session_id=? OR json_extract(data,'$.incoming.sessionId')=?) AND state='error'", id, id, id, id); e != nil {
			return e
		}

		if _, e := tx.Exec("UPDATE deliveries SET state='cancelled',data=json_set(data,'$.state','cancelled') WHERE json_extract(data,'$.sessionId')=? AND state IN ('pending','unknown')", id); e != nil {
			return e
		}
		var raw string
		if e := tx.QueryRow("SELECT data FROM projects WHERE id=?", ss.ProjectID).Scan(&raw); e != nil {
			return e
		}
		p, e := decodeJSON[Project](raw)
		if e != nil {
			return e
		}
		if p.DefaultSessionID == id {
			fresh := Session{ID: newID(), ProjectID: p.ID, Title: "项目助手", Kind: "assistant", CreatedAt: now(), UpdatedAt: now()}
			fresh.HarnessID = "pw-" + fresh.ID
			if _, e = tx.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?)", fresh.ID, p.ID, fresh.HarnessID, fresh.Kind, fresh.UpdatedAt, encode(fresh)); e != nil {
				return e
			}
			p.DefaultSessionID = fresh.ID
			if _, e = tx.Exec("UPDATE projects SET data=? WHERE id=?", encode(p), p.ID); e != nil {
				return e
			}
		}
		if _, e = tx.Exec("UPDATE entry_focus SET session_id=?,updated_at=? WHERE session_id=?", p.DefaultSessionID, now(), id); e != nil {
			return e
		}
		return nil
	})
	if e != nil {
		return ss, e
	}
	if e = s.moveSessionFiles(ss, false); e != nil {
		return ss, e
	}
	ss.Lifecycle = ""
	if e = s.saveSession(ss); e != nil {
		return ss, e
	}
	return ss, s.Emit("", "", "session.recycled", map[string]string{"id": ss.ID, "projectId": ss.ProjectID})
}

// Move each native generation directory out of every runtime mount. A journal
// makes both directions recoverable without guessing which files were moved.
type sessionMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Store) moveSessionFiles(ss Session, restore bool) error {
	base := filepath.Join(s.Root, "trash", ss.ID)
	if e := os.MkdirAll(base, 0700); e != nil {
		return e
	}
	manifest := filepath.Join(base, "moves.json")
	moves := []sessionMove{}
	if b, e := os.ReadFile(manifest); e == nil {
		var err error
		moves, err = decodeJSON[[]sessionMove](string(b))
		if err != nil {
			return err
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if !restore {
		root := filepath.Join(s.Root, "runtime", "projects", ss.ProjectID, "sessions")
		e := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
			if errors.Is(e, os.ErrNotExist) {
				return nil
			}
			if e != nil {
				return e
			}
			if d.IsDir() && d.Name() == ss.HarnessID {
				found := false
				for _, m := range moves {
					if m.From == path {
						found = true
					}
				}
				if !found {
					moves = append(moves, sessionMove{path, filepath.Join(base, fmt.Sprint(len(moves)))})
				}
				return filepath.SkipDir
			}
			return nil
		})
		if e != nil {
			return e
		}
		if e = os.WriteFile(manifest, []byte(encode(moves)), 0600); e != nil {
			return e
		}
	}
	for _, m := range moves {
		from, to := m.From, m.To
		if restore {
			from, to = to, from
		}
		if !strings.HasPrefix(m.From, filepath.Join(s.Root, "runtime", "projects", ss.ProjectID)+string(os.PathSeparator)) || !strings.HasPrefix(m.To, base+string(os.PathSeparator)) {
			return errors.New("会话文件清单路径无效")
		}
		if _, e := os.Stat(from); errors.Is(e, os.ErrNotExist) {
			continue
		} else if e != nil {
			return e
		}
		if _, e := os.Stat(to); e == nil {
			return errors.New("会话文件目标已存在，停止覆盖")
		}
		if e := os.MkdirAll(filepath.Dir(to), 0700); e != nil {
			return e
		}
		if e := os.Rename(from, to); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) RestoreSession(id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, e := s.rawSession(id)
	if e != nil {
		return ss, e
	}
	if ss.RecycledAt == 0 {
		return ss, nil
	}
	if ss.ExpiresAt <= now() {
		return ss, errors.New("对话已超过 15 天保留期")
	}
	if e = s.CheckUnlocked(ss.ProjectID); e != nil {
		return ss, e
	}
	ss.Lifecycle = "restoring"
	if e = s.saveSession(ss); e != nil {
		return ss, e
	}
	if e = s.moveSessionFiles(ss, true); e != nil {
		return ss, e
	}
	return s.finishSessionRestore(ss)
}
func (s *Store) finishSessionRestore(ss Session) (Session, error) {
	if ss.Kind == "assistant" {
		ss.Kind = "topic"
		ss.Title = "项目助手（找回）"
		ss.TitleSource = "manual"
	}
	ss.RecycledAt = 0
	ss.ExpiresAt = 0
	ss.RecycleReason = ""
	ss.Lifecycle = ""
	ss.LifecycleError = ""
	e := s.saveSession(ss)
	if e == nil {
		e = os.RemoveAll(filepath.Join(s.Root, "trash", ss.ID))
	}
	return ss, e
}
func (s *Store) RecoverSessionLifecycle() error {
	sessions, e := listJSON[Session](s.DB, "SELECT data FROM sessions WHERE coalesce(json_extract(data,'$.lifecycle'),'')<>''")
	if e != nil {
		return e
	}
	for _, ss := range sessions {
		switch ss.Lifecycle {
		case "moving":
			if _, e = s.recycleSession(ss.ID, ss.RecycleReason, ""); e != nil {
				return e
			}
			ss.Lifecycle = ""
			if e = s.saveSession(ss); e != nil {
				return e
			}
		case "restoring":
			if e = s.moveSessionFiles(ss, true); e != nil {
				return e
			}
			if _, e = s.finishSessionRestore(ss); e != nil {
				return e
			}
		case "purging":
			if e = s.purgeSession(ss); e != nil {
				return e
			}
		case "archiving":
			var n int
			s.DB.QueryRow("SELECT count(*) FROM runs WHERE session_id=? AND state IN ('queued','running','waiting','stopping') AND json_extract(data,'$.kind')='archive'", ss.ID).Scan(&n)
			if n == 0 {
				ss.Lifecycle = ""
				ss.LifecycleError = "归档被重启中断，原对话已保留，请重试"
				if e = s.saveSession(ss); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func (s *Store) PurgeExpiredSessions() error {
	sessions, e := listJSON[Session](s.DB, "SELECT data FROM sessions WHERE coalesce(json_extract(data,'$.recycledAt'),0)>0 AND json_extract(data,'$.expiresAt')<=?", now())
	if e != nil {
		return e
	}
	for _, ss := range sessions {
		if e = s.PurgeSession(ss.ID); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) PurgeSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, e := s.rawSession(id)
	if e != nil {
		return e
	}
	if ss.RecycledAt == 0 {
		return errors.New("请先移入回收站")
	}
	ss.Lifecycle = "purging"
	if e = s.saveSession(ss); e != nil {
		return e
	}
	return s.purgeSession(ss)
}
func (s *Store) purgeSession(ss Session) error {
	// The durable marker contains no conversation content. Backup scrubbing and
	// restore tooling use it to prevent resurrection from an older snapshot.
	marker := filepath.Join(s.Root, "purged-sessions", ss.ID+".json")
	if e := os.MkdirAll(filepath.Dir(marker), 0700); e != nil {
		return e
	}
	hashes := []string{}
	fingerprintRows, e := s.DB.Query("SELECT source,event_id FROM inbox WHERE session_id=? OR json_extract(data,'$.incoming.sessionId')=?", ss.ID, ss.ID)
	if e != nil {
		return e
	}
	for fingerprintRows.Next() {
		var source, event string
		if e = fingerprintRows.Scan(&source, &event); e != nil {
			fingerprintRows.Close()
			return e
		}
		hashes = append(hashes, digest([]byte(source+":"+event)))
	}
	e = fingerprintRows.Err()
	fingerprintRows.Close()
	if e != nil {
		return e
	}
	if e := os.WriteFile(marker, []byte(encode(map[string]any{"id": ss.ID, "projectId": ss.ProjectID, "harnessId": ss.HarnessID, "purgedAt": now(), "messageHashes": hashes})), 0600); e != nil {
		return e
	}
	if len(s.BackupScrubCommand) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		cmd := exec.CommandContext(ctx, s.BackupScrubCommand[0], s.BackupScrubCommand[1:]...)
		_, e := cmd.CombinedOutput()
		cancel()
		if e != nil {
			return fmt.Errorf("受管快照清理尚未完成，原始数据仍被隔离：%w", e)
		}
	}
	if e := s.moveSessionFiles(ss, false); e != nil {
		return e
	}
	runs, _ := listJSON[Run](s.DB, "SELECT data FROM runs WHERE session_id=?", ss.ID)
	for _, r := range runs {
		if e := os.Remove(filepath.Join(s.Root, "runtime", "projects", ss.ProjectID, "invocation-"+r.ID+".yml")); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	e = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		rows, e := tx.Query("SELECT data FROM inbox WHERE session_id=? OR json_extract(data,'$.incoming.sessionId')=?", ss.ID, ss.ID)
		if e != nil {
			return e
		}
		var messages []Message
		for rows.Next() {
			var raw string
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return e
			}
			m, e := decodeJSON[Message](raw)
			if e != nil {
				rows.Close()
				return e
			}
			messages = append(messages, m)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, m := range messages {
			if _, e = tx.Exec("INSERT OR IGNORE INTO legacy_consumed(hash) VALUES(?)", digest([]byte(m.Incoming.Source+":"+m.Incoming.EventID))); e != nil {
				return e
			}
			if _, e = tx.Exec("DELETE FROM events WHERE json_extract(data,'$.id')=? OR json_extract(data,'$.messageId')=? OR json_extract(data,'$.eventId')=?", m.ID, m.ID, m.Incoming.EventID); e != nil {
				return e
			}
		}
		for _, q := range []string{
			"DELETE FROM gateway_crm_receipts WHERE session_id=?",
			"DELETE FROM deliveries WHERE json_extract(data,'$.sessionId')=?",
			"DELETE FROM interactions WHERE run_id IN (SELECT id FROM runs WHERE session_id=?)",
			"DELETE FROM baselines WHERE run_id IN (SELECT id FROM runs WHERE session_id=?)",
			"UPDATE revisions SET run_id='' WHERE run_id IN (SELECT id FROM runs WHERE session_id=?)",
			"DELETE FROM events WHERE session_id=?", "DELETE FROM entry_focus WHERE session_id=?",
			"DELETE FROM harness_events WHERE session_id=?", "DELETE FROM runtime_sessions WHERE session_id=?",
			"DELETE FROM runs WHERE session_id=?",
			"DELETE FROM inbox WHERE session_id=? OR json_extract(data,'$.incoming.sessionId')=?",
			"DELETE FROM sessions WHERE id=?",
		} {
			args := []any{ss.ID}
			if strings.Count(q, "?") == 2 {
				args = append(args, ss.ID)
			}
			if _, e = tx.Exec(q, args...); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	if e = os.RemoveAll(filepath.Join(s.Root, "trash", ss.ID)); e != nil {
		return e
	}
	return s.Emit("", "", "session.purged", map[string]string{"id": ss.ID, "projectId": ss.ProjectID})
}

func (s *Store) ApplyPurgeLedger() error {
	if len(s.BackupScrubCommand) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		_, e := exec.CommandContext(ctx, s.BackupScrubCommand[0], s.BackupScrubCommand[1:]...).CombinedOutput()
		cancel()
		if e != nil {
			return e
		}
	}
	files, e := filepath.Glob(filepath.Join(s.Root, "purged-sessions", "*.json"))
	if e != nil {
		return e
	}
	for _, p := range files {
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		var marker struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(b, &marker); err != nil {
			return err
		}
		if ss, e := s.rawSession(marker.ID); e == nil {
			if e = s.purgeSession(ss); e != nil {
				return e
			}
		}
	}
	// An older snapshot may have pointed its default assistant at an original
	// that the independent purge ledger has removed. Restore a fresh assistant,
	// never a replacement history under the deleted identity.
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		rows, err := tx.Query("SELECT data FROM projects WHERE NOT EXISTS (SELECT 1 FROM sessions WHERE sessions.id=json_extract(projects.data,'$.defaultSessionId'))")
		if err != nil {
			return err
		}
		var projects []Project
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			p, e := decodeJSON[Project](raw)
			if e != nil {
				rows.Close()
				return e
			}
			projects = append(projects, p)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, p := range projects {
			ss := Session{ID: newID(), ProjectID: p.ID, Title: "项目助手", Kind: "assistant", CreatedAt: now(), UpdatedAt: now()}
			ss.HarnessID = "pw-" + ss.ID
			if _, err = tx.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?)", ss.ID, p.ID, ss.HarnessID, ss.Kind, ss.UpdatedAt, encode(ss)); err != nil {
				return err
			}
			p.DefaultSessionID = ss.ID
			if _, err = tx.Exec("UPDATE projects SET data=? WHERE id=?", encode(p), p.ID); err != nil {
				return err
			}
		}
		return nil
	})
}
