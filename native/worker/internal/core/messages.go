package core

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"net/url"
	"strings"
	"time"
)

// A transport is a standard MCP server. Business routing and approvals live
// here; platform authentication and wire formats remain in the connector.
type Transport struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Endpoint string   `json:"endpoint"`
	TokenEnv string   `json:"tokenEnv"`
	Projects []string `json:"projects"`
	Enabled  bool     `json:"enabled"`
}
type MessageRoute struct {
	ID             string      `json:"id"`
	ConnectionID   string      `json:"connectionId"`
	ConversationID string      `json:"conversationId"`
	ActorID        string      `json:"actorId"`
	Principal      Principal   `json:"principal"`
	AutoReply      bool        `json:"autoReply"`
	Enabled        bool        `json:"enabled"`
	Context        []RecordRef `json:"context,omitempty"`
}
type IncomingMessage struct {
	OccurredAt     *time.Time `json:"occurredAt,omitempty"`
	ConnectionID   string     `json:"connectionId"`
	EventID        string     `json:"eventId"`
	ConversationID string     `json:"conversationId"`
	ActorID        string     `json:"actorId"`
	Text           string     `json:"text"`
	ReplyTo        string     `json:"replyTo,omitempty"`
}
type StoredMessage struct {
	ID         string `json:"id"`
	DecisionID string `json:"decisionId,omitempty"`
	IncomingMessage
	RouteID   string      `json:"routeId,omitempty"`
	Principal Principal   `json:"principal"`
	State     string      `json:"state"`
	CreatedAt time.Time   `json:"createdAt"`
	Context   []RecordRef `json:"context,omitempty"`
}
type Delivery struct {
	ReceiptChecks       int               `json:"receiptChecks"`
	ReceiptCheckAttempt string            `json:"receiptCheckAttempt,omitempty"`
	ID                  string            `json:"id"`
	RunID               string            `json:"runId"`
	ItemID              string            `json:"itemId"`
	ConnectionID        string            `json:"connectionId"`
	Target              map[string]string `json:"target"`
	Text                string            `json:"text"`
	State               string            `json:"state"`
	Receipt             string            `json:"receipt,omitempty"`
	Error               string            `json:"error,omitempty"`
	CreatedAt           time.Time         `json:"createdAt"`
	UpdatedAt           time.Time         `json:"updatedAt"`
}

