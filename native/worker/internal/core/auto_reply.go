package core

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

// ReplySettings is one ordinary project's channel configuration, not a second
// agent/workflow definition. Secrets stay in the installed connector.
type ReplySettings struct {
	ProjectID    string          `json:"projectId"`
	ConnectionID string          `json:"connectionId"`
	ExpertSkill  string          `json:"expertSkill"`
	Knowledge    []Ref           `json:"knowledge"`
	Subject      *SubjectBinding `json:"subject,omitempty"`
	Notify       bool            `json:"notify"`
	Enabled      bool            `json:"enabled"`
	Revision     int64           `json:"revision"`
}
type SubjectBinding struct {
	ResourceID string `json:"resourceId"`
	Section    string `json:"section"`
	MatchField string `json:"matchField,omitempty"`
}

func (s *Store) ReplySettings(ctx context.Context, p Principal, project string) (ReplySettings, error) {
	if !p.IsOwner() || !ValidID(project) {
		return ReplySettings{}, ErrDenied
	}
	v, e := scanJSON[ReplySettings](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=?`, "reply-"+project))
	if IsMissing(e) {
		return ReplySettings{ProjectID: project, Knowledge: []Ref{}}, nil
	}
	return v, e
}
func (s *Store) PutReplySettings(ctx context.Context, p Principal, v ReplySettings) (ReplySettings, error) {
	if !p.IsOwner() || !ValidID(v.ProjectID) || !ValidID(v.ConnectionID) || !ValidID(v.ExpertSkill) || len(v.Knowledge) > 8 {
		return v, ErrInvalid
	}
	scope := Principal{Kind: "project", ProjectID: v.ProjectID, SessionID: "reply-config"}
	if other, e := s.ReplySettingsForConnection(ctx, v.ConnectionID); e == nil && other.ProjectID != v.ProjectID {
		return v, ErrConflict
	}
	if _, e := s.Transport(ctx, scope, v.ConnectionID); e != nil {
		return v, e
	}
	var exists int
	if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_projects WHERE id=?`, v.ProjectID).Scan(&exists); e != nil {
		return v, e
	}
	if exists != 1 {
		return v, ErrInvalid
	}
	for _, ref := range v.Knowledge {
		if e := Authorize(ctx, s, scope, ref); e != nil {
			return v, e
		}
	}
	if v.Subject != nil {
		r, e := s.Resource(ctx, v.Subject.ResourceID)
		if e != nil {
			return v, e
		}
		if v.Subject.MatchField != "" && !ValidID(v.Subject.MatchField) {
			return v, ErrInvalid
		}
		if e = Authorize(ctx, s, scope, Ref{r.ID, r.CurrentVersion, v.Subject.Section}); e != nil {
			return v, e
		}
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	var transport string
	if e = tx.QueryRowContext(ctx, `SELECT id FROM wb_transports WHERE id=? FOR UPDATE`, v.ConnectionID).Scan(&transport); e != nil {
		return v, e
	}
	var occupied int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_settings WHERE id LIKE 'reply-%' AND id<>? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.connectionId'))=?`, "reply-"+v.ProjectID, v.ConnectionID).Scan(&occupied); e != nil {
		return v, e
	}
	if occupied != 0 {
		return v, ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_settings VALUES(?,?)`, "reply-"+v.ProjectID, jsonText(ReplySettings{ProjectID: v.ProjectID})); e != nil {
		return v, e
	}
	old, e := scanJSON[ReplySettings](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=? FOR UPDATE`, "reply-"+v.ProjectID))
	if e != nil {
		return v, e
	}
	if old.Revision != v.Revision {
		return v, ErrConflict
	}
	// Rebinding a live account would orphan sessions. Choose a new ordinary project.
	if old.ConnectionID != "" && old.ConnectionID != v.ConnectionID {
		return v, ErrConflict
	}
	v.Revision++
	_, e = tx.ExecContext(ctx, `UPDATE wb_settings SET body=? WHERE id=?`, jsonText(v), "reply-"+v.ProjectID)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (s *Store) ReplySettingsForConnection(ctx context.Context, connection string) (ReplySettings, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_settings WHERE id LIKE 'reply-%' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.connectionId'))=? LIMIT 2`, connection)
	if e != nil {
		return ReplySettings{}, e
	}
	defer rows.Close()
	var out ReplySettings
	count := 0
	for rows.Next() {
		out, e = scanJSON[ReplySettings](rows)
		if e != nil {
			return out, e
		}
		count++
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if count == 0 {
		return out, sql.ErrNoRows
	}
	if count != 1 {
		return out, ErrConflict
	}
	return out, nil
}

// Binding is derived only from the trusted channel identity. A model cannot
// supply another customer's identifier or change an existing Session owner.
func (s *Store) EnsureReplyRoute(ctx context.Context, m IncomingMessage) (MessageRoute, error) {
	existing, e := scanJSON[MessageRoute](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE connection_id=? AND conversation_id=? AND actor_id=?`, m.ConnectionID, m.ConversationID, m.ActorID))
	if e == nil {
		return existing, nil
	}
	if !IsMissing(e) {
		return existing, e
	}
	cfg, e := s.ReplySettingsForConnection(ctx, m.ConnectionID)
	if e != nil {
		return existing, e
	}
	if !cfg.Enabled {
		return existing, ErrDenied
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("object/"+m.ConnectionID+"/"+m.ConversationID+"/"+m.ActorID)).String()
	p := Principal{Kind: "project", ProjectID: cfg.ProjectID, SessionID: "object-" + id}
	route := MessageRoute{ID: id, ConnectionID: m.ConnectionID, ConversationID: m.ConversationID, ActorID: m.ActorID, ExternalUserID: m.ExternalUserID, Principal: p, Enabled: true, AutoReply: true, Revision: 1, DisplayName: m.DisplayName}
	if e = s.PutRoute(ctx, Principal{Kind: "owner", SessionID: "connector-binding"}, route); e != nil && !IsConflict(e) {
		return route, e
	}
	return s.Route(ctx, id)
}
func IsConflict(e error) bool { return e == ErrConflict }

func (s *Store) replyInputs(ctx context.Context, route MessageRoute, m IncomingMessage) ([]Ref, []RecordRef, string, int64, error) {
	cfg, e := s.ReplySettingsForConnection(ctx, m.ConnectionID)
	if IsMissing(e) {
		return nil, route.Context, "", 0, nil
	}
	if e != nil {
		return nil, nil, "", 0, e
	}
	if !cfg.Enabled {
		return nil, nil, "", -cfg.Revision, nil
	}
	if cfg.ProjectID != route.Principal.ProjectID {
		return nil, nil, "", 0, ErrDenied
	}
	knowledge := []Ref{}
	for _, ref := range cfg.Knowledge {
		r, e := s.Resource(ctx, ref.ResourceID)
		if e != nil {
			return nil, nil, "", 0, e
		}
		ref.Version = r.CurrentVersion
		if e = Authorize(ctx, s, route.Principal, ref); e != nil {
			return nil, nil, "", 0, e
		}
		knowledge = append(knowledge, ref)
	}
	contextRefs := append([]RecordRef{}, route.Context...)
	if cfg.Subject != nil {
		r, e := s.Resource(ctx, cfg.Subject.ResourceID)
		if e != nil {
			return nil, nil, "", 0, e
		}
		ref := Ref{r.ID, r.CurrentVersion, cfg.Subject.Section}
		if e = Authorize(ctx, s, route.Principal, ref); e != nil {
			return nil, nil, "", 0, e
		}
		identity := m.ExternalUserID
		if identity == "" {
			identity = m.ActorID
		}
		if cfg.Subject.MatchField == "" {
			records, e := s.ReadIDs(ctx, route.Principal, ref, []string{identity})
			if e != nil {
				return nil, nil, "", 0, e
			}
			for _, record := range records {
				contextRefs = append(contextRefs, RecordRef{ref, record.ID})
			}
		} else {
			snap, e := s.Snapshot(ctx, ref.ResourceID, ref.Version)
			if e != nil {
				return nil, nil, "", 0, e
			}
			allowed := false
			for _, section := range snap.Sections {
				if section.ID == ref.Section {
					for _, field := range section.Fields {
						if field == cfg.Subject.MatchField {
							allowed = true
						}
					}
				}
			}
			if !allowed {
				return nil, nil, "", 0, ErrDenied
			}
			rows, e := s.DB.QueryContext(ctx, `SELECT item_id FROM wb_records WHERE resource_id=? AND version=? AND JSON_UNQUOTE(JSON_EXTRACT(body,?)) COLLATE utf8mb4_0900_bin=? LIMIT 2`, ref.ResourceID, ref.Version, "$."+cfg.Subject.MatchField, identity)
			if e != nil {
				return nil, nil, "", 0, e
			}
			ids := []string{}
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					return nil, nil, "", 0, e
				}
				ids = append(ids, id)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return nil, nil, "", 0, e
			}
			if len(ids) > 1 {
				return nil, nil, "", 0, ErrConflict
			}
			for _, id := range ids {
				contextRefs = append(contextRefs, RecordRef{ref, id})
			}
		}
	}
	return knowledge, contextRefs, cfg.ExpertSkill, cfg.Revision, nil
}

type ConversationRow struct {
	ID            string `json:"id"`
	SessionID     string `json:"sessionId"`
	DisplayName   string `json:"displayName"`
	ActorID       string `json:"actorId"`
	AutoReply     bool   `json:"autoReply"`
	Revision      int64  `json:"revision"`
	LastMessageAt string `json:"lastMessageAt"`
	Preview       string `json:"preview"`
}
type ConversationPage struct {
	Items []ConversationRow `json:"items"`
	Next  string            `json:"next,omitempty"`
}

func (s *Store) Conversations(ctx context.Context, p Principal, project, query, after string, paused bool) (ConversationPage, error) {
	out := ConversationPage{Items: []ConversationRow{}}
	if !p.IsOwner() || !ValidID(project) || len(query) > 191 {
		return out, ErrDenied
	}
	clause := `project_id=?`
	args := []any{project}
	if paused {
		clause += ` AND auto_reply=FALSE`
	}
	if query != "" {
		clause += ` AND (LOCATE(?,display_name)>0 OR LOCATE(?,actor_id)>0)`
		args = append(args, query, query)
	}
	if after != "" {
		raw, e := base64.RawURLEncoding.DecodeString(after)
		if e != nil {
			return out, ErrInvalid
		}
		var c []string
		if json.Unmarshal(raw, &c) != nil || len(c) != 2 || !ValidID(c[1]) {
			return out, ErrInvalid
		}
		clause += ` AND (last_activity<? OR (last_activity=? AND id<?))`
		args = append(args, c[0], c[0], c[1])
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_message_routes WHERE `+clause+` ORDER BY last_activity DESC,id DESC LIMIT 51`, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scanJSON[MessageRoute](rows)
		if e != nil {
			return out, e
		}
		if len(out.Items) == 50 {
			last := out.Items[49]
			raw, _ := json.Marshal([]string{last.LastMessageAt, last.ID})
			out.Next = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		out.Items = append(out.Items, ConversationRow{v.ID, v.Principal.SessionID, v.DisplayName, v.ActorID, v.AutoReply, routeRevision(v), v.LastMessageAt, v.Preview})
	}
	return out, rows.Err()
}
func routeRevision(r MessageRoute) int64 {
	if r.Revision < 1 {
		return 1
	}
	return r.Revision
}
func (s *Store) ChangeReplyMode(ctx context.Context, p Principal, id string, revision int64, enabled bool) (MessageRoute, error) {
	if !p.IsOwner() {
		return MessageRoute{}, ErrDenied
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return MessageRoute{}, e
	}
	defer tx.Rollback()
	v, e := scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE id=? FOR UPDATE`, id))
	if e != nil {
		return v, e
	}
	if v.Principal.Kind != "project" {
		return v, ErrDenied
	}
	if routeRevision(v) != revision {
		return v, ErrConflict
	}
	v.AutoReply = enabled
	v.Revision = revision + 1
	if !enabled {
		if e = pausePending(ctx, tx, id); e != nil {
			return v, e
		}
	}
	if e = modeChangedTx(ctx, tx, &v); e != nil {
		return v, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE wb_message_routes SET body=? WHERE id=?`, jsonText(v), id)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func preview(text string) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) > 160 {
		r = r[:160]
	}
	return string(r)
}
func messageTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000Z") }

