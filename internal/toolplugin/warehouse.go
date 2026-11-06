package toolplugin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"time"
)

var userID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,160}$`)

func normalize(row map[string]any, idField string) (string, map[string]any, error) {
	id, ok := row[idField].(string)
	if !ok || !userID.MatchString(id) {
		return "", nil, errors.New("source identity missing or invalid")
	}
	clean := map[string]any{}
	for k, v := range row {
		clean[k] = v
	}
	if name, ok := clean["name"].(string); ok {
		clean["name"] = strings.TrimSpace(name)
	}
	return id, clean, nil
}
func (s *Server) syncContexts(ctx context.Context, j job) (any, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	if method, ok := j.Request["workbench_method"].(map[string]any); ok {
		if version := str(method, "rule_version"); version != "" && version != "canonical.clean.v1" {
			return nil, errors.New("清洗规则版本未安装，不能只改标签代替规则")
		}
	}
	metadata := map[string]any{}
	aliases := map[string]string{}
	generation := j.ID
	seen := map[string]string{}
	count := 0
	found := false
	// Retried full builds replace only their own unpublished staging generation.
	var current, previous string
	check, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	var global int64
	if e = check.QueryRowContext(ctx, "SELECT epoch FROM plugin_worker_epoch WHERE id=1 FOR UPDATE").Scan(&global); e != nil || global != s.epoch {
		check.Rollback()
		return nil, errors.New("execution lease changed")
	}
	e = check.QueryRowContext(ctx, "SELECT generation,previous_generation FROM context_state WHERE dataset='users' FOR UPDATE").Scan(&current, &previous)
	if e != nil && e != sql.ErrNoRows {
		check.Rollback()
		return nil, e
	}
	if current == generation || previous == generation {
		e = settlePublishedSync(ctx, check, generation, time.Now().UnixMilli())
		if e != nil {
			check.Rollback()
			return nil, e
		}
		return nil, check.Commit()
	}
	var pinned bool
	e = check.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_activities
		WHERE generation=? AND state NOT IN ('sent','completed_with_failures','cancelled'))`, generation).Scan(&pinned)
	if e != nil || pinned {
		check.Rollback()
		if e != nil {
			return nil, e
		}
		return nil, errors.New("existing context generation is pinned by an unfinished activity")
	}
	if _, e = check.ExecContext(ctx, "DELETE FROM context_records WHERE generation=?", generation); e != nil {
		check.Rollback()
		return nil, e
	}
	if e = check.Commit(); e != nil {
		return nil, e
	}
	res, e := s.sourceRequest(ctx, "GET", s.Config.SourcePath, nil)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	sourceDigest := sha256.New()
	decoder := json.NewDecoder(io.TeeReader(io.LimitReader(res.Body, 256<<20), sourceDigest))
	decoder.UseNumber()
	first, e := decoder.Token()
	if e != nil || first != json.Delim('{') {
		return nil, errors.New("source snapshot must be an object")
	}
	var tx *sql.Tx
	var stmt *sql.Stmt
	begin := func() error {
		var er error
		tx, er = s.DB.BeginTx(ctx, nil)
		if er != nil {
			return er
		}
		var lease int64
		if er = tx.QueryRowContext(ctx, "SELECT epoch FROM plugin_worker_epoch WHERE id=1 FOR UPDATE").Scan(&lease); er != nil || lease != s.epoch {
			return errors.New("execution lease changed")
		}
		stmt, er = tx.PrepareContext(ctx, "INSERT INTO context_records(generation,user_ref,content_hash,raw_json,clean_json) VALUES(?,?,?,?,?)")
		return er
	}
	if e = begin(); e != nil {
		return nil, e
	}
	defer func() {
		if stmt != nil {
			stmt.Close()
		}
		if tx != nil {
			tx.Rollback()
		}
	}()
	for decoder.More() {
		token, er := decoder.Token()
		if er != nil {
			return nil, er
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid snapshot key")
		}
		if key != "users" {
			var value any
			if er = decoder.Decode(&value); er != nil {
				return nil, er
			}
			if key == "identity_aliases" {
				if m, ok := value.(map[string]any); ok {
					for from, to := range m {
						v, ok := to.(string)
						if !ok || !userID.MatchString(from) || !userID.MatchString(v) {
							return nil, errors.New("invalid source alias mapping")
						}
						aliases[from] = v
					}
				}
			} else if contains([]string{"snapshot_id", "crm_source_snapshot_id", "generated_at", "published_at", "version", "schema_version", "source_version", "updated_at"}, key) && len(encode(value)) < 16000 {
				metadata[key] = value
			}
			continue
		}
		found = true
		token, er = decoder.Token()
		if er != nil || token != json.Delim('[') {
			return nil, errors.New("users must be array")
		}
		for decoder.More() {
			if count%100 == 0 && s.cancelled(ctx, j.ID) {
				return nil, errors.New("cancelled")
			}
			var row map[string]any
			if er = decoder.Decode(&row); er != nil {
				return nil, er
			}
			for _, field := range s.Config.RequiredFields {
				if _, exists := row[field]; !exists {
					return nil, fmt.Errorf("required source field missing: %s", field)
				}
			}
			id, clean, er := normalize(row, s.Config.IDField)
			if er != nil {
				return nil, er
			}
			for _, field := range s.Config.TrimFields {
				if v, ok := clean[field].(string); ok {
					clean[field] = strings.TrimSpace(v)
				}
			}
			raw := encode(row)
			h := hash(raw)
			if old, exists := seen[id]; exists {
				if old != h {
					return nil, errors.New("conflicting source identities")
				}
				continue
			}
			seen[id] = h
			if _, er = stmt.ExecContext(ctx, generation, id, h, raw, encode(clean)); er != nil {
				return nil, er
			}
			count++
			if count > 100000 {
				return nil, errors.New("source exceeds configured first-release limit")
			}
			if count%500 == 0 {
				stmt.Close()
				if er = tx.Commit(); er != nil {
					return nil, er
				}
				if er = begin(); er != nil {
					return nil, er
				}
			}
		}
		if _, er = decoder.Token(); er != nil {
			return nil, er
		}
	}
	if _, e = decoder.Token(); e != nil {
		return nil, e
	}
	var extra any
	if e = decoder.Decode(&extra); e != io.EOF {
		return nil, errors.New("incomplete or trailing source data")
	}
	if !found || count == 0 {
		return nil, errors.New("empty snapshot refused")
	}
	stmt.Close()
	stmt = nil
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	tx = nil
	tx, e = s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var lease int64
	if e = tx.QueryRowContext(ctx, "SELECT epoch FROM plugin_worker_epoch WHERE id=1 FOR UPDATE").Scan(&lease); e != nil || lease != s.epoch {
		return nil, errors.New("execution lease changed")
	}
	var old string
	var oldCount int
	e = tx.QueryRowContext(ctx, "SELECT generation,record_count FROM context_state WHERE dataset='users' FOR UPDATE").Scan(&old, &oldCount)
	if e != nil && e != sql.ErrNoRows {
		return nil, e
	}
	if oldCount > 0 && count*100 < oldCount*80 {
		return nil, errors.New("source count dropped more than20%; old publication retained")
	}
	var epoch int64
	var cancelled bool
	var jobState string
	if e = tx.QueryRowContext(ctx, "SELECT lease_epoch,state,cancel_requested FROM plugin_jobs WHERE id=? FOR UPDATE", j.ID).Scan(&epoch, &jobState, &cancelled); e != nil {
		return nil, e
	}
	if epoch != s.epoch || jobState != "running" || cancelled {
		return nil, errors.New("execution lease changed")
	}
	if len(aliases) > 0 && metadata["crm_source_snapshot_id"] == nil {
		return nil, errors.New("source aliases require authoritative snapshot")
	}
	for from, to := range aliases {
		visited := map[string]bool{from: true}
		for aliases[to] != "" {
			if visited[to] {
				return nil, errors.New("source alias cycle")
			}
			visited[to] = true
			to = aliases[to]
		}
		if _, ok := seen[to]; !ok {
			return nil, errors.New("source alias target not in published users")
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO context_aliases(generation,alias_ref,target_ref) VALUES(?,?,?) ON DUPLICATE KEY UPDATE target_ref=VALUES(target_ref)", generation, from, to); e != nil {
			return nil, e
		}
	}
	now := time.Now().UnixMilli()
	metadata["source_file_sha256"] = hex.EncodeToString(sourceDigest.Sum(nil))
	metadata["identity_alias_count"] = len(aliases)
	metadata["fetched_at"] = now
	metadata["source"] = s.Config.SourceBase + s.Config.SourcePath
	_, e = tx.ExecContext(ctx, `INSERT INTO context_state(dataset,generation,previous_generation,record_count,source_json,published_at) VALUES('users',?,?,?,?,?) ON DUPLICATE KEY UPDATE previous_generation=generation,generation=VALUES(generation),record_count=VALUES(record_count),source_json=VALUES(source_json),published_at=VALUES(published_at)`, generation, old, count, encode(metadata), now)
	if e != nil {
		return nil, e
	}
	result := map[string]any{"published": true, "generation": generation, "previous_generation": old, "users": count, "previous_users": oldCount, "source_metadata": metadata, "published_at": now}
	updated, e := tx.ExecContext(ctx, `UPDATE plugin_jobs SET state='succeeded',result_json=?,error_text=NULL,updated_at=?,available_at=0 WHERE id=? AND state='running' AND lease_epoch=?`, encode(result), now, j.ID, s.epoch)
	if e != nil {
		return nil, fmt.Errorf("persist publication result: %w", e)
	}
	if n, e := updated.RowsAffected(); e != nil || n != 1 {
		return nil, errors.New("publication job did not settle")
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	tx = nil
	return result, nil
}

