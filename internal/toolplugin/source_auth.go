package toolplugin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The existing source has a browser-session gateway in front of its API/static
// data. Authenticate once without following redirects or replaying business POSTs.
func (s *Server) authenticateSource(ctx context.Context) error {
	if s.Config.SourceAuth != "browser_session" {
		return nil
	}
	if s.HTTP.Jar == nil {
		return errors.New("source session cookie storage unavailable")
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if time.Now().Before(s.authUntil) {
		return nil
	}
	base := strings.TrimRight(s.Config.SourceBase, "/")
	req, e := http.NewRequestWithContext(ctx, "GET", base+"/_workbench/login", nil)
	if e != nil {
		return e
	}
	res, e := s.HTTP.Do(req)
	if e != nil {
		return errors.New("source login unavailable")
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("source login gateway unavailable")
	}
	u, _ := url.Parse(base)
	csrf := ""
	for _, c := range s.HTTP.Jar.Cookies(u) {
		if c.Name == "__Host-daru_browser_csrf" {
			csrf = c.Value
		}
	}
	if len(csrf) != 43 {
		return errors.New("source login CSRF unavailable")
	}
	form := url.Values{"username": {s.Config.SourceUser}, "password": {s.Config.SourcePassword}, "csrf": {csrf}, "next": {"/api/local/session"}}
	req, e = http.NewRequestWithContext(ctx, "POST", base+"/_workbench/login", strings.NewReader(form.Encode()))
	if e != nil {
		return e
	}
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, e = s.HTTP.Do(req)
	if e != nil {
		return errors.New("source login result unavailable")
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	if res.StatusCode != 303 {
		return errors.New("source credentials rejected by login gateway")
	}
	for _, c := range s.HTTP.Jar.Cookies(u) {
		if c.Name == "__Host-daru_browser_session" && len(c.Value) == 43 {
			s.authUntil = time.Now().Add(30 * time.Minute)
			return nil
		}
	}
	return errors.New("source login session missing")
}
