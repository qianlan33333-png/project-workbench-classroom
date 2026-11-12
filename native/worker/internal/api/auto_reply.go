package api

import (
	"context"
	"encoding/json"
	"errors"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"net/http"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func (a *Server) replyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /reply-states", func(w http.ResponseWriter, r *http.Request) {
		var keys []string
		if json.Unmarshal([]byte(r.URL.Query().Get("keys")), &keys) != nil {
			respond(w, nil, core.ErrInvalid)
			return
		}
		v, e := a.Store.ChannelStates(r.Context(), principal(r.Context()), r.URL.Query().Get("sessionId"), keys)
		respond(w, v, e)
	})

	mux.HandleFunc("GET /reply-timeline", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.ChannelTimeline(r.Context(), principal(r.Context()), r.URL.Query().Get("sessionId"), r.URL.Query().Get("before"))
		respond(w, v, e)
	})

	mux.HandleFunc("GET /reply-accounts", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		project := r.URL.Query().Get("projectId")
		if !p.IsOwner() || !core.ValidID(project) {
			respond(w, nil, core.ErrDenied)
			return
		}
		rows, err := a.Store.DB.QueryContext(r.Context(), `SELECT body FROM wb_transports ORDER BY id`)
		if err != nil {
			respond(w, nil, err)
			return
		}
		configs := []core.Transport{}
		for rows.Next() {
			var raw []byte
			var c core.Transport
			if err = rows.Scan(&raw); err != nil {
				break
			}
			if err = json.Unmarshal(raw, &c); err != nil {
				break
			}
			for _, id := range c.Projects {
				if id == project && c.Enabled {
					configs = append(configs, c)
					break
				}
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			respond(w, nil, err)
			return
		}
		out := []engine.ConnectionAccount{}
		for _, c := range configs {
			v, e := engine.DescribeConnection(r.Context(), c)
			if e != nil {
				v = engine.ConnectionAccount{ConnectionID: c.ID, Status: "unknown", Error: "无法读取网关账号，请检查连接后重试。"}
			}
			out = append(out, v)
		}
		respond(w, out, nil)
	})
	mux.HandleFunc("POST /reply-manual", func(w http.ResponseWriter, r *http.Request) {
		var in core.OwnerMessage
		if !decode(w, r, &in) {
			return
		}
		run, err := a.Store.PrepareOwnerMessage(r.Context(), principal(r.Context()), in)
		if err != nil {
			if errors.Is(err, core.ErrInvalid) || errors.Is(err, core.ErrDenied) || errors.Is(err, core.ErrConflict) || core.IsMissing(err) {
				respond(w, nil, err)
			} else {
				http.Error(w, "发送登记结果待核实；请使用原请求编号重试。", 503)
			}
			return
		}
		state := "recovering"
		if a.Temporal != nil {
			_, err = a.Temporal.ExecuteWorkflow(r.Context(), client.StartWorkflowOptions{ID: run.ID, TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.SendMessage, run.ID)
			if accepted(err) == nil {
				state = "queued"
			}
		}
		// The immutable intent is already durable. A queue outage must never
		// masquerade as a rejected send and cause a new client idempotency key.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		write(w, struct {
			RunID string `json:"runId"`
			State string `json:"state"`
		}{run.ID, state})
	})
	mux.HandleFunc("POST /conversation-control", func(w http.ResponseWriter, r *http.Request) {
		var in core.ConversationControl
		if decode(w, r, &in) {
			out, err := a.Store.ControlConversation(r.Context(), principal(r.Context()), in)
			if err == nil {
				write(w, out)
				return
			}
			status, code := http.StatusServiceUnavailable, "unavailable"
			switch {
			case core.IsMissing(err):
				status, code = 404, "not_bound"
			case errors.Is(err, core.ErrDenied):
				status, code = 403, "forbidden"
			case errors.Is(err, core.ErrInvalid):
				status, code = 400, "invalid_request"
			case errors.Is(err, core.ErrConflict):
				status, code = 409, "stale_version"
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			write(w, map[string]string{"error": code})
		}
	})
	mux.HandleFunc("POST /connector-state/compare", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID       string         `json:"id"`
			Expected map[string]any `json:"expected"`
			Value    map[string]any `json:"value"`
		}
		if decode(w, r, &in) {
			respond(w, map[string]bool{"ok": true}, a.Store.CompareConnectorState(r.Context(), principal(r.Context()), in.ID, in.Expected, in.Value))
		}
	})
	mux.HandleFunc("GET /reply-channel-history", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.ChannelHistory(r.Context(), principal(r.Context()), r.URL.Query().Get("sessionId"))
		respond(w, v, e)
	})

	mux.HandleFunc("POST /messages/settle", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		var v struct {
			RunID string `json:"runId"`
		}
		if !decode(w, r, &v) {
			return
		}
		m, e := a.Store.IncomingForRun(r.Context(), v.RunID)
		if e == nil && m.State == "received" {
			e = a.Store.MessageDisposition(r.Context(), m.Principal, m.ConnectionID, m.EventID, "handoff")
		}
		respond(w, map[string]bool{"settled": e == nil}, e)
	})
	mux.HandleFunc("GET /messages/admit", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		m, e := a.Store.IncomingForRun(r.Context(), r.URL.Query().Get("runId"))
		allowed := false
		if e == nil {
			allowed, e = a.Store.CanAdmitIncoming(r.Context(), m)
			if e == nil && !allowed {
				m, e = a.Store.Message(r.Context(), principal(r.Context()), m.ConnectionID, m.EventID)
			}
		}
		respond(w, map[string]bool{"allowed": allowed, "terminal": e == nil && m.State != "received"}, e)
	})
	mux.HandleFunc("GET /reply-resources", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		id := r.URL.Query().Get("projectId")
		if !p.IsOwner() || !core.ValidID(id) {
			respond(w, nil, core.ErrDenied)
			return
		}
		v, e := a.Store.Resources(r.Context(), core.Principal{Kind: "project", ProjectID: id, SessionID: "reply-config"})
		respond(w, v, e)
	})

	mux.HandleFunc("POST /reply-projects", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			ProjectID string `json:"projectId"`
		}
		if decode(w, r, &v) {
			out, e := a.Store.InitReplyProject(r.Context(), principal(r.Context()), v.ProjectID)
			respond(w, out, e)
		}
	})
	mux.HandleFunc("GET /transports", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		rows, e := a.Store.DB.QueryContext(r.Context(), `SELECT body FROM wb_transports ORDER BY id`)
		if e != nil {
			respond(w, nil, e)
			return
		}
		defer rows.Close()
		out := []core.Transport{}
		for rows.Next() {
			var v core.Transport
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				break
			}
			if e = json.Unmarshal(raw, &v); e != nil {
				break
			}
			out = append(out, v)
		}
		if e == nil {
			e = rows.Err()
		}
		respond(w, out, e)
	})
	mux.HandleFunc("GET /reply-settings", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.ReplySettings(r.Context(), principal(r.Context()), r.URL.Query().Get("projectId"))
		respond(w, v, e)
	})
	mux.HandleFunc("POST /reply-settings", func(w http.ResponseWriter, r *http.Request) {
		var v core.ReplySettings
		if decode(w, r, &v) {
			v, e := a.Store.PutReplySettings(r.Context(), principal(r.Context()), v)
			respond(w, v, e)
		}
	})
	mux.HandleFunc("GET /message-routes", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		v, e := a.Store.Conversations(r.Context(), principal(r.Context()), q.Get("projectId"), q.Get("q"), q.Get("after"), q.Get("paused") == "true")
		respond(w, v, e)
	})
	mux.HandleFunc("GET /reply-projects", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		rows, e := a.Store.DB.QueryContext(r.Context(), `SELECT body FROM wb_settings WHERE id LIKE 'reply-%' ORDER BY id`)
		if e != nil {
			respond(w, nil, e)
			return
		}
		defer rows.Close()
		out := []core.ReplySettings{}
		for rows.Next() {
			var raw []byte
			var v core.ReplySettings
			if e = rows.Scan(&raw); e != nil {
				break
			}
			if e = json.Unmarshal(raw, &v); e != nil {
				break
			}
			out = append(out, v)
		}
		if e == nil {
			e = rows.Err()
		}
		respond(w, out, e)
	})
	mux.HandleFunc("GET /reply-session", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() || !core.ValidID(r.URL.Query().Get("sessionId")) {
			respond(w, nil, core.ErrDenied)
			return
		}
		var raw []byte
		e := a.Store.DB.QueryRowContext(r.Context(), `SELECT body FROM wb_message_routes WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=?`, r.URL.Query().Get("sessionId")).Scan(&raw)
		var v core.MessageRoute
		if e == nil {
			e = json.Unmarshal(raw, &v)
		}
		respond(w, v, e)
	})
	mux.HandleFunc("POST /messages/receipt-event", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		var v struct {
			ConnectionID string `json:"connectionId"`
			EventID      string `json:"eventId"`
			MessageID    string `json:"messageId"`
			State        string `json:"state"`
		}
		if !decode(w, r, &v) {
			return
		}
		if v.MessageID == "" || !core.ValidID(v.ConnectionID) || (v.State != "sent" && v.State != "delivered" && v.State != "failed") {
			respond(w, map[string]bool{"ignored": true}, nil)
			return
		}
		// Only update an existing immutable target, never create a delivery from a callback.
		rows, e := a.Store.DB.QueryContext(r.Context(), `SELECT id FROM wb_deliveries WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.connectionId'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.receipt'))=?`, v.ConnectionID, v.MessageID)
		if e != nil {
			respond(w, nil, e)
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				break
			}
			ids = append(ids, id)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e == nil {
			for _, id := range ids {
				var delivery core.Delivery
				delivery, e = a.Store.FinishDelivery(r.Context(), id, v.State, v.MessageID, "")
				if e != nil {
					break
				}
				// The existing sweep recovers durable drafts if startup fails.
				_ = engine.StartNextReplyPart(r.Context(), a.Store, a.Temporal, delivery)
			}
		}
		respond(w, map[string]bool{"recorded": e == nil}, e)
	})
}

