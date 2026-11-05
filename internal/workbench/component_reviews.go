package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const componentReviewSchema = `CREATE TABLE IF NOT EXISTS component_reviews(id TEXT PRIMARY KEY,call_id TEXT NOT NULL REFERENCES component_calls(id) ON DELETE CASCADE,session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,state TEXT NOT NULL,updated_at INTEGER NOT NULL,data TEXT NOT NULL,UNIQUE(call_id));
CREATE INDEX IF NOT EXISTS component_review_pending ON component_reviews(state,updated_at);
CREATE TRIGGER IF NOT EXISTS component_review_call_guard BEFORE DELETE ON component_calls WHEN EXISTS(SELECT 1 FROM component_reviews WHERE call_id=OLD.id AND state IN ('awaiting_owner','approved','dispatching','unknown')) BEGIN SELECT RAISE(ABORT,'保留组件审核与授权记录'); END;`

type ComponentReview struct {
	ID                string          `json:"id"`
	CallID            string          `json:"callId"`
	ProjectID         string          `json:"projectId"`
	SessionID         string          `json:"sessionId"`
	PluginID          string          `json:"pluginId"`
	Binding           string          `json:"binding"`
	TaskRef           string          `json:"taskRef"`
	Digest            string          `json:"digest"`
	Title             string          `json:"title"`
	Preview           string          `json:"preview"`
	State             string          `json:"state"`
	ConfirmAction     string          `json:"confirmAction"`
	CancelCallID      string          `json:"cancelCallId,omitempty"`
	ConfirmCallID     string          `json:"confirmCallId,omitempty"`
	OwnerMessageID    string          `json:"ownerMessageId,omitempty"`
	NoticeRunID       string          `json:"noticeRunId,omitempty"`
	PersonalMessageID string          `json:"personalMessageId,omitempty"`
	ConnectionID      string          `json:"connectionId,omitempty"`
	ConversationID    string          `json:"conversationId,omitempty"`
	ActorID           string          `json:"actorId,omitempty"`
	CreatedAt         int64           `json:"createdAt"`
	UpdatedAt         int64           `json:"updatedAt"`
	Error             string          `json:"error,omitempty"`
	Status            string          `json:"status,omitempty"`
	Result            json.RawMessage `json:"result,omitempty"`
	PersonalNotices   map[string]bool `json:"personalNotices,omitempty"`
}

