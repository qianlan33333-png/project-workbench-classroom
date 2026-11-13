package core

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"
)

// OwnerMessage is a direct, authenticated UI intent. It is deliberately not
// registered as a model tool. Recipient identity comes from the existing route.
type OwnerMessage struct {
	SessionID string `json:"sessionId"`
	Key       string `json:"key"`
	Text      string `json:"text"`
	Revision  int64  `json:"revision"`
}

type ownerMessageProof struct {
	RouteID  string `json:"routeId"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

func (s *Store) PrepareOwnerMessage(ctx context.Context, owner Principal, in OwnerMessage) (Run, error) {
	if !owner.IsOwner() || !ValidID(in.SessionID) || !ValidID(in.Key) || strings.TrimSpace(in.Text) == "" || len(in.Text) > 16000 || in.Revision < 1 {
		return Run{}, ErrDenied
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(in.SessionID+"/owner-message/"+in.Key)).String()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	proofKey := "owner-message-" + id
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_settings VALUES(?,JSON_OBJECT())`, proofKey); err != nil {
		return Run{}, err
	}
	proof, err := scanJSON[ownerMessageProof](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=? FOR UPDATE`, proofKey))
	if err != nil {
		return Run{}, err
	}
	if proof.Digest != "" {
		r, e := scanJSON[Run](tx.QueryRowContext(ctx, `SELECT body FROM wb_runs WHERE id=?`, id))
		if e != nil {
			return r, e
		}
		if r.Principal.SessionID != in.SessionID || r.Goal != in.Text {
			return r, ErrConflict
		}
		return r, tx.Commit()
	}
	route, err := scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=? FOR UPDATE`, in.SessionID))
	if err != nil {
		return Run{}, err
	}
	if IsReplyPracticeConnection(route.ConnectionID) {
		return Run{}, ErrDenied
	}
	if !route.Enabled || route.Principal.Kind != "project" || routeRevision(route) != in.Revision {
		return Run{}, ErrConflict
	}
	var unresolved int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_deliveries WHERE state IN ('submitting','unknown') AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.connectionId'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.target.actorId'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.text'))=?`, route.ConnectionID, route.ActorID, in.Text).Scan(&unresolved); err != nil {
		return Run{}, err
	}
	if unresolved != 0 {
		return Run{}, ErrConflict
	}
	transport, err := scanJSON[Transport](tx.QueryRowContext(ctx, `SELECT body FROM wb_transports WHERE id=? FOR SHARE`, route.ConnectionID))
	if err != nil {
		return Run{}, err
	}
	allowed := false
	for _, project := range transport.Projects {
		if project == route.Principal.ProjectID {
			allowed = true
		}
	}
	if !transport.Enabled || !allowed {
		return Run{}, ErrDenied
	}
	if route.AutoReply {
		route.AutoReply = false
		route.Revision = routeRevision(route) + 1
		if err = pausePending(ctx, tx, route.ID); err != nil {
			return Run{}, err
		}
		if err = modeChangedTx(ctx, tx, &route); err != nil {
			return Run{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE wb_message_routes SET body=? WHERE id=?`, jsonText(route), route.ID); err != nil {
			return Run{}, err
		}
	}
	var replyTo string
	if err = tx.QueryRowContext(ctx, `SELECT event_id FROM wb_messages WHERE route_id=? ORDER BY arrival_at DESC,event_id DESC LIMIT 1`, route.ID).Scan(&replyTo); err != nil {
		return Run{}, err
	}
	r := Run{ID: id, Principal: route.Principal, Operation: "owner_message", Title: "人工回复", Goal: in.Text}
	d := Delivery{ID: id, RunID: id, ItemID: "owner-message", ConnectionID: route.ConnectionID, Target: map[string]string{"conversationId": route.ConversationID, "actorId": route.ActorID, "replyTo": replyTo}, Text: in.Text, State: "prepared", CreatedAt: time.Now().UTC()}
	d.UpdatedAt = d.CreatedAt
	proof = ownerMessageProof{route.ID, routeRevision(route), Digest(d.Target) + Digest(d.Text) + d.ConnectionID}
	if _, err = tx.ExecContext(ctx, `INSERT INTO wb_runs(id,project_id,session_id,input_digest,body) VALUES(?,?,?,?,?)`, r.ID, r.Principal.ProjectID, r.Principal.SessionID, Digest(r), jsonText(r)); err != nil {
		return Run{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO wb_deliveries(id,run_id,item_id,state,body) VALUES(?,?,?,?,?)`, d.ID, d.RunID, d.ItemID, d.State, jsonText(d)); err != nil {
		return Run{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE wb_settings SET body=? WHERE id=?`, jsonText(proof), proofKey); err != nil {
		return Run{}, err
	}
	return r, tx.Commit()
}

func ownerMessageGate(ctx context.Context, tx *sql.Tx, r Run, d Delivery) error {
	proof, e := scanJSON[ownerMessageProof](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=?`, "owner-message-"+r.ID))
	if e != nil {
		return ErrDenied
	}
	if proof.Digest != Digest(d.Target)+Digest(d.Text)+d.ConnectionID || r.Goal != d.Text {
		return ErrDenied
	}
	route, e := scanJSON[MessageRoute](tx.QueryRowContext(ctx, `SELECT body FROM wb_message_routes WHERE id=? FOR SHARE`, proof.RouteID))
	if e != nil {
		return e
	}
	if route.AutoReply || !route.Enabled || route.Principal != r.Principal || route.ConnectionID != d.ConnectionID || route.ActorID != d.Target["actorId"] || route.ConversationID != d.Target["conversationId"] || routeRevision(route) != proof.Revision {
		return ErrDenied
	}
	return nil
}

// Recovery uses the original prepared delivery. Resuming AI invalidates its proof.
func (s *Store) ExpireOwnerDraft(ctx context.Context, id string) (bool, error) {
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
	if r.Operation != "owner_message" {
		return false, nil
	}
	if e = ownerMessageGate(ctx, tx, r, d); e == nil {
		return false, nil
	}
	if e != ErrDenied && !IsMissing(e) {
		return false, e
	}
	d.State = "failed"
	d.Error = "人工回复未提交：会话已恢复AI或已更改。"
	d.UpdatedAt = time.Now().UTC()
	_, e = tx.ExecContext(ctx, `UPDATE wb_deliveries SET state='failed',body=? WHERE id=?`, jsonText(d), id)
	if e != nil {
		return false, e
	}
	return true, tx.Commit()
}
