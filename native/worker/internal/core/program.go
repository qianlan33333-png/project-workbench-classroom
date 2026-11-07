package core

import "context"

func (s *Store) RunSteps(ctx context.Context, p Principal, id string) ([]Run, error) {
	if _, e := s.Run(ctx, p, id); e != nil {
		return nil, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_runs WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.parentRunId'))=? ORDER BY created_at,id`, id)
	if e != nil {
		return nil, e
	}
	all := []Run{}
	for rows.Next() {
		r, err := scanJSON[Run](rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for i, r := range all {
		if _, e = s.Run(ctx, p, r.ID); e != nil {
			return nil, e
		}
		if e = s.RememberRunRead(ctx, p, r); e != nil {
			return nil, e
		}
		all[i].Method.Instructions = ""
	}
	return all, nil
}

func (s *Store) PinRunSources(ctx context.Context, p Principal, id string, refs []Ref) error {
	if _, e := s.Run(ctx, p, id); e != nil {
		return e
	}
	if e := AuthorizeAll(ctx, s, p, refs); e != nil {
		return e
	}
	for _, ref := range UnionRefs(refs) {
		if _, e := s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_run_sources VALUES(?,?,?,?)`, id, ref.ResourceID, ref.Version, ref.Section); e != nil {
			return e
		}
	}
	return nil
}