func (s *Store) ComponentReview(id string) (ComponentReview, error) {
	return getJSON[ComponentReview](s.DB, "SELECT data FROM component_reviews WHERE id=?", id)
}
func (s *Store) ComponentReviews(session string) ([]ComponentReview, error) {
	if _, e := s.Session(session); e != nil {
		return nil, e
	}
	return listJSON[ComponentReview](s.DB, "SELECT data FROM component_reviews WHERE session_id=? ORDER BY updated_at DESC LIMIT 30", session)
}
func (s *Store) materializeComponentReview(c ComponentCall) error {
	if c.State != "succeeded" {
		return nil
	}
	p, e := s.Plugin(c.PluginID)
	if e != nil {
		return e
	}
	if p.Component == nil || p.Component.OwnerReviewAction == "" || componentBinding(p) != c.Binding {
		return nil
	}
	valid := false
	for _, a := range p.Component.Actions {
		if a.Name == p.Component.OwnerReviewAction && a.RequiresOwner {
			valid = true
		}
	}
	if !valid {
		return errors.New("审核动作未登记为本人确认")
	}
	var result struct {
		Review *struct {
			TaskRef                string `json:"task_ref"`
			Digest, Title, Preview string
		} `json:"owner_review"`
	}
	if json.Unmarshal(c.Result, &result) != nil || result.Review == nil {
		return nil
	}
	v := result.Review
	if v.TaskRef != c.JobRef || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(v.Digest) || len(v.Preview) > 48000 || len(v.Title) > 500 {
		return errors.New("组件审核快照无效")
	}
	r := ComponentReview{ID: c.ID, CallID: c.ID, ProjectID: c.ProjectID, SessionID: c.SessionID, PluginID: c.PluginID, Binding: c.Binding, TaskRef: v.TaskRef, Digest: v.Digest, Title: v.Title, Preview: v.Preview, State: "awaiting_owner", ConfirmAction: p.Component.OwnerReviewAction, CreatedAt: now(), UpdatedAt: now()}
	_, e = s.DB.Exec("INSERT OR IGNORE INTO component_reviews VALUES(?,?,?,?,?,?)", r.ID, r.CallID, r.SessionID, r.State, r.UpdatedAt, encode(r))
	return e
}
func (s *Store) reviewTarget(r ComponentReview) (Connection, string, error) {
	p, e := s.Plugin(r.PluginID)
	if e != nil {
		return Connection{}, "", e
	}
	var candidates []Connection
	cs, e := s.Connections()
	if e != nil {
		return Connection{}, "", e
	}
	for _, c := range cs {
		if c.Kind == "wechat" && c.Enabled && !strings.HasPrefix(c.OwnerID, "pairing:") && (p.Component.OwnerNotifyConnectionID == "" || p.Component.OwnerNotifyConnectionID == c.ID) {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) != 1 {
		return Connection{}, "", errors.New("需要绑定唯一的本人微信抽检入口")
	}
	c := candidates[0]
	var conversation string
	e = s.DB.QueryRow("SELECT data->>'$.incoming.conversationId' FROM inbox WHERE source='wechat' AND connection_id=? AND data->>'$.incoming.actorId'=? AND coalesce(data->>'$.incoming.conversationId','')<>'' ORDER BY created_at DESC LIMIT 1", c.ID, c.OwnerID).Scan(&conversation)
	if e != nil || conversation == "" {
		return c, "", errors.New("本人微信尚无可验证的私聊记录")
	}
	return c, conversation, nil
}
func (s *Store) saveComponentReview(r ComponentReview, old int64) (ComponentReview, error) {
	r.UpdatedAt = max(now(), old+1)
	res, e := s.DB.Exec("UPDATE component_reviews SET state=?,updated_at=?,data=? WHERE id=? AND updated_at=?", r.State, r.UpdatedAt, encode(r), r.ID, old)
	if e != nil {
		return r, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return r, e
	}
	if n == 0 {
		return r, ErrConflict
	}
	return r, s.Emit(r.SessionID, "", "component.review", r)
}

// A completed host-owned notice Run anchors normal attributed session messaging.
// It is never queued for Harness execution and never pretends to be owner text.
func (s *Store) queueReviewPersonal(r *ComponentReview) error {
	return s.queueReviewPersonalPhase(r, "review", "组件抽检通知（非本人指令）："+r.Title+"\n"+r.Preview+"\n批次审核码："+r.ID[:8]+"。宿主向本人微信投递；等待本人批准。不要代替本人发送、批准或重复通知。")
}
func (s *Store) queueReviewPersonalPhase(r *ComponentReview, phase, body string) error {
	personal, e := s.PersonalSpace()
	if e != nil {
		return e
	}
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		fresh, e := getJSON[ComponentReview](tx, "SELECT data FROM component_reviews WHERE id=?", r.ID)
		if e != nil {
			return e
		}
		if phase == "review" && fresh.State != "awaiting_owner" {
			return nil
		}
		if fresh.PersonalNotices[phase] {
			*r = fresh
			return nil
		}
		source, e := communicationTargetTx(tx, r.SessionID)
		if e != nil {
			return e
		}
		target, e := communicationTargetTx(tx, personal.DefaultSessionID)
		if e != nil {
			return e
		}
		mid := "component-notice:" + r.ID + ":" + phase
		rid := mid
		at := now()
		text := body
		if len(text) > 15000 {
			text = string([]rune(text)[:4000]) + "\n完整样本见原活动Session。"
		}
		m := Message{ID: mid, Incoming: Incoming{Source: "component", EventID: mid, SessionID: source.ID, Text: "工具组件已生成待抽检结果"}, State: "handled", SessionID: source.ID, RunID: rid, CreatedAt: at}
		run := Run{ID: rid, ProjectID: source.ProjectID, SessionID: source.ID, MessageID: mid, State: "completed", Source: "component", Kind: "component_notice", Text: m.Incoming.Text, Result: r.Preview, CreatedAt: at, StartedAt: at, FinishedAt: at}
		if _, e = tx.Exec("INSERT OR IGNORE INTO inbox VALUES(?,?,?,?,?,?,?,?,?)", mid, "component", "", mid, "component:"+r.ID, m.State, source.ID, at, encode(m)); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT OR IGNORE INTO runs VALUES(?,?,?,?,?,?,?,?)", rid, source.ProjectID, source.ID, mid, "completed", 0, at, encode(run)); e != nil {
			return e
		}
		aid := "component-review-agent:" + r.ID + ":" + phase
		a := &AgentMessage{FromSessionID: source.ID, FromProjectID: source.ProjectID, FromRunID: rid, FromLabel: "组件结果 · " + r.Title, RootID: rid, Delivery: "wake", Execution: "read"}
		notice := Message{ID: aid, Incoming: Incoming{Source: "agent", ConnectionID: source.ID, EventID: aid, SessionID: target.ID, Text: text}, State: "unrouted", SessionID: target.ID, Agent: a, CreatedAt: at}
		if _, e = tx.Exec("INSERT OR IGNORE INTO communication_roots VALUES(?,1,0)", rid); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT OR IGNORE INTO inbox VALUES(?,?,?,?,?,?,?,?,?)", aid, "agent", source.ID, aid, "component:"+r.ID, "unrouted", target.ID, at, encode(notice)); e != nil {
			return e
		}
		if phase == "review" {
			fresh.NoticeRunID = rid
			fresh.PersonalMessageID = aid
		}
		if fresh.PersonalNotices == nil {
			fresh.PersonalNotices = map[string]bool{}
		}
		fresh.PersonalNotices[phase] = true
		fresh.UpdatedAt = max(now(), fresh.UpdatedAt+1)
		if _, e = tx.Exec("UPDATE component_reviews SET updated_at=?,data=? WHERE id=?", fresh.UpdatedAt, encode(fresh), fresh.ID); e != nil {
			return e
		}
		*r = fresh
		return nil
	})
}
func (s *Store) notifyComponentReview(r ComponentReview) error {
	p, e := s.Plugin(r.PluginID)
	if e != nil {
		return e
	}
	if componentBinding(p) != r.Binding {
		return errors.New("组件授权或版本改变，旧抽检通知失效")
	}
	if e := s.queueReviewPersonal(&r); e != nil {
		return e
	}
	c, conversation, e := s.reviewTarget(r)
	if e != nil {
		return e
	}
	if r.ConnectionID != "" && (r.ConnectionID != c.ID || r.ActorID != c.OwnerID || r.ConversationID != conversation) {
		return errors.New("本人渠道绑定改变，请重新核对抽检")
	}
	if r.ConnectionID == "" {
		old := r.UpdatedAt
		r.ConnectionID = c.ID
		r.ConversationID = conversation
		r.ActorID = c.OwnerID
		r, e = s.saveComponentReview(r, old)
		if e != nil {
			return e
		}
	}
	text := fmt.Sprintf("【待你抽检】%s\n审核码 %s\n%s\n\n本批次名单与文案已冻结。确认请回复“发”；多批同时待审请引用本条消息，或回复“发 %s”。此回复仅授权本批次，发送状态以实际渠道回执为准。", r.Title, r.ID[:8], r.Preview, r.ID[:8])
	m := Message{ID: "component-review-notify:" + r.ID, SessionID: r.SessionID, Incoming: Incoming{Source: c.Kind, ConnectionID: c.ID, ActorID: c.OwnerID, ConversationID: conversation}}
	return s.QueueReply(m, "", r.SessionID, "component-review", text)
}
func (s *Store) ApproveComponentReview(ctx context.Context, id, digestValue, event string) (ComponentReview, error) {
	r, e := s.ComponentReview(id)
	if e != nil {
		return r, e
	}
	if event == "" || len(event) > 300 {
		return r, errors.New("需要稳定的本人确认事件")
	}
	if r.Digest != digestValue {
		return r, errors.New("名单或文案已变化，请重新抽检")
	}
	_, p, e := s.componentPlugin(r.SessionID, r.PluginID)
	if e != nil {
		return r, e
	}
	if componentBinding(p) != r.Binding {
		return r, errors.New("组件版本或授权已改变，旧确认无效")
	}
	e = s.Transaction(ctx, func(tx *sql.Tx) error {
		fresh, er := getJSON[ComponentReview](tx, "SELECT data FROM component_reviews WHERE id=?", r.ID)
		if er != nil {
			return er
		}
		plugin, er := getJSON[Plugin](tx, "SELECT data FROM component_plugins WHERE id=?", r.PluginID)
		if er != nil || !componentEnabled(plugin) || componentBinding(plugin) != r.Binding {
			return errors.New("组件授权已改变")
		}
		if fresh.Digest != digestValue {
			return ErrConflict
		}
		if strings.HasPrefix(event, "message:") {
			m, er := getJSON[Message](tx, "SELECT data FROM inbox WHERE id=?", strings.TrimPrefix(event, "message:"))
			if er != nil {
				return er
			}
			if m.Agent != nil {
				return errors.New("Agent不能代替本人确认")
			}
			if m.Incoming.Source != "web" {
				connection, er := getJSON[Connection](tx, "SELECT data FROM connections WHERE id=?", m.Incoming.ConnectionID)
				if er != nil || !connection.Enabled || connection.OwnerID != m.Incoming.ActorID || connection.ID != fresh.ConnectionID || connection.OwnerID != fresh.ActorID || m.Incoming.ConversationID != fresh.ConversationID {
					return errors.New("本人抽检渠道已改变")
				}
			} else if m.Incoming.SessionID != fresh.SessionID {
				return errors.New("本人确认不属于该会话")
			}
		}
		if fresh.State == "awaiting_owner" {
			fresh.State = "approved"
			fresh.OwnerMessageID = event
			fresh.UpdatedAt = max(now(), fresh.UpdatedAt+1)
			if _, er = tx.Exec("UPDATE component_reviews SET state=?,updated_at=?,data=? WHERE id=?", fresh.State, fresh.UpdatedAt, encode(fresh), fresh.ID); er != nil {
				return er
			}
		}
		r = fresh
		return nil
	})
	if e != nil {
		return r, e
	}

	if r.State != "approved" && r.State != "dispatching" {
		return r, errors.New("当前审核已取消、过期或结案")
	}
	return s.dispatchComponentReview(ctx, r)
}
func (s *Store) dispatchComponentReview(ctx context.Context, r ComponentReview) (ComponentReview, error) {
	if r.ConfirmCallID != "" {
		return r, nil
	}
	v, e := s.submitComponent(ctx, r.SessionID, ComponentRequest{PluginID: r.PluginID, Action: r.ConfirmAction, Arguments: map[string]any{"task_ref": r.TaskRef, "digest": r.Digest, "approval_event": r.OwnerMessageID}, OperationKey: "owner-review:" + r.ID + ":" + r.Digest, OwnerConfirmed: true}, true)
	if e != nil {
		return r, e
	}
	old := r.UpdatedAt
	r.State = "dispatching"
	r.ConfirmCallID = v.ID
	return s.saveComponentReview(r, old)
}
func (s *Store) pollComponentReviews(ctx context.Context) {
	calls, _ := listJSON[ComponentCall](s.DB, "SELECT c.data FROM component_calls c LEFT JOIN component_reviews r ON r.call_id=c.id WHERE c.state='succeeded' AND json_type(c.data,'$.result.owner_review')='object' AND r.id IS NULL AND coalesce(c.data->>'$.reviewRejected',0)=0 ORDER BY c.updated_at LIMIT 4")
	for _, c := range calls {
		err := s.materializeComponentReview(c)
		if _, er := s.ComponentReview(c.ID); er != nil {
			c.ReviewRejected = true
			if err != nil {
				c.Error = safeRuntimeError(err.Error())
			}
			_, _ = s.saveComponentCall(c)
		}
	}
	reviews, _ := listJSON[ComponentReview](s.DB, "SELECT data FROM component_reviews WHERE state IN ('awaiting_owner','approved','dispatching','unknown') OR (state='cancelled' AND coalesce(data->>'$.cancelCallId','')='') OR (state IN ('completed','failed') AND coalesce(data->>'$.personalNotices.final',0)=0) ORDER BY updated_at LIMIT 10")
	for _, r := range reviews {
		if ctx.Err() != nil {
			return
		}
		if r.State == "awaiting_owner" {
			e := s.notifyComponentReview(r)
			if e == nil {
				fresh, er := s.ComponentReview(r.ID)
				if er == nil {
					old := fresh.UpdatedAt
					fresh.Error = ""
					_, _ = s.saveComponentReview(fresh, old)
				}
			}
			if e != nil {
				fresh, er := s.ComponentReview(r.ID)
				if er == nil {
					old := fresh.UpdatedAt
					fresh.Error = safeRuntimeError(e.Error())
					_, _ = s.saveComponentReview(fresh, old)
				}
			}
		} else if r.State == "approved" {
			_, e := s.dispatchComponentReview(ctx, r)
			if e != nil {
				old := r.UpdatedAt
				r.Error = safeRuntimeError(e.Error())
				_, _ = s.saveComponentReview(r, old)
			}
		} else if r.State == "cancelled" {
			_ = s.cancelReviewPlugin(r)
		} else {
			_ = s.syncComponentReview(r)
		}
	}
}

