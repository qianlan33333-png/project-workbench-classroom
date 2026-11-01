// Package wecomgateway implements the published gateway v1 protocol. It has no
// dependency on a workbench, business expert, or model runtime.
package wecomgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL, APIKey string
	HTTP            *http.Client
}
type Error struct {
	Status    int    `json:"status"`
	Code      string `json:"code"`
	RequestID string `json:"requestId,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("gateway HTTP %d: %s (%s)", e.Status, e.Code, e.RequestID)
}

type Response struct {
	Status    int             `json:"httpStatus"`
	RequestID string          `json:"requestId,omitempty"`
	Data      json.RawMessage `json:"data"`
}

func ValidateURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid API base URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	h := u.Hostname()
	ip := net.ParseIP(h)
	if u.Scheme == "http" && (h == "localhost" || ip != nil && ip.IsLoopback()) {
		return nil
	}
	return errors.New("HTTPS required (HTTP allowed only on loopback)")
}
func (c Client) Do(ctx context.Context, method, path string, body any, key string) (Response, error) {
	var out Response
	if e := ValidateURL(c.BaseURL); e != nil {
		return out, e
	}
	if c.APIKey == "" {
		return out, errors.New("API credential missing")
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "..") {
		return out, errors.New("invalid API path")
	}
	var b []byte
	var e error
	if body != nil {
		b, e = json.Marshal(body)
		if e != nil {
			return out, errors.New("invalid JSON input")
		}
	}
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(b))
	if e != nil {
		return out, errors.New("invalid request")
	}
	r.Header.Set("Authorization", "Bearer "+c.APIKey)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		if len(key) > 128 || strings.ContainsAny(key, "\r\n") {
			return out, errors.New("invalid idempotency key")
		}
		r.Header.Set("Idempotency-Key", key)
	}
	h := http.Client{Timeout: 15 * time.Second}
	if c.HTTP != nil {
		h = *c.HTTP
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := h.Do(r)
	if e != nil {
		return out, errors.New("gateway transport failed; submission may be unknown")
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if e != nil || len(data) > 4<<20 {
		return out, errors.New("gateway response unreadable; submission may be unknown")
	}
	out = Response{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-Id"), Data: data}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var v struct{ Code, RequestID string }
		_ = json.Unmarshal(data, &v)
		if v.Code == "" {
			v.Code = "HTTP_ERROR"
		}
		if v.RequestID == "" {
			v.RequestID = out.RequestID
		}
		// Never return server-provided prose: it can echo credentials or payloads.
		return out, &Error{Status: resp.StatusCode, Code: strings.ReplaceAll(v.Code, c.APIKey, "[redacted]"), RequestID: strings.ReplaceAll(v.RequestID, c.APIKey, "[redacted]")}
	}
	if !json.Valid(data) {
		return out, errors.New("gateway response is not JSON")
	}
	return out, nil
}

type Message struct {
	MessageID  string    `json:"messageId"`
	AccountID  string    `json:"accountId"`
	Direction  string    `json:"direction"`
	ContactID  string    `json:"contactId"`
	RoomID     string    `json:"roomId"`
	Type       string    `json:"type"`
	Historical bool      `json:"historical"`
	Status     string    `json:"status"`
	FailReason string    `json:"failReason"`
	SentAt     time.Time `json:"sentAt"`
	Sender     struct {
		ContactID string `json:"contactId"`
		Name      string `json:"name"`
	} `json:"sender"`
	Content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}
type SendRequest struct {
	AccountID string `json:"accountId"`
	To        struct {
		ContactID string `json:"contactId"`
	} `json:"to"`
	Type    string `json:"type"`
	Content struct {
		Text string `json:"text"`
	} `json:"content"`
	Mode string `json:"mode"`
}

func TextRequest(account, contact, text string) SendRequest {
	v := SendRequest{AccountID: account, Type: "text", Mode: "immediate"}
	v.To.ContactID = contact
	v.Content.Text = text
	return v
}
func (c Client) SendResponse(ctx context.Context, v SendRequest, key string) (Response, error) {
	if key == "" || v.AccountID == "" || v.To.ContactID == "" || v.Type != "text" || v.Mode != "immediate" || strings.TrimSpace(v.Content.Text) == "" || len(v.Content.Text) > 16000 {
		return Response{}, errors.New("private immediate text and idempotency key required")
	}
	return c.Do(ctx, "POST", "/messages", v, key)
}
func (c Client) Send(ctx context.Context, v SendRequest, key string) (Message, error) {
	var m Message
	r, e := c.SendResponse(ctx, v, key)
	if e != nil {
		return m, e
	}
	e = json.Unmarshal(r.Data, &m)
	if e == nil && m.MessageID == "" {
		e = errors.New("gateway message receipt missing")
	}
	return m, e
}

type Webhook struct {
	URL       string   `json:"url"`
	Events    []string `json:"events"`
	HasSecret bool     `json:"hasSecret"`
	Secret    string   `json:"secret,omitempty"`
}

// ApplyWebhook never replaces another receiver or rotates an existing secret.
func (c Client) ApplyWebhook(ctx context.Context, target string, haveSecret bool) (Webhook, error) {
	var old Webhook
	r, e := c.Do(ctx, "GET", "/webhook", nil, "")
	if e != nil {
		return old, e
	}
	if e = json.Unmarshal(r.Data, &old); e != nil {
		return old, e
	}
	if e = ValidateURL(target); e != nil {
		return old, e
	}
	if old.URL != "" && old.URL != target {
		return old, errors.New("webhook conflict: another receiver is configured")
	}
	if old.HasSecret && !haveSecret {
		return old, errors.New("existing webhook secret required; automatic rotation refused")
	}
	if old.URL == target && len(old.Events) == 0 {
		return old, nil
	}
	r, e = c.Do(ctx, "PUT", "/webhook", map[string]any{"url": target, "events": []string{}, "rotateSecret": false}, "")
	if e == nil {
		e = json.Unmarshal(r.Data, &old)
	}
	return old, e
}
