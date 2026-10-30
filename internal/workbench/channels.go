package workbench

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func (s *Store) Connections() ([]Connection, error) {
	return listJSON[Connection](s.DB, "SELECT data FROM connections ORDER BY id")
}
func (s *Store) Connection(id string) (Connection, error) {
	return getJSON[Connection](s.DB, "SELECT data FROM connections WHERE id=?", id)
}
func (s *Store) SaveConnection(c Connection, secrets map[string]any) (Connection, error) {
	if c.Kind != "wechat" && c.Kind != "feishu" {
		return c, errors.New("仅支持微信和飞书")
	}
	if strings.TrimSpace(c.OwnerID) == "" {
		if c.Kind == "feishu" {
			c.OwnerID = "pairing:" + strings.ReplaceAll(newID(), "-", "")[:12]
		} else {
			return c, errors.New("请填写绑定的本人账号 ID")
		}
	}
	if c.ID == "" {
		c.ID = newID()
	}
	old, e := s.Connection(c.ID)
	if e == nil {
		c.LastHeartbeat = old.LastHeartbeat
		c.Status = old.Status
	} else {
		c.Status = "disconnected"
	}
	err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO connections VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,data=excluded.data", c.ID, c.Kind, encode(c)); err != nil {
			return err
		}
		if secrets != nil {
			_, err := tx.Exec("INSERT INTO connection_secrets VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", c.ID, encode(secrets))
			return err
		}
		return nil
	})
	return c, err
}
func (s *Store) BindConnection(id, code, actor string) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, e := s.Connection(id)
	if e != nil {
		return c, e
	}
	if c.Kind != "feishu" || !c.Enabled || c.OwnerID != "pairing:"+code || strings.TrimSpace(actor) == "" {
		return c, errors.New("绑定码无效或已使用")
	}
	c.OwnerID = actor
	c.Status = "connected"
	_, e = s.DB.Exec("UPDATE connections SET data=? WHERE id=?", encode(c), c.ID)
	return c, e
}
func (s *Store) Heartbeat(id, status string) error {
	c, err := s.Connection(id)
	if err != nil {
		return err
	}
	c.LastHeartbeat = now()
	c.Status = status
	_, err = s.DB.Exec("UPDATE connections SET data=? WHERE id=?", encode(c), id)
	return err
}
func (s *Store) QueueReply(m Message, run, session, kind, text string) error {
	if m.Incoming.Source != "wechat" && m.Incoming.Source != "feishu" {
		return nil
	}
	d := Delivery{ID: newID(), MessageID: m.ID, RunID: run, SessionID: session, ConnectionID: m.Incoming.ConnectionID, ConversationID: m.Incoming.ConversationID, ActorID: m.Incoming.ActorID, ReplyTo: m.Incoming.EventID, Kind: kind, Text: text, State: "pending", CreatedAt: now()}
	key := m.ID + ":" + kind
	if run != "" {
		key = run + ":" + kind
	}
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if session != "" {
			var exists int
			if e := tx.QueryRow("SELECT count(*) FROM sessions WHERE id=?", session).Scan(&exists); e != nil {
				return e
			}
			if exists == 0 {
				return nil
			}
		}
		var deleted int
		if e := tx.QueryRow("SELECT count(*) FROM inbox WHERE id=? AND state='deleted'", m.ID).Scan(&deleted); e != nil {
			return e
		}
		if deleted > 0 {
			return nil
		}
		_, err := tx.Exec("INSERT OR IGNORE INTO deliveries VALUES(?,?,?,?,?,?)", d.ID, key, d.ConnectionID, d.State, d.CreatedAt, encode(d))
		return err
	})
}
func (s *Store) ClaimDelivery(connection string) (*Delivery, error) {
	var out *Delivery
	err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRow("SELECT data FROM deliveries WHERE connection_id=? AND state='pending' AND (json_extract(data,'$.sessionId')='' OR json_extract(data,'$.sessionId') IN (SELECT id FROM sessions WHERE coalesce(json_extract(data,'$.recycledAt'),0)=0)) ORDER BY created_at LIMIT 1", connection).Scan(&raw)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		d, err := decodeJSON[Delivery](raw)
		if err != nil {
			return err
		}
		d.State = "sending"
		if _, err = tx.Exec("UPDATE deliveries SET state='sending',data=? WHERE id=? AND state='pending'", encode(d), d.ID); err != nil {
			return err
		}
		out = &d
		return nil
	})
	return out, err
}
func (s *Store) FinishDelivery(id, state, receipt, detail string) error {
	return s.FinishDeliveryReceipts(id, state, receipt, nil, detail)
}
func (s *Store) FinishDeliveryReceipts(id, state, receipt string, receipts []string, detail string) error {
	if state != "sent" && state != "accepted" && state != "unknown" && state != "failed" {
		return errors.New("投递状态无效")
	}
	d, err := getJSON[Delivery](s.DB, "SELECT data FROM deliveries WHERE id=?", id)
	if err != nil {
		return err
	}
	if d.State != "sending" {
		if d.State == state && d.Receipt == receipt {
			return nil
		}
		return errors.New("投递已结算，不能更换结果")
	}
	if (state == "sent" || state == "accepted") && receipt == "" {
		return errors.New("成功投递必须有平台回执或客户端发送标识")
	}
	d.State = state
	d.Receipt = receipt
	d.Receipts = receipts
	d.Error = detail
	_, err = s.DB.Exec("UPDATE deliveries SET state=?,data=? WHERE id=? AND state='sending'", state, encode(d), id)
	if err != nil {
		return err
	}
	return s.Emit(d.SessionID, d.RunID, "delivery.updated", d)
}
func (s *Store) Deliveries() ([]Delivery, error) {
	return listJSON[Delivery](s.DB, "SELECT data FROM deliveries WHERE coalesce(json_extract(data,'$.sessionId'),'')='' OR json_extract(data,'$.sessionId') IN (SELECT id FROM sessions WHERE coalesce(json_extract(data,'$.recycledAt'),0)=0) ORDER BY created_at DESC LIMIT 100")
}
func (s *Store) PendingInteractions() ([]Interaction, error) {
	return listJSON[Interaction](s.DB, "SELECT data FROM interactions WHERE state='pending'")
}
func (s *Store) RecoverDeliveries() error {
	ds, err := listJSON[Delivery](s.DB, "SELECT data FROM deliveries WHERE state='sending'")
	if err != nil {
		return err
	}
	for _, d := range ds {
		if err = s.FinishDelivery(d.ID, "unknown", "", "连接中断，平台是否接收尚未确认；不会自动重发"); err != nil {
			return err
		}
	}
	return nil
}
func resultReply(project Project, run Run) string {
	body := run.Result
	if run.State != "completed" {
		body = "任务" + stateLabel(run.State) + "。"
		if run.Error != "" {
			body += "\n" + run.Error
		}
		if run.Result != "" {
			body += "\n\n已产生的内容：\n" + run.Result
		}
	}
	return fmt.Sprintf("%s · #T%s\n\n%s", project.Name, run.ID[:8], body)
}
func stateLabel(state string) string {
	switch state {
	case "cancelled":
		return "已停止"
	case "failed":
		return "失败"
	case "interrupted":
		return "已中断"
	case "waiting_user", "waiting":
		return "等待你的回复"
	case "running":
		return "执行中"
	case "completed":
		return "已完成"
	case "queued":
		return "已排队"
	default:
		return state
	}
}
