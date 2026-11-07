package core

import "context"

type DelegationReport struct {
	RunID  string `json:"runId"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// The native Session is authoritative for assistant turns. This outbox only
// forwards its report; it does not claim a tool or external effect succeeded.
func (s *Store) ReportDelegation(ctx context.Context, p Principal, v DelegationReport) error {
	if len(v.Text) > 96000 || len(v.Reason) > 64 || v.Reason == "" {
		return ErrInvalid
	}
	r, e := s.Run(ctx, p, v.RunID)
	if e != nil {
		return e
	}
	if p != r.Principal || p.Kind != "project" || r.Operation != "dispatch" || r.NotifySession == "" {
		return ErrDenied
	}
	refs, e := s.SessionSources(ctx, p)
	if e != nil {
		return e
	}
	if e = AuthorizeAll(ctx, s, p, refs); e != nil {
		return e
	}
	checkpoint, e := s.FreezeCheckpoint(ctx, r, "delegation-report", Checkpoint{Refs: refs, Input: map[string]any{"text": v.Text, "reason": v.Reason}})
	if e != nil {
		return e
	}
	text, _ := checkpoint.Input["text"].(string)
	reason, _ := checkpoint.Input["reason"].(string)
	return s.Emit(ctx, RunEvent{ID: r.ID + "-report", SessionID: r.NotifySession, RunID: r.ID, Kind: "project.reported", Text: "目标项目助手已汇报本轮结果（不代替工具和渠道回执）：\n" + text, Details: map[string]any{"projectId": p.ProjectID, "sessionId": p.SessionID, "turnEnd": reason}})
}

func (s *Store) PendingDelegations(ctx context.Context, p Principal, after string) ([]Run, error) {
	if !p.IsOwner() {
		return nil, ErrDenied
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT r.body FROM wb_runs r WHERE r.id>? AND JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.operation'))='dispatch' AND NOT EXISTS(SELECT 1 FROM wb_events e WHERE e.id=CONCAT(r.id,'-report')) ORDER BY r.id LIMIT 50`, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, e := scanJSON[Run](rows)
		if e != nil {
			return nil, e
		}
		out = append(out, Run{ID: r.ID, Principal: r.Principal})
	}
	return out, rows.Err()
}