func (s *Store) syncComponentReview(r ComponentReview) error {
	c, e := s.ComponentCall(r.ConfirmCallID)
	if e != nil {
		return e
	}
	old := r.UpdatedAt
	r.Result = c.Result
	r.Error = c.Error
	status := c.State
	var result map[string]any
	_ = json.Unmarshal(c.Result, &result)
	if v, ok := result["state"].(string); ok {
		status = v
	}
	r.Status = status
	if c.State == "succeeded" {
		r.State = "completed"
	} else if c.State == "failed" || c.State == "cancelled" {
		r.State = "failed"
	} else if c.State == "unknown" {
		r.State = "unknown"
	} else {
		r.State = "dispatching"
	}
	r, e = s.saveComponentReview(r, old)
	if e != nil {
		return e
	}
	// Phase identifiers are host-generated; one outbox entry per meaningful state.
	phase := digest([]byte(status))[:12]
	if r.State == "completed" || r.State == "failed" {
		phase = "final"
	}
	body := fmt.Sprintf("【批次进度】%s\n审核码 %s\n%s\n%s", r.Title, r.ID[:8], c.Summary, r.Error)
	if len(c.Result) > 0 {
		body += "\n" + string(c.Result)
	}
	if len(body) > 22000 {
		body = string([]rune(body)[:min(5500, len([]rune(body)))]) + "\n完整回执见活动Session。"
	}
	if r.ConnectionID != "" {
		connection, er := s.Connection(r.ConnectionID)
		if er != nil {
			return er
		}
		if !connection.Enabled || connection.OwnerID != r.ActorID {
			return errors.New("本人渠道已变更，进度未外发")
		}
		m := Message{ID: "component-review-progress:" + r.ID + ":" + phase, SessionID: r.SessionID, Incoming: Incoming{Source: connection.Kind, ConnectionID: r.ConnectionID, ActorID: r.ActorID, ConversationID: r.ConversationID}}
		if e = s.QueueReply(m, "", r.SessionID, "component-review-progress", body); e != nil {
			return e
		}
	}
	return s.queueReviewPersonalPhase(&r, phase, body)
}