// Public knowledge tools remain standard resource tools, scoped to the fixed
// snapshots of an incoming message. They never expose the subject's whole table.
func (s *Store) CheckMessageKnowledge(ctx context.Context, p Principal, ref Ref) error {
	if p.Kind != "project" {
		return nil
	}
	route, e := s.SessionRoute(ctx, p)
	if IsMissing(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !route.Enabled {
		return ErrDenied
	}
	cfg, e := s.ReplySettingsForConnection(ctx, route.ConnectionID)
	if e != nil || !cfg.Enabled {
		return ErrDenied
	}
	allowed := false
	for _, r := range cfg.Knowledge {
		if r.ResourceID == ref.ResourceID && r.Section == ref.Section {
			allowed = true
		}
	}
	if !allowed {
		return ErrDenied
	}
	m, e := scanJSON[StoredMessage](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE route_id=? AND message_state='received' ORDER BY arrival_at,event_id LIMIT 1`, route.ID))
	if IsMissing(e) {
		m, e = scanJSON[StoredMessage](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE route_id=? ORDER BY arrival_at DESC,event_id DESC LIMIT 1`, route.ID))
	}
	if e != nil {
		return ErrDenied
	}

	for _, r := range m.Knowledge {
		if r == ref {
			return nil
		}
	}
	return ErrDenied
}
func (s *Store) MessageHistory(ctx context.Context, p Principal, connection, event string) ([]IncomingMessage, error) {
	m, e := s.Message(ctx, p, connection, event)
	if e != nil {
		return nil, e
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT body FROM wb_messages WHERE connection_id=? AND route_id=? AND arrival_at<=? ORDER BY arrival_at DESC,event_id DESC LIMIT 10`, connection, m.RouteID, m.CreatedAt.Format(time.RFC3339Nano))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []IncomingMessage{}
	for rows.Next() {
		v, e := scanJSON[StoredMessage](rows)
		if e != nil {
			return nil, e
		}
		if v.EventID == event {
			continue
		}
		r := []rune(v.Text)
		if len(r) > 1200 {
			v.Text = string(r[:1200]) + "（截取，原消息仍保留）"
		}
		out = append(out, v.IncomingMessage)
	}
	return out, rows.Err()
}

func pausePending(ctx context.Context, tx *sql.Tx, id string) error {
	rows, e := tx.QueryContext(ctx, `SELECT body FROM wb_messages WHERE route_id=? AND message_state='received'`, id)
	if e != nil {
		return e
	}
	var inputs []StoredMessage
	for rows.Next() {
		m, err := scanJSON[StoredMessage](rows)
		if err != nil {
			rows.Close()
			return err
		}
		inputs = append(inputs, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, m := range inputs {
		if e = messageHistoryEvent(ctx, tx, m, "客户消息（处理前已暂停）"); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE wb_messages SET body=JSON_SET(body,'$.state','paused') WHERE route_id=? AND message_state IN ('received','reply_queued')`, id)
	return e
}
func handoffEvent(ctx context.Context, tx *sql.Tx, m StoredMessage) error {
	event := RunEvent{ID: m.ID + "-handoff", SessionID: m.Principal.SessionID, Kind: "message.handoff", Text: "该客户会话已停止自动回复，需要本人接管。", CreatedAt: time.Now().UTC()}
	event.Details = map[string]any{"sessionId": m.Principal.SessionID, "projectId": m.Principal.ProjectID, "routeId": m.RouteID}
	_, e := tx.ExecContext(ctx, `INSERT IGNORE INTO wb_events(id,session_id,run_id,body) VALUES(?,?,?,?)`, event.ID, event.SessionID, event.RunID, jsonText(event))
	if e != nil {
		return e
	}
	var configRaw []byte
	ce := tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=?`, "reply-"+m.Principal.ProjectID).Scan(&configRaw)
	if ce == nil {
		var cfg ReplySettings
		if ce = json.Unmarshal(configRaw, &cfg); ce != nil {
			return ce
		}
		if !cfg.Notify {
			return nil
		}
	} else if !IsMissing(ce) {
		return ce
	}
	var raw []byte
	e = tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id='personal'`).Scan(&raw)
	if IsMissing(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var personal struct {
		SessionID string `json:"sessionId"`
	}
	if e = json.Unmarshal(raw, &personal); e != nil {
		return e
	}
	if personal.SessionID != "" && personal.SessionID != event.SessionID {
		event.ID += "-notify"
		event.SessionID = personal.SessionID
		_, e = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_events(id,session_id,run_id,body) VALUES(?,?,?,?)`, event.ID, event.SessionID, "", jsonText(event))
	}
	return e
}

func (s *Store) IncomingForRun(ctx context.Context, id string) (StoredMessage, error) {
	// Trusted control-plane callers inspect binding/state, never protected facts.
	// Dispatch and all business reads retain their normal resource authorization.
	r, e := scanJSON[Run](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, id))
	if e != nil {
		return StoredMessage{}, e
	}
	if r.Operation != "incoming" || r.OriginMessage == "" {
		return StoredMessage{}, ErrDenied
	}
	m, e := scanJSON[StoredMessage](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.id'))=?`, r.OriginMessage))
	if e != nil {
		return m, e
	}
	if m.Principal != r.Principal || m.ID != r.OriginMessage {
		return m, ErrDenied
	}
	return m, nil
}

