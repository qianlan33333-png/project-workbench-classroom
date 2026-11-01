package workbench

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIndependentProjectsAndPersonalRunTogether(t *testing.T) {
	s := testStore(t)
	a, b, c := testProject(t, s, "A"), testProject(t, s, "B"), testProject(t, s, "C")
	personal, err := s.PersonalSpace()
	if err != nil {
		t.Fatal(err)
	}
	submit := func(p Project) string {
		return submitRoute(t, s, Incoming{Source: "web", SessionID: p.DefaultSessionID, Text: "处理", EventID: newID()}).RunID
	}
	a1, a2, b1, c1, personal1 := submit(a), submit(a), submit(b), submit(c), submit(personal)
	ex := &fakeExecutor{store: s, block: true}
	w := NewWorker(s, ex, nil)
	ctx, stop := context.WithCancel(context.Background())
	w.Start(ctx)
	defer func() { stop(); w.Wait() }()
	state := func(id string) string {
		r, err := s.Run(id)
		if err != nil {
			t.Fatal(err)
		}
		return r.State
	}
	waitFor(t, func() bool { return state(a1) == "running" && state(b1) == "running" && state(personal1) == "running" })
	if state(a2) != "queued" || state(c1) != "queued" {
		t.Fatal("project lease or project cap bypassed")
	}
	if err := w.Stop(b1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return state(c1) == "running" && state(b1) == "cancelled" })
	if state(a2) != "queued" || state(a1) != "running" || state(personal1) != "running" {
		t.Fatal("stop affected wrong run")
	}
	if err := w.Stop(a1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return state(a2) == "running" && state(a1) == "cancelled" })
}

func TestVisualBrowserBindsToOneRun(t *testing.T) {
	s := testStore(t)
	backend := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
				t.Error("credentials forwarded")
			}
			_, _ = w.Write([]byte(name + r.URL.Path))
		}))
	}
	a, b := backend("A"), backend("B")
	defer a.Close()
	defer b.Close()
	s.browsers.Store("first", browserEndpoint{strings.TrimPrefix(a.URL, "http://"), "项目A"})
	s.browsers.Store("second", browserEndpoint{strings.TrimPrefix(b.URL, "http://"), "项目B"})
	g := gin.New()
	g.GET("/api/v1/browser/*path", (&API{Store: s}).projectBrowser)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := httptest.NewRequestWithContext(ctx, "GET", "/api/v1/browser/"+path, nil)
		r.Header.Set("Authorization", "private")
		g.ServeHTTP(w, r)
		return w
	}
	menu := get("vnc.html")
	if menu.Code != 200 || !strings.Contains(menu.Body.String(), "项目A") || !strings.Contains(menu.Body.String(), "项目B") {
		t.Fatal(menu.Body.String())
	}
	if r := get("run/first/file"); r.Body.String() != "A/file" {
		t.Fatal(r.Body.String())
	}
	if r := get("run/second/file"); r.Body.String() != "B/file" {
		t.Fatal(r.Body.String())
	}
	s.browsers.Delete("first")
	if r := get("run/first/websockify"); r.Code != 410 {
		t.Fatal("expired link reached another project")
	}
	if r := get("vnc.html"); r.Code != 307 || !strings.Contains(r.Header().Get("Location"), "/run/second/vnc.html") {
		t.Fatal("single browser entry unavailable")
	}
}