func (s *Store) handleComponentOwnerControl(ctx context.Context, m Message) (bool, error) {
	command := regexp.MustCompile(`^发(?:\s+#?([a-f0-9-]{8,36}))?$`).FindStringSubmatch(strings.TrimSpace(m.Incoming.Text))
	if len(command) == 0 || (!isChatChannel(m.Incoming.Source) && m.Incoming.Source != "web") || m.Agent != nil {
		return false, nil
	}
	if m.State != "unrouted" {
		return true, nil
	}
	var rs []ComponentReview
	var e error
	if m.Incoming.Source == "web" {
		if m.Incoming.SessionID == "" {
			return false, nil
		}
		query := "SELECT data FROM component_reviews WHERE session_id=? AND state IN ('awaiting_owner','approved','dispatching') AND id LIKE ?"
		args := []any{m.Incoming.SessionID, command[1] + "%"}
		if command[1] == "" {
			query += " AND data->>'$.createdAt'<?"
			args = append(args, m.CreatedAt)
		}
		query += " ORDER BY data->>'$.createdAt' DESC LIMIT 2"
		rs, e = listJSON[ComponentReview](s.DB, query, args...)
	} else {
		c, er := s.Connection(m.Incoming.ConnectionID)
		if er != nil || !c.Enabled || c.Kind != m.Incoming.Source || c.OwnerID != m.Incoming.ActorID || m.Incoming.ConversationID == "" {
			return true, errors.New("仅本人已验证私聊可确认")
		}
		query := `SELECT r.data FROM component_reviews r JOIN deliveries d ON d.data->>'$.messageId'='component-review-notify:'||r.id WHERE r.state IN ('awaiting_owner','approved','dispatching') AND r.id LIKE ? AND d.connection_id=? AND d.data->>'$.actorId'=? AND d.data->>'$.conversationId'=? AND d.state IN ('sent','accepted')`
		args := []any{command[1] + "%", c.ID, c.OwnerID, m.Incoming.ConversationID}
		if m.Incoming.ReplyTo == "" && command[1] == "" {
			query += " AND coalesce(d.data->>'$.finishedAt',0)>0 AND d.data->>'$.finishedAt'<?"
			args = append(args, m.CreatedAt)
		}
		if m.Incoming.ReplyTo != "" {
			query += ` AND (d.id=? OR d.data->>'$.receipt'=? OR EXISTS(SELECT 1 FROM json_each(d.data,'$.receipts') WHERE value=?))`
			args = append(args, m.Incoming.ReplyTo, m.Incoming.ReplyTo, m.Incoming.ReplyTo)
		}
		query += " ORDER BY r.updated_at DESC LIMIT 2"
		rs, e = listJSON[ComponentReview](s.DB, query, args...)
	}
	if e != nil {
		return true, e
	}
	reply := "没有找到唯一的待审批次，未执行发送。请引用抽检通知，或回复“发 批次审核码”。"
	if len(rs) == 1 {
		r := rs[0]
		m.SessionID = r.SessionID
		if r.State != "awaiting_owner" {
			reply = "这批已收到本人确认，将沿原任务查回结果，不会重复发送。"
		} else {
			r, e = s.ApproveComponentReview(ctx, r.ID, r.Digest, "message:"+m.ID)
			if e != nil {
				reply = "本次确认未执行：" + safeRuntimeError(e.Error())
			} else {
				reply = "已确认“" + r.Title + "”这批固定版本，开始沿原发送流程执行；实际发送与失败以原渠道回执为准。"
			}
		}
	}
	m.State = "handled"
	if er := s.saveMessage(m); er != nil {
		return true, er
	}
	if er := s.Emit(m.SessionID, "", "component.owner-confirmation", map[string]string{"messageId": m.ID, "text": reply}); er != nil {
		return true, er
	}
	return true, s.QueueReply(m, "", m.SessionID, "component-owner-confirmation", reply)
}
func (s *Store) cancelReviewPlugin(r ComponentReview) error {
	p, e := s.Plugin(r.PluginID)
	if e != nil {
		return e
	}
	if p.Component == nil || componentBinding(p) != r.Binding || p.Component.CancelAction == "" {
		return errors.New("原取消动作不可用，原草稿保留")
	}
	call, e := s.submitComponent(context.Background(), r.SessionID, ComponentRequest{PluginID: r.PluginID, Action: p.Component.CancelAction, Arguments: map[string]any{"task_ref": r.TaskRef}, OperationKey: "review-stop:" + r.ID}, true)
	if e != nil {
		return e
	}
	old := r.UpdatedAt
	r.CancelCallID = call.ID
	_, e = s.saveComponentReview(r, old)
	return e
}
func (s *Store) CancelComponentReview(id string) (ComponentReview, error) {
	r, e := s.ComponentReview(id)
	if e != nil {
		return r, e
	}
	if r.State != "awaiting_owner" {
		return r, errors.New("任务已确认或执行，须按原任务状态收拢，不能推定未发送")
	}
	old := r.UpdatedAt
	r.State = "cancelled"
	r, e = s.saveComponentReview(r, old)
	if e != nil {
		return r, e
	}
	if r.NoticeRunID != "" {
		_, e = s.DB.Exec("UPDATE communication_roots SET cancelled=1 WHERE id=?", r.NoticeRunID)
	}
	if e == nil {
		e = s.cancelReviewPlugin(r)
	}
	return r, e
}
