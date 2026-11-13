package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Selection is frozen into the first persisted incoming message. Replaying an
// old bare 发 can never select a decision created later.
func (s *Store) matchSendingDecision(ctx context.Context, text string, occurred *time.Time) (string, error) {
	if text != "发" && !strings.HasPrefix(text, "发 ") {
		return "", nil
	}
	query := `SELECT d.body FROM wb_decisions d WHERE d.state='waiting' AND EXISTS(SELECT 1 FROM wb_deliveries m WHERE m.run_id=d.run_id)`
	args := []any{}
	if text == "发" && (occurred == nil || occurred.After(time.Now().Add(time.Minute))) {
		return "", nil
	}
	if text != "发" {
		code := strings.TrimSpace(strings.TrimPrefix(text, "发 "))
		if len(code) != 8 {
			return "", nil
		}
		query += ` AND LEFT(d.run_id,8)=?`
		args = append(args, code)
	}
	rows, e := s.DB.QueryContext(ctx, query+` LIMIT 2`, args...)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	id := ""
	count := 0
	for rows.Next() {
		decision, err := scanJSON[Decision](rows)
		if err != nil {
			return "", err
		}
		if text == "发" && (decision.CreatedAt.IsZero() || !occurred.After(decision.CreatedAt)) {
			return "", nil
		}
		id = decision.ID
		count++
	}
	if e = rows.Err(); e != nil {
		return "", e
	}
	if count != 1 {
		return "", nil
	}
	return id, nil
}
func (s *Store) OwnerMessageDecision(ctx context.Context, m StoredMessage) (Decision, bool, error) {
	if m.Principal.Kind != "personal" || m.DecisionID == "" {
		return Decision{}, false, nil
	}
	route, e := s.Route(ctx, m.RouteID)
	if e != nil {
		return Decision{}, false, e
	}
	if !route.Enabled || route.Principal != m.Principal {
		return Decision{}, false, ErrDenied
	}
	d, e := s.Decision(ctx, m.Principal, m.DecisionID)
	return d, e == nil, e
}

