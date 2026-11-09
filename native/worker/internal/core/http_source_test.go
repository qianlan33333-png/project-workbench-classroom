package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
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

func TestHTTPSourceCompositeIdentity(t *testing.T) {
	spec := JSONSource{KeyPointers: []string{"/scope", "/key"}, Fields: map[string]string{"raw": ""}}
	read := func(body string) ([]Record, error) {
		var rows []Record
		err := streamJSON(strings.NewReader(body), spec, func(r Record) error { rows = append(rows, r); return nil })
		return rows, err
	}
	body := `[{"scope":"a|b","key":"c"},{"scope":"a","key":"b|c"},{"scope":"a","key":9007199254740993},{"scope":"a","key":"9007199254740993"},{"scope":"A","key":"b|c"}]`
	rows, err := read(body)
	if err != nil || len(rows) != 5 {
		t.Fatal(rows, err)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.ID] || len(row.ID) != 67 {
			t.Fatal("distinct source keys collided")
		}
		seen[row.ID] = true
	}
	again, err := read(`[{"key":"b|c","scope":"A"},{"key":9007199254740993,"scope":"a"}]`)
	if err != nil || len(again) != 2 || again[0].ID != rows[4].ID || again[1].ID != rows[2].ID {
		t.Fatal("identity changed with field or row order", err)
	}
	for _, body := range []string{`[{"scope":"a"}]`, `[{"scope":"a","key":null}]`, `[{"scope":"a","key":""}]`, `[{"scope":"a","key":true}]`, `[{"scope":"a","key":{}}]`} {
		if _, err := read(body); err == nil {
			t.Fatal("invalid key accepted", body)
		}
	}
	for _, keys := range [][]string{nil, {"/scope"}, {"/scope", "/scope"}, {"/scope", ""}, {"/scope", "missing-slash"}} {
		bad := spec
		bad.KeyPointers = keys
		if bad.validate() == nil {
			t.Fatal("invalid composite key configuration accepted", keys)
		}
	}
	spec.KeyPointer = "/key"
	if spec.validate() == nil {
		t.Fatal("both identity modes accepted")
	}
}

func TestHTTPSourceParticipantIdentityAndRepeatedRows(t *testing.T) {
	read := func(spec JSONSource, body string) []Record {
		t.Helper()
		var rows []Record
		if err := streamJSON(strings.NewReader(body), spec, func(r Record) error { rows = append(rows, r); return nil }); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	fields := map[string]string{"raw": ""}
	rows := read(JSONSource{KeyPointers: []string{"/message_id", "/customer_id", "/participant_role"}, Fields: fields}, `[{"message_id":"m","customer_id":1,"participant_role":"sender"},{"message_id":"m","customer_id":2,"participant_role":"recipient"},{"message_id":"m","customer_id":1,"participant_role":"recipient"}]`)
	if len(rows) != 3 || rows[0].ID == rows[1].ID || rows[0].ID == rows[2].ID || rows[1].ID == rows[2].ID {
		t.Fatal("message participants collapsed")
	}
	spec := JSONSource{RowKey: "content-occurrence", Fields: fields}
	first := read(spec, `[{"customer_id":1,"channel_code":"x","occurred_at":"same"},{"customer_id":1,"channel_code":"x","occurred_at":"same"},{"customer_id":2,"channel_code":"x","occurred_at":"same"}]`)
	second := read(spec, `[{"occurred_at":"same","channel_code":"x","customer_id":2},{"occurred_at":"same","channel_code":"x","customer_id":1},{"occurred_at":"same","channel_code":"x","customer_id":1}]`)
	ids := func(rows []Record) []string {
		out := []string{}
		for _, r := range rows {
			out = append(out, r.ID)
		}
		sort.Strings(out)
		return out
	}
	if len(first) != 3 || first[0].ID == first[1].ID || !reflect.DeepEqual(ids(first), ids(second)) {
		t.Fatal("receipt multiplicity or order independence lost")
	}
	for _, bad := range []JSONSource{
		{RowKey: "unknown", Fields: fields},
		{RowKey: "content-occurrence", KeyPointer: "/id", Fields: fields},
		{RowKey: "content-occurrence", KeyPointers: []string{"/a", "/b"}, Fields: fields},
	} {
		if bad.validate() == nil {
			t.Fatal("ambiguous record key accepted")
		}
	}
}

func TestMySQLContentSourceKeepsRepeatedReceipts(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	body := `[{"customer_id":1,"channel":"same","time":"same"},{"customer_id":1,"channel":"same","time":"same"}]`
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer source.Close()
	original := http.DefaultTransport
	http.DefaultTransport = source.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = original })
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	conn := Connection{ID: id + "conn", Name: "receipt source", Kind: "http-json", Location: source.URL}
	check(s.PutConnection(ctx, owner, conn))
	resource := Resource{ID: id + "res", Name: "raw repeated receipt projections", ConnectionID: conn.ID, JSONSource: &JSONSource{RowKey: "content-occurrence", Fields: map[string]string{"raw": ""}}, Sections: []Section{{ID: "facts", Fields: []string{"raw"}}}}
	check(s.PutResource(ctx, owner, resource))
	run := Run{ID: id + "first", Principal: owner, Destination: resource.ID}
	snap, err := s.SyncSource(ctx, run, func() {})
	check(err)
	page, err := s.Read(ctx, owner, Ref{ResourceID: resource.ID, Version: snap.Version, Section: "facts"}, "", 5)
	check(err)
	if snap.Count != 2 || len(page.Records) != 2 || page.Records[0].ID == page.Records[1].ID || !reflect.DeepEqual(page.Records[0].Fields, page.Records[1].Fields) {
		t.Fatal("identical receipt projections were lost")
	}
	// Successful original tasks keep their publication instead of re-reading.
	body = `[]`
	again, err := s.SyncSource(ctx, run, func() {})
	check(err)
	if again.Version != snap.Version || again.Count != 2 {
		t.Fatal("completed task changed")
	}
}

