package core

import (
	"context"
	"encoding/json"
	"errors"
	"time"
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

type CatalogRun struct {
	Run
	CreatedAt time.Time
}

func (s *Store) RunCatalog(ctx context.Context, p Principal, exclude string) ([]CatalogRun, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	q := `SELECT body, created_at FROM wb_runs WHERE JSON_EXTRACT(body,'$.parentRunId') IS NULL`
	args := []any{}
	if p.Kind == "project" {
		q += ` AND project_id=? AND session_id=?`
		args = append(args, p.ProjectID, p.SessionID)
	}
	if exclude != "" {
		q += ` AND id<>?`
		args = append(args, exclude)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT 100`
	rows, e := s.DB.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	all := []CatalogRun{}
	for rows.Next() {
		var item CatalogRun
		var raw []byte
		if e := rows.Scan(&raw, &item.CreatedAt); e != nil {
			return nil, e
		}
		if e := json.Unmarshal(raw, &item.Run); e != nil {
			return nil, e
		}
		all = append(all, item)
	}
	if e := rows.Err(); e != nil {
		return nil, e
	}
	return all, nil
}

func (s *Store) RunOverviewFacts(ctx context.Context, p Principal, r Run) (RunStats, *DeliveryStats, error) {
	if !r.visibleTo(p) {
		return RunStats{}, nil, ErrDenied
	}
	if e := s.RememberRunRead(ctx, p, r); e != nil {
		return RunStats{}, nil, e
	}
	stats, e := s.runStats(ctx, p, r)
	if e != nil {
		return RunStats{}, nil, e
	}
	switch r.Operation {
	case "dispatch_batch", "message", "reply":
		delivery, e := s.runDeliveryStats(ctx, r.ID)
		return stats, &delivery, e
	default:
		return stats, nil, nil
	}
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
	visible := out[:0]
	for _, d := range out {
		r, e := scanJSON[Run](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, d.RunID))
		if e == nil {
			e = s.RememberRunRead(ctx, p, r)
		}
		if errors.Is(e, ErrDenied) || IsMissing(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		visible = append(visible, d)
	}
	return visible, nil
}
