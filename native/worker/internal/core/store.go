package core

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

//go:embed schema.sql
var schema string

type Store struct{ DB *sql.DB }

func Open(ctx context.Context, dsn string) (*Store, error) {
	c, e := mysql.ParseDSN(dsn)
	if e != nil {
		return nil, e
	}
	c.ParseTime = true
	c.Loc = time.UTC
	if c.Params == nil {
		c.Params = map[string]string{}
	}
	c.Params["time_zone"] = "'+00:00'"
	c.MultiStatements = false
	c.Timeout = 5 * time.Second
	c.ReadTimeout = 15 * time.Second
	c.WriteTimeout = 15 * time.Second
	db, e := sql.Open("mysql", c.FormatDSN())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(5 * time.Minute)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	return &Store{DB: db}, nil
}
func (s *Store) Migrate(ctx context.Context) error {
	// DDL may be reached by the API/Worker during the same restart. MySQL's
	// connection-scoped lock keeps additive migrations idempotent across them.
	lock, e := s.DB.Conn(ctx)
	if e != nil {
		return e
	}
	defer lock.Close()
	var acquired int
	if e = lock.QueryRowContext(ctx, `SELECT GET_LOCK(CONCAT(DATABASE(),'/migrate'),30)`).Scan(&acquired); e != nil {
		return e
	}
	if acquired != 1 {
		return fmt.Errorf("migration is already running")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = lock.ExecContext(release, `DO RELEASE_LOCK(CONCAT(DATABASE(),'/migrate'))`)
	}()
	for _, q := range strings.Split(schema, ";") {
		if strings.TrimSpace(q) != "" {
			if _, e := s.DB.ExecContext(ctx, q); e != nil {
				return e
			}
		}
	}
	var unsafe int
	if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND LEFT(table_name,3)='wb_' AND ((character_set_name='ascii' AND collation_name<>'ascii_bin') OR (column_name='item_id' AND collation_name<>'utf8mb4_0900_bin'))`).Scan(&unsafe); e != nil {
		return e
	}
	if unsafe > 0 {
		return fmt.Errorf("unsafe identifier collation: initialize a fresh native database or apply the reviewed binary collation migration before startup")
	}
	var n int
	if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='wb_events' AND column_name='sequence'`).Scan(&n); e != nil {
		return e
	}
	if n == 0 {
		if _, e := s.DB.ExecContext(ctx, `ALTER TABLE wb_events ADD sequence BIGINT UNSIGNED NOT NULL AUTO_INCREMENT UNIQUE`); e != nil {
			return e
		}
	}
	for _, column := range []struct{ name, definition string }{
		{"external_user_id", "VARCHAR(191) COLLATE utf8mb4_0900_bin GENERATED ALWAYS AS (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(body,'$.externalUserId')),'')) STORED"},
		{"project_id", "VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin GENERATED ALWAYS AS (JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.projectId'))) STORED"},
		{"auto_reply", "BOOLEAN GENERATED ALWAYS AS (JSON_EXTRACT(body,'$.autoReply')=TRUE) STORED"},
		{"display_name", "VARCHAR(191) GENERATED ALWAYS AS (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(body,'$.displayName')),'')) STORED"},
		{"last_activity", "VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin GENERATED ALWAYS AS (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(body,'$.lastMessageAt')),'')) STORED"},
	} {
		var count int
		if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='wb_message_routes' AND column_name=?`, column.name).Scan(&count); e != nil {
			return e
		}
		if count == 0 {
			if _, e := s.DB.ExecContext(ctx, "ALTER TABLE wb_message_routes ADD COLUMN "+column.name+" "+column.definition); e != nil {
				return e
			}
		}
	}
	for name, columns := range map[string]string{"wb_routes_actor": "connection_id,actor_id", "wb_routes_external": "connection_id,external_user_id", "wb_routes_recent": "project_id,last_activity DESC,id DESC", "wb_routes_paused": "project_id,auto_reply,last_activity DESC,id DESC"} {
		var count int
		if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='wb_message_routes' AND index_name=?`, name).Scan(&count); e != nil {
			return e
		}
		if count == 0 {
			if _, e := s.DB.ExecContext(ctx, "CREATE INDEX "+name+" ON wb_message_routes ("+columns+")"); e != nil {
				return e
			}
		}
	}
	for _, column := range []struct{ name, definition string }{
		{"route_id", "VARCHAR(96) CHARACTER SET ascii COLLATE ascii_bin GENERATED ALWAYS AS (JSON_UNQUOTE(JSON_EXTRACT(body,'$.routeId'))) STORED"},
		{"message_state", "VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin GENERATED ALWAYS AS (JSON_UNQUOTE(JSON_EXTRACT(body,'$.state'))) STORED"},
		{"arrival_at", "VARCHAR(40) CHARACTER SET ascii COLLATE ascii_bin GENERATED ALWAYS AS (JSON_UNQUOTE(JSON_EXTRACT(body,'$.createdAt'))) STORED"},
	} {
		var count int
		if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='wb_messages' AND column_name=?`, column.name).Scan(&count); e != nil {
			return e
		}
		if count == 0 {
			if _, e := s.DB.ExecContext(ctx, "ALTER TABLE wb_messages ADD COLUMN "+column.name+" "+column.definition); e != nil {
				return e
			}
		}
	}
	for name, columns := range map[string]string{"wb_messages_route": "route_id,arrival_at,event_id", "wb_messages_pending": "route_id,message_state,arrival_at,event_id"} {
		var count int
		if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='wb_messages' AND index_name=?`, name).Scan(&count); e != nil {
			return e
		}
		if count == 0 {
			if _, e := s.DB.ExecContext(ctx, "CREATE INDEX "+name+" ON wb_messages ("+columns+")"); e != nil {
				return e
			}
		}
	}
	var kindColumn int
	if e = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='wb_events' AND column_name='event_kind'`).Scan(&kindColumn); e != nil {
		return e
	}
	if kindColumn == 0 {
		if _, e = s.DB.ExecContext(ctx, `ALTER TABLE wb_events ADD COLUMN event_kind VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin GENERATED ALWAYS AS (JSON_UNQUOTE(JSON_EXTRACT(body,'$.kind'))) STORED, ADD INDEX wb_events_kind(event_kind,sequence)`); e != nil {
			return e
		}
	}
	if err := s.ensureReplyActivitySource(ctx); err != nil {
		return err
	}
	return s.migrateResourceGrants(ctx)
}
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func scanJSON[T any](row interface{ Scan(...any) error }) (T, error) {
	var v T
	var raw []byte
	e := row.Scan(&raw)
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(raw, &v)
	return v, e
}
func (s *Store) PutProject(ctx context.Context, p Principal, v Project) error {
	if !p.IsOwner() {
		return ErrDenied
	}
	if !ValidID(v.ID) || v.Name == "" {
		return ErrInvalid
	}
	_, e := s.DB.ExecContext(ctx, `INSERT INTO wb_projects(id,body) VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)`, v.ID, jsonText(v))
	return e
}
func (s *Store) BindSession(ctx context.Context, p Principal) error {
	if e := p.Validate(); e != nil {
		return e
	}
	// An existing Session can never be rebound to another identity.
	_, e := s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_sessions(id,project_id,kind) VALUES(?,?,?)`, p.SessionID, p.ProjectID, p.Kind)
	if e != nil {
		return e
	}
	var project, kind string
	e = s.DB.QueryRowContext(ctx, `SELECT project_id,kind FROM wb_sessions WHERE id=?`, p.SessionID).Scan(&project, &kind)
	if e != nil {
		return e
	}
	if project != p.ProjectID || kind != p.Kind {
		return ErrDenied
	}
	return nil
}
func (s *Store) Resource(ctx context.Context, id string) (Resource, error) {
	var r Resource
	var raw []byte
	e := s.DB.QueryRowContext(ctx, `SELECT body,current_version,revision FROM wb_resources WHERE id=?`, id).Scan(&raw, &r.CurrentVersion, &r.Revision)
	if e != nil {
		return r, e
	}
	v, rev := r.CurrentVersion, r.Revision
	e = json.Unmarshal(raw, &r)
	r.CurrentVersion = v
	r.Revision = rev
	return r, e
}
func (s *Store) Resources(ctx context.Context, p Principal) ([]Resource, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	allowed := map[string]map[string]bool{}
	if p.Kind == "project" {
		rows, e := s.DB.QueryContext(ctx, `SELECT resource_id,section_id FROM wb_grants WHERE project_id=?`, p.ProjectID)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id, section string
			if e = rows.Scan(&id, &section); e != nil {
				rows.Close()
				return nil, e
			}
			if allowed[id] == nil {
				allowed[id] = map[string]bool{}
			}
			allowed[id][section] = true
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body,current_version,revision FROM wb_resources WHERE id<>? ORDER BY id`, replyActivitySource.ResourceID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Resource{}
	for rows.Next() {
		var r Resource
		var raw []byte
		var version string
		var revision int64
		if e = rows.Scan(&raw, &version, &revision); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &r); e != nil {
			return nil, e
		}
		r.CurrentVersion = version
		r.Revision = revision
		if p.Kind == "project" {
			sections := []Section{}
			for _, v := range r.Sections {
				if allowed[r.ID][v.ID] || allowed[r.ID]["*"] {
					sections = append(sections, v)
				}
			}
			if len(sections) == 0 {
				continue
			}
			r.Sections = sections
		}
		if !p.IsOwner() {
			allowedSections := map[string]bool{}
			for _, section := range r.Sections {
				allowedSections[section.ID] = true
			}
			checks := []RecordCheck{}
			for _, check := range r.Checks {
				if allowedSections[check.Section] {
					checks = append(checks, check)
				}
			}
			r.Checks = checks
			r.ConnectionID = ""
			r.Relation = ""
			r.KeyField = ""
			r.WatermarkField = ""
			r.RecordSchema = nil
			r.TrimFields = nil
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PutResource also provisions the project access for a resource created by an existing
// workbench workflow. Browser registration does not derive ACLs from ownership.
func (s *Store) PutResource(ctx context.Context, p Principal, r Resource) error {
	return s.putResource(ctx, p, r, true)
}
func (s *Store) RegisterResource(ctx context.Context, p Principal, r Resource) error {
	return s.putResource(ctx, p, r, false)
}
func (s *Store) putResource(ctx context.Context, p Principal, r Resource, seed bool) error {
	if !p.IsOwner() || r.ID == replyActivitySource.ResourceID {
		return ErrDenied
	}
	if e := r.Validate(); e != nil {
		return e
	}
	if r.Revision == 0 {
		tx, e := s.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, `INSERT INTO wb_resources(id,owner_project,body) VALUES(?,?,?)`, r.ID, r.OwnerProject, jsonText(r)); e != nil {
			return e
		}
		if seed {
			if e = seedResourceGrants(ctx, tx, r); e != nil {
				return e
			}
		}
		return tx.Commit()
	}
	result, e := s.DB.ExecContext(ctx, `UPDATE wb_resources SET owner_project=?,body=?,revision=revision+1 WHERE id=? AND revision=?`, r.OwnerProject, jsonText(r), r.ID, r.Revision)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) Granted(ctx context.Context, id, project, section string) (bool, error) {
	var n int
	e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_grants WHERE resource_id=? AND project_id=? AND section_id IN ('*',?)`, id, project, section).Scan(&n)
	return n > 0, e
}
func (s *Store) SetGrant(ctx context.Context, p Principal, g Grant) error {
	_, err := s.SetResourceGrant(ctx, p, g.ResourceID, ResourceAccess{ProjectID: g.ProjectID, Sections: g.Sections})
	return err
}
func (s *Store) Snapshot(ctx context.Context, id, version string) (Snapshot, error) {
	return readSnapshot(ctx, s.DB, id, version)
}
func readSnapshot(ctx context.Context, q resourceQuery, id, version string) (Snapshot, error) {
	var v Snapshot
	var b []byte
	e := q.QueryRowContext(ctx, `SELECT body,state,record_count,published_at FROM wb_snapshots WHERE resource_id=? AND version=?`, id, version).Scan(&b, &v.State, &v.Count, &v.PublishedAt)
	if e != nil {
		return v, e
	}
	state, count, at := v.State, v.Count, v.PublishedAt
	e = json.Unmarshal(b, &v)
	v.State = state
	v.Count = count
	v.PublishedAt = at
	return v, e
}
func (s *Store) canWrite(ctx context.Context, p Principal, id string) (Resource, error) {
	if id == replyActivitySource.ResourceID {
		return Resource{}, ErrDenied
	}
	if e := p.Validate(); e != nil {
		return Resource{}, e
	}
	r, e := s.Resource(ctx, id)
	if e != nil {
		return r, e
	}
	return r, s.snapshotWriteAllowed(ctx, s.DB, p, r)
}
func (s *Store) BeginSnapshot(ctx context.Context, p Principal, v Snapshot) error {
	r, e := s.canWrite(ctx, p, v.ResourceID)
	if e != nil {
		return e
	}
	if !ValidID(v.Version) {
		return ErrInvalid
	}
	if len(v.Dependencies) > MaxSourceRefs {
		return ErrInvalid
	}
	for _, ref := range v.Dependencies {
		if ref.ResourceID == v.ResourceID && ref.Version == v.Version {
			return ErrInvalid
		}
		if e = Authorize(ctx, s, p, ref); e != nil {
			return e
		}
	}
	v.Previous = r.CurrentVersion
	v.Sections = r.Sections
	v.RecordSchema = r.RecordSchema
	v.State = "staging"
	v.Count = 0
	v.PublishedAt = nil
	old, e := s.Snapshot(ctx, v.ResourceID, v.Version)
	if e == nil {
		if Digest(old.Dependencies) != Digest(v.Dependencies) || old.SourceWatermark != v.SourceWatermark {
			return ErrConflict
		}
		return nil
	}
	if e != sql.ErrNoRows {
		return e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guardWrite(ctx, tx); e != nil {
		return e
	}
	if _, e = s.lockSnapshotWrite(ctx, tx, p, v.ResourceID); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO wb_snapshots(resource_id,version,previous_version,state,body) VALUES(?,?,?,'staging',?)`, v.ResourceID, v.Version, v.Previous, jsonText(v))
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Stage(ctx context.Context, p Principal, id, version string, records []Record) error {
	if _, e := s.canWrite(ctx, p, id); e != nil {
		return e
	}
	if e := ValidateRecords(records); e != nil {
		return e
	}
	if len(records) == 0 {
		return nil
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guardWrite(ctx, tx); e != nil {
		return e
	}
	if _, e = s.lockSnapshotWrite(ctx, tx, p, id); e != nil {
		return e
	}
	var state string
	var raw []byte
	if e = tx.QueryRowContext(ctx, `SELECT state,body FROM wb_snapshots WHERE resource_id=? AND version=? FOR UPDATE`, id, version).Scan(&state, &raw); e != nil {
		return e
	}
	if state != "staging" {
		return ErrConflict
	}
	var snapshot Snapshot
	if e = json.Unmarshal(raw, &snapshot); e != nil {
		return e
	}
	if e = AuthorizeAll(ctx, resourceTransactionGate{tx}, p, snapshot.Dependencies); e != nil {
		return e
	}
	validator, e := (Method{OutputSchema: snapshot.RecordSchema}).ResolveOutput()
	if e != nil {
		return e
	}
	if validator != nil {
		for _, record := range records {
			if e = validator.Validate(record.Fields); e != nil {
				return fmt.Errorf("%w: record does not satisfy the snapshot schema", ErrInvalid)
			}
		}
	}
	marks := make([]string, len(records))
	args := []any{id, version}
	for i, r := range records {
		marks[i] = "?"
		args = append(args, r.ID)
	}
	rows, e := tx.QueryContext(ctx, `SELECT item_id,digest FROM wb_records WHERE resource_id=? AND version=? AND item_id IN (`+strings.Join(marks, ",")+`)`, args...)
	if e != nil {
		return e
	}
	existing := map[string]string{}
	for rows.Next() {
		var id, digest string
		if e = rows.Scan(&id, &digest); e != nil {
			rows.Close()
			return e
		}
		existing[id] = digest
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	values := []string{}
	args = nil
	for _, r := range records {
		digest := Digest(r.Fields)
		if previous, ok := existing[r.ID]; ok {
			if previous != digest {
				return fmt.Errorf("%w: duplicate item differs", ErrConflict)
			}
			continue
		}
		values = append(values, "(?,?,?,?,?)")
		args = append(args, id, version, r.ID, digest, jsonText(r.Fields))
	}
	if len(values) > 0 {
		if _, e = tx.ExecContext(ctx, `INSERT INTO wb_records VALUES `+strings.Join(values, ","), args...); e != nil {
			return e
		}
	}

	return tx.Commit()
}
func (s *Store) Publish(ctx context.Context, p Principal, id, version string, expectedCount int) (Snapshot, error) {
	if _, e := s.canWrite(ctx, p, id); e != nil {
		return Snapshot{}, e
	}
	v, e := s.Snapshot(ctx, id, version)
	if e != nil {
		return v, e
	}
	for _, dep := range v.Dependencies {
		if e = Authorize(ctx, s, p, dep); e != nil {
			return v, e
		}
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	if e = guardWrite(ctx, tx); e != nil {
		return v, e
	}
	if _, e = s.lockSnapshotWrite(ctx, tx, p, id); e != nil {
		return v, e
	}
	if e = AuthorizeAll(ctx, resourceTransactionGate{tx}, p, v.Dependencies); e != nil {
		return v, e
	}
	var current, state, previous string
	e = tx.QueryRowContext(ctx, `SELECT current_version FROM wb_resources WHERE id=? FOR UPDATE`, id).Scan(&current)
	if e != nil {
		return v, e
	}
	e = tx.QueryRowContext(ctx, `SELECT state,previous_version FROM wb_snapshots WHERE resource_id=? AND version=? FOR UPDATE`, id, version).Scan(&state, &previous)
	if e != nil {
		return v, e
	}
	if state == "published" {
		if e = tx.Commit(); e != nil {
			return v, e
		}
		return s.Snapshot(ctx, id, version)
	}
	if state != "staging" || current != previous {
		return v, ErrConflict
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_records WHERE resource_id=? AND version=?`, id, version).Scan(&count); e != nil {
		return v, e
	}
	if count != expectedCount {
		return v, fmt.Errorf("%w: expected %d records, got %d", ErrInvalid, expectedCount, count)
	}
	if _, e = tx.ExecContext(ctx, `UPDATE wb_snapshots SET state='published',record_count=?,published_at=UTC_TIMESTAMP(6) WHERE resource_id=? AND version=?`, count, id, version); e != nil {
		return v, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE wb_resources SET current_version=?,revision=revision+1 WHERE id=?`, version, id); e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return v, e
	}
	return s.Snapshot(ctx, id, version)
}
func (s *Store) Read(ctx context.Context, p Principal, ref Ref, after string, limit int) (Page, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 200 {
		return Page{}, ErrInvalid
	}
	if e := Authorize(ctx, s, p, ref); e != nil {
		return Page{}, e
	}
	snap, e := s.Snapshot(ctx, ref.ResourceID, ref.Version)
	if e != nil {
		return Page{}, e
	}
	var selected Section
	for _, section := range snap.Sections {
		if section.ID == ref.Section {
			selected = section
			break
		}
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT item_id,body FROM wb_records WHERE resource_id=? AND version=? AND item_id>? ORDER BY item_id LIMIT ?`, ref.ResourceID, ref.Version, after, limit+1)
	if e != nil {
		return Page{}, e
	}
	out := Page{Ref: ref, Dependencies: snap.Dependencies, Count: snap.Count, PublishedAt: snap.PublishedAt, Records: []Record{}}
	for rows.Next() {
		var id string
		var b []byte
		if e = rows.Scan(&id, &b); e != nil {
			rows.Close()
			return Page{}, e
		}
		if len(out.Records) == limit {
			out.Next = out.Records[len(out.Records)-1].ID
			break
		}
		var full map[string]any
		if e = json.Unmarshal(b, &full); e != nil {
			rows.Close()
			return Page{}, e
		}
		projected, err := projectFields(selected, full)
		if err != nil {
			rows.Close()
			return Page{}, err
		}
		record := Record{ID: id, Fields: projected}
		out.Records = append(out.Records, record)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return Page{}, e
	}
	// A revocation that occurred while the query was in flight must also reject the result.
	if e = Authorize(ctx, s, p, ref); e != nil {
		return Page{}, e
	}
	out.Stale, e = s.IsStale(ctx, ref)
	if e != nil {
		return Page{}, e
	}
	if e = s.RememberRead(ctx, p, []Ref{ref}); e != nil {
		return Page{}, e
	}
	return out, nil
}

