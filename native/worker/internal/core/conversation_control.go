package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ModeChange is channel-neutral. The installed connector supplies its own
// trusted staff identity, callback credentials and wire representation.
type ModeChange struct {
	ConnectionID   string `json:"connectionId"`
	ActorID        string `json:"actorId"`
	ExternalUserID string `json:"externalUserId,omitempty"`
	SessionID      string `json:"sessionId"`
	Version        int64  `json:"version"`
	Mode           string `json:"mode"`
}

type ConversationState struct {
	Mode      string `json:"mode"`
	Version   int64  `json:"version"`
	SessionID string `json:"sessionId"`
	ActorID   string `json:"actorId"`
}

type ConversationControl struct {
	Op             string `json:"op"`
	ConnectionID   string `json:"connectionId"`
	ExternalUserID string `json:"externalUserId"`
	ContactID      string `json:"contactId,omitempty"`
	SessionID      string `json:"sessionId,omitempty"`
	Version        int64  `json:"version,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
	Scope          string `json:"scope"`
	Actor          string `json:"actor,omitempty"`
}

func conversationState(r MessageRoute) ConversationState {
	mode := "human"
	if r.AutoReply {
		mode = "auto"
	}
	return ConversationState{mode, routeRevision(r), r.Principal.SessionID, r.ActorID}
}

func modeChangedTx(ctx context.Context, tx *sql.Tx, r *MessageRoute) error {
	if r.Principal.Kind != "project" {
		return nil
	}
	r.ModeChangedAt = time.Now().UTC()
	state := conversationState(*r)
	e := RunEvent{
		ID:        uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("conversation-mode/%s/%d", r.ID, state.Version))).String(),
		SessionID: r.Principal.SessionID,
		Kind:      "conversation.mode.changed",
		Text:      "客户会话模式已更新。",
		CreatedAt: r.ModeChangedAt,
		Details:   ModeChange{r.ConnectionID, r.ActorID, r.ExternalUserID, state.SessionID, state.Version, state.Mode},
	}
	_, err := tx.ExecContext(ctx, `INSERT IGNORE INTO wb_events(id,session_id,run_id,body) VALUES(?,?,?,?)`, e.ID, e.SessionID, "", jsonText(e))
	return err
}

// ControlConversation is called only by the authenticated installed adapter,
// never a model tool. No missing relationship is provisioned by this path.
func (s *Store) ControlConversation(ctx context.Context, p Principal, in ConversationControl) (ConversationState, error) {
	if !p.IsOwner() || !ValidID(in.ConnectionID) || in.ExternalUserID == "" || len(in.ExternalUserID) > 191 || len(in.ContactID) > 191 || in.Scope == "" || len(in.Scope) > 512 || (in.Op != "status" && in.Op != "resume") {
		return ConversationState{}, ErrDenied
	}
	if in.Op == "resume" {
		if _, err := uuid.Parse(in.RequestID); err != nil || !ValidID(in.SessionID) || in.Version < 1 || in.Actor == "" || len(in.Actor) > 191 {
			return ConversationState{}, ErrInvalid
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ConversationState{}, err
	}
	defer tx.Rollback()
	// Reuse the existing durable connector KV for request receipts. Its namespace
	// includes the authenticated binding scope; a duplicate never resumes twice.
	receiptID := "conversation-receipt-" + Digest([]string{in.Scope, in.ConnectionID, in.RequestID})
	type receipt struct {
		Digest string            `json:"digest"`
		State  ConversationState `json:"state"`
	}
	if in.Op == "resume" {
		if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_connector_state VALUES(?,JSON_OBJECT())`, receiptID); err != nil {
			return ConversationState{}, err
		}
		old, e := scanJSON[receipt](tx.QueryRowContext(ctx, `SELECT body FROM wb_connector_state WHERE id=? FOR UPDATE`, receiptID))
		if e != nil {
			return ConversationState{}, e
		}
		if old.Digest != "" {
			if old.Digest != Digest(in) {
				return ConversationState{}, ErrConflict
			}
			return old.State, tx.Commit()
		}
	}
	cfg, err := scanJSON[ReplySettings](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id LIKE 'reply-%' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.connectionId'))=? FOR SHARE`, in.ConnectionID))
	if err != nil || !cfg.Enabled {
		return ConversationState{}, ErrDenied
	}
	routes, err := controlRoutes(ctx, tx, in.ConnectionID, in.ContactID, in.ExternalUserID, "FOR UPDATE")
	if err != nil {
		return ConversationState{}, err
	}
	if len(routes) == 0 {
		return ConversationState{}, sql.ErrNoRows
	}
	if len(routes) != 1 {
		return ConversationState{}, ErrConflict
	}
	r := routes[0]
	if !r.Enabled || r.Principal.Kind != "project" || r.Principal.ProjectID != cfg.ProjectID || (r.ExternalUserID != "" && r.ExternalUserID != in.ExternalUserID) {
		return ConversationState{}, ErrDenied
	}
	if in.Op == "status" {
		return conversationState(r), tx.Commit()
	}
	if r.Principal.SessionID != in.SessionID || routeRevision(r) != in.Version {
		return ConversationState{}, ErrConflict
	}
	// The adapter has verified this mapping; persist it without changing the
	// customer/session identity, so other configured transports share this gate.
	r.ExternalUserID = in.ExternalUserID
	if !r.AutoReply {
		r.AutoReply = true
		r.Revision = routeRevision(r) + 1
		if err = modeChangedTx(ctx, tx, &r); err != nil {
			return ConversationState{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE wb_message_routes SET body=? WHERE id=?`, jsonText(r), r.ID); err != nil {
		return ConversationState{}, err
	}
	state := conversationState(r)
	if _, err = tx.ExecContext(ctx, `UPDATE wb_connector_state SET body=? WHERE id=?`, jsonText(receipt{Digest(in), state}), receiptID); err != nil {
		return ConversationState{}, err
	}
	return state, tx.Commit()
}

func (s *Store) modeDeliveryGate(ctx context.Context, tx *sql.Tx, c Transport, d Delivery) error {
	connection := c.ModeConnectionID
	if connection == "" {
		connection = d.ConnectionID
	}
	actor, external := d.Target["actorId"], d.Target["externalUserId"]
	if actor == "" && external == "" {
		if c.ModeConnectionID != "" {
			return ErrDenied
		}
		return nil
	}
	routes, err := controlRoutes(ctx, tx, connection, actor, external, "FOR SHARE")
	if err != nil {
		return err
	}
	count := 0
	for _, r := range routes {
		if r.Principal.Kind != "project" {
			continue
		}
		count++
		if !r.Enabled || !r.AutoReply || (!r.ModeChangedAt.IsZero() && !d.CreatedAt.After(r.ModeChangedAt)) {
			return fmt.Errorf("%w: customer mode changed; old draft cannot be submitted", ErrDenied)
		}
	}
	if count > 1 || (c.ModeConnectionID != "" && count != 1) {
		return ErrDenied
	}
	return nil
}

// The event and mode share a transaction; ordinary notifications scan it using
// an independent cursor and durable Run/Delivery IDs. No new scheduler exists.
func (s *Store) QueueModeNotification(ctx context.Context, event RunEvent) (Run, error) {
	var change ModeChange
	raw, err := json.Marshal(event.Details)
	if err != nil || json.Unmarshal(raw, &change) != nil || event.Kind != "conversation.mode.changed" {
		return Run{}, ErrInvalid
	}
	var p Principal
	p.SessionID = event.SessionID
	if err = s.DB.QueryRowContext(ctx, `SELECT kind,project_id FROM wb_sessions WHERE id=?`, p.SessionID).Scan(&p.Kind, &p.ProjectID); err != nil {
		return Run{}, err
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("mode-notification/"+event.ID)).String()
	// Status facts contain no business-source text. Revoking a profile must not
	// prevent pausing or notifying the operator.
	p = Principal{Kind: "owner", SessionID: "conversation-control"}
	r := Run{ID: id, Principal: p, Operation: "control_notification", Title: "侧边栏会话状态通知", Goal: string(raw), OriginMessage: event.ID}
	if err = s.SaveRun(ctx, r); err != nil {
		return r, err
	}
	err = s.PutDelivery(ctx, r, Delivery{ID: id, RunID: id, ItemID: "mode", ConnectionID: change.ConnectionID, Target: map[string]string{"kind": "conversation.mode.changed", "eventId": event.ID}, Text: string(raw)})
	return r, err
}

func (s *Store) PreparingTransport(ctx context.Context, r Run, id string) (Transport, error) {
	if r.Operation == "control_notification" && r.Principal.IsOwner() {
		return scanJSON[Transport](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_transports WHERE id=?`, id))
	}
	return s.Transport(ctx, r.Principal, id)
}

