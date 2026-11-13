package core

import (
	"context"
	"database/sql"
)

// A fence only protects commits from stale workers; Temporal owns scheduling.
type WriteFence struct {
	ProjectID   string
	Epoch       int64
	Execution   string
	SyncAttempt int32 `json:"SyncAttempt,omitempty"`
}
type fenceKey struct{}

func WithFence(ctx context.Context, v WriteFence) context.Context {
	return context.WithValue(ctx, fenceKey{}, v)
}
func (s *Store) BeginWrite(ctx context.Context, project, execution string) (WriteFence, error) {
	f := WriteFence{ProjectID: project, Execution: execution}
	if !ValidID(project) || !ValidID(execution) {
		return f, ErrInvalid
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return f, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_write_fences(project_id,epoch,execution,active) VALUES(?,0,'',FALSE)`, project); e != nil {
		return f, e
	}
	var currentExecution string
	var active bool
	if e = tx.QueryRowContext(ctx, `SELECT epoch,execution,active FROM wb_write_fences WHERE project_id=? FOR UPDATE`, project).Scan(&f.Epoch, &currentExecution, &active); e != nil {
		return f, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_write_attempts(execution,project_id) VALUES(?,?)`, execution, project); e != nil {
		return f, e
	}
	var attemptEpoch int64
	var closed bool
	var boundProject string
	if e = tx.QueryRowContext(ctx, `SELECT epoch,closed,project_id FROM wb_write_attempts WHERE execution=? FOR UPDATE`, execution).Scan(&attemptEpoch, &closed, &boundProject); e != nil {
		return f, e
	}
	if closed || boundProject != project {
		return f, ErrDenied
	}
	if attemptEpoch > 0 {
		if currentExecution != execution || !active || attemptEpoch != f.Epoch {
			return f, ErrDenied
		}
		return f, tx.Commit()
	}
	f.Epoch++
	if _, e = tx.ExecContext(ctx, `UPDATE wb_write_fences SET epoch=?,execution=?,active=TRUE,sync_attempt=0 WHERE project_id=?`, f.Epoch, execution, project); e != nil {
		return f, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE wb_write_attempts SET epoch=? WHERE execution=?`, f.Epoch, execution); e != nil {
		return f, e
	}
	return f, tx.Commit()
}

// SyncResource has one scheduled Activity per writer workflow. A higher
// Temporal attempt revokes the earlier attempt even if its worker is still live.
func (s *Store) BeginSyncAttempt(ctx context.Context, f WriteFence, attempt int32) (WriteFence, error) {
	if attempt < 1 || f.SyncAttempt != 0 || f.Epoch < 1 {
		return f, ErrDenied
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE wb_write_fences SET sync_attempt=?
		WHERE project_id=? AND epoch=? AND execution=? AND active=TRUE AND sync_attempt<?`, attempt, f.ProjectID, f.Epoch, f.Execution, attempt)
	if err != nil {
		return f, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return f, err
	}
	if count != 1 {
		return f, ErrDenied
	}
	f.SyncAttempt = attempt
	return f, nil
}

func (s *Store) EndWrite(ctx context.Context, f WriteFence) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Lock order is project, then execution, matching both acquisition and writes.
	if _, e = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_write_fences(project_id,epoch,execution,active) VALUES(?,0,'',FALSE)`, f.ProjectID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE wb_write_fences SET active=IF(execution=?,FALSE,active) WHERE project_id=?`, f.Execution, f.ProjectID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO wb_write_attempts(execution,project_id,closed) VALUES(?,?,TRUE) ON DUPLICATE KEY UPDATE closed=TRUE`, f.Execution, f.ProjectID); e != nil {
		return e
	}
	return tx.Commit()
}
func guardWrite(ctx context.Context, tx *sql.Tx) error {
	f, ok := ctx.Value(fenceKey{}).(WriteFence)
	if !ok {
		return nil
	}
	if f.ProjectID == "" || f.Epoch < 1 || f.Execution == "" {
		return ErrDenied
	}
	var epoch int64
	var execution string
	var active bool
	var attempt int32
	if e := tx.QueryRowContext(ctx, `SELECT epoch,execution,active,sync_attempt FROM wb_write_fences WHERE project_id=? FOR SHARE`, f.ProjectID).Scan(&epoch, &execution, &active, &attempt); e != nil {
		return e
	}
	if !active || epoch != f.Epoch || execution != f.Execution || attempt != f.SyncAttempt {
		return ErrDenied
	}
	return nil
}
