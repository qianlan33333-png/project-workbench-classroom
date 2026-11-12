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
	for _, target := range v.Targets {
		if len(target.Target) != 2 || target.Target["conversationId"] == "" || target.Target["actorId"] == "" {
			return ErrInvalid
		}
		var count int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_message_routes r JOIN wb_transports t ON t.id=r.connection_id
			WHERE r.connection_id=? AND r.conversation_id=? AND r.actor_id=? AND JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.principal.kind'))='personal'
			AND JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.principal.sessionId'))=? AND JSON_EXTRACT(r.body,'$.enabled')=TRUE AND JSON_EXTRACT(t.body,'$.enabled')=TRUE`,
			target.ConnectionID, target.Target["conversationId"], target.Target["actorId"], v.SessionID).Scan(&count); e != nil {
			return e
		}
		if count != 1 {
			return ErrDenied
		}
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

// Switching accounts requires a new destination choice. Prior deliveries keep
// their pinned transport for receipt reconciliation.
func (s *Store) RetirePersonalTransport(ctx context.Context, p Principal, id, replacement string) error {
	if !p.IsOwner() {
		return ErrDenied
	}
	if !ValidID(id) || !ValidID(replacement) || id == replacement {
		return ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_settings VALUES('notifications',?)`, jsonText(NotificationSettings{})); err != nil {
		return err
	}
	settings, err := scanJSON[NotificationSettings](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id='notifications' FOR UPDATE`))
	if err != nil {
		return err
	}
	old, err := scanJSON[Transport](tx.QueryRowContext(ctx, `SELECT body FROM wb_transports WHERE id=? FOR UPDATE`, id))
	if err != nil {
		return err
	}
	next, err := scanJSON[Transport](tx.QueryRowContext(ctx, `SELECT body FROM wb_transports WHERE id=? FOR SHARE`, replacement))
	if err != nil {
		return err
	}
	if (next.Kind != "feishu" && next.Kind != "wechat") || (old.Kind != "" && old.Kind != next.Kind) || !next.Enabled || len(old.Projects) != 0 || len(next.Projects) != 0 {
		return ErrDenied
	}
	var personal, other int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(IF(connection_id=?
		AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.kind'))='personal',1,NULL)),
		COUNT(IF(JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.kind'))<>'personal',1,NULL))
		FROM wb_message_routes WHERE connection_id IN (?,?)`, replacement, id, replacement).Scan(&personal, &other); err != nil {
		return err
	}
	if personal != 1 || other != 0 {
		return ErrDenied
	}
	if _, err = tx.ExecContext(ctx, `UPDATE wb_message_routes SET body=JSON_SET(body,'$.enabled',CAST('true' AS JSON))
		WHERE connection_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.kind'))='personal'`, replacement); err != nil {
		return err
	}
	old.Enabled = false
	if _, err = tx.ExecContext(ctx, `UPDATE wb_transports SET body=? WHERE id=?`, jsonText(old), id); err != nil {
		return err
	}
	kept := settings.Targets[:0]
	for _, target := range settings.Targets {
		if target.ConnectionID != id {
			kept = append(kept, target)
		}
	}
	settings.Targets = kept
	if _, err = tx.ExecContext(ctx, `UPDATE wb_settings SET body=? WHERE id='notifications'`, jsonText(settings)); err != nil {
		return err
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