// Both identity lookups use narrow indexes. Merge by route ID; ambiguous
// bindings are rejected rather than choosing one customer by query order.
func controlRoutes(ctx context.Context, tx *sql.Tx, connection, actor, external, lock string) ([]MessageRoute, error) {
	byID := map[string]MessageRoute{}
	for _, key := range []struct{ column, value string }{{"actor_id", actor}, {"external_user_id", external}} {
		if key.value == "" {
			continue
		}
		rows, err := tx.QueryContext(ctx, "SELECT body FROM wb_message_routes WHERE connection_id=? AND "+key.column+"=? ORDER BY id LIMIT 2 "+lock, connection, key.value)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			r, e := scanJSON[MessageRoute](rows)
			if e != nil {
				rows.Close()
				return nil, e
			}
			byID[r.ID] = r
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	out := make([]MessageRoute, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	return out, nil
}

// An auto-increment sequence is not a commit-order watermark. Query events
// without an accepted workflow marker so late commits and crashes stay visible.
func (s *Store) PendingModeEvents(ctx context.Context) ([]RunEvent, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_events e WHERE event_kind='conversation.mode.changed' AND NOT EXISTS(SELECT 1 FROM wb_connector_state c WHERE c.id=CONCAT('mode-queued-',e.id)) ORDER BY sequence LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RunEvent{}
	for rows.Next() {
		v, e := scanJSON[RunEvent](rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) MarkModeQueued(ctx context.Context, eventID string) error {
	_, e := s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_connector_state VALUES(?,JSON_OBJECT('queued',TRUE))`, "mode-queued-"+eventID)
	return e
}
