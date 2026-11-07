package core

import (
	"context"
	"fmt"
	"time"
)

type Decision struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
	RunID     string    `json:"runId"`
	Revision  int64     `json:"revision"`
	Digest    string    `json:"digest"`
	Question  string    `json:"question"`
	Answer    string    `json:"answer,omitempty"`
	State     string    `json:"state"`
}

func (s *Store) Answers(ctx context.Context, r Run) ([]Decision, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_decisions WHERE run_id=? AND state='answered' ORDER BY revision`, r.ID)
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

func (s *Store) Ask(ctx context.Context, r Run, revision int64, question string) (Decision, error) {
	if revision < 1 || question == "" || len(question) > 24000 {
		return Decision{}, ErrInvalid
	}
	d := Decision{CreatedAt: time.Now().UTC(), ID: fmt.Sprintf("%s-d%d", r.ID, revision), RunID: r.ID, Revision: revision, Digest: Digest(question), Question: question, State: "waiting"}
	_, e := s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_decisions(id,run_id,revision,content_digest,state,body) VALUES(?,?,?,?,?,?)`, d.ID, d.RunID, d.Revision, d.Digest, d.State, jsonText(d))
	if e != nil {
		return d, e
	}
	old, e := s.Decision(ctx, r.Principal, d.ID)
	if e != nil {
		return old, e
	}
	if old.Digest != d.Digest {
		return old, ErrConflict
	}
	if old.State == "waiting" {
		e = s.Emit(ctx, RunEvent{ID: d.ID + "-waiting", SessionID: r.Principal.SessionID, RunID: r.ID, Kind: "decision.waiting", Text: "需要本人决定后继续。", Details: map[string]any{"decisionId": d.ID}})
	}
	return old, e
}
func (s *Store) Decision(ctx context.Context, p Principal, id string) (Decision, error) {
	d, e := scanJSON[Decision](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_decisions WHERE id=?`, id))
	if e != nil {
		return d, e
	}
	r, e := s.Run(ctx, p, d.RunID)
	if e != nil {
		return Decision{}, e
	}
	e = s.RememberRunRead(ctx, p, r)
	return d, e
}

// Only the owner route accepts a human answer. Agent tools cannot confirm themselves.
func (s *Store) Answer(ctx context.Context, p Principal, input Decision) (Decision, error) {
	if !p.IsOwner() {
		return Decision{}, ErrDenied
	}
	if input.Answer == "" || len(input.Answer) > 8000 {
		return Decision{}, ErrInvalid
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Decision{}, e
	}
	defer tx.Rollback()
	d, e := scanJSON[Decision](tx.QueryRowContext(ctx, `SELECT body FROM wb_decisions WHERE id=? FOR UPDATE`, input.ID))
	if e != nil {
		return d, e
	}
	if d.Revision != input.Revision || d.Digest != input.Digest {
		return d, ErrConflict
	}
	if d.State == "answered" {
		if d.Answer != input.Answer {
			return d, ErrConflict
		}
		return d, nil
	}
	if d.State != "waiting" {
		return d, ErrConflict
	}
	d.Answer = input.Answer
	d.State = "answered"
	_, e = tx.ExecContext(ctx, `UPDATE wb_decisions SET state='answered',body=? WHERE id=?`, jsonText(d), d.ID)
	if e != nil {
		return d, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO wb_signal_outbox VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)`, d.ID, jsonText(d)); e != nil {
		return d, e
	}
	return d, tx.Commit()
}