func (s *Store) PutTransport(ctx context.Context, p Principal, v Transport) error {
	if !p.IsOwner() {
		return ErrDenied
	}
	u, e := url.Parse(v.Endpoint)
	if !ValidID(v.ID) || v.Name == "" || e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || u.Hostname() != "127.0.0.1")) || !strings.HasPrefix(v.TokenEnv, "WORKBENCH_CONNECTOR_") {
		return ErrInvalid
	}
	for _, id := range v.Projects {
		if !ValidID(id) {
			return ErrInvalid
		}
	}
	_, e = s.DB.ExecContext(ctx, `INSERT INTO wb_transports VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)`, v.ID, jsonText(v))
	return e
}
func (s *Store) Transport(ctx context.Context, p Principal, id string) (Transport, error) {
	v, e := scanJSON[Transport](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_transports WHERE id=?`, id))
	if e != nil {
		return v, e
	}
	if !v.Enabled {
		return v, ErrDenied
	}
	if p.Kind == "project" {
		allowed := false
		for _, id := range v.Projects {
			if id == p.ProjectID {
				allowed = true
			}
		}
		if !allowed {
			return v, ErrDenied
		}
	}
	return v, nil
}
func (s *Store) PutRoute(ctx context.Context, p Principal, v MessageRoute) error {
	if !p.IsOwner() {
		return ErrDenied
	}
	if !ValidID(v.ID) || len(v.ConversationID) > 191 || len(v.ActorID) > 191 || v.ConversationID == "" || v.ActorID == "" || v.Principal.Kind == "owner" || len(v.Context) > 16 {
		return ErrInvalid
	}
	for _, ref := range v.Context {
		if ref.ID == "" || len(ref.ID) > 191 {
			return ErrInvalid
		}
		if e := Authorize(ctx, s, v.Principal, ref.Ref); e != nil {
			return e
		}
	}
	if _, e := s.Transport(ctx, v.Principal, v.ConnectionID); e != nil {
		return e
	}
	if e := v.Principal.Validate(); e != nil {
		return e
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	inserted, e := tx.ExecContext(ctx, `INSERT IGNORE INTO wb_sessions(id,project_id,kind) VALUES(?,?,?)`, v.Principal.SessionID, v.Principal.ProjectID, v.Principal.Kind)
	if e != nil {
		return e
	}
	fresh, e := inserted.RowsAffected()
	if e != nil {
		return e
	}
	// Serialize binding on the durable Session row. Personal channels intentionally
	// share 今日; customer object sessions never do.
	var locked, project, kind string
	if e = tx.QueryRowContext(ctx, `SELECT id,project_id,kind FROM wb_sessions WHERE id=? FOR UPDATE`, v.Principal.SessionID).Scan(&locked, &project, &kind); e != nil {
		return e
	}
	if project != v.Principal.ProjectID || kind != v.Principal.Kind {
		return ErrDenied
	}
	if v.Principal.Kind == "project" && fresh == 0 {
		var same int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_message_routes WHERE id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=?`, v.ID, v.Principal.SessionID).Scan(&same); e != nil {
			return e
		}
		if same != 1 {
			return ErrConflict
		}
	}
	if v.Principal.Kind == "project" {
		var occupied int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_message_routes WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=? AND id<>?`, v.Principal.SessionID, v.ID).Scan(&occupied); e != nil {
			return e
		}
		if occupied > 0 {
			return ErrConflict
		}
	}
	old, e := scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE id=? FOR UPDATE`, v.ID))
	if e == nil {
		if v.Principal.Kind == "project" && Digest(contextIdentities(old.Context)) != Digest(contextIdentities(v.Context)) {
			return ErrConflict
		}
		if old.ConnectionID != v.ConnectionID || old.ConversationID != v.ConversationID || old.ActorID != v.ActorID || old.Principal != v.Principal {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, `UPDATE wb_message_routes SET body=? WHERE id=?`, jsonText(v), v.ID)
	} else if IsMissing(e) {
		_, e = tx.ExecContext(ctx, `INSERT INTO wb_message_routes(id,connection_id,conversation_id,actor_id,body) VALUES(?,?,?,?,?)`, v.ID, v.ConnectionID, v.ConversationID, v.ActorID, jsonText(v))
	} else {
		return e
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) Route(ctx context.Context, id string) (MessageRoute, error) {
	return scanJSON[MessageRoute](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE id=?`, id))
}
func (s *Store) AcceptMessage(ctx context.Context, v IncomingMessage) (StoredMessage, error) {
	if !ValidID(v.ConnectionID) || v.EventID == "" || len(v.EventID) > 191 || len(v.Text) > 64000 || v.ActorID == "" || v.ConversationID == "" {
		return StoredMessage{}, ErrInvalid
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(v.ConnectionID+"/"+v.EventID)).String()
	message := StoredMessage{ID: id, IncomingMessage: v, CreatedAt: time.Now().UTC(), State: "unrouted"}
	route, e := scanJSON[MessageRoute](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE connection_id=? AND conversation_id=? AND actor_id=?`, v.ConnectionID, v.ConversationID, v.ActorID))
	if e == nil && route.Enabled {
		message.RouteID = route.ID
		message.Principal = route.Principal
		message.State = "received"
		message.Context = route.Context
	} else if e != nil && !IsMissing(e) {
		return message, e
	}
	if message.Principal.Kind == "personal" && message.State == "received" {
		message.DecisionID, e = s.matchSendingDecision(ctx, strings.TrimSpace(message.Text), message.OccurredAt)
		if e != nil {
			return message, e
		}
	}
	_, e = s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_messages(connection_id,event_id,digest,body) VALUES(?,?,?,?)`, v.ConnectionID, v.EventID, Digest(v), jsonText(message))
	if e != nil {
		return message, e
	}
	var existing string
	if e = s.DB.QueryRowContext(ctx, `SELECT digest FROM wb_messages WHERE connection_id=? AND event_id=?`, v.ConnectionID, v.EventID).Scan(&existing); e != nil {
		return message, e
	}
	if existing != Digest(v) {
		return message, ErrConflict
	}
	return scanJSON[StoredMessage](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE connection_id=? AND event_id=?`, v.ConnectionID, v.EventID))
}
func (s *Store) Message(ctx context.Context, p Principal, connection, event string) (StoredMessage, error) {
	m, e := scanJSON[StoredMessage](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE connection_id=? AND event_id=?`, connection, event))
	if e != nil {
		return m, e
	}
	if m.State == "unrouted" || (!p.IsOwner() && (p != m.Principal)) {
		return m, ErrDenied
	}
	return m, nil
}
func (s *Store) MessageDisposition(ctx context.Context, p Principal, connection, event, state string) error {
	if state != "ignored" && state != "handoff" {
		return ErrInvalid
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	m, e := scanJSON[StoredMessage](tx.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE connection_id=? AND event_id=? FOR UPDATE`, connection, event))
	if e != nil {
		return e
	}
	if m.State == "unrouted" || (!p.IsOwner() && p != m.Principal) {
		return ErrDenied
	}
	if m.State == state {
		return nil
	}
	if m.State != "received" && m.State != "reply_queued" {
		return ErrConflict
	}
	m.State = state
	if _, e = tx.ExecContext(ctx, `UPDATE wb_messages SET body=? WHERE connection_id=? AND event_id=?`, jsonText(m), connection, event); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) SetMessageState(ctx context.Context, p Principal, connection, event, state string) error {
	result, e := s.DB.ExecContext(ctx, `UPDATE wb_messages SET body=JSON_SET(body,'$.state',?) WHERE connection_id=? AND event_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.state'))='received'`, state, connection, event)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n == 1 {
		return nil
	}
	m, e := s.Message(ctx, p, connection, event)
	if e != nil {
		return e
	}
	if m.State == state {
		return nil
	}
	return ErrConflict
}

