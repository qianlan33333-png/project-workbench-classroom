package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPSourceStreaming(t *testing.T) {
	spec := JSONSource{ArrayField: "records", KeyPointer: "/key", Fields: map[string]string{"name": "/display/name", "raw": ""}}
	body := `{"metadata":{"a":[1,2]},"records":[{"key":"a","display":{"name":"甲"}},{"key":"a ","display":{"name":"乙"}}],"more":false}`
	var records []Record
	e := streamJSON(strings.NewReader(body), spec, func(r Record) error { records = append(records, r); return nil })
	if e != nil || len(records) != 2 || records[1].ID != "a " || records[0].Fields["name"] != "甲" {
		t.Fatal(records, e)
	}
	for _, raw := range []string{`{"wrong":[]}`, `{"records":null}`, `{"records":[{"key":null}]}`, `{"records":[],"records":[]}`, `{"records":[]}{}`, `{"records":[{"key":"1"}]`} {
		if e := streamJSON(strings.NewReader(raw), spec, func(Record) error { return nil }); e == nil {
			t.Fatal("accepted malformed source", raw)
		}
	}
	stop := errors.New("revoked")
	if e := streamJSON(strings.NewReader(body), spec, func(Record) error { return stop }); !errors.Is(e, stop) {
		t.Fatal(e)
	}
}
func TestHTTPFormLoginNoRedirect(t *testing.T) {
	var target string
	posts := 0
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "test-owner" || password != "gateway-password" || r.Header.Get("X-Gateway") != "required" {
			t.Error("login omitted gateway credentials")
			w.WriteHeader(401)
			return
		}
		if r.Method == "GET" {
			http.SetCookie(w, &http.Cookie{Name: "csrf", Value: "token", Path: "/"})
			return
		}
		posts++
		r.ParseForm()
		if r.Form.Get("csrf") != "token" || r.Form.Get("password") != "test" {
			t.Error("missing standard form fields")
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "valid", Path: "/"})
		w.Header().Set("Location", other.URL)
		w.WriteHeader(303)
	}))
	defer srv.Close()
	target = srv.URL
	secret := HTTPSourceSecret{Username: "test-owner", Password: "gateway-password", Headers: map[string]string{"X-Gateway": "required"}}
	secret.Login = &struct {
		URL           string            `json:"url"`
		Fields        map[string]string `json:"fields"`
		CSRFCookie    string            `json:"csrfCookie"`
		CSRFField     string            `json:"csrfField"`
		SuccessStatus int               `json:"successStatus"`
	}{URL: target, Fields: map[string]string{"password": "test"}, CSRFCookie: "csrf", CSRFField: "csrf", SuccessStatus: 303}
	client, e := httpSourceClient(context.Background(), target+"/data", secret)
	if e != nil || client == nil || posts != 1 || leaked {
		t.Fatal(posts, leaked, e)
	}
	secret.Login.URL = other.URL
	if _, e = httpSourceClient(context.Background(), target, secret); e == nil {
		t.Fatal("cross origin login permitted")
	}
	if validHTTPSource("http://example.com/data") || validHTTPSource("https://name:password@example.com/data") || !validHTTPSource("https://example.com/data") {
		t.Fatal("bad URL validation")
	}
}

func TestMySQLHTTPSourceKeepsPublicationOnFailure(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	body := `{"items":[{"id":"a","value":"one"},{"id":"b","value":"two"},{"id":"c","value":"three"}]}`
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer source.Close()
	original := http.DefaultTransport
	http.DefaultTransport = source.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = original })
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	conn := Connection{ID: id + "conn", Name: "generic source", Kind: "http-json", Location: source.URL}
	must(s.PutConnection(ctx, owner, conn))
	resource := Resource{ID: id + "res", Name: "JSON snapshot", ConnectionID: conn.ID, MinRecords: 1, MinRetainedPercent: 80, JSONSource: &JSONSource{ArrayField: "items", KeyPointer: "/id", Fields: map[string]string{"text": "/value"}}, Sections: []Section{{ID: "main", Fields: []string{"text"}}}}
	must(s.PutResource(ctx, owner, resource))
	run := Run{ID: id + "first", Principal: owner, Operation: "sync", Title: "publication contract", Destination: resource.ID}
	snap, e := s.SyncSource(ctx, run, func() {})
	must(e)
	if snap.Count != 3 || len(snap.SourceWatermark) != 64 {
		t.Fatal(snap)
	}
	for i, bad := range []string{
		`{"items":[{"id":"a","value":"one"}]}`,
		`{"items":[{"id":"a","value":"one"},{"id":"a","value":"other"}]}`,
		`{"items":[{"id":"a","value":"one"}]`,
	} {
		body = bad
		run.ID = id + "bad" + string(rune('a'+i))
		if _, e = s.SyncSource(ctx, run, func() {}); e == nil {
			t.Fatal("bad source published", i)
		}
		current, e := s.Resource(ctx, resource.ID)
		must(e)
		if current.CurrentVersion != snap.Version {
			t.Fatal("publication replaced", current)
		}
	}
	// Retrying the same staging version with new bytes must not mix generations.
	body = `{"items":[{"id":"a","value":"one"},{"id":"b","value":"two"},{"id":"c","value":"three"}]}`
	if _, e = s.SyncSource(ctx, run, func() {}); !errors.Is(e, ErrConflict) {
		t.Fatal("changed retry did not stop", e)
	}
}

func TestHTTPSourceStopsBeforeOversizedObjectAllocation(t *testing.T) {
	spec := JSONSource{KeyPointer: "/id", Fields: map[string]string{"raw": ""}}
	reader := &countingSourceReader{Reader: strings.NewReader(`[{"id":"one","large":[` + strings.Repeat(`"x",`, 400000) + `"x"]}]`)}
	if e := streamJSON(reader, spec, func(Record) error { t.Fatal("oversized source was accepted"); return nil }); e == nil {
		t.Fatal("budget missing")
	}
	if reader.count > 600<<10 {
		t.Fatal("record fully read before rejection", reader.count)
	}
}

type countingSourceReader struct {
	*strings.Reader
	count int
}

func (r *countingSourceReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.count += n
	return n, e
}
