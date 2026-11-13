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
	Kind             string   `json:"kind,omitempty"`
	ModeConnectionID string   `json:"modeConnectionId,omitempty"`
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Endpoint         string   `json:"endpoint"`
	TokenEnv         string   `json:"tokenEnv"`
	Projects         []string `json:"projects"`
	Enabled          bool     `json:"enabled"`
}
type MessageRoute struct {
	ExternalUserID string      `json:"externalUserId,omitempty"`
	ModeChangedAt  time.Time   `json:"modeChangedAt,omitempty"`
	Revision       int64       `json:"revision"`
	DisplayName    string      `json:"displayName,omitempty"`
	LastMessageAt  string      `json:"lastMessageAt"`
	Preview        string      `json:"preview,omitempty"`
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
	Kind           string     `json:"kind,omitempty"`
	DisplayName    string     `json:"displayName,omitempty"`
	ExternalUserID string     `json:"externalUserId,omitempty"`
	OccurredAt     *time.Time `json:"occurredAt,omitempty"`
	ConnectionID   string     `json:"connectionId"`
	EventID        string     `json:"eventId"`
	ConversationID string     `json:"conversationId"`
	ActorID        string     `json:"actorId"`
	Text           string     `json:"text"`
	ReplyTo        string     `json:"replyTo,omitempty"`
}
type StoredMessage struct {
	BusinessValidUntil *time.Time `json:"businessValidUntil,omitempty"`
	RouteRevision      int64      `json:"routeRevision,omitempty"`
	SettingsRevision   int64      `json:"settingsRevision,omitempty"`
	ExpertSkill        string     `json:"expertSkill,omitempty"`
	ExpertRef          string     `json:"expertRef,omitempty"`
	Knowledge          []Ref      `json:"knowledge,omitempty"`
	ID                 string     `json:"id"`
	DecisionID         string     `json:"decisionId,omitempty"`
	IncomingMessage
	RouteID   string      `json:"routeId,omitempty"`
	Principal Principal   `json:"principal"`
	State     string      `json:"state"`
	Failure   string      `json:"failure,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
	Context   []RecordRef `json:"context,omitempty"`
}
type Delivery struct {
	ReceiptChecks       int               `json:"receiptChecks"`
	ReceiptCheckAttempt string            `json:"receiptCheckAttempt,omitempty"`
	ID                  string            `json:"id"`
	RunID               string            `json:"runId"`
	ItemID              string            `json:"itemId"`
	ReplyPart           int               `json:"replyPart,omitempty"`
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
	if IsReplyPracticeConnection(v.ID) || v.Kind == "reply-practice" || (v.ModeConnectionID != "" && (!ValidID(v.ModeConnectionID) || IsReplyPracticeConnection(v.ModeConnectionID))) {
		return ErrInvalid
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
	return v, checkTransportAccess(p, v)
}

func checkTransportAccess(p Principal, v Transport) error {
	if !v.Enabled {
		return ErrDenied
	}
	if p.Kind == "project" {
		allowed := false
		for _, id := range v.Projects {
			if id == p.ProjectID {
				allowed = true
			}
		}
		if !allowed {
			return ErrDenied
		}
	}
	return nil
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
	if e := v.Principal.Validate(); e != nil {
		return e
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	transport, e := scanJSON[Transport](tx.QueryRowContext(ctx, `SELECT body FROM wb_transports WHERE id=? FOR SHARE`, v.ConnectionID))
	if e != nil {
		return e
	}
	if e = checkTransportAccess(v.Principal, transport); e != nil {
		return e
	}
	var reply ReplySettings
	if v.Principal.Kind == "project" {
		reply, e = scanJSON[ReplySettings](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=? FOR SHARE`, "reply-"+v.Principal.ProjectID))
		if e != nil && !IsMissing(e) {
			return e
		}
	}
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
		if v.Principal.Kind == "project" {
			if v.ExternalUserID != "" && old.ExternalUserID != "" && v.ExternalUserID != old.ExternalUserID {
				return ErrConflict
			}
			v.ExternalUserID = old.ExternalUserID
			v.ModeChangedAt = old.ModeChangedAt
			if v.Revision != old.Revision {
				return ErrConflict
			}
			v.Revision = routeRevision(old)
			v.AutoReply = old.AutoReply
			v.LastMessageAt = old.LastMessageAt
			v.Preview = old.Preview
		}
		if old.ConnectionID != v.ConnectionID || old.ConversationID != v.ConversationID || old.ActorID != v.ActorID || old.Principal != v.Principal {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, `UPDATE wb_message_routes SET body=? WHERE id=?`, jsonText(v), v.ID)
	} else if IsMissing(e) {
		if reply.ConnectionID == v.ConnectionID {
			value, err := readExpertDefault(ctx, tx, v.Principal.ProjectID)
			if err != nil {
				return err
			}
			ref := value.Ref
			if ref == "" && expertName.MatchString(reply.ExpertSkill) {
				ref = "legacy:" + reply.ExpertSkill
			}
			if _, err = tx.ExecContext(ctx, `UPDATE wb_sessions SET expert_ref=? WHERE id=? AND expert_ref=''`, ref, v.Principal.SessionID); err != nil {
				return err
			}
		}
		if v.Revision == 0 {
			v.Revision = 1
		}
		if v.LastMessageAt == "" {
			v.LastMessageAt = messageTime(time.Now())
		}
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
	if !ValidID(v.ConnectionID) || v.EventID == "" || len(v.EventID) > 191 || len(v.Text) > 64000 || v.ActorID == "" || len(v.ActorID) > 191 || v.ConversationID == "" || len(v.ConversationID) > 191 || len(v.DisplayName) > 191 || len(v.ExternalUserID) > 191 || (v.Kind != "" && v.Kind != "human") {
		return StoredMessage{}, ErrInvalid
	}
	// Duplicate transport events must not depend on today's mutable resource configuration.
	if old, err := scanJSON[StoredMessage](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE connection_id=? AND event_id=?`, v.ConnectionID, v.EventID)); err == nil {
		if Digest(incomingIdentity(old.IncomingMessage)) != Digest(incomingIdentity(v)) {
			return old, ErrConflict
		}
		return old, nil
	} else if !IsMissing(err) {
		return StoredMessage{}, err
	}
	// Resolve trusted identities before the route transaction; no model input is used.
	route, routeErr := scanJSON[MessageRoute](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE connection_id=? AND conversation_id=? AND actor_id=?`, v.ConnectionID, v.ConversationID, v.ActorID))
	identityConflict := routeErr == nil && route.ExternalUserID != "" && v.ExternalUserID != "" && route.ExternalUserID != v.ExternalUserID
	var knowledge []Ref
	var contextRefs []RecordRef
	var skill string
	var settingsRevision int64
	var inputErr error
	if routeErr == nil && !identityConflict && route.Enabled && route.AutoReply && v.Kind != "human" {
		var e error
		knowledge, contextRefs, skill, settingsRevision, e = s.replyInputs(ctx, route, v)
		if e != nil {
			inputErr = e
			knowledge = nil
			contextRefs = nil
		}
	} else if routeErr != nil && !IsMissing(routeErr) {
		return StoredMessage{}, routeErr
	}
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return StoredMessage{}, e
	}
	defer tx.Rollback()
	if routeErr == nil {
		route, e = scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE id=? FOR UPDATE`, route.ID))
		if e != nil {
			return StoredMessage{}, e
		}
	}
	existing, e := scanJSON[StoredMessage](tx.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE connection_id=? AND event_id=? FOR UPDATE`, v.ConnectionID, v.EventID))
	if e == nil {
		var digest string
		if e = tx.QueryRowContext(ctx, `SELECT digest FROM wb_messages WHERE connection_id=? AND event_id=?`, v.ConnectionID, v.EventID).Scan(&digest); e != nil {
			return existing, e
		}
		if digest != Digest(v) && Digest(incomingIdentity(existing.IncomingMessage)) != Digest(incomingIdentity(v)) {
			return existing, ErrConflict
		}
		return existing, tx.Commit()
	}
	if !IsMissing(e) {
		return existing, e
	}
	message := StoredMessage{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(v.ConnectionID+"/"+v.EventID)).String(), IncomingMessage: v, CreatedAt: time.Now().UTC(), State: "unrouted"}
	if routeErr == nil && route.ExternalUserID != "" && v.ExternalUserID != "" && route.ExternalUserID != v.ExternalUserID {
		// A trusted contact changing external identity is not a silent remap.
		// Preserve the input as unrouted, outside the old customer's Session.
		route.AutoReply = false
		route.Revision = routeRevision(route) + 1
		if e = pausePending(ctx, tx, route.ID); e != nil {
			return message, e
		}
		if e = modeChangedTx(ctx, tx, &route); e != nil {
			return message, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE wb_message_routes SET body=? WHERE id=?`, jsonText(route), route.ID); e != nil {
			return message, e
		}
		routeErr = ErrDenied
	}
	if routeErr == nil && route.Enabled {
		if route.ExternalUserID == "" {
			route.ExternalUserID = v.ExternalUserID
		}
		message.RouteID = route.ID
		message.Principal = route.Principal
		message.RouteRevision = routeRevision(route)
		message.SettingsRevision = max(settingsRevision, -settingsRevision)
		message.Context = contextRefs
		message.Knowledge = knowledge
		message.ExpertSkill = skill
		if route.Principal.Kind == "project" && skill != "" {
			if e = tx.QueryRowContext(ctx, `SELECT expert_ref FROM wb_sessions WHERE id=? AND project_id=? AND kind='project'`,
				route.Principal.SessionID, route.Principal.ProjectID).Scan(&message.ExpertRef); e != nil {
				return message, e
			}
			if message.ExpertRef != "" {
				message.ExpertSkill = ExpertName(message.ExpertRef)
			}
		}
		message.State = "received"
		if route.Principal.Kind == "project" && (!route.AutoReply || settingsRevision < 0) {
			message.State = "paused"
		}
		if inputErr != nil && IsReplyPracticeConnection(v.ConnectionID) {
			message.State, message.Failure = "practice_failed", "knowledge"
		} else if inputErr != nil {
			message.State = "handoff"
			route.AutoReply = false
			route.Revision = routeRevision(route) + 1
			if e = pausePending(ctx, tx, route.ID); e != nil {
				return message, e
			}
			if e = handoffEvent(ctx, tx, message); e != nil {
				return message, e
			}
			if e = modeChangedTx(ctx, tx, &route); e != nil {
				return message, e
			}
		}
		if v.Kind == "human" {
			message.State = "human"
			route.AutoReply = false
			route.Revision = routeRevision(route) + 1
			if e = pausePending(ctx, tx, route.ID); e != nil {
				return message, e
			}
			if e = modeChangedTx(ctx, tx, &route); e != nil {
				return message, e
			}
		}
		route.LastMessageAt = messageTime(message.CreatedAt)
		route.Preview = preview(v.Text)
		if v.DisplayName != "" {
			route.DisplayName = v.DisplayName
		}
		if _, e = tx.ExecContext(ctx, `UPDATE wb_message_routes SET body=? WHERE id=?`, jsonText(route), route.ID); e != nil {
			return message, e
		}
	}
	if message.Principal.Kind == "personal" && message.State == "received" {
		message.DecisionID, e = s.matchSendingDecision(ctx, strings.TrimSpace(message.Text), message.OccurredAt)
		if e != nil {
			return message, e
		}
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO wb_messages(connection_id,event_id,digest,body) VALUES(?,?,?,?)`, v.ConnectionID, v.EventID, Digest(v), jsonText(message))
	if e != nil {
		return message, e
	}
	if message.Principal.Kind == "project" && (message.State == "paused" || message.State == "human" || message.State == "handoff") {
		label := "客户消息（自动回复已暂停）"
		if message.State == "human" {
			label = "人工已回复，自动回复保持暂停"
		}
		if message.State == "handoff" {
			label = "资料配置或读取校验未通过，本客户已暂停自动回复，请检查项目接入设置。"
		}
		text := message.Text
		if text == "" {
			text = "原渠道未提供正文，不能推测回复内容。"
		}
		event := RunEvent{ID: message.ID + "-history", SessionID: message.Principal.SessionID, RunID: "", Kind: "message.history", Text: label + "\n" + text, CreatedAt: message.CreatedAt}
		if _, e = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_events(id,session_id,run_id,body) VALUES(?,?,?,?)`, event.ID, event.SessionID, event.RunID, jsonText(event)); e != nil {
			return message, e
		}
	}
	return message, tx.Commit()
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
	first, e := s.Message(ctx, p, connection, event)
	if e != nil {
		return e
	}
	route, e := scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE id=? FOR UPDATE`, first.RouteID))
	if e != nil {
		return e
	}
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
	if e = s.setMessageDisposition(ctx, tx, route, m, state); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) setMessageDisposition(ctx context.Context, tx *sql.Tx, route MessageRoute, m StoredMessage, state string) error {
	var e error
	m.State = state
	if state == "handoff" {
		if routeRevision(route) != max(int64(1), m.RouteRevision) {
			return ErrConflict
		}
		if route.Principal.Kind == "project" {
			route.AutoReply = false
			route.Revision = routeRevision(route) + 1
			if e = pausePending(ctx, tx, route.ID); e != nil {
				return e
			}
			if e = handoffEvent(ctx, tx, m); e != nil {
				return e
			}
			if e = modeChangedTx(ctx, tx, &route); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE wb_message_routes SET body=? WHERE id=?`, jsonText(route), route.ID); e != nil {
				return e
			}
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE wb_messages SET body=? WHERE connection_id=? AND event_id=?`, jsonText(m), m.ConnectionID, m.EventID)
	return e
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
	cfg, err := s.ReplySettingsForConnection(ctx, connection)
	if err != nil && !IsMissing(err) {
		return nil, err
	}
	if err == nil && cfg.ReadProfile != nil && !*cfg.ReadProfile {
		return []Record{}, nil
	}
	var records []Record
	for _, ref := range m.Context {
		allowed := false
		for _, current := range append(route.Context, m.Context...) {
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
	if IsReplyPracticeConnection(v.ConnectionID) {
		return ErrDenied
	}
	if v.Text == "" || len(v.Text) > 16000 || len(v.Target) == 0 || !ValidID(v.ID) || v.RunID != r.ID || v.ItemID == "" {
		return ErrInvalid
	}
	if r.Operation == "control_notification" {
		if _, e := scanJSON[Transport](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_transports WHERE id=?`, v.ConnectionID)); e != nil {
			return e
		}
	} else if _, e := s.Transport(ctx, r.Principal, v.ConnectionID); e != nil {
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
	// A multipart sequence may stop after bounded receipt checks even if
	// the gateway initially queued this part. Preserve its original receipt.
	partTimedOut := state == "unknown" && v.ReplyPart > 0 && v.ReceiptChecks >= 3
	if state != "failed" && !partTimedOut && rank[state] < rank[v.State] {
		return v, nil
	}
	if receipt == "" {
		receipt = v.Receipt
	}
	previousState := v.State
	v.State = state
	v.Receipt = receipt
	v.Error = reason
	v.UpdatedAt = time.Now().UTC()
	if _, e = tx.ExecContext(ctx, `UPDATE wb_deliveries SET state=?,body=? WHERE id=?`, v.State, jsonText(v), id); e != nil {
		return v, e
	}
	if e = finishReplyMessage(ctx, tx, v, previousState); e != nil {
		return v, e
	}
	return v, tx.Commit()
}

// Gateway retries may carry a newer display name or envelope timestamp. These
// metadata changes do not create a second input; identity/content must match.
func incomingIdentity(v IncomingMessage) IncomingMessage {
	v.DisplayName = ""
	v.OccurredAt = nil
	return v
}