func (s *Store) SessionRoute(ctx context.Context, p Principal) (MessageRoute, error) {
	v, e := scanJSON[MessageRoute](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=?`, p.SessionID))
	if e == nil && v.Principal != p {
		return v, ErrDenied
	}
	return v, e
}

// Customer-facing agents get fixed record inputs rather than a database query
// tool. This is an input binding, not a general row-permission language.
func (s *Store) MessageContext(ctx context.Context, p Principal, connection, event string) ([]Record, error) {
	m, e := s.Message(ctx, p, connection, event)
	if e != nil {
		return nil, e
	}
	route, e := s.Route(ctx, m.RouteID)
	if e != nil || !route.Enabled || route.Principal != p {
		return nil, ErrDenied
	}
	if m.State != "received" {
		return nil, ErrConflict
	}
	var records []Record
	for _, ref := range m.Context {
		allowed := false
		for _, current := range route.Context {
			if current == ref {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, ErrDenied
		}
		if ref.ID == "" {
			return nil, ErrInvalid
		}
		v, e := s.ReadIDs(ctx, p, ref.Ref, []string{ref.ID})
		if e != nil {
			return nil, e
		}
		records = append(records, v...)
	}
	if len(jsonText(records)) > 24000 {
		return nil, fmt.Errorf("%w: context budget exceeded", ErrInvalid)
	}
	return records, nil
}
func (s *Store) PutDelivery(ctx context.Context, r Run, v Delivery) error {
	if v.Text == "" || len(v.Text) > 16000 || len(v.Target) == 0 || !ValidID(v.ID) || v.RunID != r.ID || v.ItemID == "" {
		return ErrInvalid
	}
	if _, e := s.Transport(ctx, r.Principal, v.ConnectionID); e != nil {
		return e
	}
	v.State = "prepared"
	v.CreatedAt = time.Now().UTC()
	v.UpdatedAt = v.CreatedAt
	_, e := s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_deliveries(id,run_id,item_id,state,body) VALUES(?,?,?,?,?)`, v.ID, v.RunID, v.ItemID, v.State, jsonText(v))
	if e != nil {
		return e
	}
	old, e := s.Delivery(ctx, v.ID)
	if e != nil {
		return e
	}
	if old.RunID != v.RunID || old.ItemID != v.ItemID || old.ConnectionID != v.ConnectionID || old.Text != v.Text || Digest(old.Target) != Digest(v.Target) {
		return ErrConflict
	}
	return nil
}
func (s *Store) Delivery(ctx context.Context, id string) (Delivery, error) {
	return scanJSON[Delivery](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_deliveries WHERE id=?`, id))
}
func (s *Store) ClaimDelivery(ctx context.Context, id string) (Delivery, bool, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Delivery{}, false, e
	}
	defer tx.Rollback()
	v, e := scanJSON[Delivery](tx.QueryRowContext(ctx, `SELECT body FROM wb_deliveries WHERE id=? FOR UPDATE`, id))
	if e != nil {
		return v, false, e
	}
	if v.State != "prepared" {
		return v, false, nil
	}
	if e = s.deliveryGate(ctx, tx, v); e != nil {
		return v, false, e
	}
	v.State = "submitting"
	v.UpdatedAt = time.Now().UTC()
	if _, e = tx.ExecContext(ctx, `UPDATE wb_deliveries SET state=?,body=? WHERE id=?`, v.State, jsonText(v), id); e != nil {
		return v, false, e
	}
	return v, true, tx.Commit()
}
func (s *Store) FinishDelivery(ctx context.Context, id, state, receipt, reason string) (Delivery, error) {
	switch state {
	case "accepted", "pending_confirmation", "sent", "delivered", "failed", "unknown":
	default:
		return Delivery{}, ErrInvalid
	}
	if (state == "sent" || state == "delivered" || state == "accepted" || state == "pending_confirmation") && receipt == "" {
		return Delivery{}, fmt.Errorf("%w: receipt required", ErrInvalid)
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Delivery{}, e
	}
	defer tx.Rollback()
	v, e := scanJSON[Delivery](tx.QueryRowContext(ctx, `SELECT body FROM wb_deliveries WHERE id=? FOR UPDATE`, id))
	if e != nil {
		return v, e
	}
	if v.State == "delivered" || v.State == "failed" || (v.State == "sent" && state != "delivered") {
		return v, nil
	}
	if v.State == "prepared" {
		return v, ErrConflict
	}
	rank := map[string]int{"prepared": 0, "submitting": 0, "unknown": 0, "accepted": 1, "pending_confirmation": 2, "sent": 3, "delivered": 4}
	if state != "failed" && rank[state] < rank[v.State] {
		return v, nil
	}
	if receipt == "" {
		receipt = v.Receipt
	}
	v.State = state
	v.Receipt = receipt
	v.Error = reason
	v.UpdatedAt = time.Now().UTC()
	if _, e = tx.ExecContext(ctx, `UPDATE wb_deliveries SET state=?,body=? WHERE id=?`, v.State, jsonText(v), id); e != nil {
		return v, e
	}
	return v, tx.Commit()
}