func TestMySQLCompositeSourceIdentityKeepsAllFacts(t *testing.T) {
	s, ctx, id := acceptanceStore(t)
	body := `[{"kind":"unionid","scope":"a","value":"same","status":"  active  "},{"kind":"unionid","scope":"b","value":"same","status":"active"}]`
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer source.Close()
	original := http.DefaultTransport
	http.DefaultTransport = source.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = original })
	owner := Principal{Kind: "owner", SessionID: id + "owner"}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	conn := Connection{ID: id + "conn", Name: "composite source", Kind: "http-json", Location: source.URL}
	check(s.PutConnection(ctx, owner, conn))
	resource := Resource{ID: id + "res", Name: "independent source facts", ConnectionID: conn.ID, MinRecords: 1,
		JSONSource: &JSONSource{KeyPointers: []string{"/kind", "/scope", "/value"}, Fields: map[string]string{"scope": "/scope", "status": "/status"}},
		TrimFields: []string{"status"}, Sections: []Section{{ID: "facts", Fields: []string{"scope", "status"}}}}
	check(s.PutResource(ctx, owner, resource))
	run := Run{ID: id + "first", Principal: owner, Destination: resource.ID}
	snap, err := s.SyncSource(ctx, run, func() {})
	check(err)
	page, err := s.Read(ctx, owner, Ref{ResourceID: resource.ID, Version: snap.Version, Section: "facts"}, "", 5)
	check(err)
	if snap.Count != 2 || len(page.Records) != 2 || page.Records[0].ID == page.Records[1].ID {
		t.Fatal("source scopes were collapsed")
	}
	for _, row := range page.Records {
		if row.Fields["status"] != "active" {
			t.Fatal("cleaning did not run")
		}
	}
	for i, bad := range []string{
		`[{"kind":"unionid","scope":"a","value":"same"},{"kind":"unionid","scope":"a","value":"same"}]`,
		`[{"kind":"unionid","value":"same"}]`,
	} {
		body = bad
		run.ID = id + "bad" + string(rune('a'+i))
		if _, err := s.SyncSource(ctx, run, func() {}); err == nil {
			t.Fatal("invalid identity source published")
		}
		current, err := s.Resource(ctx, resource.ID)
		check(err)
		if current.CurrentVersion != snap.Version {
			t.Fatal("failed refresh replaced published facts")
		}
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
