package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// PrepareReply commits the immutable draft and its inbound disposition together.
// The existing receipt sweep starts prepared deliveries if workflow startup fails.
func (s *Store) PrepareReply(ctx context.Context, p Principal, connection, event, text string) (Run, error) {
	return s.PrepareReplyWithLimits(ctx, p, connection, event, text, 0, 0)
}

func (s *Store) PrepareReplyWithLimits(ctx context.Context, p Principal, connection, event, text string, maxSingleChars, maxPartChars int) (Run, error) {
	if text == "" || len(text) > 16000 {
		return Run{}, ErrInvalid
	}
	if err := s.BindSession(ctx, p); err != nil {
		return Run{}, err
	}
	m, err := s.Message(ctx, p, connection, event)
	if err != nil {
		return Run{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	// Match configuration updates: transport precedes route/settings/message.
	c, err := scanJSON[Transport](tx.QueryRowContext(ctx, `SELECT body FROM wb_transports WHERE id=? FOR SHARE`, connection))
	if err != nil {
		return Run{}, err
	}
	route, err := scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE id=? FOR SHARE`, m.RouteID))
	if err != nil {
		return Run{}, err
	}
	m, err = scanJSON[StoredMessage](tx.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE connection_id=? AND event_id=? FOR UPDATE`, connection, event))
	if err != nil {
		return Run{}, err
	}
	if m.Principal != p || m.RouteID != route.ID || route.Principal != p {
		return Run{}, ErrDenied
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(m.ID+"/reply")).String()
	old, err := scanJSON[Run](tx.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, id))
	existingRun, existingDelivery := err == nil, false
	if existingRun {
		if old.Principal != p || old.OriginMessage != m.ID || old.Operation != "reply" || old.Goal != text {
			return Run{}, ErrConflict
		}
	} else if !IsMissing(err) {
		return Run{}, err
	}
	// Old project replies without segment metadata retain their original
	// immutable body and IDs, including a legacy interrupted submission.
	// The original WeCom binding predates the transport kind field.
	wecom := c.Kind == "wecom-gateway" || (c.Kind == "" && c.ID == "auto-reply-gateway")
	splitLines := p.Kind == "project" && wecom
	legacy := existingRun && splitLines && maxSingleChars == 0 && maxPartChars == 0 && len(old.ReplyParts) == 0
	var parts []string
	if !legacy {
		parts, err = splitReplyText(text, maxSingleChars, maxPartChars, splitLines)
		if err != nil {
			return Run{}, err
		}
	}
	r := Run{ID: id, Principal: p, Operation: "reply", Title: "回复新消息", Goal: text, OriginMessage: m.ID, ReplyParts: parts}
	if existingRun {
		if !slices.Equal(old.ReplyParts, parts) {
			return Run{}, ErrConflict
		}
		r = old
	}
	first := Delivery{ID: id, RunID: id, ItemID: "reply", ConnectionID: connection, Target: map[string]string{"conversationId": m.ConversationID, "actorId": m.ActorID, "replyTo": event}, Text: text}
	if len(r.ReplyParts) > 0 {
		first.ReplyPart = 1
		first.Text = r.ReplyParts[0]
	}
	if existingRun {
		d, readErr := scanJSON[Delivery](tx.QueryRowContext(ctx, `SELECT body FROM wb_deliveries WHERE id=?`, id))
		if readErr != nil && !IsMissing(readErr) {
			return Run{}, readErr
		}
		existingDelivery = readErr == nil
		if existingDelivery {
			if d.Text != first.Text || d.ReplyPart != first.ReplyPart || d.RunID != id || d.ConnectionID != connection || d.Target["replyTo"] != event || d.Target["actorId"] != m.ActorID || d.Target["conversationId"] != m.ConversationID {
				return Run{}, ErrConflict
			}
			if m.State != "received" {
				return old, nil
			}
			if d.State != "prepared" {
				return Run{}, ErrConflict
			}
		}
	}
	// Also finish legacy interrupted three-write submissions. Their immutable
	// run/draft are retained, and only a still-valid received input is queued.
	if !route.Enabled || !route.AutoReply || routeRevision(route) != max(int64(1), m.RouteRevision) || m.State != "received" {
		return Run{}, ErrDenied
	}
	cfg, err := scanJSON[ReplySettings](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=? FOR SHARE`, "reply-"+p.ProjectID))
	if err != nil && !IsMissing(err) {
		return Run{}, err
	}
	if err == nil && (!cfg.Enabled || cfg.ConnectionID != connection || cfg.Revision != m.SettingsRevision) {
		return Run{}, ErrDenied
	}
	allowed := p.Kind != "project"
	for _, project := range c.Projects {
		allowed = allowed || project == p.ProjectID
	}
	if !c.Enabled || !allowed {
		return Run{}, ErrDenied
	}
	if !existingRun {
		rows, err := tx.QueryContext(ctx, `SELECT resource_id,version,section_id FROM wb_session_sources WHERE session_id=?`, p.SessionID)
		if err != nil {
			return Run{}, err
		}
		for rows.Next() {
			var ref Ref
			if err = rows.Scan(&ref.ResourceID, &ref.Version, &ref.Section); err != nil {
				rows.Close()
				return Run{}, err
			}
			r.Lineage = append(r.Lineage, ref)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return Run{}, err
		}
		r.Lineage = UnionRefs(r.Lineage)
		// Preserve the existing notification destination without replacing the
		// originating customer message with a delegated task's origin.
		parent, err := scanJSON[Run](tx.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE session_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='dispatch' ORDER BY created_at LIMIT 1`, p.SessionID))
		if err == nil {
			r.NotifySession = parent.NotifySession
		} else if !IsMissing(err) {
			return Run{}, err
		}
		if r.NotifySession == "" {
			var raw []byte
			err = tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id='personal'`).Scan(&raw)
			if err == nil {
				var cfg struct {
					SessionID string `json:"sessionId"`
				}
				if err = json.Unmarshal(raw, &cfg); err != nil {
					return Run{}, err
				}
				r.NotifySession = cfg.SessionID
			} else if !IsMissing(err) {
				return Run{}, err
			}
		}
	}
	if err = AuthorizeAll(ctx, transactionGate{tx}, p, UnionRefs(r.Sources, r.Lineage)); err != nil {
		return Run{}, err
	}
	if !existingRun {
		if _, err = tx.ExecContext(ctx, `INSERT INTO wb_runs(id,project_id,session_id,input_digest,body) VALUES(?,?,?,?,?)`, id, p.ProjectID, p.SessionID, Digest(r), jsonText(r)); err != nil {
			return Run{}, err
		}
	}
	if !existingDelivery {
		now := time.Now().UTC()
		first.State, first.CreatedAt, first.UpdatedAt = "prepared", now, now
		if _, err = tx.ExecContext(ctx, `INSERT INTO wb_deliveries(id,run_id,item_id,state,body) VALUES(?,?,?,?,?)`, id, id, first.ItemID, first.State, jsonText(first)); err != nil {
			return Run{}, err
		}
	}
	m.State = "reply_queued"
	if _, err = tx.ExecContext(ctx, `UPDATE wb_messages SET body=? WHERE connection_id=? AND event_id=?`, jsonText(m), connection, event); err != nil {
		return Run{}, err
	}
	return r, tx.Commit()
}

// Treat explicit line breaks as message boundaries when the caller supplies
// limits or this is a WeCom project auto-reply. Screen wrapping is not a boundary.
func splitReplyText(text string, singleLimit, limit int, splitLines bool) ([]string, error) {
	const maxParts = 8
	if limit < 0 || limit > 16000 || singleLimit > 16000 ||
		(singleLimit != 0 && (limit == 0 || singleLimit < limit)) {
		return nil, ErrInvalid
	}
	if limit == 0 && !splitLines {
		return nil, nil
	}
	var parts []string
	for _, paragraph := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
	}) {
		if line := strings.TrimSpace(paragraph); line != "" {
			parts = append(parts, line)
		}
	}
	if len(parts) == 0 {
		return nil, ErrInvalid
	}
	if len(parts) > maxParts {
		return nil, fmt.Errorf("%w: shorten reply to at most %d messages", ErrInvalid, maxParts)
	}
	if len(parts) == 1 && singleLimit > 0 {
		limit = singleLimit
	}
	for _, part := range parts {
		if limit > 0 && utf8.RuneCountInString(part) > limit {
			return nil, fmt.Errorf("%w: divide the draft at natural breaks using newlines, at most %d characters per line; preserve links and identifiers intact", ErrInvalid, limit)
		}
	}
	if len(parts) == 1 && parts[0] == text {
		return nil, nil
	}
	return parts, nil
}

// ReplyPartID addresses each part independently without changing the reply run.
func ReplyPartID(runID string, part int) string {
	if part == 1 {
		return runID
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("%s/reply-part/%d", runID, part))).String()
}

func finishReplyMessage(ctx context.Context, tx *sql.Tx, d Delivery, previousState string) error {
	state := "reply_" + d.State
	var next Delivery
	if d.ReplyPart > 0 {
		// Later receipt updates for an earlier part cannot advance the sequence
		// or change the state of a newer part. Unknown never schedules more sends.
		if previousState != "submitting" && previousState != "accepted" && previousState != "pending_confirmation" {
			return nil
		}
		r, err := scanJSON[Run](tx.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, d.RunID))
		if err != nil {
			return err
		}
		if r.Operation != "reply" || d.ReplyPart > len(r.ReplyParts) || r.ReplyParts[d.ReplyPart-1] != d.Text {
			return ErrInvalid
		}
		if d.ReplyPart < len(r.ReplyParts) && (d.State == "accepted" || d.State == "pending_confirmation") {
			return nil
		}
		if d.ReplyPart < len(r.ReplyParts) && (d.State == "sent" || d.State == "delivered") {
			part := d.ReplyPart + 1
			now := time.Now().UTC()
			next = Delivery{ID: ReplyPartID(d.RunID, part), RunID: d.RunID, ItemID: fmt.Sprintf("reply-%d", part), ReplyPart: part, ConnectionID: d.ConnectionID, Target: d.Target, Text: r.ReplyParts[part-1], State: "prepared", CreatedAt: now, UpdatedAt: now}
			state = "reply_queued"
		}
	}
	updated, err := tx.ExecContext(ctx, `UPDATE wb_messages SET body=JSON_SET(body,'$.state',?) WHERE connection_id=? AND event_id=? AND message_state='reply_submitting' AND EXISTS(SELECT 1 FROM wb_runs WHERE id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='reply')`, state, d.ConnectionID, d.Target["replyTo"], d.RunID)
	if err != nil || next.ID == "" {
		return err
	}
	n, err := updated.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO wb_deliveries(id,run_id,item_id,state,body) VALUES(?,?,?,?,?)`, next.ID, next.RunID, next.ItemID, next.State, jsonText(next))
	return err
}

