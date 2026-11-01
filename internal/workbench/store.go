package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

type Store struct {
	BackupScrubCommand []string
	DB                 *sql.DB
	Root               string
	mu                 sync.Mutex
	expertMu           sync.RWMutex
	browsers           sync.Map // run ID -> browserEndpoint, only while its container is alive
	notify             chan struct{}
	toolManager        func(string, string, []string) (ToolJob, error)
}

const schema = `
CREATE TABLE IF NOT EXISTS schema_version(version INTEGER NOT NULL);
INSERT INTO schema_version SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM schema_version);
CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY, name TEXT NOT NULL, archived INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), harness_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, updated_at INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TRIGGER IF NOT EXISTS immutable_session_project BEFORE UPDATE OF project_id,harness_id ON sessions
WHEN NEW.project_id <> OLD.project_id OR NEW.harness_id <> OLD.harness_id BEGIN SELECT RAISE(ABORT,'session identity is immutable'); END;
CREATE TABLE IF NOT EXISTS experts(id TEXT PRIMARY KEY, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS inbox(id TEXT PRIMARY KEY, source TEXT NOT NULL, connection_id TEXT NOT NULL, event_id TEXT NOT NULL, entry_key TEXT NOT NULL, state TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, data TEXT NOT NULL, UNIQUE(source,connection_id,event_id));
CREATE TABLE IF NOT EXISTS entry_focus(entry_key TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS runs(id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), session_id TEXT NOT NULL REFERENCES sessions(id), message_id TEXT NOT NULL UNIQUE REFERENCES inbox(id), state TEXT NOT NULL, priority INTEGER NOT NULL, created_at INTEGER NOT NULL, data TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS run_queue ON runs(state,priority,created_at);
CREATE TABLE IF NOT EXISTS project_locks(project_id TEXT PRIMARY KEY REFERENCES projects(id), run_id TEXT NOT NULL UNIQUE, acquired_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS events(seq INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL DEFAULT '', run_id TEXT NOT NULL DEFAULT '', type TEXT NOT NULL, data TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS session_events ON events(session_id,seq);
CREATE TABLE IF NOT EXISTS harness_events(session_id TEXT NOT NULL REFERENCES sessions(id), native_seq INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(session_id,native_seq));
CREATE TABLE IF NOT EXISTS runtime_sessions(session_id TEXT PRIMARY KEY REFERENCES sessions(id), state TEXT NOT NULL, last_message_id TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS interactions(id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES runs(id), state TEXT NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS revisions(id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), run_id TEXT NOT NULL, path TEXT NOT NULL, before_hash TEXT NOT NULL, after_hash TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS baselines(run_id TEXT PRIMARY KEY REFERENCES runs(id), data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS connections(id TEXT PRIMARY KEY, kind TEXT NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS connection_secrets(id TEXT PRIMARY KEY REFERENCES connections(id), data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS legacy_consumed(hash TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS deliveries(id TEXT PRIMARY KEY, dedupe_key TEXT NOT NULL UNIQUE, connection_id TEXT NOT NULL REFERENCES connections(id), state TEXT NOT NULL, created_at INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS schedules(id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), enabled INTEGER NOT NULL, next_at INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS schedule_occurrences(schedule_id TEXT NOT NULL, scheduled_at INTEGER NOT NULL, message_id TEXT NOT NULL, PRIMARY KEY(schedule_id,scheduled_at));
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS logins(hash TEXT PRIMARY KEY, expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS tickets(hash TEXT PRIMARY KEY, expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS cli_tools(id TEXT PRIMARY KEY,data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS tool_jobs(id TEXT PRIMARY KEY,tool_id TEXT NOT NULL,state TEXT NOT NULL,created_at INTEGER NOT NULL,data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS plugins(id TEXT PRIMARY KEY, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS documents(project_id TEXT NOT NULL, path TEXT NOT NULL, hash TEXT NOT NULL, content TEXT NOT NULL, PRIMARY KEY(project_id,path));
CREATE VIRTUAL TABLE IF NOT EXISTS document_search USING fts5(project_id UNINDEXED,path,content,tokenize='trigram');
`

