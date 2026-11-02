package wecomgateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

type Event struct {
	EventID    string          `json:"eventId"`
	Type       string          `json:"type"`
	TenantID   string          `json:"tenantId"`
	AccountID  string          `json:"accountId"`
	OccurredAt time.Time       `json:"occurredAt"`
	Data       json.RawMessage `json:"data"`
}

func Verify(secret string, raw []byte, signature, headerID string, at time.Time) (Event, error) {
	var event Event
	if secret == "" || len(raw) > 1<<20 {
		return event, errors.New("invalid webhook")
	}
	parts := map[string]string{}
	for _, p := range strings.Split(signature, ",") {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) != 2 || parts[kv[0]] != "" {
			return event, errors.New("invalid signature header")
		}
		parts[kv[0]] = kv[1]
	}
	t, e := strconv.ParseInt(parts["t"], 10, 64)
	if e != nil || at.Unix()-t > 300 || t-at.Unix() > 300 {
		return event, errors.New("signature timestamp expired")
	}
	expected := hmac.New(sha256.New, []byte(secret))
	expected.Write([]byte(parts["t"] + "."))
	expected.Write(raw)
	got, e := hex.DecodeString(parts["v1"])
	if e != nil || !hmac.Equal(got, expected.Sum(nil)) {
		return event, errors.New("signature rejected")
	}
	if e = json.Unmarshal(raw, &event); e != nil || event.EventID == "" || event.EventID != headerID || event.TenantID == "" || event.AccountID == "" || event.OccurredAt.IsZero() {
		return Event{}, errors.New("invalid event envelope")
	}
	return event, nil
}
