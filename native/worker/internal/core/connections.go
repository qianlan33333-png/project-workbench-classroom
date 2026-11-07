package core

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Store) Connections(ctx context.Context, p Principal) ([]Connection, error) {
	if !p.IsOwner() {
		return nil, ErrDenied
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_connections ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		v, e := scanJSON[Connection](rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) PutConnection(ctx context.Context, p Principal, v Connection) error {
	if !p.IsOwner() {
		return ErrDenied
	}
	if !ValidID(v.ID) || v.Name == "" {
		return ErrInvalid
	}
	switch v.Kind {
	case "mysql":
		if !ValidID(v.SecretEnv) || !strings.HasPrefix(v.SecretEnv, "WORKBENCH_SOURCE_") {
			return ErrInvalid
		}
	case "http-json":
		if !validHTTPSource(v.Location) || (v.SecretEnv != "" && (!ValidID(v.SecretEnv) || !strings.HasPrefix(v.SecretEnv, "WORKBENCH_SOURCE_"))) {
			return ErrInvalid
		}
	case "jsonl", "directory":
		if !filepath.IsAbs(v.Location) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	_, e := s.DB.ExecContext(ctx, `INSERT INTO wb_connections VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)`, v.ID, jsonText(v))
	return e
}
func (s *Store) Grants(ctx context.Context, p Principal) ([]Grant, error) {
	if !p.IsOwner() {
		return nil, ErrDenied
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT resource_id,project_id,section_id FROM wb_grants ORDER BY resource_id,project_id,section_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		var section string
		if e = rows.Scan(&g.ResourceID, &g.ProjectID, &section); e != nil {
			return nil, e
		}
		g.Sections = []string{section}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_projects ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		v, e := scanJSON[Project](rows)
		if e != nil {
			return nil, e
		}
		v.Workspace = ""
		out = append(out, v)
	}
	return out, rows.Err()
}

// SyncSource reads only an owner-registered relation/directory. Models never submit SQL or credentials.
// A source transaction is one repeatable-read snapshot. A retry that observes changed data fails
// on conflicting staged records instead of silently combining source versions.
func (s *Store) SyncSource(ctx context.Context, r Run, heartbeat func()) (Snapshot, error) {
	resource, e := s.canWrite(ctx, r.Principal, r.Destination)
	if e != nil {
		return Snapshot{}, e
	}
	conn, e := scanJSON[Connection](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_connections WHERE id=?`, resource.ConnectionID))
	if e != nil {
		return Snapshot{}, e
	}
	snap := Snapshot{ResourceID: resource.ID, Version: r.ID, SourceWatermark: r.ID}
	existing, e := s.Snapshot(ctx, resource.ID, r.ID)
	if e != nil && !IsMissing(e) {
		return snap, e
	}
	if existing.State == "published" {
		return existing, nil
	}
	validator, e := (Method{OutputSchema: resource.RecordSchema}).ResolveOutput()
	if e != nil {
		return snap, e
	}
	count := 0
	page := []Record{}
	flush := func() error {
		if len(page) == 0 {
			return nil
		}
		heartbeat()
		if _, e = s.canWrite(ctx, r.Principal, resource.ID); e != nil {
			return e
		}
		if e = s.Stage(ctx, r.Principal, resource.ID, r.ID, page); e != nil {
			return e
		}
		count += len(page)
		page = nil
		return nil
	}
	fields := map[string]bool{}
	for _, section := range resource.Sections {
		for _, f := range section.Fields {
			fields[f] = true
		}
	}
	accept := func(v Record) error {
		clean := map[string]any{}
		for k := range fields {
			if value, ok := v.Fields[k]; ok {
				clean[k] = value
			}
		}
		for _, key := range resource.TrimFields {
			if text, ok := clean[key].(string); ok {
				clean[key] = strings.TrimSpace(text)
			}
		}
		if validator != nil {
			if e = validator.Validate(clean); e != nil {
				return fmt.Errorf("%w: source record does not satisfy the configured schema", ErrInvalid)
			}
		}
		v.Fields = clean
		if e = ValidateRecords([]Record{v}); e != nil {
			return e
		}
		page = append(page, v)
		if len(page) == 200 {
			return flush()
		}
		return nil
	}
	switch conn.Kind {
	case "http-json":
		if resource.JSONSource == nil {
			return snap, ErrInvalid
		}
		file, digest, err := downloadJSON(ctx, conn)
		if err != nil {
			return snap, err
		}
		defer func() { file.Close(); os.Remove(file.Name()) }()
		snap.SourceWatermark = digest
		if e = s.BeginSnapshot(ctx, r.Principal, snap); e != nil {
			return snap, e
		}
		if e = streamJSON(file, *resource.JSONSource, accept); e != nil {
			return snap, e
		}
	case "directory":
		records, digest, err := directoryRecords(ctx, conn.Location, resource.Relation)
		if err != nil {
			return snap, err
		}
		snap.SourceWatermark = digest
		if e = s.BeginSnapshot(ctx, r.Principal, snap); e != nil {
			return snap, e
		}
		for _, record := range records {
			if e = accept(record); e != nil {
				return snap, e
			}
		}
		_, again, err := directoryRecords(ctx, conn.Location, resource.Relation)
		if err != nil {
			return snap, err
		}
		if again != digest {
			return snap, ErrConflict
		}
	case "mysql":
		if !ValidID(resource.Relation) || !ValidID(resource.KeyField) {
			return snap, ErrInvalid
		}
		dsn := os.Getenv(conn.SecretEnv)
		if dsn == "" {
			return snap, fmt.Errorf("source connection is not configured")
		}
		source, e := Open(ctx, dsn)
		if e != nil {
			return snap, fmt.Errorf("source connection unavailable")
		}
		defer source.DB.Close()
		tx, e := source.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if e != nil {
			return snap, e
		}
		defer tx.Rollback()
		var total, distinct, nonnull int
		key := "`" + resource.KeyField + "`"
		if e = tx.QueryRowContext(ctx, "SELECT COUNT(*),COUNT(DISTINCT "+key+"),COUNT("+key+") FROM `"+resource.Relation+"`").Scan(&total, &distinct, &nonnull); e != nil {
			return snap, e
		}
		if total != distinct || total != nonnull {
			return snap, fmt.Errorf("%w: source key must be unique and non-null", ErrInvalid)
		}
		if resource.WatermarkField != "" {
			var watermark sql.NullString
			var versions, withWatermark int
			column := "`" + resource.WatermarkField + "`"
			if e = tx.QueryRowContext(ctx, "SELECT COUNT(DISTINCT "+column+"),MIN("+column+"),COUNT("+column+") FROM `"+resource.Relation+"`").Scan(&versions, &watermark, &withWatermark); e != nil {
				return snap, e
			}
			if versions != 1 || !watermark.Valid || strings.TrimSpace(watermark.String) == "" || withWatermark != total {
				return snap, fmt.Errorf("%w: source must have one published watermark", ErrInvalid)
			}
			snap.SourceWatermark = watermark.String
		}
		if e = s.BeginSnapshot(ctx, r.Principal, snap); e != nil {
			return snap, e
		}
		columns := []string{}
		for f := range fields {
			columns = append(columns, f)
		}
		sort.Strings(columns)
		quoted := []string{"`" + resource.KeyField + "`"}
		for _, c := range columns {
			quoted = append(quoted, "`"+c+"`")
		}
		var cursor any
		fetched := 0
		for {
			query := "SELECT " + strings.Join(quoted, ",") + " FROM `" + resource.Relation + "`"
			args := []any{}
			if cursor != nil {
				query += " WHERE `" + resource.KeyField + "` > ?"
				args = append(args, cursor)
			}
			query += " ORDER BY `" + resource.KeyField + "` LIMIT 200"
			rows, e := tx.QueryContext(ctx, query, args...)
			if e != nil {
				return snap, e
			}
			columnTypes, e := rows.ColumnTypes()
			if e != nil {
				rows.Close()
				return snap, e
			}
			n := 0
			for rows.Next() {
				values := make([]any, len(quoted))
				ptr := make([]any, len(values))
				for i := range ptr {
					ptr[i] = &values[i]
				}
				if e = rows.Scan(ptr...); e != nil {
					rows.Close()
					return snap, e
				}
				for i, v := range values {
					if b, ok := v.([]byte); ok {
						if columnTypes[i].DatabaseTypeName() == "JSON" {
							if e = json.Unmarshal(b, &values[i]); e != nil {
								rows.Close()
								return snap, e
							}
						} else {
							values[i] = string(b)
						}
					}
				}
				cursor = values[0]
				fetched++
				v := Record{ID: fmt.Sprint(cursor), Fields: map[string]any{}}
				for i, k := range columns {
					v.Fields[k] = values[i+1]
				}
				if e = accept(v); e != nil {
					rows.Close()
					return snap, e
				}
				n++
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return snap, e
			}
			if n < 200 {
				break
			}
		}
		if fetched != total {
			return snap, fmt.Errorf("%w: source cursor did not cover all records", ErrInvalid)
		}
		if e = tx.Commit(); e != nil {
			return snap, e
		}
	case "jsonl":
		if !ValidID(resource.Relation) {
			return snap, ErrInvalid
		}
		root, e := filepath.EvalSymlinks(conn.Location)
		if e != nil {
			return snap, e
		}
		path, e := filepath.EvalSymlinks(filepath.Join(root, resource.Relation+".jsonl"))
		if e != nil {
			return snap, e
		}
		relative, e := filepath.Rel(root, path)
		if e != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
			return snap, ErrDenied
		}
		file, e := os.Open(path)
		if e != nil {
			return snap, e
		}
		defer file.Close()
		before, e := file.Stat()
		if e != nil {
			return snap, e
		}
		hash := sha256.New()
		if _, e = io.Copy(hash, file); e != nil {
			return snap, e
		}
		snap.SourceWatermark = hex.EncodeToString(hash.Sum(nil))
		if _, e = file.Seek(0, io.SeekStart); e != nil {
			return snap, e
		}
		if e = s.BeginSnapshot(ctx, r.Principal, snap); e != nil {
			return snap, e
		}
		parsedHash := sha256.New()
		scanner := bufio.NewScanner(io.TeeReader(file, parsedHash))
		scanner.Buffer(make([]byte, 65536), 512*1024)
		for scanner.Scan() {
			var v Record
			if e = json.Unmarshal(scanner.Bytes(), &v); e != nil {
				return snap, e
			}
			if e = accept(v); e != nil {
				return snap, e
			}
		}
		if e = scanner.Err(); e != nil {
			return snap, e
		}
		after, e := file.Stat()
		if e != nil {
			return snap, e
		}
		if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || hex.EncodeToString(parsedHash.Sum(nil)) != snap.SourceWatermark {
			return snap, ErrConflict
		}
	default:
		return snap, ErrInvalid
	}
	if e = flush(); e != nil {
		return snap, e
	}

	if count < resource.MinRecords {
		return snap, fmt.Errorf("%w: source is below configured minimum record count", ErrInvalid)
	}
	if resource.MinRetainedPercent > 0 && resource.CurrentVersion != "" {
		previous, err := s.Snapshot(ctx, resource.ID, resource.CurrentVersion)
		if err != nil {
			return snap, err
		}
		if int64(count)*100 < int64(previous.Count)*int64(resource.MinRetainedPercent) {
			return snap, fmt.Errorf("%w: source count dropped below configured retention threshold", ErrInvalid)
		}
	}
	return s.Publish(ctx, r.Principal, resource.ID, r.ID, count)
}
