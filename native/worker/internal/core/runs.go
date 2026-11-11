package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Method struct {
	Name               string         `json:"name"`
	Instructions       string         `json:"instructions"`
	Digest             string         `json:"digest"`
	RequiredFields     []string       `json:"requiredFields"`
	MaxInputBytes      int            `json:"maxInputBytes"`
	MaxOutputTokens    int            `json:"maxOutputTokens"`
	ReasoningEffort    string         `json:"reasoningEffort,omitempty"`
	OutputSchema       map[string]any `json:"outputSchema,omitempty"`
	FixedOutputPointer string         `json:"fixedOutputPointer,omitempty"`
}
type Run struct {
	Methods        map[string]Method    `json:"methods,omitempty"`
	MethodRefs     map[string]RecordRef `json:"methodRefs,omitempty"`
	MethodPointer  string               `json:"methodPointer,omitempty"`
	Operations     *OperationsSpec      `json:"operations,omitempty"`
	Quiet          bool                 `json:"quiet,omitempty"`
	ExplicitInputs bool                 `json:"explicitInputs,omitempty"`
	Selection      *Selection           `json:"selection,omitempty"`
	TargetRunIDs   []string             `json:"targetRunIds,omitempty"`
	WorkflowID     string               `json:"workflowId,omitempty"`
	Dispatch       *DispatchPlan        `json:"dispatch,omitempty"`
	ParentRunID    string               `json:"parentRunId,omitempty"`
	Program        *PipelinePlan        `json:"program,omitempty"`
	ID             string               `json:"id"`
	Principal      Principal            `json:"principal"`
	Operation      string               `json:"operation"`
	Sources        []Ref                `json:"sources"`
	Lineage        []Ref                `json:"lineage,omitempty"`
	Destination    string               `json:"destination,omitempty"`
	Method         Method               `json:"method"`
	Shared         map[string]any       `json:"shared,omitempty"`
	Title          string               `json:"title"`
	Goal           string               `json:"goal,omitempty"`
	NotifySession  string               `json:"notifySession,omitempty"`
	OriginMessage  string               `json:"originMessage,omitempty"`
	ReplyParts     []string             `json:"replyParts,omitempty"`
}

func (r Run) ExecutionID() string {
	if r.WorkflowID != "" {
		return r.WorkflowID
	}
	return r.ID
}

// PipelinePlan configures the inputs of one ordinary Temporal SDK program. It
// is not an expression language or an arbitrary workflow graph.
type ResourceSelection struct {
	ResourceID string   `json:"resourceId"`
	Version    string   `json:"version,omitempty"`
	Sections   []string `json:"sections"`
	Refresh    bool     `json:"refresh"`
	Optional   bool     `json:"optional,omitempty"`
}
type PipelinePlan struct {
	Sources      []ResourceSelection `json:"sources"`
	LatestMethod *RecordRef          `json:"latestMethod,omitempty"`
	Knowledge    *Ref                `json:"knowledge,omitempty"`
}

// LoadMethod resolves a published method record and returns the exact source
// reference for the resulting run. A missing version means the current release.
func (s *Store) LoadMethod(ctx context.Context, p Principal, ref RecordRef) (Method, Ref, error) {
	var method Method
	if ref.Ref.Version == "" {
		resource, err := s.Resource(ctx, ref.Ref.ResourceID)
		if err != nil {
			return method, ref.Ref, err
		}
		ref.Ref.Version = resource.CurrentVersion
	}
	records, err := s.ReadIDs(ctx, p, ref.Ref, []string{ref.ID})
	if err != nil {
		return method, ref.Ref, err
	}
	if len(records) != 1 {
		return method, ref.Ref, ErrInvalid
	}
	body, err := json.Marshal(records[0].Fields["method"])
	if err != nil {
		return method, ref.Ref, err
	}
	if err = json.Unmarshal(body, &method); err != nil {
		return method, ref.Ref, err
	}
	if method.Instructions == "" || len(method.Instructions) > 64000 {
		return method, ref.Ref, ErrInvalid
	}
	if _, err = method.ResolveOutput(); err != nil {
		return method, ref.Ref, err
	}
	method.Digest = ""
	method.Digest = Digest(method)
	return method, ref.Ref, nil
}

