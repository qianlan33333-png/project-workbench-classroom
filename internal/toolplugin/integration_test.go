package toolplugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeIdentityAndFacts(t *testing.T) {
	row := map[string]any{"safe_user_ref": "usr_123456789abc", "name": " 示例 ", "paid": false, "amount": "12.00"}
	id, clean, e := normalize(row, "safe_user_ref")
	if e != nil || id != "usr_123456789abc" || clean["name"] != "示例" || row["name"] != " 示例 " || clean["paid"] != false || clean["amount"] != "12.00" {
		t.Fatal("identity/fact preservation failed")
	}
	if _, _, e = normalize(map[string]any{"name": "无身份"}, "safe_user_ref"); e == nil {
		t.Fatal("invented identity")
	}
}
func TestPluginMySQLPublicationRecoveryAnd2000Items(t *testing.T) {
	dsn := os.Getenv("PW_PLUGIN_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN required")
	}
	var mode atomic.Int64
	var preparations atomic.Int64
	var active atomic.Value
	active.Store("")
	var s *Server
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/data/canonical_users_live.json" {
			if mode.Load() == 1 {
				fmt.Fprint(w, `{"snapshot_id":"broken","users":[`)
				return
			}
			fmt.Fprint(w, `{"snapshot_id":"source-v1","users":[{"safe_user_ref":"usr_000000000001","name":" A ","paid":false},{"safe_user_ref":"usr_000000000002","name":"B"},{"safe_user_ref":"usr_000000000003","name":"C"}]}`)
			return
		}
		if r.URL.Path == "/api/local/copy/generate" {
			var a map[string]any
			json.NewDecoder(r.Body).Decode(&a)
			n := preparations.Add(1)
			if n == 750 {
				s.DB.Exec("UPDATE plugin_jobs SET cancel_requested=TRUE WHERE id=?", active.Load().(string))
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "automatic_send_allowed": false, "candidates": []any{map[string]any{"message_text": a["safe_user_ref"]}}, "request_id": a["request_id"]})
			return
		}
		http.NotFound(w, r)
	}))
	defer fixture.Close()
	g := Grant{Token: strings.Repeat("t", 40), ProjectID: "test-project", Actions: []string{"sync_contexts", "batch_generate", "status", "lookup", "cancel", "retry"}, Operations: []string{"generate"}}
	cfg := Config{GenerationConcurrency: 1, DSN: dsn, SourceBase: fixture.URL, Grants: []Grant{g}, Operations: map[string]Operation{"generate": {Method: "POST", Path: "/api/local/copy/generate", Effect: "prepare"}}}
	var e error
	s, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	s.HTTP = fixture.Client()
	for _, table := range []string{"plugin_items", "plugin_jobs", "context_state", "context_records", "plugin_worker_epoch"} {
		if _, e = s.DB.Exec("DELETE FROM " + table); e != nil {
			t.Fatal(e)
		}
	}
	s.epoch = 1
	s.DB.Exec("INSERT INTO plugin_worker_epoch VALUES(1,1)")
	enqueue := func(kind, key string, extra map[string]any) job {
		a := map[string]any{"project_id": g.ProjectID, "session_id": "test-session", "invocation_key": key}
		for k, v := range extra {
			a[k] = v
		}
		if _, e = s.enqueue(context.Background(), g, kind, a); e != nil {
			t.Fatal(e)
		}
		j, e := scanJob(s.DB.QueryRow(jobSelect+" WHERE invocation_key=?", key))
		if e != nil {
			t.Fatal(e)
		}
		return j
	}
	restricted := g
	restricted.Token = strings.Repeat("r", 40)
	restricted.Operations = nil
	s.Config.Grants = append(s.Config.Grants, restricted)
	if _, er := s.enqueue(context.Background(), restricted, "batch_generate", map[string]any{"invocation_key": "restricted-invocation", "project_id": g.ProjectID, "session_id": "test-session", "operation": "generate", "items": []any{"usr_000000000001"}}); er == nil {
		t.Fatal("narrow grant borrowed wider permission")
	}
	first := enqueue("sync_contexts", "sync-valid", nil)
	s.execute(context.Background(), first)
	var generation string
	var count int
	if e = s.DB.QueryRow("SELECT generation,record_count FROM context_state").Scan(&generation, &count); e != nil || count != 3 {
		t.Fatal("publication failed", e, count)
	}
	mode.Store(1)
	broken := enqueue("sync_contexts", "sync-broken", nil)
	s.execute(context.Background(), broken)
	var after string
	s.DB.QueryRow("SELECT generation FROM context_state").Scan(&after)
	if after != generation {
		t.Fatal("partial source replaced good publication")
	}
	items := []any{}
	for i := 0; i < 2000; i++ {
		items = append(items, fmt.Sprintf("usr_%012d", i))
	}
	batch := enqueue("batch_generate", "batch-2000", map[string]any{"operation": "generate", "items": items})
	active.Store(batch.ID)
	drain := func() {
		for i := 0; i < 150; i++ {
			s.execute(context.Background(), batch)
			var state string
			s.DB.QueryRow("SELECT state FROM plugin_jobs WHERE id=?", batch.ID).Scan(&state)
			if state != "queued" {
				return
			}
		}
		t.Fatal("batch slices did not settle")
	}
	drain()
	var state string
	s.DB.QueryRow("SELECT state FROM plugin_jobs WHERE id=?", batch.ID).Scan(&state)
	if state != "cancelled" || preparations.Load() != 750 {
		t.Fatal("mid-batch cancellation failed", state, preparations.Load())
	}
	_, e = s.jobControl(context.Background(), g, "retry", map[string]any{"session_id": "test-session", "job_ref": batch.ID})
	if e != nil {
		t.Fatal(e)
	}
	drain()
	var done int
	s.DB.QueryRow("SELECT count(*) FROM plugin_items WHERE job_id=? AND state='succeeded'", batch.ID).Scan(&done)
	if done != 2000 || preparations.Load() != 2000 {
		t.Fatal("resume duplicated or missed items", done, preparations.Load())
	}
	var incorrect int
	s.DB.QueryRow("SELECT count(*) FROM plugin_items WHERE job_id=? AND json_unquote(json_extract(result_json,'$.candidates[0].message_text'))<>item_key", batch.ID).Scan(&incorrect)
	if incorrect != 0 {
		t.Fatal("cross-item contamination", incorrect)
	}
	if _, e = s.batchResults(context.Background(), Grant{ProjectID: "other"}, map[string]any{"session_id": "test-session", "job_ref": batch.ID}); e == nil {
		t.Fatal("cross-project batch leak")
	}
	if _, er := s.batchResults(context.Background(), restricted, map[string]any{"session_id": "test-session", "job_ref": batch.ID}); er == nil {
		t.Fatal("same-project narrow grant read another grant result")
	}
	revoked := enqueue("sync_contexts", "revoked-action", nil)
	savedActions := s.Config.Grants[0].Actions
	s.Config.Grants[0].Actions = []string{"status"}
	s.execute(context.Background(), revoked)
	s.Config.Grants[0].Actions = savedActions
	if er := s.DB.QueryRow("SELECT state FROM plugin_jobs WHERE id=?", revoked.ID).Scan(&state); er != nil || state != "failed" {
		t.Fatal("revoked sync grant executed", state, er)
	}
	// A stale worker may not claim any queued job after the global epoch changes.
	stale := enqueue("sync_contexts", "stale-worker", nil)
	s.DB.Exec("UPDATE plugin_worker_epoch SET epoch=2 WHERE id=1")
	s.execute(context.Background(), stale)
	s.DB.QueryRow("SELECT state FROM plugin_jobs WHERE id=?", stale.ID).Scan(&state)
	if state != "queued" {
		t.Fatal("stale worker claimed a job")
	}
	t.Logf("MySQL publication, partial failure retention, cancellation/resume, 2000 isolated results and stale-worker fencing passed at %s", time.Now().UTC().Format(time.RFC3339))
}

func TestNullableBatchResultJSON(t *testing.T) {
	if got := encode(map[string]any{"result": nullableJSON(sql.NullString{})}); got != "{\"result\":null}" {
		t.Fatal("invalid null result", got)
	}
}

func TestReadActionRejectsGETWithSideEffects(t *testing.T) {
	s := &Server{Config: Config{Operations: map[string]Operation{"side": {Method: "GET", Path: "/api/local/copy/batch-runs", Effect: "execute", OwnerOnly: true}}}}
	g := Grant{Operations: []string{"side"}}
	if _, e := s.upstream(context.Background(), g, map[string]any{"operation": "side"}, false, false); e == nil {
		t.Fatal("GET side effect escaped read boundary")
	}
}
