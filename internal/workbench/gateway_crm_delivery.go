package workbench

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type crmNotice struct {
	V              int    `json:"v"`
	Op             string `json:"op"`
	EventID        string `json:"eventId"`
	StaffUserID    string `json:"staffUserId"`
	ExternalUserID string `json:"externalUserId"`
	crmState
}

func (s *Store) crmNotifyTx(tx *sql.Tx, g GatewaySession) error {
	for _, grant := range s.crmGrants {
		if grant.BindingID != g.BindingID || grant.BindingVersion != g.BindingVersion || g.IdentityConflict {
			continue
		}
		id := g.ExternalUserID
		if legacy := grant.ContactMappings[g.ContactID]; legacy != "" {
			if id != "" && id != legacy {
				continue
			}
			id = legacy
		}
		if id == "" {
			continue
		}
		event := newID()
		d := Delivery{ID: event, SessionID: g.ID, ConnectionID: g.ConnectionID, Kind: "gateway.crm", State: "pending", CreatedAt: now(), Gateway: &GatewayDelivery{Version: g.Version, BindingVersion: g.BindingVersion, AccountID: g.AccountID, CRMKey: grant.KeyID, CRMNotice: &crmNotice{1, "state.changed", event, grant.StaffUserID, id, stateOf(g)}}}
		_, e := tx.Exec("INSERT INTO deliveries VALUES(?,?,?,?,?,?)", d.ID, "crm-state:"+g.ID+":"+encode(g.Version), d.ConnectionID, d.State, d.CreatedAt, encode(d))
		return e
	}
	return nil
}
func (w *Worker) crmDeliverLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.crmDeliverOne(ctx)
		}
	}
}
func (w *Worker) crmDeliverOne(ctx context.Context) {
	s := w.Store
	var d Delivery
	var grant crmGrant
	var target string
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		var raw string
		e := tx.QueryRow("SELECT data FROM deliveries WHERE state='pending' AND json_extract(data,'$.kind')='gateway.crm' AND coalesce(json_extract(data,'$.gateway.nextAt'),0)<=? ORDER BY created_at,rowid LIMIT 1", now()).Scan(&raw)
		if e != nil {
			return e
		}
		d, e = decodeJSON[Delivery](raw)
		if e != nil {
			return e
		}
		if d.Gateway == nil || d.Gateway.CRMNotice == nil {
			return errors.New("invalid CRM delivery")
		}
		g, ok := s.crmGrants[d.Gateway.CRMKey]
		grant = g
		d.State = "cancelled"
		if ok && g.BindingVersion == d.Gateway.BindingVersion && g.StaffUserID == d.Gateway.CRMNotice.StaffUserID {
			b, be := activeCRMGrant(tx, g, d.ConnectionID)
			if be != nil {
				var ce crmError
				if !errors.As(be, &ce) {
					return be
				}
			} else {
				gs, e := resolveCRM(tx, g, b, d.Gateway.CRMNotice.ExternalUserID)
				var ce crmError
				if e != nil && !errors.As(e, &ce) {
					return e
				}
				if e == nil && gs.ID == d.SessionID && gs.Version == d.Gateway.Version {
					target = g.CallbackURL
					var configured string
					e := tx.QueryRow("SELECT url FROM gateway_crm_routes WHERE grant=?", g.scope()).Scan(&configured)
					if e == nil {
						target = configured
					} else if !errors.Is(e, sql.ErrNoRows) {
						return e
					}
					if trustedCRM(g.CallbackOrigin, target) && d.Gateway.Attempts < 5 {
						d.State = "sending"
						d.Gateway.Attempts++
					}
				}
			}
		}
		_, e = tx.Exec("UPDATE deliveries SET state=?,data=? WHERE id=?", d.State, encode(d), d.ID)
		return e
	})
	if err != nil || d.State != "sending" {
		return
	}
	var ack struct {
		OK bool `json:"ok"`
	}
	e := s.crmPost(ctx, grant, target, d.Gateway.CRMNotice, &ack)
	if e == nil && ack.OK {
		d.State = "sent"
		d.Error = ""
	} else if d.Gateway.Attempts >= 5 {
		d.State = "failed"
		d.Error = "CRM 状态通知未确认，可从侧边栏读取最新状态"
	} else {
		d.State = "pending"
		d.Error = "CRM 状态通知未确认"
		d.Gateway.NextAt = now() + int64(d.Gateway.Attempts)*5000
	}
	_ = s.saveGatewayDelivery(d)
}
