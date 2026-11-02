package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	gw "projectworkbench/pkg/wecomgateway"
)

const gatewaySource = "wecom-gateway"
const gatewaySchema = `
CREATE TABLE IF NOT EXISTS gateway_bindings(id TEXT PRIMARY KEY, connection_id TEXT NOT NULL REFERENCES connections(id), account_id TEXT NOT NULL, data TEXT NOT NULL, UNIQUE(connection_id,account_id));
CREATE TABLE IF NOT EXISTS gateway_sessions(id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE, binding_id TEXT NOT NULL REFERENCES gateway_bindings(id), external_key TEXT NOT NULL UNIQUE, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS gateway_events(connection_id TEXT NOT NULL REFERENCES connections(id), event_id TEXT NOT NULL, hash TEXT NOT NULL, PRIMARY KEY(connection_id,event_id));
`

type GatewayConnection struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Label    string   `json:"label"`
	Enabled  bool     `json:"enabled"`
	BaseURL  string   `json:"baseUrl"`
	TenantID string   `json:"tenantId"`
	Accounts []string `json:"accounts"`
}
type GatewayRegistration struct {
	GatewayConnection
	APIKey        string `json:"apiKey"`
	WebhookSecret string `json:"webhookSecret"`
}
type GatewaySecrets struct {
	APIKey        string `json:"apiKey"`
	WebhookSecret string `json:"webhookSecret"`
}
type GatewayBinding struct {
	KnowledgePaths []string `json:"knowledgePaths,omitempty"`
	ContactIDs     []string `json:"contactIds,omitempty"`
	ID             string   `json:"id"`
	ConnectionID   string   `json:"connectionId"`
	AccountID      string   `json:"accountId"`
	ProjectID      string   `json:"projectId"`
	ExpertID       string   `json:"expertId"`
	Enabled        bool     `json:"enabled"`
	Version        int64    `json:"version"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	NotifyChatID   string   `json:"notifyChatId"`
	ToolIDs        []string `json:"toolIds,omitempty"`
	PluginIDs      []string `json:"pluginIds,omitempty"`
}
type GatewaySession struct {
	ExternalUserID   string `json:"externalUserId,omitempty"`
	IdentityConflict bool   `json:"identityConflict,omitempty"`
	ResumeNS         int64  `json:"resumeNs,omitempty"`
	HistoryCursor    int64  `json:"historyCursor,omitempty"`
	ManualReply      string `json:"manualReply,omitempty"`
	ID               string `json:"id"`
	BindingID        string `json:"bindingId"`
	BindingVersion   int64  `json:"bindingVersion"`
	ConnectionID     string `json:"connectionId"`
	TenantID         string `json:"tenantId"`
	AccountID        string `json:"accountId"`
	ContactID        string `json:"contactId"`
	Name             string `json:"name"`
	Mode             string `json:"mode"`
	Version          int64  `json:"version"`
	Reason           string `json:"reason,omitempty"`
	LastHumanAt      int64  `json:"lastHumanAt"`
	LastMessageAt    int64  `json:"lastMessageAt"`
}
type GatewayRun struct {
	HistoryFrom    int64    `json:"historyFrom,omitempty"`
	HistoryThrough int64    `json:"historyThrough,omitempty"`
	HistoryPreview string   `json:"historyPreview,omitempty"`
	HistoryFile    bool     `json:"historyFile,omitempty"`
	HistoryRole    string   `json:"historyRole,omitempty"`
	KnowledgePaths []string `json:"knowledgePaths,omitempty"`
	ManualReply    string   `json:"manualReply,omitempty"`
	Version        int64    `json:"version"`
	BindingVersion int64    `json:"bindingVersion"`
	Deadline       int64    `json:"deadline"`
	ToolIDs        []string `json:"toolIds,omitempty"`
	PluginIDs      []string `json:"pluginIds,omitempty"`
}
type GatewayDelivery struct {
	CRMKey         string     `json:"crmKey,omitempty"`
	CRMNotice      *crmNotice `json:"crmNotice,omitempty"`
	FirstAttemptAt int64      `json:"firstAttemptAt,omitempty"`
	Version        int64      `json:"version"`
	BindingVersion int64      `json:"bindingVersion"`
	Deadline       int64      `json:"deadline"`
	ContactID      string     `json:"contactId"`
	AccountID      string     `json:"accountId"`
	NotifyChatID   string     `json:"notifyChatId,omitempty"`
	Attempts       int        `json:"attempts"`
	NextAt         int64      `json:"nextAt"`
}

func (s *Store) GatewayConnections() ([]GatewayConnection, error) {
	return listJSON[GatewayConnection](s.DB, "SELECT data FROM connections WHERE kind=? ORDER BY id", gatewaySource)
}
func (s *Store) gatewayConnection(id string) (GatewayConnection, GatewaySecrets, error) {
	c, e := getJSON[GatewayConnection](s.DB, "SELECT data FROM connections WHERE id=? AND kind=?", id, gatewaySource)
	if e != nil {
		return c, GatewaySecrets{}, e
	}
	k, e := getJSON[GatewaySecrets](s.DB, "SELECT data FROM connection_secrets WHERE id=?", id)
	return c, k, e
}
func (s *Store) SaveGatewayConnection(v GatewayRegistration) (GatewayConnection, error) {
	c := v.GatewayConnection
	c.Kind = gatewaySource
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.ID == "" {
		c.ID = newID()
	}
	if !toolIdentifier.MatchString(c.ID) || len(c.Accounts) == 0 || v.APIKey == "" || v.WebhookSecret == "" {
		return c, errors.New("connection identity, accounts and credentials required")
	}
	if e := gw.ValidateURL(c.BaseURL); e != nil {
		return c, e
	}
	seen := map[string]bool{}
	for _, a := range c.Accounts {
		if a == "" || seen[a] {
			return c, errors.New("invalid account list")
		}
		seen[a] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		rows, e := tx.Query("SELECT data FROM connections WHERE kind=?", gatewaySource)
		if e != nil {
			return e
		}
		var old *GatewayConnection
		for rows.Next() {
			var raw string
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return e
			}
			var other GatewayConnection
			if e = json.Unmarshal([]byte(raw), &other); e != nil {
				rows.Close()
				return e
			}
			if other.ID == c.ID {
				copy := other
				old = &copy
			} else if c.Enabled && other.Enabled && other.BaseURL == c.BaseURL && (other.TenantID == c.TenantID || other.TenantID == "" || c.TenantID == "") {
				rows.Close()
				return errors.New("gateway tenant already has an ingress connection")
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if old != nil && c.TenantID == "" {
			c.TenantID = old.TenantID
		}
		if old != nil && (old.BaseURL != c.BaseURL || old.TenantID != c.TenantID || encode(old.Accounts) != encode(c.Accounts)) {
			return errors.New("connection identity/accounts immutable; disable this connection and create a replacement connection")
		}
		var kind string
		e = tx.QueryRow("SELECT kind FROM connections WHERE id=?", c.ID).Scan(&kind)
		if e == nil && kind != gatewaySource {
			return errors.New("connection ID belongs to another channel")
		}
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if _, e = tx.Exec("INSERT INTO connections VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", c.ID, gatewaySource, encode(c)); e != nil {
			return e
		}
		if old != nil && old.Enabled && !c.Enabled {
			if _, e = tx.Exec("UPDATE gateway_sessions SET data=json_set(data,'$.mode','human','$.version',json_extract(data,'$.version')+1,'$.reason','连接已停用') WHERE json_extract(data,'$.connectionId')=?", c.ID); e != nil {
				return e
			}
			if _, e = tx.Exec("UPDATE deliveries SET state='cancelled',data=json_set(data,'$.state','cancelled') WHERE connection_id=? AND state='pending' AND json_extract(data,'$.kind')='gateway.answer'", c.ID); e != nil {
				return e
			}
		}
		_, e = tx.Exec("INSERT INTO connection_secrets VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", c.ID, encode(GatewaySecrets{v.APIKey, v.WebhookSecret}))
		return e
	})
	return c, err
}
func (s *Store) SaveGatewayBinding(b GatewayBinding) (GatewayBinding, error) {
	c, _, e := s.gatewayConnection(b.ConnectionID)
	if e != nil {
		return b, e
	}
	if !contains(c.Accounts, b.AccountID) {
		return b, errors.New("account outside connection scope")
	}
	p, e := s.Project(b.ProjectID)
	if e != nil {
		return b, e
	}
	if p.ID == personalProjectID || p.Archived {
		return b, errors.New("active business project required")
	}
	if b.ExpertID == "" {
		b.ExpertID = p.DefaultExpertID
	}
	if b.ExpertID != "" && b.ExpertID != "general" && !contains(p.ExpertIDs, b.ExpertID) {
		return b, errors.New("expert not in project")
	}
	if b.TimeoutSeconds == 0 {
		b.TimeoutSeconds = 120
	}
	if b.TimeoutSeconds < 1 || b.TimeoutSeconds > 120 || b.NotifyChatID == "" {
		return b, errors.New("timeout must be 1..120 seconds and notification chat required")
	}
	if e := validateGatewayKnowledge(b.KnowledgePaths); e != nil {
		return b, e
	}
	for _, id := range b.ToolIDs {
		t, e := s.CLITool(id)
		if e != nil || !t.Installed || !t.Enabled || id == "lark" || t.Command == "lark-cli" || t.Command == "wecom-gw-cli" {
			return b, errors.New("only explicitly enabled non-transport tools may be granted")
		}
	}
	plugins, e := s.enabledProjectPlugins(b.ProjectID)
	if e != nil {
		return b, e
	}
	for _, id := range b.PluginIDs {
		found := false
		for _, p := range plugins {
			if p.ID == id {
				found = true
			}
		}
		if !found {
			return b, errors.New("configured business MCP unavailable")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRow("SELECT data FROM gateway_bindings WHERE connection_id=? AND account_id=?", b.ConnectionID, b.AccountID).Scan(&raw)
		requestedVersion := b.Version
		b.Version = 1
		if err == nil {
			var old GatewayBinding
			if err = json.Unmarshal([]byte(raw), &old); err != nil {
				return err
			}
			if b.ID != "" && b.ID != old.ID {
				return errors.New("account already bound")
			}
			providedID := b.ID
			if providedID != "" && requestedVersion > 0 && requestedVersion != old.Version {
				return ErrConflict
			}
			b.ID = old.ID
			b.Version = old.Version
			if encode(b) == encode(old) {
				return nil
			}
			if providedID == "" {
				return errors.New("account already bound; updating requires its explicit binding ID")
			}
			b.ID = old.ID
			b.Version = old.Version + 1
			if _, err = tx.Exec("UPDATE deliveries SET state='cancelled',data=json_set(data,'$.state','cancelled') WHERE connection_id=? AND state='pending' AND json_extract(data,'$.gateway.accountId')=?", b.ConnectionID, b.AccountID); err != nil {
				return err
			}
		} else if err != sql.ErrNoRows {
			return err
		} else if b.ID != "" {
			return errors.New("binding ID not found; account identity cannot be changed")
		}
		if b.ID == "" {
			b.ID = newID()
		}
		_, err = tx.Exec("INSERT INTO gateway_bindings VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", b.ID, b.ConnectionID, b.AccountID, encode(b))
		return err
	})
	return b, e
}
func (s *Store) GatewaySessions() ([]GatewaySession, error) {
	return listJSON[GatewaySession](s.DB, "SELECT data FROM gateway_sessions ORDER BY rowid DESC LIMIT 200")
}
func (s *Store) gatewaySession(id string) (GatewaySession, error) {
	return getJSON[GatewaySession](s.DB, "SELECT data FROM gateway_sessions WHERE id=?", id)
}
func txGatewaySession(tx *sql.Tx, id string) (GatewaySession, error) {
	var raw string
	var g GatewaySession
	e := tx.QueryRow("SELECT data FROM gateway_sessions WHERE id=?", id).Scan(&raw)
	if e == nil {
		e = json.Unmarshal([]byte(raw), &g)
	}
	return g, e
}
func saveGatewaySession(tx *sql.Tx, g GatewaySession) error {
	_, e := tx.Exec("UPDATE gateway_sessions SET data=? WHERE id=?", encode(g), g.ID)
	return e
}

func (s *Store) SetGatewayMode(id, mode, reason string) error {
	if mode != "auto" && mode != "human" {
		return errors.New("invalid conversation mode")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		g, e := txGatewaySession(tx, id)
		if e != nil {
			return e
		}
		return s.changeGatewayModeTx(tx, &g, mode, reason)
	})
}
func (s *Store) changeGatewayModeTx(tx *sql.Tx, g *GatewaySession, mode, reason string) error {
	if mode == "auto" {
		var active int
		e := tx.QueryRow(`SELECT count(*) FROM gateway_bindings b JOIN connections c ON c.id=b.connection_id JOIN sessions ss ON ss.id=? JOIN projects p ON p.id=ss.project_id WHERE b.id=? AND json_extract(b.data,'$.projectId')=p.id AND json_extract(b.data,'$.enabled')=1 AND json_extract(c.data,'$.enabled')=1 AND json_extract(b.data,'$.version')=? AND coalesce(json_extract(ss.data,'$.recycledAt'),0)=0 AND p.archived=0`, g.ID, g.BindingID, g.BindingVersion).Scan(&active)
		if e != nil {
			return e
		}
		if active != 1 {
			return crmError("not_bound")
		}
	}
	if g.Mode == mode {
		return nil
	}
	g.Mode = mode
	g.Version++
	g.Reason = reason
	if mode == "auto" {
		g.ResumeNS = time.Now().UnixNano()
	}
	if mode == "human" {
		if _, e := tx.Exec(`UPDATE inbox SET state=CASE state WHEN 'gateway_pending' THEN 'handled' ELSE state END,
      data=json_set(data,'$.gateway.historyRole','客户','$.state',CASE state WHEN 'gateway_pending' THEN 'handled' ELSE state END)
      WHERE source=? AND session_id=? AND state IN ('gateway_pending','queued') AND NOT EXISTS (SELECT 1 FROM runs WHERE message_id=inbox.id AND state<>'queued')`, gatewaySource, g.ID); e != nil {
			return e
		}
	}
	if e := saveGatewaySession(tx, *g); e != nil {
		return e
	}
	if e := s.crmNotifyTx(tx, *g); e != nil {
		return e
	}
	_, e := tx.Exec("UPDATE deliveries SET state='cancelled',data=json_set(data,'$.state','cancelled') WHERE json_extract(data,'$.sessionId')=? AND state='pending' AND json_extract(data,'$.kind')='gateway.answer'", g.ID)
	return e
}

// ReceiveGateway commits the signed event, normalized message and conversation
// together. Model work is queued later by the existing worker.
func (s *Store) ReceiveGateway(connection string, event gw.Event, raw []byte) error {
	c, _, e := s.gatewayConnection(connection)
	if e != nil {
		return e
	}
	if !c.Enabled || c.TenantID != "" && c.TenantID != event.TenantID || !contains(c.Accounts, event.AccountID) {
		return errors.New("event outside connection scope")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var current string
		if e := tx.QueryRow("SELECT data FROM connections WHERE id=? AND kind=?", connection, gatewaySource).Scan(&current); e != nil {
			return e
		}
		if e := json.Unmarshal([]byte(current), &c); e != nil {
			return e
		}
		if !c.Enabled || c.TenantID != "" && c.TenantID != event.TenantID || !contains(c.Accounts, event.AccountID) {
			return errors.New("connection changed before receive")
		}
		if c.TenantID == "" {
			c.TenantID = event.TenantID
			if _, e := tx.Exec("UPDATE connections SET data=? WHERE id=?", encode(c), c.ID); e != nil {
				return e
			}
		}
		var hash string
		e := tx.QueryRow("SELECT hash FROM gateway_events WHERE connection_id=? AND event_id=?", connection, event.EventID).Scan(&hash)
		if e == nil {
			if hash != digest(raw) {
				return errors.New("event ID reused with different content")
			}
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		if _, e = tx.Exec("INSERT INTO gateway_events VALUES(?,?,?)", connection, event.EventID, digest(raw)); e != nil {
			return e
		}
		if event.Type == "message.status" {
			return s.gatewayStatusTx(tx, connection, event.Data)
		}
		if event.Type != "message.received" && event.Type != "message.self_sent" {
			return nil
		}
		var payload struct {
			Message gw.Message `json:"message"`
		}
		if e = json.Unmarshal(event.Data, &payload); e != nil {
			return errors.New("invalid message event")
		}
		m := payload.Message
		if m.MessageID == "" || m.AccountID != "" && m.AccountID != event.AccountID {
			return errors.New("message identity mismatch")
		}
		if m.RoomID != "" || m.Historical {
			return nil
		}
		contact := m.ContactID
		if contact == "" {
			contact = m.Sender.ContactID
		}
		if contact == "" {
			return errors.New("private conversation identity missing")
		}
		var existing string
		// A replacement ingress must not replay a message already accepted by
		// its retired predecessor. Gateway message IDs are tenant scoped.
		e = tx.QueryRow(`SELECT i.id FROM inbox i JOIN connections c ON c.id=i.connection_id
			WHERE i.source=? AND i.event_id=? AND c.kind=?
			AND json_extract(c.data,'$.baseUrl')=? AND json_extract(c.data,'$.tenantId')=? LIMIT 1`, gatewaySource, m.MessageID, gatewaySource, c.BaseURL, c.TenantID).Scan(&existing)
		if e == nil {
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		var bindingRaw string
		e = tx.QueryRow("SELECT data FROM gateway_bindings WHERE connection_id=? AND account_id=?", connection, event.AccountID).Scan(&bindingRaw)
		if e == sql.ErrNoRows {
			return errors.New("account binding is not configured; retry after configuration")
		}
		if e != nil {
			return e
		}
		var b GatewayBinding
		if e = json.Unmarshal([]byte(bindingRaw), &b); e != nil {
			return e
		}
		if !b.Enabled {
			return errors.New("account binding is disabled")
		}
		if len(b.ContactIDs) > 0 && !contains(b.ContactIDs, contact) {
			return nil
		}
		if event.Type == "message.received" && (m.Type == "system" || m.Type == "unsupported") {
			return nil
		}
		key := digest([]byte(encode([]any{c.ID, c.TenantID, event.AccountID, contact, b.Version})))
		var g GatewaySession
		var gr string
		e = tx.QueryRow("SELECT data FROM gateway_sessions WHERE external_key=?", key).Scan(&gr)
		if e == nil {
			if e = json.Unmarshal([]byte(gr), &g); e != nil {
				return e
			}
		} else if e == sql.ErrNoRows {
			ss := Session{ID: newID(), ProjectID: b.ProjectID, HarnessID: "", Title: "企微 · " + contact, Kind: "topic", ExpertID: b.ExpertID, TitleSource: "manual", CreatedAt: now(), UpdatedAt: now()}
			ss.HarnessID = "pw-" + ss.ID
			if _, e = tx.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?)", ss.ID, ss.ProjectID, ss.HarnessID, ss.Kind, ss.UpdatedAt, encode(ss)); e != nil {
				return e
			}
			g = GatewaySession{ID: ss.ID, BindingID: b.ID, BindingVersion: b.Version, ConnectionID: connection, TenantID: c.TenantID, AccountID: event.AccountID, ContactID: contact, Name: m.Sender.Name, Mode: "auto", Version: 1}
			if _, e = tx.Exec("INSERT INTO gateway_sessions VALUES(?,?,?,?)", g.ID, b.ID, key, encode(g)); e != nil {
				return e
			}
		} else {
			return e
		}
		if event.Type == "message.received" && m.Direction == "in" {
			external := m.ExternalUserID
			if m.Sender.ContactID == "" || m.Sender.ContactID == contact {
				if external == "" {
					external = m.Sender.ExternalUserID
				} else if m.Sender.ExternalUserID != "" && external != m.Sender.ExternalUserID {
					g.IdentityConflict = true
				}
			}
			if external != "" {
				if g.ExternalUserID != "" && g.ExternalUserID != external {
					g.IdentityConflict = true
				} else {
					g.ExternalUserID = external
				}
			}
		}
		eventTime := event.OccurredAt
		if !m.SentAt.IsZero() {
			eventTime = m.SentAt
		}
		historical := beforeGatewayResume(eventTime, g.ResumeNS)
		at := event.OccurredAt.UnixMilli()
		if !m.SentAt.IsZero() {
			at = m.SentAt.UnixMilli()
		}
		if at > now() {
			at = now()
		}
		earlierThan := func(mark int64) bool { return beforeGatewayResume(eventTime, mark*int64(time.Millisecond)) }
		state := "gateway_pending"
		if event.Type == "message.self_sent" {
			state = "handled"
			if !historical && !earlierThan(g.LastHumanAt) {
				g.LastHumanAt = at
				if g.Mode != "human" {
					if e = s.changeGatewayModeTx(tx, &g, "human", "人工已在企微回复"); e != nil {
						return e
					}
				}
				g.Reason = "人工已在企微回复"
				g.ManualReply = m.Content.Text
				if e = saveGatewaySession(tx, g); e != nil {
					return e
				}
			}
		}
		if event.Type == "message.received" && (m.Direction != "in" || m.Type != "text" || strings.TrimSpace(m.Content.Text) == "" || len(m.Content.Text) > 16000) {
			state = "handled"
			if m.Direction == "in" && !historical {
				if e = s.gatewayHandoffTx(tx, &g, b, "首期仅自动处理文字消息", ""); e != nil {
					return e
				}
			}
		}
		if !historical && event.Type == "message.received" && earlierThan(g.LastMessageAt) && g.Mode == "auto" {
			if e = s.gatewayHandoffTx(tx, &g, b, "消息乱序，需要核对完整上下文", m.Content.Text); e != nil {
				return e
			}
		}
		if historical || g.Mode != "auto" || earlierThan(g.LastMessageAt) || earlierThan(g.LastHumanAt) {
			state = "handled"
		}
		if at > g.LastMessageAt {
			g.LastMessageAt = at
		}
		if e = saveGatewaySession(tx, g); e != nil {
			return e
		}
		msg := Message{ID: newID(), SessionID: g.ID, State: state, CreatedAt: at, Gateway: &GatewayRun{Version: g.Version, BindingVersion: b.Version, Deadline: at + int64(b.TimeoutSeconds)*1000, ToolIDs: b.ToolIDs, PluginIDs: b.PluginIDs, KnowledgePaths: b.KnowledgePaths}, Incoming: Incoming{Source: gatewaySource, ConnectionID: connection, ActorID: contact, ConversationID: contact, EventID: m.MessageID, SessionID: g.ID, ExpertID: b.ExpertID, Text: m.Content.Text}}
		if state == "handled" && (event.Type == "message.self_sent" || m.Direction == "in") {
			msg.Gateway.HistoryRole = "客户"
			if event.Type == "message.self_sent" {
				msg.Gateway.HistoryRole = "人工客服"
			}
		}
		_, e = tx.Exec("INSERT INTO inbox VALUES(?,?,?,?,?,?,?,?,?)", msg.ID, gatewaySource, connection, m.MessageID, entryKey(msg.Incoming), state, g.ID, msg.CreatedAt, encode(msg))
		return e
	})
}

func (s *Store) gatewayHandoffTx(tx *sql.Tx, g *GatewaySession, b GatewayBinding, reason, summary string) error {
	if g.Mode == "human" {
		return nil
	}
	if e := s.changeGatewayModeTx(tx, g, "human", reason); e != nil {
		return e
	}
	text := fmt.Sprintf("需要人工接管\n会话：%s\n账号：%s\n客户：%s (%s)\n原因：%s\n摘要：%s\n请在企微直接回复；恢复AI需明确操作。", g.ID, g.AccountID, g.Name, g.ContactID, reason, summary)
	d := Delivery{ID: newID(), SessionID: g.ID, ConnectionID: g.ConnectionID, ConversationID: g.ContactID, Kind: "gateway.handoff", Text: text, State: "pending", CreatedAt: now(), Gateway: &GatewayDelivery{Version: g.Version, BindingVersion: g.BindingVersion, NotifyChatID: b.NotifyChatID}}
	_, e := tx.Exec("INSERT OR IGNORE INTO deliveries VALUES(?,?,?,?,?,?)", d.ID, fmt.Sprintf("gateway-handoff:%s:%d", g.ID, g.Version), d.ConnectionID, d.State, d.CreatedAt, encode(d))
	return e
}
func (s *Store) gatewayBinding(id string) (GatewayBinding, error) {
	return getJSON[GatewayBinding](s.DB, "SELECT data FROM gateway_bindings WHERE id=?", id)
}
func gatewayBindingTx(tx *sql.Tx, id string) (GatewayBinding, error) {
	var raw string
	var b GatewayBinding
	e := tx.QueryRow("SELECT data FROM gateway_bindings WHERE id=?", id).Scan(&raw)
	if e == nil {
		e = json.Unmarshal([]byte(raw), &b)
	}
	return b, e
}

func (s *Store) gatewayRunMetadata(m Message) (*GatewayRun, error) {
	g, e := s.gatewaySession(m.SessionID)
	if e != nil {
		return nil, e
	}
	b, e := s.gatewayBinding(g.BindingID)
	if e != nil {
		return nil, e
	}
	c, _, e := s.gatewayConnection(g.ConnectionID)
	if e != nil {
		return nil, e
	}
	if m.Gateway == nil || !c.Enabled || g.Mode != "auto" || !b.Enabled || g.BindingVersion != b.Version || m.Gateway.Version != g.Version || m.Gateway.BindingVersion != b.Version {
		return nil, errors.New("conversation no longer active")
	}
	m.Gateway.ManualReply = g.ManualReply
	return m.Gateway, nil
}
func (s *Store) queueGateway(m Message) error {
	meta, e := s.gatewayRunMetadata(m)
	if e != nil {
		_, e = s.DB.Exec("UPDATE inbox SET state='handled',data=json_set(data,'$.state','handled','$.gateway.historyRole','客户') WHERE id=? AND state='gateway_pending'", m.ID)
		return e
	}
	if meta.Deadline <= now() {
		return s.handoffGatewayVersion(m.SessionID, m.Gateway, "等待应答超时", m.Incoming.Text)
	}
	_, e = s.queue(m, m.SessionID, "", false)
	if e != nil {
		return s.handoffGatewayVersion(m.SessionID, m.Gateway, "无法启动专家应答", m.Incoming.Text)
	}
	return nil
}
func (s *Store) handoffGatewayVersion(session string, expected *GatewayRun, reason, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		g, e := txGatewaySession(tx, session)
		if e != nil {
			return e
		}
		b, e := gatewayBindingTx(tx, g.BindingID)
		if e != nil {
			return e
		}
		if expected != nil && (g.Version != expected.Version || g.BindingVersion != expected.BindingVersion || b.Version != expected.BindingVersion) {
			return nil
		}
		if e = s.gatewayHandoffTx(tx, &g, b, reason, summary); e != nil {
			return e
		}
		_, e = tx.Exec("UPDATE inbox SET state='handled',data=json_set(data,'$.state','handled','$.gateway.historyRole','客户') WHERE session_id=? AND state='gateway_pending'", session)
		return e
	})
}

func (s *Store) finishGatewayRun(r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		g, e := txGatewaySession(tx, r.SessionID)
		if e != nil {
			return e
		}
		b, e := gatewayBindingTx(tx, g.BindingID)
		if e != nil {
			return e
		}
		valid := r.Gateway != nil && g.Mode == "auto" && g.Version == r.Gateway.Version && b.Enabled && b.Version == r.Gateway.BindingVersion && b.Version == g.BindingVersion
		if valid {
			var decision struct {
				Action  string `json:"action"`
				Text    string `json:"text,omitempty"`
				Reason  string `json:"reason,omitempty"`
				Summary string `json:"summary,omitempty"`
			}
			dec := json.NewDecoder(strings.NewReader(r.Result))
			dec.DisallowUnknownFields()
			parseErr := dec.Decode(&decision)
			var trailing any
			if parseErr == nil && dec.Decode(&trailing) != io.EOF {
				parseErr = errors.New("trailing result")
			}
			if r.State != "completed" || r.Gateway.Deadline <= now() || parseErr != nil || !contains([]string{"reply", "handoff", "ignore"}, decision.Action) || decision.Action == "reply" && (strings.TrimSpace(decision.Text) == "" || len(decision.Text) > 16000) {
				decision.Action = "handoff"
				decision.Reason = "专家应答失败、超时或结果格式不完整"
				decision.Summary = ""
			}
			switch decision.Action {
			case "reply":
				d := Delivery{ID: newID(), MessageID: r.MessageID, RunID: r.ID, SessionID: r.SessionID, ConnectionID: g.ConnectionID, ConversationID: g.ContactID, Kind: "gateway.answer", Text: decision.Text, State: "pending", CreatedAt: now(), Gateway: &GatewayDelivery{Version: g.Version, BindingVersion: b.Version, Deadline: r.Gateway.Deadline, ContactID: g.ContactID, AccountID: g.AccountID}}
				_, e = tx.Exec("INSERT OR IGNORE INTO deliveries VALUES(?,?,?,?,?,?)", d.ID, "gateway-answer:"+r.ID, d.ConnectionID, d.State, d.CreatedAt, encode(d))
			case "handoff":
				if decision.Summary == "" {
					decision.Summary = r.Text
				}
				if decision.Reason == "" {
					decision.Reason = "专家请求人工处理"
				}
				e = s.gatewayHandoffTx(tx, &g, b, decision.Reason, decision.Summary)
			}
			if e != nil {
				return e
			}
		}
		if _, e = tx.Exec("UPDATE runs SET state=?,data=? WHERE id=?", r.State, encode(r), r.ID); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE inbox SET state=?,data=json_set(data,'$.state',?) WHERE id=?", r.State, r.State, r.MessageID); e != nil {
			return e
		}
		_, e = tx.Exec("DELETE FROM project_locks WHERE run_id=?", r.ID)
		return e
	})
}

func gatewayClient(c GatewayConnection, k GatewaySecrets) gw.Client {
	return gw.Client{BaseURL: c.BaseURL, APIKey: k.APIKey}
}
func gatewayMessagePath(id string) string { return "/messages/" + url.PathEscape(id) }