type ItemResult struct {
	CreatedAt time.Time      `json:"createdAt"`
	ID        string         `json:"id"`
	State     string         `json:"state"`
	Text      string         `json:"text,omitempty"`
	Error     string         `json:"error,omitempty"`
	InputHash string         `json:"inputHash"`
	Usage     map[string]int `json:"usage,omitempty"`
}
type RunStats struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Pending   int `json:"pending"`
}
type RunEvent struct {
	CreatedAt time.Time `json:"createdAt"`
	Sequence  uint64    `json:"sequence"`
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	RunID     string    `json:"runId"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	Details   any       `json:"details,omitempty"`
}

func (s *Store) SaveRun(ctx context.Context, r Run) error {
	if !ValidID(r.ID) || r.Title == "" {
		return ErrInvalid
	}
	if e := r.Principal.Validate(); e != nil {
		return e
	}
	if e := s.BindSession(ctx, r.Principal); e != nil {
		return e
	}
	// Re-submission compares the caller's plan with the original plan. Later
	// reads in the Session must not change an already accepted operation key.
	old, previousErr := scanJSON[Run](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, r.ID))
	if previousErr == nil {
		candidate := r
		candidate.Lineage = old.Lineage
		if candidate.NotifySession == "" {
			candidate.NotifySession = old.NotifySession
		}
		if candidate.OriginMessage == "" {
			candidate.OriginMessage = old.OriginMessage
		}
		if Digest(candidate) != Digest(old) {
			return ErrConflict
		}
		_, e := s.Run(ctx, r.Principal, r.ID)
		return e
	}
	if !IsMissing(previousErr) {
		return previousErr
	}
	if r.Principal.Kind == "project" && r.Operation != "dispatch" && r.NotifySession == "" {
		parent, e := scanJSON[Run](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE session_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='dispatch' ORDER BY created_at LIMIT 1`, r.Principal.SessionID))
		if e == nil {
			r.NotifySession = parent.NotifySession
			r.OriginMessage = parent.OriginMessage
		} else if !IsMissing(e) {
			return e
		}
	}
	if r.Principal.Kind == "project" && r.NotifySession == "" {
		var config struct {
			SessionID string `json:"sessionId"`
		}
		var raw []byte
		e := s.DB.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id='personal'`).Scan(&raw)
		if e == nil {
			if e = json.Unmarshal(raw, &config); e != nil {
				return e
			}
			r.NotifySession = config.SessionID
		} else if !IsMissing(e) {
			return e
		}
	}
	// Independent programs have a complete input contract and never consume
	// chat history. Keep their actual dependencies without inheriting earlier reads.
	if !r.ExplicitInputs {
		lineage, e := s.SessionSources(ctx, r.Principal)
		if e != nil {
			return e
		}
		r.Lineage = UnionRefs(r.Lineage, lineage)
	}
	if e := AuthorizeAll(ctx, s, r.Principal, UnionRefs(r.Sources, r.Lineage)); e != nil {
		return e
	}
	if r.Destination != "" {
		if _, e := s.canWrite(ctx, r.Principal, r.Destination); e != nil {
			return e
		}
	}
	digest := Digest(r)
	_, e := s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_runs(id,project_id,session_id,input_digest,body) VALUES(?,?,?,?,?)`, r.ID, r.Principal.ProjectID, r.Principal.SessionID, digest, jsonText(r))
	if e != nil {
		return e
	}
	var existing string
	e = s.DB.QueryRowContext(ctx, `SELECT input_digest FROM wb_runs WHERE id=?`, r.ID).Scan(&existing)
	if e != nil {
		return e
	}
	if existing != digest {
		return ErrConflict
	}
	return nil
}
func (s *Store) Run(ctx context.Context, p Principal, id string) (Run, error) {
	if e := p.Validate(); e != nil {
		return Run{}, e
	}
	r, e := scanJSON[Run](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, id))
	if e != nil {
		return r, e
	}
	if p.Kind == "project" && (p.ProjectID != r.Principal.ProjectID || p.SessionID != r.Principal.SessionID) {
		return Run{}, ErrDenied
	}
	actual, e := s.ActualRunSources(ctx, r.ID)
	if e != nil {
		return Run{}, e
	}
	if e := AuthorizeAll(ctx, s, p, UnionRefs(r.Sources, r.Lineage, actual)); e != nil {
		return Run{}, e
	}
	return r, nil
}
func (s *Store) RecordItem(ctx context.Context, run string, v ItemResult) error {
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guardWrite(ctx, tx); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_items(run_id,item_id,state,body) VALUES(?,?,?,?)`, run, v.ID, v.State, jsonText(v))
	if e != nil {
		return e
	}
	old, e := scanJSON[ItemResult](tx.QueryRowContext(ctx, `SELECT body FROM wb_items WHERE run_id=? AND item_id=? FOR UPDATE`, run, v.ID))
	if e != nil {
		return e
	}
	if old.InputHash != v.InputHash {
		return ErrConflict
	}
	if old.State != "succeeded" {
		if _, e = tx.ExecContext(ctx, `UPDATE wb_items SET state=?,body=? WHERE run_id=? AND item_id=?`, v.State, jsonText(v), run, v.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Item(ctx context.Context, run, id string) (ItemResult, error) {
	return scanJSON[ItemResult](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_items WHERE run_id=? AND item_id=?`, run, id))
}
func (s *Store) Items(ctx context.Context, p Principal, run, after string) ([]ItemResult, error) {
	r, e := s.Run(ctx, p, run)
	if e != nil {
		return nil, e
	}
	if e = s.RememberRunRead(ctx, p, r); e != nil {
		return nil, e
	}
	if r.Operation == "pipeline" {
		steps, e := s.RunSteps(ctx, p, run)
		if e != nil {
			return nil, e
		}
		for _, step := range steps {
			if step.Operation == "generate" {
				return s.Items(ctx, p, step.ID, after)
			}
		}
		return []ItemResult{}, nil
	}
	query := `SELECT body FROM wb_items WHERE run_id=? AND item_id>?`
	if r.Operation == "operations_package" || r.Operation == "operations_batch_confirm" {
		// The review projection contains connector-only customer and material IDs.
		query += ` AND item_id NOT LIKE 'original-review-row-%' AND item_id NOT IN ('original-review','original-batch-result')`
	}
	rows, e := s.DB.QueryContext(ctx, query+` ORDER BY item_id LIMIT 200`, run, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ItemResult{}
	for rows.Next() {
		v, e := scanJSON[ItemResult](rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Stats(ctx context.Context, p Principal, run string) (RunStats, error) {
	r, e := s.Run(ctx, p, run)
	if e != nil {
		return RunStats{}, e
	}
	if e = s.RememberRunRead(ctx, p, r); e != nil {
		return RunStats{}, e
	}
	if r.Operation == "pipeline" {
		child, e := scanJSON[Run](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.parentRunId'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='generate' LIMIT 1`, r.ID))
		if IsMissing(e) {
			return RunStats{}, nil
		}
		if e != nil {
			return RunStats{}, e
		}
		return s.Stats(ctx, p, child.ID)
	}
	var stats RunStats
	if r.Operation == "generate" && len(r.Sources) > 0 {
		snap, e := s.Snapshot(ctx, r.Sources[0].ResourceID, r.Sources[0].Version)
		if e != nil {
			return stats, e
		}
		stats.Total = snap.Count
		if r.Selection != nil {
			info, err := s.SelectionInfo(ctx, p, r.Sources[0], r.Selection)
			if err != nil {
				return stats, err
			}
			stats.Total = info.SelectedCount
		}
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT state,COUNT(*) FROM wb_items WHERE run_id=? GROUP BY state`, run)
	if e != nil {
		return stats, e
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var n int
		if e = rows.Scan(&state, &n); e != nil {
			return stats, e
		}
		switch state {
		case "succeeded":
			stats.Succeeded = n
		case "failed":
			stats.Failed = n
		}
	}
	if r.Operation != "generate" {
		stats.Total = stats.Succeeded + stats.Failed
	}
	stats.Pending = max(0, stats.Total-stats.Succeeded-stats.Failed)
	return stats, rows.Err()
}
func (s *Store) Emit(ctx context.Context, e RunEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	child := false
	if e.RunID != "" {
		r, err := scanJSON[Run](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, e.RunID))
		if err != nil {
			return err
		}
		child = r.ParentRunID != ""
	}
	_, err := s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_events(id,session_id,run_id,body,delivered) VALUES(?,?,?,?,?)`, e.ID, e.SessionID, e.RunID, jsonText(e), child)
	if err != nil {
		return err
	}
	if child {
		return nil
	}
	if e.RunID != "" {
		r, readErr := scanJSON[Run](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, e.RunID))
		if readErr != nil {
			return readErr
		}
		if r.NotifySession != "" && r.NotifySession != e.SessionID {
			e.ID += "-notify"
			e.SessionID = r.NotifySession
			_, err = s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_events(id,session_id,run_id,body) VALUES(?,?,?,?)`, e.ID, e.SessionID, e.RunID, jsonText(e))
		}
	}
	return err
}
func (s *Store) Events(ctx context.Context, p Principal, after string) ([]RunEvent, error) {
	var cursor uint64
	if after != "" {
		var e error
		cursor, e = strconv.ParseUint(after, 10, 64)
		if e != nil {
			return nil, ErrInvalid
		}
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body,sequence FROM wb_events WHERE session_id=? AND sequence>? ORDER BY sequence LIMIT 200`, p.SessionID, cursor)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RunEvent{}
	for rows.Next() {
		var v RunEvent
		var raw []byte
		var seq uint64
		if e := rows.Scan(&raw, &seq); e != nil {
			return nil, e
		}
		if e := json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		v.Sequence = seq

		out = append(out, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for i, v := range out {
		if v.RunID != "" {
			r, err := s.Run(ctx, p, v.RunID)
			if err == nil {
				err = s.RememberRunRead(ctx, p, r)
			}
			if err != nil {
				out[i].Text = "此任务的来源资料已不可读"
				out[i].Details = nil
			}
		}
	}
	return out, nil
}

// ExecutionRun is only used by the Worker. Stored identity is never supplied by the model.
func (s *Store) ExecutionRun(ctx context.Context, id string) (Run, error) {
	r, e := scanJSON[Run](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, id))
	if e != nil {
		return r, e
	}
	return s.Run(ctx, r.Principal, id)
}

type Memory struct {
	Sources   []Ref  `json:"sources,omitempty"`
	ID        string `json:"id"`
	Revision  int64  `json:"revision"`
	Text      string `json:"text"`
	Source    string `json:"source"`
	Confirmed bool   `json:"confirmed"`
	Forgotten bool   `json:"forgotten"`
}

func (s *Store) Memories(ctx context.Context, p Principal) ([]Memory, error) {
	if p.Kind == "project" {
		return nil, ErrDenied
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body,revision FROM wb_memory WHERE forgotten=FALSE ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var b []byte
		var v Memory
		if e = rows.Scan(&b, &v.Revision); e != nil {
			return nil, e
		}
		rev := v.Revision
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		v.Revision = rev
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	rows.Close()
	for _, v := range out {
		if e = AuthorizeAll(ctx, s, p, v.Sources); e != nil {
			return nil, e
		}
		if e = s.RememberRead(ctx, p, v.Sources); e != nil {
			return nil, e
		}
	}
	return out, nil
}

// Memory returns the canonical revision, including a just-forgotten entry.
func (s *Store) Memory(ctx context.Context, p Principal, id string) (Memory, error) {
	if p.Kind != "owner" && p.Kind != "personal" {
		return Memory{}, ErrDenied
	}
	var raw []byte
	var v Memory
	if e := s.DB.QueryRowContext(ctx, `SELECT body,revision FROM wb_memory WHERE id=?`, id).Scan(&raw, &v.Revision); e != nil {
		return v, e
	}
	revision := v.Revision
	if e := json.Unmarshal(raw, &v); e != nil {
		return v, e
	}
	v.Revision = revision
	if e := AuthorizeAll(ctx, s, p, v.Sources); e != nil {
		return Memory{}, e
	}
	if e := s.RememberRead(ctx, p, v.Sources); e != nil {
		return Memory{}, e
	}
	return v, nil
}
func (s *Store) PutMemory(ctx context.Context, p Principal, v Memory) error {
	if p.Kind != "owner" && p.Kind != "personal" {
		return ErrDenied
	}
	if !ValidID(v.ID) || v.Source == "" || len(v.Text) > 16000 {
		return ErrInvalid
	}
	if p.Kind == "personal" {
		v.Confirmed = false
		refs, e := s.SessionSources(ctx, p)
		if e != nil {
			return e
		}
		v.Sources = UnionRefs(v.Sources, refs)
	}
	if v.Revision > 0 {
		old, e := scanJSON[Memory](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_memory WHERE id=?`, v.ID))
		if e != nil {
			return e
		}
		v.Sources = UnionRefs(v.Sources, old.Sources)
	}
	if e := AuthorizeAll(ctx, s, p, v.Sources); e != nil {
		return e
	}
	if v.Revision == 0 {
		if v.Forgotten {
			return ErrInvalid
		}
		v.Revision = 1
		_, e := s.DB.ExecContext(ctx, `INSERT INTO wb_memory VALUES(?,1,FALSE,?)`, v.ID, jsonText(v))
		return e
	}
	res, e := s.DB.ExecContext(ctx, `UPDATE wb_memory SET revision=revision+1,forgotten=?,body=? WHERE id=? AND revision=? AND forgotten=FALSE`, v.Forgotten, jsonText(v), v.ID, v.Revision)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func PrepareInput(method Method, records []Record) (map[string]any, error) {
	out := map[string]any{}
	for _, r := range records {
		for k, v := range r.Fields {
			if old, ok := out[k]; ok && Digest(old) != Digest(v) {
				return nil, fmt.Errorf("conflicting field %q", k)
			}
			out[k] = v
		}
	}
	for _, f := range method.RequiredFields {
		if out[f] == nil || out[f] == "" {
			return nil, fmt.Errorf("missing required field %q", f)
		}
	}
	limit := method.MaxInputBytes
	if limit == 0 {
		limit = 24000
	}
	if limit < 256 || limit > 256*1024 {
		return nil, ErrInvalid
	}
	b, e := json.Marshal(out)
	if e != nil {
		return nil, e
	}
	// Never silently truncate required evidence. A smaller slice must be selected explicitly.
	if len(b) > limit {
		return nil, fmt.Errorf("context budget exceeded: %d > %d bytes", len(b), limit)
	}
	return out, nil
}
func IsMissing(e error) bool { return e == sql.ErrNoRows }

func (s *Store) PreparePage(ctx context.Context, r Run, after string) ([]string, string, error) {
	if len(r.Sources) == 0 {
		return nil, "", ErrInvalid
	}
	limit := 200
	if after == "" && r.Selection != nil {
		after = r.Selection.After
	}
	var first Page
	var e error
	if r.Selection != nil && len(r.Selection.RecordIDs) > 0 {
		ids := r.Selection.RecordIDs
		start := sort.Search(len(ids), func(i int) bool { return ids[i] > after })
		end := min(start+200, len(ids))
		first.Records, e = s.ReadIDs(ctx, r.Principal, r.Sources[0], ids[start:end])
		if e == nil && len(first.Records) != end-start {
			return nil, "", ErrInvalid
		}
		if end < len(ids) {
			first.Next = ids[end-1]
		}
	} else {
		if r.Selection != nil {
			prior := 0
			if after != "" {
				ref := r.Sources[0]
				if e = Authorize(ctx, s, r.Principal, ref); e != nil {
					return nil, "", e
				}
				if e = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_records WHERE resource_id=? AND version=? AND item_id<=?`, ref.ResourceID, ref.Version, after).Scan(&prior); e != nil {
					return nil, "", e
				}
				if r.Selection.After != "" {
					var offset int
					if e = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_records WHERE resource_id=? AND version=? AND item_id<=?`, ref.ResourceID, ref.Version, r.Selection.After).Scan(&offset); e != nil {
						return nil, "", e
					}
					prior -= offset
				}
			}
			limit = min(limit, r.Selection.Limit-prior)
			if limit <= 0 {
				return []string{}, "", nil
			}
		}
		first, e = s.Read(ctx, r.Principal, r.Sources[0], after, limit)
		if r.Selection != nil && limit < 200 {
			first.Next = ""
		}
	}
	if e != nil {
		return nil, "", e
	}
	ids := []string{}
	records := map[string][]Record{}
	for _, row := range first.Records {
		ids = append(ids, row.ID)
		records[row.ID] = []Record{row}
	}
	for _, ref := range r.Sources[1:] {
		rows, e := s.ReadIDs(ctx, r.Principal, ref, ids)
		if e != nil {
			return nil, "", e
		}
		for _, row := range rows {
			records[row.ID] = append(records[row.ID], row)
		}
	}
	if len(r.Shared) > 0 {
		for _, id := range ids {
			records[id] = append(records[id], Record{ID: id, Fields: r.Shared})
		}
	}
	if len(ids) > 0 {
		values := make([]string, len(ids))
		args := []any{}
		for i, id := range ids {
			values[i] = "(?,?,?)"
			args = append(args, r.ID, id, jsonText(records[id]))
		}
		tx, e := s.DB.BeginTx(ctx, nil)
		if e != nil {
			return nil, "", e
		}
		defer tx.Rollback()
		if e = guardWrite(ctx, tx); e != nil {
			return nil, "", e
		}
		if _, e = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_prepared VALUES `+strings.Join(values, ","), args...); e != nil {
			return nil, "", e
		}
		if e = tx.Commit(); e != nil {
			return nil, "", e
		}
	}

	return ids, first.Next, nil
}
func (s *Store) Prepared(ctx context.Context, run, id string) ([]Record, error) {
	return scanJSON[[]Record](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_prepared WHERE run_id=? AND item_id=?`, run, id))
}