// PrepareReplyDelivery checks the draft exactly once, including when connection
// preparation failed. Gate writes are rolled back unless this attempt claims.
func (s *Store) PrepareReplyDelivery(ctx context.Context, id string, connected Transport, ready bool) (Delivery, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Delivery{}, false, err
	}
	defer tx.Rollback()
	d, err := scanJSON[Delivery](tx.QueryRowContext(ctx, `SELECT body FROM wb_deliveries WHERE id=? FOR UPDATE`, id))
	if err != nil {
		return d, false, err
	}
	if d.State != "prepared" {
		return d, false, nil
	}
	r, err := scanJSON[Run](tx.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, d.RunID))
	if err != nil {
		return d, false, err
	}
	if r.Operation != "reply" {
		return d, false, ErrInvalid
	}
	if _, err = tx.ExecContext(ctx, `SAVEPOINT reply_gate`); err != nil {
		return d, false, err
	}
	if err = s.deliveryGate(ctx, tx, d); err != nil {
		expired := errors.Is(err, errBusinessPolicyExpired)
		if !expired && !errors.Is(err, ErrDenied) && !errors.Is(err, ErrConflict) {
			return d, false, err
		}
		if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT reply_gate`); err != nil {
			return d, false, err
		}
		d.State = "failed"
		d.Error = "自动回复未提交：会话、资料或配置已停止或失效。"
		d.UpdatedAt = time.Now().UTC()
		if expired {
			d.Error = "自动回复未提交：业务口径已过期，已转人工。"
			if err = s.expiredBusinessReply(ctx, tx, d); err != nil {
				return d, false, err
			}
		} else if _, err = tx.ExecContext(ctx, `UPDATE wb_messages SET body=JSON_SET(body,'$.state','reply_failed') WHERE connection_id=? AND event_id=? AND message_state='reply_queued'`, d.ConnectionID, d.Target["replyTo"]); err != nil {
			return d, false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE wb_deliveries SET state=?,body=? WHERE id=?`, d.State, jsonText(d), id); err != nil {
			return d, false, err
		}
		return d, false, tx.Commit()
	}
	// The gate pins this transport under a shared lock. Do not send through
	// a connection opened for an earlier endpoint or credential configuration.
	pinned, err := scanJSON[Transport](tx.QueryRowContext(ctx, `SELECT body FROM wb_delivery_transports WHERE id=?`, id))
	if err != nil {
		return d, false, err
	}
	if !ready || Digest(pinned) != Digest(connected) {
		return d, false, nil
	}
	d.State = "submitting"
	d.UpdatedAt = time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE wb_deliveries SET state=?,body=? WHERE id=?`, d.State, jsonText(d), id); err != nil {
		return d, false, err
	}
	return d, true, tx.Commit()
}

func (s *Store) expiredBusinessReply(ctx context.Context, tx *sql.Tx, d Delivery) error {
	m, err := scanJSON[StoredMessage](tx.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE connection_id=? AND event_id=?`, d.ConnectionID, d.Target["replyTo"]))
	if err != nil {
		return err
	}
	route, err := scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE id=? FOR UPDATE`, m.RouteID))
	if err != nil {
		return err
	}
	return s.setMessageDisposition(ctx, tx, route, m, "handoff")
}
