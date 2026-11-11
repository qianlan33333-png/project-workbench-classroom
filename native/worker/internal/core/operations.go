package core

import (
	"context"
	"os"
	"strings"
	"workbench-native/internal/operations"
)

func (s *Store) CleanupOperationsSources(ctx context.Context, run Run) error {
	if run.Operation != "data_refresh" {
		return nil
	}
	frozen, e := s.Checkpoint(ctx, run, "operations-input")
	if e != nil {
		return e
	}
	paths := map[string]bool{}
	for _, ref := range frozen.Refs {
		snapshot, e := s.Snapshot(ctx, ref.ResourceID, ref.Version)
		if e != nil {
			return e
		}
		if snapshot.State != "published" {
			return ErrConflict
		}
		resource, e := s.Resource(ctx, ref.ResourceID)
		if e != nil {
			return e
		}
		if resource.ConnectionID == "" {
			continue
		}
		connection, e := s.sourceConnection(ctx, resource)
		if e != nil {
			return e
		}
		if connection.Kind == "operations-original" {
			paths[os.Getenv(connection.SecretEnv)] = true
		}
	}
	for path := range paths {
		if e = operations.CleanupCollected(ctx, path, run.ID); e != nil {
			return e
		}
	}
	return nil
}

// OperationsSpec contains only the inputs of the two deterministic tools.
type OperationsSpec struct {
	Review       *OperationsDrafts   `json:"review,omitempty"`
	StrategyKeys []string            `json:"strategyKeys,omitempty"`
	Limit        int                 `json:"limit,omitempty"`
	Refresh      []ResourceSelection `json:"refresh,omitempty"`
	AsOf         string              `json:"asOf,omitempty"`
}

// TrimOperationsSelection preserves authoritative identity keys while ensuring
// the existing generator's bounded source is the globally highest priority list.
func (s *Store) TrimOperationsSelection(ctx context.Context, r Run) error {
	if r.Operation != "strategy_select" || r.Operations == nil {
		return ErrInvalid
	}
	if _, e := s.canWrite(ctx, r.Principal, r.Destination); e != nil {
		return e
	}
	limit := r.Operations.Limit
	if limit == 0 {
		return nil
	}
	if limit < 1 || limit > 5000 {
		return ErrInvalid
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guardWrite(ctx, tx); e != nil {
		return e
	}
	var state string
	if e = tx.QueryRowContext(ctx, `SELECT state FROM wb_snapshots WHERE resource_id=? AND version=? FOR UPDATE`, r.Destination, r.ID).Scan(&state); e != nil {
		return e
	}
	if state == "published" {
		return nil
	}
	rows, e := tx.QueryContext(ctx, `SELECT item_id FROM wb_records WHERE resource_id=? AND version=? ORDER BY CAST(COALESCE(JSON_EXTRACT(body,'$.priority'),0) AS SIGNED) DESC,item_id LIMIT 18446744073709551615 OFFSET ?`, r.Destination, r.ID, limit)
	if e != nil {
		return e
	}
	deferred := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		deferred = append(deferred, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for start := 0; start < len(deferred); start += 200 {
		ids := deferred[start:min(start+200, len(deferred))]
		marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		args := []any{r.ID}
		for _, id := range ids {
			args = append(args, id)
		}
		if _, e = tx.ExecContext(ctx, `UPDATE wb_items SET body=JSON_SET(body,'$.text','{"selected":false,"reason":"deferred_by_limit"}') WHERE run_id=? AND item_id IN (`+marks+`)`, args...); e != nil {
			return e
		}
		args = []any{r.Destination, r.ID}
		for _, id := range ids {
			args = append(args, id)
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM wb_records WHERE resource_id=? AND version=? AND item_id IN (`+marks+`)`, args...); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// References only; the final identities and text are read from these frozen runs.
type OperationsDrafts struct {
	Selection Ref   `json:"selection"`
	Drafts    []Ref `json:"drafts"`
}

func IsLocalOperations(r Run) bool {
	return r.Operation == "operations_package" && r.Shared["local"] == true
}
