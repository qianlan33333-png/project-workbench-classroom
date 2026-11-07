package core

import (
	"context"
	"encoding/json"
)

type NotificationTarget struct {
	ConnectionID string            `json:"connectionId"`
	Target       map[string]string `json:"target"`
}
type NotificationSettings struct {
	Enabled   bool                 `json:"enabled"`
	SessionID string               `json:"sessionId"`
	Targets   []NotificationTarget `json:"targets"`
	After     uint64               `json:"after"`
}

func (s *Store) NotificationSettings(ctx context.Context) (NotificationSettings, error) {
	v, e := scanJSON[NotificationSettings](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id='notifications'`))
	if IsMissing(e) {
		e = nil
	}
	return v, e
}
func (s *Store) PutNotificationSettings(ctx context.Context, p Principal, v NotificationSettings) error {
	if !p.IsOwner() {
		return ErrDenied
	}
	if !ValidID(v.SessionID) || len(v.Targets) > 3 {
		return ErrInvalid
	}
	var kind string
	if e := s.DB.QueryRowContext(ctx, `SELECT kind FROM wb_sessions WHERE id=?`, v.SessionID).Scan(&kind); e != nil || kind != "personal" {
		return ErrDenied
	}
	for _, target := range v.Targets {
		if len(target.Target) != 2 || target.Target["conversationId"] == "" || target.Target["actorId"] == "" {
			return ErrInvalid
		}
		if _, e := s.Transport(ctx, Principal{Kind: "personal", SessionID: v.SessionID}, target.ConnectionID); e != nil {
			return e
		}
		var count int
		if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_message_routes WHERE connection_id=? AND conversation_id=? AND actor_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.kind'))='personal' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=? AND JSON_EXTRACT(body,'$.enabled')=TRUE`, target.ConnectionID, target.Target["conversationId"], target.Target["actorId"], v.SessionID).Scan(&count); e != nil {
			return e
		}
		if count != 1 {
			return ErrDenied
		}
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_settings VALUES('notifications',?)`, jsonText(NotificationSettings{})); e != nil {
		return e
	}
	old, e := scanJSON[NotificationSettings](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id='notifications' FOR UPDATE`))
	if e != nil {
		return e
	}
	v.After = old.After
	if !old.Enabled {
		if e = tx.QueryRowContext(ctx, `SELECT CAST(GREATEST(?,COALESCE(MAX(sequence),0)) AS UNSIGNED) FROM wb_events`, old.After).Scan(&v.After); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE wb_settings SET body=? WHERE id='notifications'`, jsonText(v)); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) NotificationEvents(ctx context.Context, after uint64) ([]RunEvent, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT body,sequence FROM wb_events WHERE sequence>? ORDER BY sequence LIMIT 100`, after)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RunEvent{}
	for rows.Next() {
		var v RunEvent
		var b []byte
		if e = rows.Scan(&b, &v.Sequence); e != nil {
			return nil, e
		}
		seq := v.Sequence
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		v.Sequence = seq
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) AdvanceNotifications(ctx context.Context, after uint64) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE wb_settings SET body=JSON_SET(body,'$.after',GREATEST(CAST(JSON_UNQUOTE(JSON_EXTRACT(body,'$.after')) AS UNSIGNED),?)) WHERE id='notifications'`, after)
	return e
}
