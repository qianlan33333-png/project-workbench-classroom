package core

import (
	"context"
	"database/sql"
	"fmt"
)

type runWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

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
	return pinRunSources(ctx, s.DB, id, refs)
}

func pinRunSources(ctx context.Context, writer runWriter, id string, refs []Ref) error {
	for _, ref := range UnionRefs(refs) {
		if _, e := writer.ExecContext(ctx, `INSERT IGNORE INTO wb_run_sources VALUES(?,?,?,?)`, id, ref.ResourceID, ref.Version, ref.Section); e != nil {
			return e
		}
	}
	return nil
}

func recordSyncPublication(ctx context.Context, tx *sql.Tx, r Run, snapshot Snapshot) error {
	refs := make([]Ref, 0, len(snapshot.Sections))
	for _, section := range snapshot.Sections {
		refs = append(refs, Ref{ResourceID: snapshot.ResourceID, Version: snapshot.Version, Section: section.ID})
	}
	if err := AuthorizeAll(ctx, resourceTransactionGate{tx}, r.Principal, refs); err != nil {
		return err
	}
	if err := pinRunSources(ctx, tx, r.ID, refs); err != nil {
		return err
	}
	event := RunEvent{ID: r.ID + "-published", SessionID: r.Principal.SessionID, RunID: r.ID,
		Kind: "resource.published", Text: fmt.Sprintf("资料已发布，共 %d 条。", snapshot.Count), Details: snapshot}
	return emitRunEvent(ctx, tx, event)
}
