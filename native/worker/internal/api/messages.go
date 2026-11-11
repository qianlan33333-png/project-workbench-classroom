package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"log"
	"net/http"
	"time"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type MessageKey struct {
	ConnectionID string `json:"connectionId"`
	EventID      string `json:"eventId"`
}
type ReceiptRequest struct {
	RunID      string `json:"runId"`
	DeliveryID string `json:"deliveryId,omitempty"`
}
type ReplyInput struct {
	MessageKey
	Text           string `json:"text"`
	MaxSingleChars int    `json:"maxSingleChars,omitempty"`
	MaxPartChars   int    `json:"maxPartChars,omitempty"`
}
type PrepareMessage struct {
	OperationKey string            `json:"operationKey"`
	ConnectionID string            `json:"connectionId"`
	Target       map[string]string `json:"target"`
	Text         string            `json:"text"`
	Title        string            `json:"title"`
}

func (a *Server) messageTools(s *mcp.Server, p core.Principal) {
	type ContextResult struct {
		Records        []core.Record          `json:"records"`
		Knowledge      []core.Ref             `json:"knowledge"`
		History        []core.IncomingMessage `json:"history"`
		HistoryLimited bool                   `json:"historyLimited"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "message_context", Description: "Read only this customer's fixed profile records, allowed knowledge references and supplemental recent channel history, excluding the current event. Native Session history remains the primary conversation context; historyLimited only describes this supplemental result, not all remembered conversation. Use the Host-provided current message as the question; the loaded expert chooses authorized knowledge tools. Human history can have missing text; never invent it."}, func(ctx context.Context, _ *mcp.CallToolRequest, in MessageKey) (*mcp.CallToolResult, ContextResult, error) {
		m, e := a.Store.Message(ctx, p, in.ConnectionID, in.EventID)
		if e != nil {
			return nil, ContextResult{}, e
		}
		profileCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		records, profileErr := a.Store.ResolveReplyProfile(profileCtx, p, m, engine.ResolveContactIdentity)
		cancel()
		if profileErr != nil {
			log.Printf("Optional customer profile unavailable: %v", profileErr)
			records = []core.Record{}
		}
		history, e := a.Store.MessageHistory(ctx, p, in.ConnectionID, in.EventID)
		out := ContextResult{Records: records, Knowledge: m.Knowledge, History: history, HistoryLimited: true}
		for len(out.History) > 0 {
			raw, _ := json.Marshal(out)
			if len(raw) <= 46000 {
				break
			}
			out.History = out.History[:len(out.History)-1]
		}
		return nil, out, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "message_prepare", Description: "Prepare one immutable outgoing message for owner review. Does not send. The owner must confirm the exact saved content/version; agents cannot confirm themselves."}, func(ctx context.Context, _ *mcp.CallToolRequest, in PrepareMessage) (*mcp.CallToolResult, core.Decision, error) {
		if p.Kind != "project" || !core.ValidID(in.OperationKey) {
			return nil, core.Decision{}, core.ErrDenied
		}
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/"+in.OperationKey)).String()
		r := core.Run{ID: id, Principal: p, Operation: "message", Title: in.Title, Goal: in.Text}
		if e := a.Store.SaveRun(ctx, r); e != nil {
			return nil, core.Decision{}, e
		}
		d := core.Delivery{ID: id, RunID: id, ItemID: "message", ConnectionID: in.ConnectionID, Target: in.Target, Text: in.Text}
		if e := a.Store.PutDelivery(ctx, r, d); e != nil {
			return nil, core.Decision{}, e
		}
		target, _ := json.Marshal(in.Target)
		decision, e := a.Store.Ask(ctx, r, 1, "请抽检这条已固定的消息。\n目标："+string(target)+"\n内容：\n"+in.Text+"\n确认后回复：发 "+id[:8]+"\n消息摘要："+core.Digest(struct {
			Target           map[string]string
			Text, Connection string
		}{in.Target, in.Text, in.ConnectionID}))
		if e != nil {
			return nil, decision, e
		}
		_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.WaitAndSend, engine.FollowupInput{RunID: id, DecisionID: decision.ID, Waiting: true})
		return nil, decision, accepted(e)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "message_reply", Description: "Submit one fixed reply for a persisted incoming message with automatic replies enabled. The recipient is bound to that message. For WeCom project automatic replies or when maxPartChars is provided, each nonempty line becomes its own message. With maxPartChars, maxSingleChars optionally sets a larger Unicode-character limit for a single line. Submit the entire reply once."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReplyInput) (*mcp.CallToolResult, RunRef, error) {
		r, e := a.Store.PrepareReplyWithLimits(ctx, p, in.ConnectionID, in.EventID, in.Text, in.MaxSingleChars, in.MaxPartChars)
		if e != nil {
			return nil, RunRef{}, e
		}
		id := r.ID
		_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.SendMessage, id)
		return nil, RunRef{RunID: id}, accepted(e)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "message_disposition", Description: "Record ignore or human handoff for one persisted incoming message. Never sends anything."}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		MessageKey
		State string `json:"state"`
	}) (*mcp.CallToolResult, MessageKey, error) {
		return nil, in.MessageKey, a.Store.MessageDisposition(ctx, p, in.ConnectionID, in.EventID, in.State)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "message_receipt", Description: "Reconcile the original delivery using the connector's receipt tool. For a batch supply root runId and deliveryId from message_batch_read. Never resends. Returns stored status; the queued read updates it asynchronously."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReceiptRequest) (*mcp.CallToolResult, core.Delivery, error) {
		return a.reconcileDelivery(ctx, p, in, false)
	})
}
func (a *Server) messageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /notification-targets", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		rows, e := a.Store.DB.QueryContext(r.Context(), `SELECT r.body,JSON_UNQUOTE(JSON_EXTRACT(t.body,'$.name')) FROM wb_message_routes r JOIN wb_transports t ON t.id=r.connection_id WHERE JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.principal.kind'))='personal' AND JSON_EXTRACT(r.body,'$.enabled')=TRUE AND JSON_EXTRACT(t.body,'$.enabled')=TRUE`)
		if e != nil {
			respond(w, nil, e)
			return
		}
		defer rows.Close()
		type Option struct {
			Name      string `json:"name"`
			SessionID string `json:"sessionId"`
			core.NotificationTarget
		}
		out := []Option{}
		for rows.Next() {
			var raw []byte
			var name string
			if e = rows.Scan(&raw, &name); e != nil {
				respond(w, nil, e)
				return
			}
			var route core.MessageRoute
			if e = json.Unmarshal(raw, &route); e != nil {
				respond(w, nil, e)
				return
			}
			out = append(out, Option{Name: name, SessionID: route.Principal.SessionID, NotificationTarget: core.NotificationTarget{ConnectionID: route.ConnectionID, Target: map[string]string{"conversationId": route.ConversationID, "actorId": route.ActorID}}})
		}
		respond(w, out, rows.Err())
	})
	mux.HandleFunc("GET /notifications", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		v, e := a.Store.NotificationSettings(r.Context())
		respond(w, v, e)
	})
	mux.HandleFunc("POST /notifications", func(w http.ResponseWriter, r *http.Request) {
		var v core.NotificationSettings
		if decode(w, r, &v) {
			respond(w, v, a.Store.PutNotificationSettings(r.Context(), principal(r.Context()), v))
		}
	})

	mux.HandleFunc("GET /connector-state", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		id := r.URL.Query().Get("id")
		if !core.ValidID(id) {
			respond(w, nil, core.ErrInvalid)
			return
		}
		var raw json.RawMessage
		e := a.Store.DB.QueryRowContext(r.Context(), `SELECT body FROM wb_connector_state WHERE id=?`, id).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			raw = []byte(`{}`)
			e = nil
		}
		respond(w, raw, e)
	})
	mux.HandleFunc("POST /connector-state", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		var in struct {
			ID    string          `json:"id"`
			Value json.RawMessage `json:"value"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !core.ValidID(in.ID) || len(in.Value) > 256*1024 {
			respond(w, nil, core.ErrInvalid)
			return
		}
		_, e := a.Store.DB.ExecContext(r.Context(), `INSERT INTO wb_connector_state VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)`, in.ID, string(in.Value))
		respond(w, map[string]bool{"saved": e == nil}, e)
	})
	mux.HandleFunc("POST /transports", func(w http.ResponseWriter, r *http.Request) {
		var v core.Transport
		if decode(w, r, &v) {
			respond(w, v, a.Store.PutTransport(r.Context(), principal(r.Context()), v))
		}
	})
	mux.HandleFunc("POST /message-routes", func(w http.ResponseWriter, r *http.Request) {
		var v core.MessageRoute
		if decode(w, r, &v) {
			if v.ConnectionID == "" {
				out, e := a.Store.ChangeReplyMode(r.Context(), principal(r.Context()), v.ID, v.Revision, v.AutoReply)
				respond(w, out, e)
				return
			}
			if v.Principal.Kind == "project" {
				old, e := a.Store.Route(r.Context(), v.ID)
				if core.IsMissing(e) {
					v.Principal.SessionID = "object-" + uuid.NewString()
				} else if e != nil {
					respond(w, nil, e)
					return
				} else {
					v.Principal = old.Principal
				}
			}
			respond(w, v, a.Store.PutRoute(r.Context(), principal(r.Context()), v))
		}
	})
	mux.HandleFunc("POST /messages", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		var in core.IncomingMessage
		if !decode(w, r, &in) {
			return
		}
		_, _ = a.Store.EnsureReplyRoute(r.Context(), in)
		message, e := a.Store.AcceptMessage(r.Context(), in)
		if core.IsMissing(e) {
			if _, err := a.Store.EnsureReplyRoute(r.Context(), in); err == nil {
				message, e = a.Store.AcceptMessage(r.Context(), in)
			}
		}
		if e != nil || message.State != "received" {
			respond(w, message, e)
			return
		}
		// Only a configured personal route is a human-owner confirmation channel.
		if message.Principal.Kind == "personal" {
			d, matched, err := a.Store.OwnerMessageDecision(r.Context(), message)
			if err != nil {
				respond(w, nil, err)
				return
			}
			if matched {
				d.Answer = "发 " + d.RunID[:8]
				if _, err = a.Store.Answer(r.Context(), principal(r.Context()), d); err == nil {
					err = a.Store.SetMessageState(r.Context(), message.Principal, message.ConnectionID, message.EventID, "confirmed")
				}
				respond(w, message, err)
				return
			}
		}
		e = a.startIncoming(r.Context(), message)
		respond(w, message, e)
	})
}

func (a *Server) reconcileDelivery(ctx context.Context, p core.Principal, in ReceiptRequest, requireCRM bool) (*mcp.CallToolResult, core.Delivery, error) {
	run, e := a.Store.Run(ctx, p, in.RunID)
	if e != nil {
		return nil, core.Delivery{}, e
	}
	id := in.DeliveryID
	if id == "" {
		id = in.RunID
	}
	d, e := a.Store.Delivery(ctx, id)
	if e != nil {
		return nil, d, e
	}
	if d.RunID != in.RunID {
		return nil, core.Delivery{}, core.ErrDenied
	}
	if requireCRM {
		transport, err := a.Store.Transport(ctx, p, d.ConnectionID)
		if err != nil {
			return nil, core.Delivery{}, err
		}
		if transport.Kind != "crm-cli" {
			return nil, core.Delivery{}, core.ErrInvalid
		}
	}
	_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id + "-receipt-" + uuid.NewString(), TaskQueue: engine.InteractiveQueue}, engine.ReceiptOnly, id)
	if e != nil {
		return nil, core.Delivery{}, e
	}
	v, e := a.Store.Delivery(ctx, id)
	if e != nil {
		return nil, core.Delivery{}, e
	}
	if e = a.Store.RememberRunRead(ctx, p, run); e != nil {
		return nil, core.Delivery{}, e
	}
	return nil, v, nil
}
