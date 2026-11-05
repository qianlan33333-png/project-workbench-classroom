package toolplugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Server) init() error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, e := s.DB.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	var locked int
	if e = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,30)", "plugin-schema:"+s.namespace).Scan(&locked); e != nil || locked != 1 {
		return errors.New("plugin schema initialization busy")
	}
	defer conn.ExecContext(context.Background(), "DO RELEASE_LOCK(?)", "plugin-schema:"+s.namespace)

	statements := []string{activitySchema,
		`CREATE TABLE IF NOT EXISTS plugin_worker_epoch(id INT PRIMARY KEY,epoch BIGINT NOT NULL) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS plugin_jobs(id VARCHAR(64) PRIMARY KEY,project_id VARCHAR(80) NOT NULL,session_id VARCHAR(80) NOT NULL,invocation_key VARCHAR(160) NOT NULL,kind VARCHAR(40) NOT NULL,input_hash CHAR(64) NOT NULL,request_json JSON NOT NULL,state VARCHAR(20) NOT NULL,result_json JSON,error_text TEXT,cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,lease_epoch BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,UNIQUE KEY invocation_scope(project_id,session_id,invocation_key)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS plugin_items(job_id VARCHAR(64) NOT NULL,item_key VARCHAR(160) NOT NULL,state VARCHAR(20) NOT NULL,request_json JSON NOT NULL,result_json JSON,error_text TEXT,attempts INT NOT NULL DEFAULT 0,total_attempts INT NOT NULL DEFAULT 0,PRIMARY KEY(job_id,item_key)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS context_records(generation VARCHAR(64) NOT NULL,user_ref VARCHAR(160) NOT NULL,content_hash CHAR(64) NOT NULL,raw_json JSON NOT NULL,clean_json JSON NOT NULL,PRIMARY KEY(generation,user_ref)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS context_aliases(generation VARCHAR(64) NOT NULL,alias_ref VARCHAR(160) NOT NULL,target_ref VARCHAR(160) NOT NULL,PRIMARY KEY(generation,alias_ref)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS context_state(dataset VARCHAR(80) PRIMARY KEY,generation VARCHAR(64) NOT NULL,previous_generation VARCHAR(64) NOT NULL,record_count INT NOT NULL,source_json JSON NOT NULL,published_at BIGINT NOT NULL) ENGINE=InnoDB`,
	}
	for _, q := range statements {
		if _, e := s.DB.Exec(q); e != nil {
			return e
		}
	}
	for _, column := range []struct{ table, name, definition string }{{"plugin_items", "total_attempts", "BIGINT NOT NULL DEFAULT 0"}, {"plugin_jobs", "lease_epoch", "BIGINT NOT NULL DEFAULT 0"}, {"plugin_jobs", "available_at", "BIGINT NOT NULL DEFAULT 0"}} {
		var n int
		if e := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?", column.table, column.name).Scan(&n); e != nil {
			return e
		}
		if n == 0 {
			if _, e := s.DB.ExecContext(ctx, "ALTER TABLE "+column.table+" ADD COLUMN "+column.name+" "+column.definition); e != nil {
				return e
			}
		}
	}
	return nil
}

type job struct {
	ID, Project, Session, Key, Kind, State string
	Request                                map[string]any
	Result                                 json.RawMessage
	Error                                  string
}

func scanJob(row interface{ Scan(...any) error }) (job, error) {
	var j job
	var req string
	var result sql.NullString
	var er sql.NullString
	e := row.Scan(&j.ID, &j.Project, &j.Session, &j.Key, &j.Kind, &j.State, &req, &result, &er)
	if e != nil {
		return j, e
	}
	decoder := json.NewDecoder(strings.NewReader(req))
	decoder.UseNumber()
	if e = decoder.Decode(&j.Request); e != nil {
		return j, e
	}
	if result.Valid {
		j.Result = json.RawMessage(result.String)
	}
	j.Error = er.String
	return j, nil
}

const jobSelect = `SELECT id,project_id,session_id,invocation_key,kind,state,request_json,result_json,error_text FROM plugin_jobs`

