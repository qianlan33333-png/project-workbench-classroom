package main

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAdminGuardsPageAndIssueRequests(t *testing.T) {
	origin, _ := url.Parse("https://activation.example.test")
	password := sha256.Sum256([]byte("fixture-owner"))
	handler := (&issuer{
		origin: origin, adminPassword: password[:],
		adminPage: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" ||
				r.Header.Get(codeHeader) != "" || r.Header.Get(expiryHeader) != "" {
				t.Error("untrusted headers reached the page renderer")
			}
			w.WriteHeader(http.StatusNoContent)
		}),
	}).routes()
	for _, tt := range []struct {
		name, method, path, host, origin string
		auth                             bool
		status                           int
	}{
		{"anonymous admin", "GET", adminPath, origin.Host, "", false, 401},
		{"authenticated page", "GET", adminPath, origin.Host, "", true, 204},
		{"other host", "GET", adminPath, "other.example.test", "", true, 400},
		{"foreign lookup", "POST", adminPath, origin.Host, "https://other.example.test", true, 403},
		{"foreign issuance", "POST", adminPath + "issue", origin.Host, "", true, 403},
		{"authenticated lookup", "POST", adminPath, origin.Host, origin.String(), true, 204},
		{"malformed issuance", "POST", adminPath + "issue", origin.Host, origin.String(), true, 400},
		{"unexpected post", "POST", adminPath + "other", origin.Host, origin.String(), true, 405},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "https://"+tt.host+tt.path, strings.NewReader("request=invalid"))
			req.Header.Set("Origin", tt.origin)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Cookie", "harness=fixture-session")
			req.Header.Set(codeHeader, "untrusted")
			req.Header.Set(expiryHeader, "untrusted")
			if tt.auth {
				req.SetBasicAuth("admin", "fixture-owner")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tt.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
			}
		})
	}
}
