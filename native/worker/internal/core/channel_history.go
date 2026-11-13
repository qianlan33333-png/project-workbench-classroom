package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

type ChannelEntry struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Text    string    `json:"text"`
	State   string    `json:"state"`
	Failure string    `json:"failure,omitempty"`
	At      time.Time `json:"at"`
	RunID   string    `json:"runId,omitempty"`
	Key     string    `json:"key"`
}
type ChannelPage struct {
	Items  []ChannelEntry `json:"items"`
	Before string         `json:"before,omitempty"`
}
type channelCursor struct {
	At  time.Time `json:"at"`
	Key string    `json:"key"`
}

func (s *Store) ChannelHistory(ctx context.Context, p Principal, session string) ([]ChannelEntry, error) {
	v, e := s.channelPage(ctx, p, session, "", 100)
	return v.Items, e
}
func (s *Store) ChannelTimeline(ctx context.Context, p Principal, session, before string) (ChannelPage, error) {
	return s.channelPage(ctx, p, session, before, 50)
}
func (s *Store) channelPage(ctx context.Context, p Principal, session, before string, limit int) (ChannelPage, error) {
	out := ChannelPage{Items: []ChannelEntry{}}
	if !p.IsOwner() || !ValidID(session) {
		return out, ErrDenied
	}
	route, e := scanJSON[MessageRoute](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=?`, session))
	if e != nil {
		return out, e
	}
	var cursor channelCursor
	if before != "" {
		if len(before) > 800 {
			return out, ErrInvalid
		}
		b, e := base64.RawURLEncoding.DecodeString(before)
		if e != nil || json.Unmarshal(b, &cursor) != nil || cursor.At.IsZero() || len(cursor.Key) > 200 {
			return out, ErrInvalid
		}
	}
	bound := func(stamp, key string, args []any) (string, []any) {
		if before == "" {
			return "", args
		}
		return " AND (" + stamp + "<? OR (" + stamp + "=? AND " + key + "<?))", append(args, cursor.At, cursor.At, cursor.Key)
	}
	messageStamp := `CAST(LEFT(REPLACE(REPLACE(arrival_at,'T',' '),'Z',''),26) AS DATETIME(6))`
	clause, args := bound(messageStamp, "CONCAT('m:',JSON_UNQUOTE(JSON_EXTRACT(body,'$.id')))", []any{route.ID})
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_messages WHERE route_id=?`+clause+` ORDER BY `+messageStamp+` DESC,JSON_UNQUOTE(JSON_EXTRACT(body,'$.id')) DESC LIMIT ?`, append(args, limit+1)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		m, err := scanJSON[StoredMessage](rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		kind := "客户"
		if m.Kind == "human" {
			kind = "人工"
		}
		text := m.Text
		if text == "" {
			text = "原渠道未提供正文"
		}
		out.Items = append(out.Items, ChannelEntry{ID: m.ID, Key: "m:" + m.ID, Kind: kind, Text: text, State: m.State, Failure: m.Failure, At: m.CreatedAt.Truncate(time.Microsecond), RunID: m.ID})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	stamp := `CAST(LEFT(REPLACE(REPLACE(JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.createdAt')),'T',' '),'Z',''),26) AS DATETIME(6))`
	clause, args = bound(stamp, "CONCAT('d:',d.id)", []any{session})
	rows, e = s.DB.QueryContext(ctx, `SELECT d.body,r.body FROM wb_deliveries d JOIN wb_runs r ON r.id=d.run_id WHERE r.session_id=?`+clause+` ORDER BY `+stamp+` DESC,d.id DESC LIMIT ?`, append(args, limit+1)...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var a, b []byte
		if e = rows.Scan(&a, &b); e != nil {
			return out, e
		}
		var d Delivery
		var r Run
		if e = json.Unmarshal(a, &d); e != nil {
			return out, e
		}
		if e = json.Unmarshal(b, &r); e != nil {
			return out, e
		}
		if r.Operation == "control_notification" {
			continue
		}
		kind := "自动回复"
		if r.Operation == "owner_message" {
			kind = "人工"
		}
		origin := r.OriginMessage
		if origin == "" {
			origin = r.ID
		}
		out.Items = append(out.Items, ChannelEntry{ID: d.ID, Key: "d:" + d.ID, Kind: kind, Text: d.Text, State: d.State, At: d.CreatedAt.Truncate(time.Microsecond), RunID: origin})
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	sort.Slice(out.Items, func(i, j int) bool {
		if out.Items[i].At.Equal(out.Items[j].At) {
			return out.Items[i].Key < out.Items[j].Key
		}
		return out.Items[i].At.Before(out.Items[j].At)
	})
	if len(out.Items) > limit {
		out.Items = out.Items[len(out.Items)-limit:]
		b, _ := json.Marshal(channelCursor{out.Items[0].At, out.Items[0].Key})
		out.Before = base64.RawURLEncoding.EncodeToString(b)
	}
	return out, nil
}

// A light bounded refresh of already displayed rows; never reloads their text
// or starts an Agent. Both SQL branches remain bound to the selected Session.
func (s *Store) ChannelStates(ctx context.Context, p Principal, session string, keys []string) (map[string]string, error) {
	out := map[string]string{}
	if !p.IsOwner() || !ValidID(session) || len(keys) > 100 {
		return nil, ErrDenied
	}
	route, e := scanJSON[MessageRoute](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=?`, session))
	if e != nil {
		return nil, e
	}
	for _, prefix := range []string{"m:", "d:"} {
		ids := []any{}
		marks := []string{}
		for _, key := range keys {
			if len(key) < 3 || !ValidID(key[2:]) || (key[:2] != "m:" && key[:2] != "d:") {
				return nil, ErrInvalid
			}
			if key[:2] == prefix {
				ids = append(ids, key[2:])
				marks = append(marks, "?")
			}
		}
		if len(ids) == 0 {
			continue
		}
		q := `SELECT JSON_UNQUOTE(JSON_EXTRACT(body,'$.id')),JSON_UNQUOTE(JSON_EXTRACT(body,'$.state')) FROM wb_messages WHERE route_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.id')) IN (` + strings.Join(marks, ",") + `)`
		args := append([]any{route.ID}, ids...)
		if prefix == "d:" {
			q = `SELECT d.id,d.state FROM wb_deliveries d JOIN wb_runs r ON r.id=d.run_id WHERE r.session_id=? AND d.id IN (` + strings.Join(marks, ",") + `)`
			args[0] = session
		}
		rows, e := s.DB.QueryContext(ctx, q, args...)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id, state string
			if e = rows.Scan(&id, &state); e != nil {
				rows.Close()
				return nil, e
			}
			out[prefix+id] = state
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
