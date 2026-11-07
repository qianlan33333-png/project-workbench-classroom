package core

import "context"

// Related runs stay within the original program or the dedicated Session
// created for one delegation. Never infer membership from a model-written goal.
func (s *Store) RelatedRuns(ctx context.Context, p Principal, id string) ([]Run, bool, error) {
	r, e := s.Run(ctx, p, id)
	if e != nil {
		return nil, false, e
	}
	query := `JSON_UNQUOTE(JSON_EXTRACT(body,'$.parentRunId'))=?`
	args := []any{id}
	if r.Operation == "dispatch" {
		query += ` OR (session_id=? AND id<>? AND JSON_EXTRACT(body,'$.parentRunId') IS NULL)`
		args = append(args, r.Principal.SessionID, id)
	}
	// Monitoring is not delegated business work; including followups would make
	// them wait on themselves or one another and consume the result bound.
	rows, e := s.DB.QueryContext(ctx, `SELECT id FROM wb_runs WHERE (`+query+`) AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))<>'followup' ORDER BY created_at,id LIMIT 21`, args...)
	if e != nil {
		return nil, false, e
	}
	ids := []string{}
	for rows.Next() {
		var item string
		if e = rows.Scan(&item); e != nil {
			rows.Close()
			return nil, false, e
		}
		ids = append(ids, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, false, e
	}
	truncated := len(ids) > 20
	if truncated {
		ids = ids[:20]
	}
	result := []Run{}
	for _, item := range ids {
		v, err := s.Run(ctx, p, item)
		if err != nil {
			return nil, false, err
		}
		if err = s.RememberRunRead(ctx, p, v); err != nil {
			return nil, false, err
		}
		v.Method.Instructions = ""
		result = append(result, v)
	}
	return result, truncated, nil
}
