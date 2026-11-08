package core

import (
	"context"
	"encoding/json"
)

// CompareConnectorState gives installed adapters the same transactional CAS
// used elsewhere. No credential or arbitrary database access is exposed.
func (s *Store) CompareConnectorState(ctx context.Context, p Principal, id string, expected, value map[string]any) error {
	if !p.IsOwner() || !ValidID(id) || expected == nil || value == nil {
		return ErrDenied
	}
	if len(jsonText(expected)) > 256*1024 || len(jsonText(value)) > 256*1024 {
		return ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_connector_state VALUES(?,JSON_OBJECT())`, id); err != nil {
		return err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT body FROM wb_connector_state WHERE id=? FOR UPDATE`, id).Scan(&raw); err != nil {
		return err
	}
	var old map[string]any
	if err = json.Unmarshal(raw, &old); err != nil {
		return err
	}
	if Digest(old) != Digest(expected) {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE wb_connector_state SET body=? WHERE id=?`, jsonText(value), id); err != nil {
		return err
	}
	return tx.Commit()
}