func (s *Store) ReadIDs(ctx context.Context, p Principal, ref Ref, ids []string) ([]Record, error) {
	return s.readIDs(ctx, p, ref, ids, false)
}

// ReadProfileUpdateInputs includes the expert's internal cache. Regular
// knowledge reads still return just the registered section fields.
func (s *Store) ReadProfileUpdateInputs(ctx context.Context, p Principal, ref Ref, ids []string) ([]Record, error) {
	return s.readIDs(ctx, p, ref, ids, true)
}

func (s *Store) readIDs(ctx context.Context, p Principal, ref Ref, ids []string, profileUpdate bool) ([]Record, error) {
	if len(ids) == 0 {
		return []Record{}, nil
	}
	if len(ids) > 200 {
		return nil, ErrInvalid
	}
	if e := Authorize(ctx, s, p, ref); e != nil {
		return nil, e
	}
	snap, e := s.Snapshot(ctx, ref.ResourceID, ref.Version)
	if e != nil {
		return nil, e
	}
	var selected Section
	for _, section := range snap.Sections {
		if section.ID == ref.Section {
			selected = section
			break
		}
	}
	args := []any{ref.ResourceID, ref.Version}
	marks := []string{}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
		args = append(args, id)
		marks = append(marks, "?")
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT item_id,body FROM wb_records WHERE resource_id=? AND version=? AND item_id IN (`+strings.Join(marks, ",")+`) ORDER BY item_id`, args...)
	if e != nil {
		return nil, e
	}
	out := []Record{}
	for rows.Next() {
		var v Record
		var b []byte
		if e = rows.Scan(&v.ID, &b); e != nil {
			rows.Close()
			return nil, e
		}
		if !wanted[v.ID] {
			rows.Close()
			return nil, fmt.Errorf("%w: source identity does not match requested record", ErrConflict)
		}
		var all map[string]any
		if e = json.Unmarshal(b, &all); e != nil {
			rows.Close()
			return nil, e
		}
		v.Fields, e = projectFields(selected, all)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if profileUpdate && all["profileSemantic"] != nil {
			v.Fields["profileSemantic"] = all["profileSemantic"]
		}
		out = append(out, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if e = Authorize(ctx, s, p, ref); e != nil {
		return nil, e
	}
	if e = s.RememberRead(ctx, p, []Ref{ref}); e != nil {
		return nil, e
	}
	return out, nil
}