func (s *Server) enqueue(ctx context.Context, g Grant, kind string, a map[string]any) (any, error) {
	for k := range a {
		if strings.HasPrefix(k, "__") {
			return nil, errors.New("内部任务字段不允许由调用方指定")
		}
	}
	if kind == "activity_generate" {
		if e := validateActivityInput(a); e != nil {
			return nil, e
		}
	}
	key := str(a, "invocation_key")
	session := str(a, "session_id")
	if key == "" || len(key) > 160 {
		return nil, errors.New("invocation key required")
	}
	if kind == "batch_generate" {
		items, ok := a["items"].([]any)
		if !ok || len(items) == 0 || len(items) > 2000 {
			return nil, errors.New("batch requires 1 to 2000 items")
		}
		if str(a, "operation") == "" && s.Config.Model.Key == "" {
			return nil, errors.New("model connection not configured")
		}
		if str(a, "operation") == "" && str(a, "instruction") == "" {
			return nil, errors.New("fixed instruction required")
		}
	}
	if op := str(a, "operation"); op != "" {
		configured, ok := s.Config.Operations[op]
		if !ok || !contains(g.Operations, op) {
			return nil, errors.New("operation not granted")
		}
		if (kind == "batch_generate" || kind == "legacy_prepare") && configured.Effect != "prepare" {
			return nil, errors.New("batch operation must prepare only")
		}
	}
	copy := map[string]any{}
	for k, v := range a {
		copy[k] = v
	}
	a = copy
	a["__grant_id"] = grantIdentity(g)
	if op := str(a, "operation"); op != "" {
		a["__operation_hash"] = hash(encode(s.Config.Operations[op]))
	}
	raw := encode(a)
	fp := hash(kind + raw)
	id := randomID()
	now := time.Now().UnixMilli()
	_, e := s.DB.ExecContext(ctx, `INSERT INTO plugin_jobs(id,project_id,session_id,invocation_key,kind,input_hash,request_json,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'queued',?,?) ON DUPLICATE KEY UPDATE id=id`, id, g.ProjectID, session, key, kind, fp, raw, now, now)
	if e != nil {
		return nil, acceptanceUnknown{errors.New("could not confirm plugin job persistence")}
	}
	var got string
	if e = s.DB.QueryRowContext(ctx, `SELECT input_hash FROM plugin_jobs WHERE project_id=? AND session_id=? AND invocation_key=?`, g.ProjectID, session, key).Scan(&got); e != nil {
		return nil, acceptanceUnknown{e}
	}
	if got != fp {
		return nil, errors.New("invocation key reused with different input")
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return s.jobControl(ctx, g, "lookup", a)
}
func (s *Server) jobControl(ctx context.Context, g Grant, action string, a map[string]any) (any, error) {
	var j job
	var e error
	if action == "lookup" {
		j, e = scanJob(s.DB.QueryRowContext(ctx, jobSelect+` WHERE project_id=? AND session_id=? AND invocation_key=?`, g.ProjectID, str(a, "session_id"), str(a, "invocation_key")))
	} else {
		j, e = scanJob(s.DB.QueryRowContext(ctx, jobSelect+` WHERE id=? AND project_id=? AND session_id=?`, taskRef(a), g.ProjectID, str(a, "session_id")))
	}
	if e == sql.ErrNoRows {
		return map[string]any{"status": "not_found", "summary": "此调用编号尚未受理"}, nil
	}
	if e != nil {
		return nil, errors.New("plugin status unavailable")
	}
	if str(j.Request, "__grant_id") != grantIdentity(g) {
		return nil, errors.New("job belongs to another installation grant")
	}
	if action == "retry" {
		if j.Kind == "legacy_action" || j.Kind == "activity_dispatch" {
			return nil, errors.New("原动作不能自动重试，须核对原回执")
		}
		if j.State != "failed" && j.State != "cancelled" {
			return nil, errors.New("仅失败或已停止任务可继续")
		}
		tx, er := s.DB.BeginTx(ctx, nil)
		if er != nil {
			return nil, er
		}
		defer tx.Rollback()
		var state string
		if er = tx.QueryRowContext(ctx, "SELECT state FROM plugin_jobs WHERE id=? FOR UPDATE", j.ID).Scan(&state); er != nil {
			return nil, er
		}
		if state != "failed" && state != "cancelled" {
			return nil, errors.New("任务状态已改变，请刷新")
		}
		if j.Kind == "activity_generate" {
			var activityState string
			er = tx.QueryRowContext(ctx, "SELECT state FROM plugin_activities WHERE id=? FOR UPDATE", j.ID).Scan(&activityState)
			if er != nil && er != sql.ErrNoRows {
				return nil, er
			}
			if activityState == "cancelled" || activityState == "awaiting_review" || activityState == "approving" || activityState == "sent" {
				return nil, errors.New("活动已冻结或停止，不能补跑旧授权")
			}
			if _, er = tx.ExecContext(ctx, "UPDATE plugin_items SET result_json=NULL WHERE job_id=? AND state='failed'", j.ID); er != nil {
				return nil, er
			}
		}
		if _, er = tx.ExecContext(ctx, "UPDATE plugin_items SET attempts=0,state='queued' WHERE job_id=? AND state<>'succeeded'", j.ID); er != nil {
			return nil, er
		}
		if _, er = tx.ExecContext(ctx, "UPDATE plugin_jobs SET state='queued',cancel_requested=FALSE,error_text=NULL,updated_at=? WHERE id=?", time.Now().UnixMilli(), j.ID); er != nil {
			return nil, er
		}
		if er = tx.Commit(); er != nil {
			return nil, er
		}
		j.State = "queued"
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	if action == "cancel" {
		if j.Kind == "activity_generate" {
			return s.cancelActivity(ctx, g, map[string]any{"task_ref": j.ID, "session_id": j.Session})
		}
		// Lock the current state so a claim cannot race cancellation or escape the
		// special boundary for an already issued external action.
		tx, er := s.DB.BeginTx(ctx, nil)
		if er != nil {
			return nil, er
		}
		defer tx.Rollback()
		var state string
		if er = tx.QueryRowContext(ctx, "SELECT state FROM plugin_jobs WHERE id=? FOR UPDATE", j.ID).Scan(&state); er != nil {
			return nil, er
		}
		if (j.Kind == "legacy_action" && (state == "running" || state == "unknown")) || j.Kind == "activity_dispatch" {
			return map[string]any{"job_ref": j.ID, "status": state, "summary": "原系统动作已发出，需先核对原回执，不能用取消推定未执行"}, nil
		}
		if _, er = tx.ExecContext(ctx, "UPDATE plugin_jobs SET cancel_requested=TRUE,state=CASE WHEN state='queued' THEN 'cancelled' ELSE state END,updated_at=? WHERE id=?", time.Now().UnixMilli(), j.ID); er != nil {
			return nil, er
		}
		if er = tx.Commit(); er != nil {
			return nil, er
		}
		j, e = scanJob(s.DB.QueryRowContext(ctx, jobSelect+` WHERE id=?`, j.ID))
		if e != nil {
			return nil, e
		}
	}
	summary := map[string]string{"queued": "后台任务已排队", "running": "后台任务执行中", "succeeded": "后台任务已完成", "failed": "任务失败，旧可用数据保留", "cancelled": "任务已停止", "unknown": "原动作结果未确认，请核查原回执"}[j.State]
	if j.Kind == "legacy_action" && j.State == "succeeded" {
		summary = "原操作请求已返回；实际完成或送达请核对原回执"
	}
	out := map[string]any{"job_ref": j.ID, "status": j.State, "summary": summary}
	if j.Kind == "batch_generate" || j.Kind == "activity_generate" {
		var total, done int
		if er := s.DB.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(state='succeeded'),0) FROM plugin_items WHERE job_id=?", j.ID).Scan(&total, &done); er == nil {
			out["progress"] = map[string]any{"total": total, "succeeded": done, "remaining": total - done}
			summary += fmt.Sprintf("；已完成%d/%d条", done, total)
			out["summary"] = summary
		}
	}
	if len(j.Result) > 0 {
		out["result"] = j.Result
	}
	if j.Error != "" {
		out["summary"] = summary + "；" + j.Error
	}
	return out, nil
}
func (s *Server) cancelled(ctx context.Context, id string) bool {
	var c bool
	e := s.DB.QueryRowContext(ctx, "SELECT cancel_requested FROM plugin_jobs WHERE id=?", id).Scan(&c)
	return e != nil || c
}

