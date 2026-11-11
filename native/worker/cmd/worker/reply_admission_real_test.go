package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
	"workbench-native/internal/core"
)

// The Host capture plugin belongs to the existing isolated acceptance fixture.
// No production endpoint or provider send is used by this test.
func TestRealReplyAdmissionReleasesRejectedSessions(t *testing.T) {
	bridge, audit := os.Getenv("WORKBENCH_TEST_HOST"), os.Getenv("WORKBENCH_TEST_HOST_AUDIT")
	dsn, project := os.Getenv("WORKBENCH_TEST_DSN"), os.Getenv("WORKBENCH_TEST_PROJECT")
	if bridge == "" || audit == "" || project == "" || dsn == "" {
		t.Skip("isolated real Host not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") || !strings.HasPrefix(bridge, "http://127.0.0.1:") || !strings.HasPrefix(audit, "http://127.0.0.1:") {
		t.Fatal("isolated local Host required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	store, e := core.Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer store.DB.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	owner := core.Principal{Kind: "owner", SessionID: "admission-audit"}
	cfg, e := store.ReplySettings(ctx, owner, project)
	must(e)
	if cfg.ConnectionID != "fixture" {
		t.Fatal("synthetic fixture connection required")
	}
	transport, e := store.Transport(ctx, owner, cfg.ConnectionID)
	must(e)
	unavailable := transport
	unavailable.Endpoint = "http://127.0.0.1:1/mcp"
	must(store.PutTransport(ctx, owner, unavailable))
	defer func() { must(store.PutTransport(context.Background(), owner, transport)) }()

	request := func(url string, body any) ([]byte, int) {
		t.Helper()
		var reader io.Reader
		method := "GET"
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
			method = "POST"
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		must(err)
		req.Header.Set("Authorization", "Bearer "+os.Getenv("WORKBENCH_HOST_TOKEN"))
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		must(err)
		defer r.Body.Close()
		raw, err := io.ReadAll(r.Body)
		must(err)
		return raw, r.StatusCode
	}
	var ids []string
	for n := 0; n < 12; n++ {
		id := "backlog-" + uuid.NewString()
		in := core.IncomingMessage{ConnectionID: cfg.ConnectionID, ConversationID: id, ActorID: id, EventID: id + "-first", Text: "synthetic first"}
		route, e := store.EnsureReplyRoute(ctx, in)
		must(e)
		_, e = store.AcceptMessage(ctx, in)
		must(e)
		run, e := store.PrepareReply(ctx, route.Principal, cfg.ConnectionID, in.EventID, "unsent synthetic draft")
		must(e)
		// No workflow is started: this draft deliberately holds the FIFO.
		in.EventID = id + "-later"
		in.Text = "must remain queued"
		m, e := store.AcceptMessage(ctx, in)
		must(e)
		later := core.Run{ID: id + "-incoming", Principal: route.Principal, Operation: "incoming", Title: "queued fixture", OriginMessage: m.ID}
		must(store.SaveRun(ctx, later))
		_, status := request(bridge+"/dispatch", map[string]any{"id": later.ID, "principal": route.Principal, "operation": "incoming", "goal": in.Text})
		if status != 503 {
			t.Fatalf("queued message status %d", status)
		}
		raw, status := request(audit+"/state", nil)
		if status != 200 {
			t.Fatal("audit unavailable", status)
		}
		var states []struct {
			ID string `json:"id"`
		}
		must(json.Unmarshal(raw, &states))
		for _, s := range states {
			if s.ID == route.Principal.SessionID {
				t.Fatal("rejected Session remained resident", s.ID)
			}
		}
		ids = append(ids, route.Principal.SessionID)
		// End this fixture without sending; do not leave it for recovery sweeps.
		_, e = store.ChangeReplyMode(ctx, owner, route.ID, route.Revision, false)
		must(e)
		_, _, e = store.PrepareReplyDelivery(ctx, run.ID, core.Transport{}, false)
		must(e)
	}
	if path := os.Getenv("WORKBENCH_TEST_CAPTURE"); path != "" {
		raw, e := os.ReadFile(path)
		must(e)
		for _, id := range ids {
			if bytes.Contains(raw, []byte(fmt.Sprintf(`"sessionId":"%s"`, id))) {
				t.Fatal("denied admission called model", id)
			}
		}
	}
}
