package core

import "context"

func (s *Store) ActualRunSources(ctx context.Context, id string) ([]Ref, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT resource_id,version,section_id FROM wb_run_sources WHERE run_id=?`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Ref{}
	for rows.Next() {
		var ref Ref
		if e = rows.Scan(&ref.ResourceID, &ref.Version, &ref.Section); e != nil {
			return nil, e
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
func (s *Store) PendingSignals(ctx context.Context, after string) ([]Decision, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_signal_outbox WHERE id>? ORDER BY id LIMIT 100`, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		d, e := scanJSON[Decision](rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) AckSignal(ctx context.Context, id string) error {
	_, e := s.DB.ExecContext(ctx, `DELETE FROM wb_signal_outbox WHERE id=?`, id)
	return e
}
