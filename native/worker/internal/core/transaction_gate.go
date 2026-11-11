package core

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Lock the exact metadata used for a send claim, so a grant/configuration
// revocation and the irreversible claim have one database ordering.
type transactionGate struct{ tx *sql.Tx }

func (g transactionGate) Resource(ctx context.Context, id string) (Resource, error) {
	var out Resource
	var raw []byte
	var version string
	var revision int64
	err := g.tx.QueryRowContext(ctx, `SELECT body,current_version,revision FROM wb_resources WHERE id=? FOR SHARE`, id).Scan(&raw, &version, &revision)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	out.CurrentVersion, out.Revision = version, revision
	return out, nil
}
func (g transactionGate) Snapshot(ctx context.Context, id, version string) (Snapshot, error) {
	var state string
	v, e := scanJSON[Snapshot](g.tx.QueryRowContext(ctx, `SELECT body FROM wb_snapshots WHERE resource_id=? AND version=? FOR SHARE`, id, version))
	if e != nil {
		return v, e
	}
	e = g.tx.QueryRowContext(ctx, `SELECT state FROM wb_snapshots WHERE resource_id=? AND version=?`, id, version).Scan(&state)
	v.State = state
	return v, e
}
func (g transactionGate) Granted(ctx context.Context, id, project, section string) (bool, error) {
	rows, e := g.tx.QueryContext(ctx, `SELECT section_id FROM wb_grants WHERE resource_id=? AND project_id=? AND section_id IN ('*',?) FOR SHARE`, id, project, section)
	if e != nil {
		return false, e
	}
	defer rows.Close()
	allowed := rows.Next()
	return allowed, rows.Err()
}