func (a *Server) startIncoming(ctx context.Context, message core.StoredMessage) error {
	goal := message.Text + "\n\n本消息connectionId=" + message.ConnectionID + " eventId=" + message.EventID + "。当前是原客户Session的继续对话，先结合已有原生历史理解本条消息。message_context只补充允许资料和近期渠道记录，historyLimited不代表原Session没有历史；本条原文以宿主本轮输入为准。是否检索、怎样检索及使用哪个已授权工具，按已加载专家方法决定，不要求每条消息都检索；业务事实须有可核实依据。仅通过message_reply或message_disposition提交本条消息的处理结果，普通聊天正文不会自动发给客户。"
	if message.ExpertSkill != "" {
		goal = "本轮使用当前配置的专家方法，历史中的旧方法不覆盖本轮配置。先调用skill加载专家方法：" + message.ExpertSkill + "。\n" + goal
	}
	if message.Principal.Kind == "personal" {
		goal = message.Text + "\n\n这是已绑定本人渠道的新消息，请由 SuperAgent 处理；需要回复本人时使用当前消息的connectionId=" + message.ConnectionID + " eventId=" + message.EventID + "。"
	}
	run := core.Run{ID: message.ID, Principal: message.Principal, Operation: "incoming", Title: "客户新消息", Goal: goal, OriginMessage: message.ID, ExpertRef: message.ExpertRef, Sources: append([]core.Ref{}, message.Knowledge...)}
	for _, ref := range message.Context {
		run.Sources = append(run.Sources, ref.Ref)
	}
	if e := a.Store.SaveRun(ctx, run); e != nil {
		return e
	}
	if a.Temporal == nil {
		return nil
	}
	task := any(engine.Dispatch)
	if message.Principal.Kind == "project" {
		task = engine.Incoming
	}
	_, e := a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: run.ID, TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, task, run.ID)
	return accepted(e)
}

