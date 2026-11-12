package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"workbench-native/internal/activation"
	"workbench-native/internal/core"
)

func TestMySQLActivationCodeHasOneWinnerAndCanBeRetried(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("real MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx := context.Background()
	store, err := core.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{Store: store, Token: strings.Repeat("a", 32)}).Handler()
	request := func(path, kind, session string, input any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(input)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
		req.Header.Set("X-Workbench-Kind", kind)
		req.Header.Set("X-Workbench-Session", session)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	if status := request("/activation/codes", "personal", "activation-personal", nil).Code; status != 403 {
		t.Fatalf("non-owner issue status=%d", status)
	}
	issued := request("/activation/codes", "owner", "activation-fixture", nil)
	var code struct{ Code string }
	if issued.Code != 200 || json.Unmarshal(issued.Body.Bytes(), &code) != nil || !activation.CodePattern.MatchString(code.Code) {
		t.Fatalf("invalid issuance status=%d", issued.Code)
	}
	digest := sha256.Sum256([]byte(code.Code))
	defer store.DB.ExecContext(ctx, "DELETE FROM wb_activation_codes WHERE digest=?", digest[:])
	type claim struct {
		instance string
		response *httptest.ResponseRecorder
	}
	done := make(chan claim, 2)
	redeem := func(instance string) *httptest.ResponseRecorder {
		return request("/activation/redeem", "owner", "activation-fixture", map[string]string{"code": code.Code, "installation": instance})
	}
	for range 2 {
		instance := uuid.NewString()
		go func() { done <- claim{instance, redeem(instance)} }()
	}
	first, second := <-done, <-done
	if first.response.Code == 409 {
		first, second = second, first
	}
	if first.response.Code != 200 || second.response.Code != 409 {
		t.Fatalf("redeem statuses=%d/%d", first.response.Code, second.response.Code)
	}
	if retry := redeem(first.instance); retry.Code != 200 || retry.Body.String() != first.response.Body.String() {
		t.Fatal("winning installation did not receive the original issue time")
	}
	expiredCode := strings.ReplaceAll(uuid.NewString(), "-", "")
	expiredDigest := sha256.Sum256([]byte(expiredCode))
	if _, err := store.DB.ExecContext(ctx, "INSERT INTO wb_activation_codes(digest, expires_at) VALUES (?, UTC_TIMESTAMP()-INTERVAL 1 DAY)", expiredDigest[:]); err != nil {
		t.Fatal(err)
	}
	defer store.DB.ExecContext(ctx, "DELETE FROM wb_activation_codes WHERE digest=?", expiredDigest[:])
	if response := request("/activation/redeem", "owner", "activation-fixture", map[string]string{"code": expiredCode, "installation": uuid.NewString()}); response.Code != 410 {
		t.Fatalf("expired code status=%d", response.Code)
	}
}
