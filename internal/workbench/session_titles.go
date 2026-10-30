package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

// A useful label is available before routing or model execution starts.
func initialSessionTitle(text string) string {
	words := strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
	if len(words) > 8 {
		words = words[:8]
	}
	title := []rune(strings.Join(words, " "))
	if len(title) > 24 {
		title = append(title[:24], '…')
	}
	if len(title) == 0 {
		return "附件讨论"
	}
	return string(title)
}

// Activate within the receipt transaction so a lost response cannot create an
// invisible accepted message. Concurrent first inputs preserve the first title.
func activateSession(tx *sql.Tx, id, text string) (*Session, error) {
	var raw string
	if err := tx.QueryRow("SELECT data FROM sessions WHERE id=?", id).Scan(&raw); err != nil {
		return nil, err
	}
	ss, err := decodeJSON[Session](raw)
	if err == nil && (ss.RecycledAt > 0 || ss.Lifecycle != "") {
		return nil, errors.New("对话正在归档或已在回收站")
	}
	if err != nil || !ss.Draft {
		return nil, err
	}
	ss.Draft = false
	if ss.TitleSource == "pending" {
		ss.Title, ss.TitleSource = initialSessionTitle(text), "fallback"
	}
	ss.UpdatedAt = max(now(), ss.UpdatedAt+1)
	_, err = tx.Exec("UPDATE sessions SET data=?,updated_at=? WHERE id=?", encode(ss), ss.UpdatedAt, id)
	return &ss, err
}

// Only the native provider's first title may replace our initial label. A manual
// rename, including one racing this callback, always wins. Legacy/default
// assistants keep their existing names.
func (s *Store) ApplySessionTitle(id string, data json.RawMessage) error {
	var event struct {
		Title  string `json:"title"`
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
	}
	if json.Unmarshal(data, &event) != nil || event.Source.Kind != "provider" {
		return nil
	}
	title := strings.Join(strings.Fields(event.Title), " ")
	if title == "" || len([]rune(title)) > 80 {
		return nil
	}
	var changed *Session
	err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRow("SELECT data FROM sessions WHERE id=?", id).Scan(&raw); e != nil {
			return e
		}
		ss, e := decodeJSON[Session](raw)
		if e != nil {
			return e
		}
		if ss.Kind != "topic" || ss.Draft || ss.TitleSource != "fallback" {
			return nil
		}
		ss.Title, ss.TitleSource = title, "ai"
		ss.UpdatedAt = max(now(), ss.UpdatedAt+1)
		if _, e = tx.Exec("UPDATE sessions SET data=?,updated_at=? WHERE id=?", encode(ss), ss.UpdatedAt, id); e != nil {
			return e
		}
		changed = &ss
		return nil
	})
	if err == nil && changed != nil {
		return s.Emit(id, "", "session.updated", changed)
	}
	return err
}
