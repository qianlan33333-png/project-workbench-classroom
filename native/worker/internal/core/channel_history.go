package core

import (
	"context"
	"sort"
	"time"
)

// ChannelHistory is a bounded audit read inside the existing native Session.
// It includes inputs that were paused before an Agent turn, not a second chat.
type ChannelEntry struct {
	ID    string    `json:"id"`
	Kind  string    `json:"kind"`
	Text  string    `json:"text"`
	State string    `json:"state"`
	At    time.Time `json:"at"`
}

func (s *Store) ChannelHistory(ctx context.Context, p Principal, session string) ([]ChannelEntry, error) {
	if !p.IsOwner() || !ValidID(session) {
		return nil, ErrDenied
	}
	route, e := scanJSON[MessageRoute](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=?`, session))
	if e != nil {
		return nil, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_messages WHERE route_id=? ORDER BY arrival_at DESC,event_id DESC LIMIT 50`, route.ID)
	if e != nil {
		return nil, e
	}
	out := []ChannelEntry{}
	for rows.Next() {
		m, err := scanJSON[StoredMessage](rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		kind := "客户"
		if m.Kind == "human" {
			kind = "人工"
		}
		text := m.Text
		if text == "" {
			text = "原渠道未提供正文"
		}
		out = append(out, ChannelEntry{m.ID, kind, text, m.State, m.CreatedAt})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = s.DB.QueryContext(ctx, `SELECT d.body FROM wb_deliveries d JOIN wb_runs r ON r.id=d.run_id WHERE r.session_id=? ORDER BY JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.createdAt')) DESC,d.id DESC LIMIT 50`, session)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanJSON[Delivery](rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ChannelEntry{d.ID, "自动回复", d.Text, d.State, d.CreatedAt})
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].ID < out[j].ID
		}
		return out[i].At.Before(out[j].At)
	})
	return out, nil
}