func (s *Store) InitReplyProject(ctx context.Context, p Principal, project string) (ReplySettings, error) {
	if !p.IsOwner() || !ValidID(project) {
		return ReplySettings{}, ErrDenied
	}
	v := ReplySettings{ProjectID: project, ExpertSkill: "customer-reply", Knowledge: []Ref{}, Notify: true}
	_, e := s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_settings VALUES(?,?)`, "reply-"+project, jsonText(v))
	if e != nil {
		return v, e
	}
	return s.ReplySettings(ctx, p, project)
}

// Admit only the oldest unhandled message of the same customer. Host retries
// queued inputs without loading an Agent or consuming their response deadline.
func (s *Store) CanAdmitIncoming(ctx context.Context, m StoredMessage) (bool, error) {
	if m.State != "received" {
		return false, nil
	}
	cfg, e := s.ReplySettingsForConnection(ctx, m.ConnectionID)
	if e != nil && !IsMissing(e) {
		return false, e
	}
	if e == nil && (!cfg.Enabled || cfg.Revision != m.SettingsRevision) {
		return false, s.PauseReceived(ctx, m)
	}
	first, e := scanJSON[StoredMessage](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE route_id=? AND message_state IN ('received','reply_queued','reply_submitting') ORDER BY arrival_at,event_id LIMIT 1`, m.RouteID))
	if e != nil {
		return false, e
	}
	return first.ID == m.ID, nil
}

