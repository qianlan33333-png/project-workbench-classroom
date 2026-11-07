package core

import (
	"context"
	"time"
)

// One immutable model input per review round. A new round may read a newer
// snapshot, but an activity retry must use the same evidence and answer set.
type Checkpoint struct {
	Refs      []Ref          `json:"refs"`
	Input     map[string]any `json:"input"`
	CreatedAt time.Time      `json:"createdAt"`
}

func (s *Store) Checkpoint(ctx context.Context, r Run, key string) (Checkpoint, error) {
	v, e := scanJSON[Checkpoint](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_prepared WHERE run_id=? AND item_id=?`, r.ID, key))
	if e != nil {
		return v, e
	}
	for _, ref := range v.Refs {
		if e = Authorize(ctx, s, r.Principal, ref); e != nil {
			return Checkpoint{}, e
		}
		if _, e = s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_run_sources VALUES(?,?,?,?)`, r.ID, ref.ResourceID, ref.Version, ref.Section); e != nil {
			return Checkpoint{}, e
		}
	}
	return v, nil
}
func (s *Store) FreezeCheckpoint(ctx context.Context, r Run, key string, v Checkpoint) (Checkpoint, error) {
	v.CreatedAt = time.Now().UTC()
	_, e := s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_prepared VALUES(?,?,?)`, r.ID, key, jsonText(v))
	if e != nil {
		return Checkpoint{}, e
	}
	return s.Checkpoint(ctx, r, key)
}
