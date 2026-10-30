package workbench

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHTTPBinaryAndResumableEvents(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "HTTP验收")
	original := []byte{0x89, 'P', 'N', 'G', 0, 0xff}
	if _, e := s.SaveFile(p.ID, "asset.png", original, ""); e != nil {
		t.Fatal(e)
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	shutdown := make(chan struct{})
	hash, _ := PasswordHash("test-http-password")
	(&API{Store: s, Config: Config{PasswordHash: hash}, Shutdown: shutdown}).Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	res, e := http.Get(srv.URL + "/api/v1/projects")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("owner endpoint exposed", res.StatusCode)
	}
	res, e = http.Post(srv.URL+"/api/v1/login", "application/json", strings.NewReader(`{"password":"test-http-password"}`))
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	cookies := res.Cookies()
	get := func(path string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		v, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	res = get("/api/v1/projects/" + p.ID + "/file?path=asset.png")
	var file map[string]any
	json.NewDecoder(res.Body).Decode(&file)
	res.Body.Close()
	if file["binary"] != true || file["content"] != "" {
		t.Fatal("binary exposed as editable text", file)
	}
	res = get("/api/v1/projects/" + p.ID + "/file?path=asset.png&raw=1")
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != string(original) {
		t.Fatal("binary roundtrip changed")
	}
	s.Emit("", "", "test.first", map[string]string{"v": "first"})
	events, _ := s.Events(0, "")
	seq := events[len(events)-1].Seq
	s.Emit("", "", "test.second", map[string]string{"v": "second"})
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/events", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	req.Header.Set("Last-Event-ID", strconv.FormatInt(seq, 10))
	res, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	close(shutdown)
	done := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(res.Body); res.Body.Close(); done <- b }()
	select {
	case b := <-done:
		if strings.Contains(string(b), "test.first") || !strings.Contains(string(b), "test.second") {
			t.Fatal(string(b))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSE did not close on service shutdown")
	}
}