func settlePublishedSync(ctx context.Context, tx *sql.Tx, id string, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE plugin_jobs j JOIN context_state c
		ON c.dataset='users' AND j.id IN (c.generation,c.previous_generation)
		SET j.state='succeeded',j.error_text=NULL,j.updated_at=?,j.available_at=0,
		j.result_json=CASE WHEN j.id=c.generation THEN JSON_OBJECT(
			'published',TRUE,'already_published',TRUE,'generation',j.id,'users',c.record_count,
			'previous_generation',c.previous_generation,'published_at',c.published_at,'source_metadata',c.source_json)
		ELSE JSON_OBJECT('published',TRUE,'already_published',TRUE,'generation',j.id,
			'users',(SELECT COUNT(*) FROM context_records r WHERE r.generation=j.id)) END
		WHERE j.id=? AND j.kind='sync_contexts' AND j.state IN ('queued','running','failed','cancelled')`, now, id)
	return err
}

func recoverPublishedSyncs(ctx context.Context, tx *sql.Tx, now int64) error {
	var current, previous string
	err := tx.QueryRowContext(ctx, "SELECT generation,previous_generation FROM context_state WHERE dataset='users' FOR UPDATE").Scan(&current, &previous)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	for _, id := range []string{current, previous} {
		if id != "" {
			if err = settlePublishedSync(ctx, tx, id, now); err != nil {
				return fmt.Errorf("reconcile published context job %s: %w", id, err)
			}
		}
	}
	return nil
}

// Reclaim old staging data without coupling it to publication or holding a
// single large delete open. Recheck the pins on every committed batch.
func (s *Server) pruneContexts(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	for _, table := range []string{"context_records", "context_aliases"} {
		if err := s.pruneContextTable(ctx, table); err != nil && parent.Err() == nil {
			log.Printf("tool plugin context cleanup (%s): %v", table, err)
		}
	}
}

func (s *Server) pruneContextTable(ctx context.Context, table string) error {
	rows, err := s.DB.QueryContext(ctx, "SELECT DISTINCT generation FROM "+table)
	if err != nil {
		return err
	}
	var generations []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		generations = append(generations, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range generations {
		for {
			deleted, err := s.DB.ExecContext(ctx, "DELETE FROM "+table+` WHERE generation=?
				AND EXISTS(SELECT 1 FROM plugin_jobs WHERE id=? AND state IN ('succeeded','failed','cancelled'))
				AND NOT EXISTS(SELECT 1 FROM context_state WHERE generation=? OR previous_generation=?)
				AND NOT EXISTS(SELECT 1 FROM plugin_activities WHERE generation=? AND state NOT IN ('sent','completed_with_failures','cancelled'))
				LIMIT 500`, id, id, id, id, id)
			if err != nil {
				return err
			}
			n, err := deleted.RowsAffected()
			if err != nil {
				return err
			}
			if n < 500 {
				break
			}
		}
	}
	return nil
}
func (s *Server) query(ctx context.Context, a map[string]any) (any, error) {
	var generation, metadata string
	var count int
	var published int64
	e := s.DB.QueryRowContext(ctx, "SELECT generation,record_count,source_json,published_at FROM context_state WHERE dataset='users'").Scan(&generation, &count, &metadata, &published)
	if e == sql.ErrNoRows {
		return nil, errors.New("no published context; run sync first")
	}
	if e != nil {
		return nil, e
	}
	limit := number(a, "limit", 10)
	if limit < 1 || limit > 20 {
		limit = 20
	}
	offset := number(a, "offset", 0)
	if offset < 0 {
		return nil, errors.New("invalid offset")
	}
	query := "SELECT user_ref,clean_json,content_hash FROM context_records WHERE generation=?"
	args := []any{generation}
	if ref := str(a, "user_ref"); ref != "" {
		query += " AND (user_ref=? OR user_ref=(SELECT target_ref FROM context_aliases WHERE generation=? AND alias_ref=?))"
		args = append(args, ref, generation, ref)
	}
	query += " ORDER BY user_ref LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, e := s.DB.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []any{}
	size := 0
	for rows.Next() {
		var ref, raw, h string
		if e = rows.Scan(&ref, &raw, &h); e != nil {
			return nil, e
		}
		size += len(raw)
		if size > 450<<10 {
			return nil, errors.New("context response too large; reduce limit")
		}
		items = append(items, map[string]any{"user_ref": ref, "context": json.RawMessage(raw), "hash": h})
	}
	return success(fmt.Sprintf("读取%d名用户的已发布上下文", len(items)), map[string]any{"generation": generation, "total": count, "published_at": published, "source_metadata": json.RawMessage(metadata), "items": items, "next_offset": offset + len(items)}), rows.Err()
}

func (s *Server) listUserRefs(ctx context.Context, a map[string]any) (any, error) {
	limit := number(a, "limit", 2000)
	if limit < 1 || limit > 2000 {
		limit = 2000
	}
	offset := number(a, "offset", 0)
	if offset < 0 {
		return nil, errors.New("invalid offset")
	}
	var gen string
	var total int
	if e := s.DB.QueryRowContext(ctx, "SELECT generation,record_count FROM context_state WHERE dataset='users'").Scan(&gen, &total); e != nil {
		return nil, errors.New("no published context")
	}
	rows, e := s.DB.QueryContext(ctx, "SELECT user_ref FROM context_records WHERE generation=? ORDER BY user_ref LIMIT ? OFFSET ?", gen, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	refs := []string{}
	for rows.Next() {
		var ref string
		if e = rows.Scan(&ref); e != nil {
			return nil, e
		}
		refs = append(refs, ref)
	}
	return success("已读取身份清单；这不是运营资格判定", map[string]any{"generation": gen, "user_refs": refs, "total": total, "next_offset": offset + len(refs)}), rows.Err()
}
