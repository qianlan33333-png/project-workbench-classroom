package engine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"net/http"
	"os"
	"time"
	"workbench-native/internal/core"
)

type Receipt struct {
	State   string `json:"state"`
	Receipt string `json:"receipt,omitempty"`
	Error   string `json:"error,omitempty"`
}

func ReceiptOnly(ctx workflow.Context, id string) error {
	return workflow.ExecuteActivity(activityContext(ctx, InteractiveQueue), "ReconcileDelivery", id).Get(ctx, nil)
}

type connectorAuth struct{ token string }

func (a connectorAuth) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+a.token)
	return http.DefaultTransport.RoundTrip(copy)
}
func connectConnector(ctx context.Context, c core.Transport) (*mcp.ClientSession, error) {
	token := os.Getenv(c.TokenEnv)
	if len(token) < 32 {
		return nil, temporal.NewNonRetryableApplicationError("connector token not configured", "Invalid", nil)
	}
	httpClient := &http.Client{Timeout: 35 * time.Second, Transport: connectorAuth{token}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	client := mcp.NewClient(&mcp.Implementation{Name: "workbench-worker", Version: "0.1.0"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.Endpoint, HTTPClient: httpClient}, nil)
}

type ConnectionAccount struct {
	AccountID    string `json:"accountId"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	SendEnabled  bool   `json:"sendEnabled"`
	ConnectionID string `json:"connectionId"`
	Error        string `json:"error,omitempty"`
}

func DescribeConnection(ctx context.Context, c core.Transport) (ConnectionAccount, error) {
	session, e := connectConnector(ctx, c)
	if e != nil {
		return ConnectionAccount{}, e
	}
	defer session.Close()
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "connection_describe", Arguments: map[string]string{"connectionId": c.ID}})
	if e != nil {
		return ConnectionAccount{}, e
	}
	if result.IsError {
		return ConnectionAccount{}, errors.New("account lookup failed")
	}
	raw, e := json.Marshal(result.StructuredContent)
	if e != nil {
		return ConnectionAccount{}, e
	}
	var v ConnectionAccount
	e = json.Unmarshal(raw, &v)
	v.ConnectionID = c.ID
	if v.AccountID == "" || v.Name == "" {
		return v, errors.New("account metadata missing")
	}
	return v, e
}

// A read-only MCP initialize is safe to wait for before prepared is claimed.
// A channel that has not started must not become an unknown external send.
func connectorReady(ctx context.Context, c core.Transport) (bool, error) {
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	session, err := connectConnector(probe, c)
	if err == nil {
		_ = session.Close()
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	var app *temporal.ApplicationError
	if errors.As(err, &app) && app.NonRetryable() {
		return false, err
	}
	return false, nil
}

func callConnector(ctx context.Context, c core.Transport, name string, input any) (Receipt, error) {
	session, e := connectConnector(ctx, c)
	if e != nil {
		return Receipt{}, e
	}
	defer session.Close()
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
	if e != nil {
		return Receipt{}, e
	}
	if result.IsError {
		return Receipt{}, errors.New("connector rejected the operation; reconcile before resending")
	}
	data, e := json.Marshal(result.StructuredContent)
	if e != nil {
		return Receipt{}, e
	}
	var receipt Receipt
	if e = json.Unmarshal(data, &receipt); e != nil {
		return receipt, e
	}
	return receipt, nil
}

// The send is attempted only by the Activity that atomically claims prepared.
// Every retry/crash path reads the receipt; no retry can claim submitting again.
func SendMessage(ctx workflow.Context, id string) error { return SubmitMessage(ctx, id) }

func (a *Activities) SendDelivery(ctx context.Context, id string) (Receipt, error) {
	if activity.IsActivity(ctx) {
		stop := keepAlive(ctx)
		defer stop()
	}
	d, e := a.Store.Delivery(ctx, id)
	if e != nil {
		return Receipt{}, e
	}
	r, e := a.Store.ExecutionRun(ctx, d.RunID)
	if e != nil {
		return Receipt{}, failure(e)
	}
	if d.State == "prepared" {
		if r.Operation == "reply" {
			expired, err := a.Store.ExpireReplyDraft(ctx, id)
			if err != nil {
				return Receipt{}, failure(err)
			}
			if expired {
				return Receipt{State: "failed", Error: "自动回复未提交：配置或会话已失效。"}, nil
			}
		}
		transport, err := a.Store.PreparingTransport(ctx, r, d.ConnectionID)
		if err != nil {
			return Receipt{}, failure(err)
		}
		ready, err := connectorReady(ctx, transport)
		if err != nil {
			return Receipt{}, err
		}
		if !ready {
			return Receipt{State: "prepared"}, nil
		}
	}
	d, claimed, e := a.Store.ClaimDelivery(ctx, id)
	if e != nil {
		return Receipt{}, failure(e)
	}
	if !claimed {
		return a.ReconcileDelivery(ctx, id)
	}
	c, e := a.Store.DeliveryTransport(ctx, id)
	if e != nil {
		return Receipt{}, e
	}
	receipt, e := callConnector(ctx, c, "message_send", map[string]any{"connectionId": c.ID, "idempotencyKey": d.ID, "target": d.Target, "text": d.Text})
	if e != nil {
		receipt = Receipt{State: "unknown", Error: "发送结果未明确；已停止重发，只允许核对回执。"}
	}
	return a.recordReceipt(ctx, r, d, receipt)
}
func (a *Activities) ReconcileDelivery(ctx context.Context, id string) (Receipt, error) {
	if activity.IsActivity(ctx) {
		stop := keepAlive(ctx)
		defer stop()
	}
	d, e := a.Store.Delivery(ctx, id)
	if e != nil {
		return Receipt{}, e
	}
	if d.State == "sent" || d.State == "delivered" || d.State == "failed" {
		return Receipt{d.State, d.Receipt, d.Error}, nil
	}
	r, e := a.Store.ExecutionRun(ctx, d.RunID)
	if e != nil {
		return Receipt{}, failure(e)
	}
	c, e := a.Store.DeliveryTransport(ctx, id)
	if e != nil {
		return Receipt{}, failure(e)
	}
	input := map[string]any{"connectionId": c.ID, "idempotencyKey": d.ID, "receipt": d.Receipt, "target": d.Target, "state": d.State}
	if r.Operation == "control_notification" || c.Kind == "crm-cli" {
		input["text"] = d.Text
	}
	receipt, e := callConnector(ctx, c, "message_receipt", input)
	if e != nil {
		return Receipt{}, e
	}
	return a.recordReceipt(ctx, r, d, receipt)
}
func (a *Activities) recordReceipt(ctx context.Context, r core.Run, d core.Delivery, receipt Receipt) (Receipt, error) {
	saved, e := a.Store.FinishDelivery(ctx, d.ID, receipt.State, receipt.Receipt, receipt.Error)
	if e != nil {
		return Receipt{}, failure(e)
	}
	if r.Operation == "dispatch_batch" || r.Operation == "control_notification" {
		return Receipt{saved.State, saved.Receipt, saved.Error}, nil
	}
	labels := map[string]string{"accepted": "渠道已受理，尚无真实送达凭据。", "pending_confirmation": "已创建手机待发送任务，等待手机端确认。", "sent": "渠道回执确认已发送。", "delivered": "渠道回执确认已送达。", "failed": "渠道明确返回失败。", "unknown": "发送结果未明确，已停止重发，需要核对。"}
	if r.Operation == "owner_message" {
		if e = a.Store.Emit(ctx, core.RunEvent{ID: d.ID + "-owner-history-" + saved.State, SessionID: r.Principal.SessionID, Kind: "message.history", Text: "本人通过工作台回复（" + labels[saved.State] + "）\n" + d.Text, Details: map[string]any{"deliveryId": d.ID, "state": saved.State}}); e != nil {
			return Receipt{}, e
		}
	}
	if e = a.Store.Emit(ctx, core.RunEvent{ID: d.ID + "-" + saved.State, SessionID: r.Principal.SessionID, RunID: r.ID, Kind: "message." + saved.State, Text: labels[saved.State], Details: map[string]any{"deliveryId": saved.ID, "state": saved.State, "receipt": saved.Receipt}}); e != nil {
		return Receipt{}, e
	}
	return Receipt{saved.State, saved.Receipt, saved.Error}, nil
}

func WaitAndSend(ctx workflow.Context, in FollowupInput) (err error) {
	defer reportFailure(ctx, in.RunID, &err)()
	ctx = activityContext(ctx, InteractiveQueue)
	for {
		var answer Decision
		if e := receiveSignal(ctx, "decision", &answer); e != nil {
			return e
		}
		var approved bool
		if err = workflow.ExecuteActivity(ctx, "ConfirmMessage", in.RunID, answer).Get(ctx, &approved); err != nil {
			return err
		}
		if approved {
			break
		}
	}
	return SendMessage(ctx, in.RunID)
}
func (a *Activities) ConfirmMessage(ctx context.Context, id string, answer Decision) (bool, error) {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return false, failure(e)
	}
	d, e := a.Store.Decision(ctx, r.Principal, answer.ID)
	if e != nil {
		return false, e
	}
	if d.RunID != id || d.Revision != answer.Revision || d.Digest != answer.Digest || d.State != "answered" {
		return false, nil
	}
	if d.Answer != "发" && d.Answer != "发 "+id[:8] {
		return false, temporal.NewNonRetryableApplicationError("owner did not confirm sending", "NotApproved", nil)
	}
	return true, nil
}
