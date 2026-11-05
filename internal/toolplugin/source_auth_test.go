package toolplugin

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSourceBrowserAuthenticationNoBusinessReplay(t *testing.T) {
	var logins, business atomic.Int64
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_workbench/login" {
			if r.Method == "GET" {
				http.SetCookie(w, &http.Cookie{Name: "__Host-daru_browser_csrf", Value: strings.Repeat("c", 43), Path: "/", Secure: true})
				return
			}
			r.ParseForm()
			if r.Form.Get("csrf") != strings.Repeat("c", 43) || r.Form.Get("password") != "test-password" || r.Header.Get("Origin") == "" {
				http.Error(w, "bad login", 403)
				return
			}
			logins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "__Host-daru_browser_session", Value: strings.Repeat("s", 43), Path: "/", Secure: true})
			w.WriteHeader(303)
			return
		}
		business.Add(1)
		w.WriteHeader(401)
	}))
	defer fixture.Close()
	client := fixture.Client()
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s := &Server{Config: Config{SourceBase: fixture.URL, SourceAuth: "browser_session", SourceUser: "test-owner", SourcePassword: "test-password"}, HTTP: client}
	if _, e := s.sourceRequest(context.Background(), "POST", "/api/local/action", strings.NewReader(`{}`)); e == nil {
		t.Fatal("rejected source was accepted")
	}
	if logins.Load() != 1 || business.Load() != 1 {
		t.Fatal("business POST replayed", logins.Load(), business.Load())
	}
	if !s.authUntil.IsZero() {
		t.Fatal("invalid session kept")
	}
}
