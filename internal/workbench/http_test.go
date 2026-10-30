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

func TestWebMessageReceiptAndRunCorrelation(t *testing.T) {
	s := testStore(t)
	project := testProject(t, s, "发送反馈验收")
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	hash, _ := PasswordHash("receipt-test-password")
	(&API{Store: s, Config: Config{PasswordHash: hash}}).Mount(router)
	server := httptest.NewServer(router)
	defer server.Close()
	call := func(method, path, body string, auth bool, status int) []byte {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+"/api/v1"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.SetBasicAuth("owner", "receipt-test-password")
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != status {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, b)
		}
		return b
	}
	body := encode(Incoming{EventID: "receipt-one", SessionID: project.DefaultSessionID, Text: "只执行一次"})
	var message Message
	json.Unmarshal(call("POST", "/messages", body, true, 200), &message)
	if message.State != "unrouted" || message.RunID != "" {
		t.Fatal("receipt must not pretend execution", message)
	}
	call("GET", "/messages/by-event/receipt-one", "", false, 401)
	var receipt Message
	json.Unmarshal(call("GET", "/messages/by-event/receipt-one", "", true, 200), &receipt)
	if receipt.ID != message.ID || receipt.Incoming.EventID != "receipt-one" {
		t.Fatal(receipt)
	}
	routed, err := s.Route(t.Context(), message.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.Run(routed.RunID)
	if run.EventID != "receipt-one" || run.MessageID != message.ID {
		t.Fatal("missing client correlation", run)
	}
	json.Unmarshal(call("POST", "/messages", body, true, 200), &receipt)
	if receipt.RunID != run.ID {
		t.Fatal("same request created another run", receipt)
	}
	rows, _ := s.Runs(project.DefaultSessionID)
	if len(rows) != 1 {
		t.Fatal("duplicate execution")
	}
	_, err = s.Submit(Incoming{Source: "automation", ConnectionID: "other", EventID: "non-web", Text: "隔离回执"})
	if err != nil {
		t.Fatal(err)
	}
	call("GET", "/messages/by-event/non-web", "", true, 404)
	events, _ := s.Events(0, "")
	found := false
	for _, event := range events {
		if event.Type == "message.received" {
			var fields map[string]string
			json.Unmarshal(event.Data, &fields)
			if fields["id"] == message.ID && fields["eventId"] == "receipt-one" && fields["sessionId"] == project.DefaultSessionID {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("SSE receipt cannot reconcile pending input")
	}
}
