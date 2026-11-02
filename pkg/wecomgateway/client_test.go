package wecomgateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebhookRawSignatureAndReplayBoundary(t *testing.T) {
	raw := []byte(`{"eventId":"evt_1", "type":"message.received","tenantId":"t","accountId":"a","occurredAt":"2026-11-02T00:00:00Z","data":{}}`)
	at := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	mac := hmac.New(sha256.New, []byte("secret"))
	fmt.Fprintf(mac, "%d.", at.Unix())
	mac.Write(raw)
	sig := fmt.Sprintf("t=%d,v1=%s", at.Unix(), hex.EncodeToString(mac.Sum(nil)))
	if _, e := Verify("secret", raw, sig, "evt_1", at); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name    string
		body    []byte
		sig, id string
		at      time.Time
	}{
		{"reserialized", []byte(strings.ReplaceAll(string(raw), ", ", ",")), sig, "evt_1", at},
		{"header mismatch", raw, sig, "evt_other", at},
		{"expired", raw, sig, "evt_1", at.Add(301 * time.Second)},
		{"future", raw, sig, "evt_1", at.Add(-301 * time.Second)},
		{"duplicate timestamp", raw, sig + ",t=1", "evt_1", at},
		{"bad hex", raw, fmt.Sprintf("t=%d,v1=xx", at.Unix()), "evt_1", at},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := Verify("secret", tc.body, tc.sig, tc.id, tc.at); e == nil {
				t.Fatal("accepted invalid signed event")
			}
		})
	}
}
func TestIdempotentSendAndNoRedirectCredentialLeak(t *testing.T) {
	keys := map[string]string{}
	var sends, puts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-test" {
			t.Error("missing credential")
		}
		switch r.URL.Path {
		case "/messages":
			key := r.Header.Get("Idempotency-Key")
			if keys[key] == "" {
				sends++
				keys[key] = "msg_1"
			}
			fmt.Fprintf(w, `{"messageId":%q,"status":"sent"}`, keys[key])
		case "/webhook":
			if r.Method == "PUT" {
				puts++
			}
			fmt.Fprint(w, `{"url":"https://other.example/hook","events":[],"hasSecret":true}`)
		case "/bad":
			w.WriteHeader(409)
			fmt.Fprint(w, `{"code":"ACCOUNT_OFFLINE","message":"local-test must never escape","requestId":"req-1"}`)
		default:
			http.Redirect(w, r, "https://other.example/credential", 302)
		}
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL, APIKey: "local-test"}
	for range 2 {
		m, e := c.Send(t.Context(), TextRequest("a", "c", "hello"), "same-key")
		if e != nil || m.MessageID != "msg_1" {
			t.Fatal(m, e)
		}
	}
	if sends != 1 {
		t.Fatal("duplicate physical send")
	}
	if _, e := c.ApplyWebhook(t.Context(), "https://new.example/hook", true); e == nil || puts != 0 {
		t.Fatal("overwrote another webhook", e)
	}
	if _, e := c.Do(t.Context(), "GET", "/bad", nil, ""); e == nil || strings.Contains(e.Error(), "local-test") {
		t.Fatal("error leaked prose/secret", e)
	}
	if _, e := c.Do(t.Context(), "GET", "/redirect", nil, ""); e == nil {
		t.Fatal("followed redirect")
	}
	if _, e := c.Send(context.Background(), TextRequest("a", "c", "x"), ""); e == nil {
		t.Fatal("send without idempotency key")
	}
}
func TestExistingWebhookSecretCannotBeSilentlyRotated(t *testing.T) {
	var puts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			puts++
		}
		fmt.Fprint(w, `{"url":"https://receiver.example/hook","events":[],"hasSecret":true}`)
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL, APIKey: "test"}
	if _, e := c.ApplyWebhook(t.Context(), "https://receiver.example/hook", false); e == nil || puts != 0 {
		t.Fatal("lost secret triggered automatic rotation")
	}
}