// The connection-bound lock prevents two process versions from executing jobs concurrently.
func (s *Server) Run(ctx context.Context) {
	conn, e := s.DB.Conn(ctx)
	if e != nil {
		return
	}
	defer conn.Close()
	var locked int
	if e = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", "plugin-worker:"+s.namespace).Scan(&locked); e != nil || locked != 1 {
		return
	}
	defer conn.ExecContext(context.Background(), "DO RELEASE_LOCK(?)", "plugin-worker:"+s.namespace)
	s.DB.ExecContext(ctx, "INSERT INTO plugin_worker_epoch VALUES(1,1) ON DUPLICATE KEY UPDATE epoch=epoch+1")
	if s.DB.QueryRowContext(ctx, "SELECT epoch FROM plugin_worker_epoch WHERE id=1").Scan(&s.epoch) != nil {
		return
	}
	s.DB.ExecContext(ctx, `UPDATE plugin_jobs SET state=CASE WHEN kind='legacy_action' THEN 'unknown' ELSE 'queued' END,updated_at=? WHERE state='running'`, time.Now().UnixMilli())
	s.active.Store(true)
	defer s.active.Store(false)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		var held int
		check, stop := context.WithTimeout(ctx, 3*time.Second)
		er := conn.QueryRowContext(check, "SELECT IS_USED_LOCK(?)=CONNECTION_ID()", "plugin-worker:"+s.namespace).Scan(&held)
		stop()
		if er != nil || held != 1 {
			return
		}
		var actual int64
		if s.DB.QueryRowContext(ctx, "SELECT epoch FROM plugin_worker_epoch WHERE id=1").Scan(&actual) != nil || actual != s.epoch {
			return
		}
		j, e := scanJob(s.DB.QueryRowContext(ctx, jobSelect+` WHERE state='queued' AND available_at<=? ORDER BY (kind='sync_contexts') DESC,updated_at LIMIT 1`, time.Now().UnixMilli()))
		if e == nil {
			s.execute(ctx, j)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}
