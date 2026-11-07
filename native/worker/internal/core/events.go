package core

import (
	"context"
	"encoding/json"
)

func (s *Store) PendingEvents(ctx context.Context, p Principal, after uint64) ([]RunEvent, error) {
	if !p.IsOwner() {
		return nil, ErrDenied
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT e.body,e.sequence FROM wb_events e WHERE e.delivered=FALSE AND e.sequence>? AND NOT EXISTS(SELECT 1 FROM wb_events older WHERE older.session_id=e.session_id AND older.delivered=FALSE AND older.sequence<e.sequence) ORDER BY e.sequence LIMIT 100`, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RunEvent{}
	for rows.Next() {
		var v RunEvent
		var b []byte
		var seq uint64
		if e = rows.Scan(&b, &seq); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		v.Sequence = seq
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	for i, v := range out {
		if v.RunID != "" {
			var target Principal
			target.SessionID = v.SessionID
			e = s.DB.QueryRowContext(ctx, `SELECT kind,project_id FROM wb_sessions WHERE id=?`, v.SessionID).Scan(&target.Kind, &target.ProjectID)
			if e == nil {
				var r Run
				r, e = s.Run(ctx, target, v.RunID)
				if e == nil {
					e = s.RememberRunRead(ctx, target, r)
				}
			}
			if e != nil {
				out[i].Details = nil
				out[i].Text = "此任务的来源资料已不可读，请检查引用范围。"
			}
		}
	}
	return out, nil
}
func (s *Store) AckEvent(ctx context.Context, p Principal, id string) error {
	if !p.IsOwner() {
		return ErrDenied
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE wb_events SET delivered=TRUE WHERE id=?`, id)
	return e
}
func (s *Store) RunCatalog(ctx context.Context, p Principal) ([]Run, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	q := `SELECT body FROM wb_runs WHERE JSON_EXTRACT(body,'$.parentRunId') IS NULL`
	args := []any{}
	if p.Kind == "project" {
		q += ` AND project_id=?`
		args = append(args, p.ProjectID)
	}
	q += ` ORDER BY created_at DESC LIMIT 100`
	rows, e := s.DB.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	all := []Run{}
	for rows.Next() {
		r, err := scanJSON[Run](rows)
		if err != nil {
			return nil, err
		}
		all = append(all, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []Run{}
	for _, r := range all {
		if _, e = s.Run(ctx, p, r.ID); e == nil {
			if e = s.RememberRunRead(ctx, p, r); e != nil {
				return nil, e
			}
			r.Method.Instructions = ""
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *Store) Decisions(ctx context.Context, p Principal) ([]Decision, error) {
	if !p.IsOwner() && p.Kind != "personal" {
		return nil, ErrDenied
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_decisions WHERE state='waiting' ORDER BY id LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		v, e := scanJSON[Decision](rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	rows.Close()
	for _, d := range out {
		r, e := s.Run(ctx, p, d.RunID)
		if e != nil {
			return nil, e
		}
		if e = s.RememberRunRead(ctx, p, r); e != nil {
			return nil, e
		}
	}
	return out, nil
}