func OpenStore(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(root, "workbench.sqlite")+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=secure_delete(1)&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s, err := NewStore(db, root)
	if err != nil {
		db.Close()
	}
	return s, err
}
func NewStore(db *sql.DB, root string) (*Store, error) {
	for _, name := range []string{"projects", "experts", "blobs", "runtime", "uploads", "secrets"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			return nil, err
		}
	}
	if _, err := db.Exec(schema + todaySchema); err != nil {
		return nil, fmt.Errorf("数据库初始化: %w", err)
	}
	var version int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil {
		return nil, err
	}
	if version != 1 {
		return nil, fmt.Errorf("不支持数据库版本 %d", version)
	}
	s := &Store{DB: db, Root: root, notify: make(chan struct{}, 1)}
	if v, _ := s.Setting("global_mcp_migrated"); v == "" {
		plugins, _ := listJSON[Plugin](db, "SELECT data FROM plugins")
		projects, _ := s.Projects()
		for _, p := range plugins {
			if p.Enabled == nil {
				enabled := false
				for _, project := range projects {
					if contains(project.PluginIDs, p.ID) {
						enabled = true
					}
				}
				p.Enabled = &enabled
				_, _ = db.Exec("UPDATE plugins SET data=? WHERE id=?", encode(p), p.ID)
			}
		}
		if e := s.SetSetting("global_mcp_migrated", "1"); e != nil {
			return nil, e
		}
	}
	tools, err := s.CLITools()
	if err != nil {
		return nil, err
	}
	for _, tool := range tools {
		if tool.Installed {
			if _, err = s.SaveCLITool(tool); err != nil {
				return nil, err
			}
		}
	}
	if _, err = db.Exec("UPDATE tool_jobs SET state='interrupted',data=json_set(data,'$.state','interrupted','$.error','服务重启，操作未重放，请检查工具状态') WHERE state IN ('queued','running')"); err != nil {
		return nil, err
	}
	if _, err = db.Exec("DELETE FROM settings WHERE key='tool_install_running'"); err != nil {
		return nil, err
	}
	return s, nil
}
func getJSON[T any](db *sql.DB, q string, args ...any) (T, error) {
	var out T
	var raw string
	err := db.QueryRow(q, args...).Scan(&raw)
	if err == sql.ErrNoRows {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}
func decodeJSON[T any](raw string) (T, error) {
	var v T
	err := json.Unmarshal([]byte(raw), &v)
	return v, err
}
func listJSON[T any](db *sql.DB, q string, args ...any) ([]T, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		var v T
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Emit(session, run, kind string, data any) error {
	s.contextEvent(session, kind, data)
	_, err := s.DB.Exec("INSERT INTO events(session_id,run_id,type,data,created_at) VALUES(?,?,?,?,?)", session, run, kind, encode(data), now())
	if err == nil {
		select {
		case s.notify <- struct{}{}:
		default:
		}
	}
	return err
}
func (s *Store) Events(after int64, session string) ([]Event, error) {
	rows, err := s.DB.Query("SELECT seq,session_id,run_id,type,data,created_at FROM events WHERE seq>? AND NOT(type='run.updated' AND coalesce(json_extract(data,'$.kind'),'')='archive') AND (session_id='' OR session_id IN (SELECT id FROM sessions WHERE coalesce(json_extract(data,'$.recycledAt'),0)=0)) AND NOT EXISTS(SELECT 1 FROM inbox i JOIN sessions ss ON ss.id=coalesce(nullif(i.session_id,''),json_extract(i.data,'$.incoming.sessionId')) WHERE coalesce(json_extract(ss.data,'$.recycledAt'),0)>0 AND i.id=coalesce(json_extract(events.data,'$.id'),json_extract(events.data,'$.messageId'))) AND (?='' OR session_id=?) ORDER BY seq LIMIT 500", after, session, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var raw string
		if err = rows.Scan(&e.Seq, &e.SessionID, &e.RunID, &e.Type, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(raw)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.DB.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}
func (s *Store) SetSetting(key, value string) error {
	_, err := s.DB.Exec("INSERT INTO settings VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}
func (s *Store) CheckUnlocked(project string) error {
	var n int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM project_locks WHERE project_id=?", project).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrLocked
	}
	return nil
}
func (s *Store) Transaction(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = f(tx); err != nil {
		return err
	}
	return tx.Commit()
}