// The commit changing prepared→submitting is the last cancellable boundary.
// Gate changes and this claim lock the same rows; after claim, only receipts
// can establish the external outcome, never a resend.
func (s *Store) deliveryGate(ctx context.Context, tx *sql.Tx, d Delivery) error {
	if IsReplyPracticeConnection(d.ConnectionID) {
		return ErrDenied
	}
	r, e := scanJSON[Run](tx.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, d.RunID))
	if e != nil {
		return e
	}
	if d.Target["kind"] == "conversation.mode.changed" && r.Operation != "control_notification" {
		return ErrDenied
	}
	c, e := scanJSON[Transport](tx.QueryRowContext(ctx, `SELECT body FROM wb_transports WHERE id=? FOR SHARE`, d.ConnectionID))
	if e != nil {
		return e
	}
	allowed := r.Principal.Kind != "project"
	for _, p := range c.Projects {
		if p == r.Principal.ProjectID {
			allowed = true
		}
	}
	if r.Operation != "control_notification" && (!c.Enabled || !allowed) {
		return ErrDenied
	}
	if r.Operation == "reply" || r.Operation == "message" || r.Operation == "dispatch_batch" {
		if e = s.modeDeliveryGate(ctx, tx, c, d); e != nil {
			return e
		}
	}
	rows, e := tx.QueryContext(ctx, `SELECT resource_id,version,section_id FROM wb_run_sources WHERE run_id=?`, r.ID)
	if e != nil {
		return e
	}
	actual := []Ref{}
	for rows.Next() {
		var ref Ref
		if e = rows.Scan(&ref.ResourceID, &ref.Version, &ref.Section); e != nil {
			rows.Close()
			return e
		}
		actual = append(actual, ref)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if e = AuthorizeAll(ctx, transactionGate{tx}, r.Principal, UnionRefs(r.Sources, r.Lineage, actual)); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_delivery_transports VALUES(?,?)`, d.ID, jsonText(c)); e != nil {
		return e
	}
	switch r.Operation {
	case "owner_message":
		return ownerMessageGate(ctx, tx, r, d)
	case "control_notification":
		event, err := scanJSON[RunEvent](tx.QueryRowContext(ctx, `SELECT body FROM wb_events WHERE id=?`, r.OriginMessage))
		if err != nil || r.Principal.Kind != "owner" || event.Kind != "conversation.mode.changed" || d.Target["kind"] != event.Kind || d.Target["eventId"] != event.ID {
			return ErrDenied
		}
		raw, err := json.Marshal(event.Details)
		if err != nil || string(raw) != d.Text || r.Goal != d.Text {
			return ErrDenied
		}
		var change ModeChange
		if json.Unmarshal(raw, &change) != nil || change.ConnectionID != d.ConnectionID {
			return ErrDenied
		}
	case "dispatch_batch":
		if r.Dispatch == nil {
			return ErrInvalid
		}
		decision, err := scanJSON[Decision](tx.QueryRowContext(ctx, `SELECT body FROM wb_decisions WHERE id=? FOR SHARE`, r.ID+"-d1"))
		if err != nil || decision.State != "answered" || (decision.Answer != "发" && decision.Answer != "发 "+r.ID[:8]) {
			return ErrDenied
		}
		var current string
		if e = tx.QueryRowContext(ctx, `SELECT current_version FROM wb_resources WHERE id=? FOR SHARE`, r.Dispatch.Eligibility.ResourceID).Scan(&current); e != nil {
			return e
		}
		if current != r.Dispatch.Eligibility.Version {
			return fmt.Errorf("%w: eligibility version changed; prepare and review a new batch", ErrConflict)
		}
		resource, e := scanJSON[Resource](tx.QueryRowContext(ctx, `SELECT body FROM wb_resources WHERE id=? FOR SHARE`, r.Dispatch.Eligibility.ResourceID))
		if e != nil {
			return e
		}
		found := false
		for _, check := range resource.Checks {
			if check.ID == r.Dispatch.Check.ID && Digest(check) == Digest(r.Dispatch.Check) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%w: registered check changed; prepare and review a new batch", ErrConflict)
		}
	case "reply":
		var routeID string
		if e = tx.QueryRowContext(ctx, `SELECT JSON_UNQUOTE(JSON_EXTRACT(body,'$.routeId')) FROM wb_messages WHERE connection_id=? AND event_id=?`, d.ConnectionID, d.Target["replyTo"]).Scan(&routeID); e != nil {
			return e
		}
		route, e := scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE id=? FOR SHARE`, routeID))
		if e != nil {
			return e
		}
		cfg, ce := scanJSON[ReplySettings](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=? FOR SHARE`, "reply-"+r.Principal.ProjectID))
		if ce != nil && !IsMissing(ce) {
			return ce
		}
		if ce == nil && (!cfg.Enabled || cfg.ConnectionID != d.ConnectionID) {
			return ErrDenied
		}
		m, e := scanJSON[StoredMessage](tx.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE connection_id=? AND event_id=? FOR UPDATE`, d.ConnectionID, d.Target["replyTo"]))
		if e != nil {
			return e
		}
		if ce == nil && cfg.Revision != m.SettingsRevision {
			return ErrDenied
		}
		if !route.Enabled || !route.AutoReply || route.Principal != r.Principal || m.State != "reply_queued" || routeRevision(route) != max(int64(1), m.RouteRevision) {
			return ErrDenied
		}
		if m.BusinessValidUntil != nil && !time.Now().Before(*m.BusinessValidUntil) {
			return errBusinessPolicyExpired
		}
		m.State = "reply_submitting"
		_, e = tx.ExecContext(ctx, `UPDATE wb_messages SET body=? WHERE connection_id=? AND event_id=?`, jsonText(m), d.ConnectionID, m.EventID)
		return e
	case "notification":
		config, e := scanJSON[NotificationSettings](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id='notifications' FOR SHARE`))
		if e != nil {
			return e
		}
		if !config.Enabled || config.SessionID != r.Principal.SessionID {
			return ErrDenied
		}
		allowed := false
		for _, t := range config.Targets {
			if t.ConnectionID == d.ConnectionID && Digest(t.Target) == Digest(d.Target) {
				allowed = true
			}
		}
		if !allowed {
			return ErrDenied
		}
		route, e := scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE connection_id=? AND conversation_id=? AND actor_id=? FOR SHARE`, d.ConnectionID, d.Target["conversationId"], d.Target["actorId"]))
		if e != nil {
			return e
		}
		if !route.Enabled || route.Principal != r.Principal {
			return ErrDenied
		}
	}
	return nil
}

func (s *Store) DeliveryTransport(ctx context.Context, id string) (Transport, error) {
	return scanJSON[Transport](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_delivery_transports WHERE id=?`, id))
}
func contextIdentities(refs []RecordRef) []string {
	out := []string{}
	for _, r := range refs {
		out = append(out, r.Ref.ResourceID+"/"+r.Ref.Section+"/"+r.ID)
	}
	sort.Strings(out)
	return out
}