// Existing persisted messages are the recovery source. No second task store.
func (a *Server) RecoverIncoming(ctx context.Context) error {
	if a.Temporal == nil {
		return nil
	}
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT body FROM wb_messages WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.state'))='received' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.kind'))='project' ORDER BY JSON_UNQUOTE(JSON_EXTRACT(body,'$.createdAt')) LIMIT 100`)
	if e != nil {
		return e
	}
	var pending []core.StoredMessage
	for rows.Next() {
		var raw []byte
		var v core.StoredMessage
		if e = rows.Scan(&raw); e != nil {
			break
		}
		if e = json.Unmarshal(raw, &v); e != nil {
			break
		}
		pending = append(pending, v)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return e
	}
	var firstError error
	for _, m := range pending {
		if _, err := a.Store.CanAdmitIncoming(ctx, m); err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		current, err := a.Store.Message(ctx, core.Principal{Kind: "owner", SessionID: "recovery"}, m.ConnectionID, m.EventID)
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		if current.State != "received" {
			continue
		}
		if err = a.startIncoming(ctx, current); err != nil {
			if errors.Is(err, core.ErrDenied) || errors.Is(err, core.ErrConflict) {
				err = a.Store.MessageDisposition(ctx, m.Principal, m.ConnectionID, m.EventID, "handoff")
			}
			if firstError == nil {
				firstError = err
			}
		}
	}
	return firstError
}