func (s *Server) execute(parent context.Context, j job) {
	tx, er := s.DB.BeginTx(parent, nil)
	if er != nil {
		return
	}
	defer tx.Rollback()
	var global int64
	if er = tx.QueryRowContext(parent, "SELECT epoch FROM plugin_worker_epoch WHERE id=1 FOR UPDATE").Scan(&global); er != nil || global != s.epoch {
		return
	}
	res, er := tx.ExecContext(parent, "UPDATE plugin_jobs SET state='running',updated_at=?,lease_epoch=? WHERE id=? AND state='queued' AND cancel_requested=FALSE", time.Now().UnixMilli(), s.epoch, j.ID)
	if er != nil {
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return
	}
	if er = tx.Commit(); er != nil {
		return
	}
	var e error
	var result any
	switch j.Kind {
	case "activity_catalog", "activity_candidates":
		if _, ok := s.grantForJob(j); !ok {
			e = errors.New("原安装授权已撤销")
		} else if j.Kind == "activity_catalog" {
			result, e = s.activityCatalog(parent, j.Request)
		} else {
			result, e = s.activityCandidates(parent, j.Request)
		}
	case "activity_generate":
		result, e = s.generateActivity(parent, j)
	case "activity_dispatch":
		result, e = s.activityDispatch(parent, j)
	case "sync_contexts":
		if _, ok := s.grantForJob(j); !ok {
			e = errors.New("original grant removed")
		} else {
			result, e = s.syncContexts(parent, j)
		}
	case "batch_generate":
		result, e = s.generateBatch(parent, j)
	case "legacy_prepare":
		g, ok := s.grantForJob(j)
		if !ok {
			e = errors.New("original grant removed")
		} else {
			result, e = s.prepare(parent, g, str(j.Request, "operation"), object(j.Request, "parameters"))
		}
	case "legacy_action":
		g, ok := s.grantForJob(j)
		if !ok {
			e = errors.New("project grant removed")
		} else {
			result, e = s.upstream(parent, g, j.Request, true, true)
		}
	default:
		e = errors.New("unsupported job")
	}
	state := "succeeded"
	message := ""
	if e != nil {
		state = "failed"
		message = e.Error()
		if j.Kind == "legacy_action" {
			state = "unknown"
		}
	}
	availableAt := int64(0)
	if errors.Is(e, errActivityWait) {
		state = "queued"
		message = ""
		availableAt = time.Now().Add(30 * time.Second).UnixMilli()
	}
	if errors.Is(e, errYield) {
		state = "queued"
		message = ""
	}
	if parent.Err() != nil && j.Kind != "legacy_action" {
		state = "queued"
		message = "执行器停止，下次继续"
	}
	published := false
	if m, ok := result.(map[string]any); ok {
		published, _ = m["published"].(bool)
	}
	if s.cancelled(context.Background(), j.ID) && j.Kind != "legacy_action" && !published {
		state = "cancelled"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.DB.ExecContext(ctx, `UPDATE plugin_jobs SET state=?,result_json=?,error_text=?,updated_at=?,available_at=? WHERE id=? AND lease_epoch=? AND EXISTS(SELECT 1 FROM plugin_worker_epoch WHERE id=1 AND epoch=?)`, state, encode(result), message, time.Now().UnixMilli(), availableAt, j.ID, s.epoch, s.epoch)
}
func (s *Server) batchResults(ctx context.Context, g Grant, a map[string]any) (any, error) {
	j, e := scanJob(s.DB.QueryRowContext(ctx, jobSelect+` WHERE id=? AND project_id=? AND session_id=?`, taskRef(a), g.ProjectID, str(a, "session_id")))
	if e != nil || str(j.Request, "__grant_id") != grantIdentity(g) {
		return nil, errors.New("job not in current scope")
	}
	limit := number(a, "limit", 20)
	if limit < 1 || limit > 50 {
		limit = 20
	}
	offset := number(a, "offset", 0)
	if offset < 0 {
		return nil, errors.New("invalid offset")
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT item_key,state,result_json,error_text,attempts FROM plugin_items WHERE job_id=? ORDER BY item_key LIMIT ? OFFSET ?`, taskRef(a), limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []any{}
	pageBytes := 0
	for rows.Next() {
		var key, state string
		var result, err sql.NullString
		var attempts int
		if e = rows.Scan(&key, &state, &result, &err, &attempts); e != nil {
			return nil, e
		}
		item := map[string]any{"item_key": key, "state": state, "result": nullableJSON(result), "error": err.String, "attempts": attempts}
		size := len(encode(item))
		if len(out) > 0 && pageBytes+size > 700<<10 {
			break
		}
		pageBytes += size
		out = append(out, item)
	}
	return success(fmt.Sprintf("读取%d条批次结果", len(out)), map[string]any{"items": out, "next_offset": offset + len(out)}), rows.Err()
}

func (s *Server) checkJobEpoch(ctx context.Context, id string) error {
	var epoch, global int64
	var state string
	e := s.DB.QueryRowContext(ctx, "SELECT j.lease_epoch,j.state,w.epoch FROM plugin_jobs j JOIN plugin_worker_epoch w ON w.id=1 WHERE j.id=?", id).Scan(&epoch, &state, &global)
	if e != nil {
		return e
	}
	var cancelled bool
	if e = s.DB.QueryRowContext(ctx, "SELECT cancel_requested FROM plugin_jobs WHERE id=?", id).Scan(&cancelled); e != nil {
		return e
	}
	if epoch != s.epoch || global != s.epoch || state != "running" || cancelled {
		return errors.New("execution lease changed")
	}
	return nil
}

func (s *Server) grantForJob(j job) (Grant, bool) {
	for _, g := range s.Config.Grants {
		if g.ProjectID == j.Project && grantIdentity(g) == str(j.Request, "__grant_id") && contains(g.Actions, j.Kind) {
			if op := str(j.Request, "operation"); op != "" {
				if !contains(g.Operations, op) || hash(encode(s.Config.Operations[op])) != str(j.Request, "__operation_hash") {
					return Grant{}, false
				}
			}
			return g, true
		}
	}
	return Grant{}, false
}

func nullableJSON(v sql.NullString) any {
	if !v.Valid || !json.Valid([]byte(v.String)) {
		return nil
	}
	return json.RawMessage(v.String)
}

// job_ref is a host-owned protocol field. General tools accept task_ref as an
// explicit opaque reference and still verify project, session and installation.
func taskRef(a map[string]any) string {
	if v := str(a, "task_ref"); v != "" {
		return v
	}
	return str(a, "job_ref")
}