// Only a still-prepared reply can expire. A submitted/unknown external action
// is never rewritten or retried by this cleanup of unsubmitted drafts.
func (s *Store) ExpireReplyDraft(ctx context.Context, id string) (bool, error) {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	d, e := scanJSON[Delivery](tx.QueryRowContext(ctx, `SELECT body FROM wb_deliveries WHERE id=? FOR UPDATE`, id))
	if e != nil {
		return false, e
	}
	if d.State != "prepared" {
		return false, nil
	}
	r, e := scanJSON[Run](tx.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, d.RunID))
	if e != nil {
		return false, e
	}
	if r.Operation != "reply" {
		return false, nil
	}
	e = s.deliveryGate(ctx, tx, d)
	if e == nil {
		return false, nil
	}
	if !errors.Is(e, ErrDenied) && !errors.Is(e, ErrConflict) {
		return false, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE wb_messages SET body=JSON_SET(body,'$.state','reply_failed') WHERE connection_id=? AND event_id=? AND message_state='reply_queued'`, d.ConnectionID, d.Target["replyTo"]); e != nil {
		return false, e
	}
	d.State = "failed"
	d.Error = "自动回复未提交：会话、资料或配置已停止或失效。"
	d.UpdatedAt = time.Now().UTC()
	if _, e = tx.ExecContext(ctx, `UPDATE wb_deliveries SET state=?,body=? WHERE id=?`, d.State, jsonText(d), id); e != nil {
		return false, e
	}
	return true, tx.Commit()
}

func messageHistoryEvent(ctx context.Context, tx *sql.Tx, m StoredMessage, label string) error {
	text := m.Text
	if text == "" {
		text = "原渠道未提供正文，不能推测回复内容。"
	}
	event := RunEvent{ID: m.ID + "-history", SessionID: m.Principal.SessionID, Kind: "message.history", Text: label + "\n" + text, CreatedAt: m.CreatedAt}
	_, e := tx.ExecContext(ctx, `INSERT IGNORE INTO wb_events(id,session_id,run_id,body) VALUES(?,?,?,?)`, event.ID, event.SessionID, "", jsonText(event))
	return e
}
func (s *Store) PauseReceived(ctx context.Context, m StoredMessage) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, `UPDATE wb_messages SET body=JSON_SET(body,'$.state','paused') WHERE connection_id=? AND event_id=? AND message_state='received'`, m.ConnectionID, m.EventID)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n == 1 {
		if e = messageHistoryEvent(ctx, tx, m, "客户消息（配置变动，本条已停止自动处理）"); e != nil {
			return e
		}
	}
	return tx.Commit()
}
