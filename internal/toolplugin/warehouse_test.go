package toolplugin

import (
	"context"
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

func TestContextPublicationSettlementAndRecovery(t *testing.T) {
	dsn := os.Getenv("PW_PLUGIN_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN required")
	}
	var reads atomic.Int64
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"snapshot_id":"source-%d","users":[{"safe_user_ref":"test_user"}]}`, reads.Add(1))
	}))
	defer fixture.Close()
	g := Grant{Token: strings.Repeat("s", 40), ProjectID: "sync-test", Actions: []string{"sync_contexts", "status"}}
	s, err := Open(Config{DSN: dsn, SourceBase: fixture.URL, Grants: []Grant{g}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	s.HTTP = fixture.Client()
	ctx := context.Background()
	runSQL := func(query string, args ...any) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	runSQL("DROP TABLE IF EXISTS test_context_record_pin")
	defer s.DB.Exec("DROP TABLE IF EXISTS test_context_record_pin")
	for _, table := range []string{"plugin_activities", "plugin_items", "plugin_jobs", "context_aliases", "context_records", "context_state", "plugin_worker_epoch"} {
		runSQL("DELETE FROM " + table)
	}
	if err = s.recoverInterruptedJobs(ctx); err != nil {
		t.Fatal(err)
	}
	enqueueSync := func(key string) job {
		t.Helper()
		_, err := s.enqueue(ctx, g, "sync_contexts", map[string]any{"session_id": "sync-session", "invocation_key": key})
		if err != nil {
			t.Fatal(err)
		}
		j, err := scanJob(s.DB.QueryRowContext(ctx, jobSelect+" WHERE invocation_key=?", key))
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	runSync := func(key string) string {
		t.Helper()
		j := enqueueSync(key)
		s.execute(ctx, j)
		return j.ID
	}
	assertReceipt := func(id, state string) map[string]any {
		t.Helper()
		out, err := s.jobControl(ctx, g, "status", map[string]any{"session_id": "sync-session", "job_ref": id})
		if err != nil {
			t.Fatal(err)
		}
		receipt := out.(map[string]any)
		if receipt["status"] != state {
			t.Fatalf("job %s: got %v; want %s", id, receipt["status"], state)
		}
		var result map[string]any
		if state == "succeeded" {
			raw, ok := receipt["result"].(json.RawMessage)
			if !ok || json.Unmarshal(raw, &result) != nil || result["generation"] != id || result["users"] != float64(1) || result["published"] != true {
				t.Fatalf("job %s has invalid publication receipt: %s", id, raw)
			}
		}
		return result
	}
	publication := func(expected, previous string) int64 {
		t.Helper()
		var current, old string
		var at int64
		if err := s.DB.QueryRowContext(ctx, "SELECT generation,previous_generation,published_at FROM context_state WHERE dataset='users'").Scan(&current, &old, &at); err != nil || current != expected || old != previous {
			t.Fatalf("publication = %s/%s, err=%v; want %s/%s", current, old, err, expected, previous)
		}
		return at
	}
	recordCount := func(id string) int {
		t.Helper()
		var n int
		if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM context_records WHERE generation=?", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	first := runSync("first")
	at := publication(first, "")
	assertReceipt(first, "succeeded")

	// Inject a failure after the pointer write, while persisting the receipt.
	runSQL(`ALTER TABLE plugin_jobs ADD CONSTRAINT test_refuse_context_receipt
		CHECK (state<>'succeeded' OR invocation_key<>'refused')`)
	defer s.DB.Exec("ALTER TABLE plugin_jobs DROP CHECK test_refuse_context_receipt")
	refused := runSync("refused")
	assertReceipt(refused, "failed")
	if publication(first, "") != at || recordCount(refused) != 1 {
		t.Fatal("receipt failure must preserve the publication and leave only staging data")
	}

	// Inject a failure in old-generation housekeeping, after publication.
	runSQL(`CREATE TABLE test_context_record_pin(generation VARCHAR(64) NOT NULL,user_ref VARCHAR(160) NOT NULL,
		FOREIGN KEY (generation,user_ref) REFERENCES context_records(generation,user_ref)) ENGINE=InnoDB`)
	runSQL("INSERT INTO test_context_record_pin VALUES(?,?)", refused, "test_user")
	current := runSync("prune-fails")
	publishedAt := publication(current, first)
	if assertReceipt(current, "succeeded")["published_at"] != float64(publishedAt) || recordCount(refused) != 1 {
		t.Fatal("prune failure must leave a usable publication receipt")
	}
	runSQL("DROP TABLE test_context_record_pin")

	// Emulate the old split commit and an unrelated, unpublished interruption.
	stopped := enqueueSync("interrupted-cancel")
	runSQL("UPDATE plugin_jobs SET state='running',result_json=NULL WHERE id IN (?,?,?)", current, stopped.ID, refused)
	runSQL("UPDATE plugin_jobs SET cancel_requested=TRUE WHERE id IN (?,?)", current, stopped.ID)
	runSQL("UPDATE plugin_jobs SET state='failed',result_json=NULL WHERE id=?", first)
	readCount := reads.Load()
	if err = s.recoverInterruptedJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if assertReceipt(current, "succeeded")["published_at"] != float64(publishedAt) || publication(current, first) != publishedAt || reads.Load() != readCount {
		t.Fatal("recovery must not re-fetch or change the existing publication")
	}
	assertReceipt(first, "succeeded")
	assertReceipt(refused, "queued")
	assertReceipt(stopped.ID, "cancelled")

	// Successful housekeeping still removes abandoned data and protects the
	// older snapshot while an unfinished activity is using it.
	runSQL("UPDATE plugin_jobs SET state='failed' WHERE id=?", refused)
	runSQL("INSERT INTO plugin_activities(id,project_id,session_id,grant_id,generation,state,data,updated_at) VALUES('activity','sync-test','sync-session','grant',?,'awaiting_review','{}',?)", first, time.Now().UnixMilli())
	last := runSync("prune-succeeds")
	lastAt := publication(last, current)
	assertReceipt(last, "succeeded")
	if recordCount(last) != 1 || recordCount(current) != 1 || recordCount(first) != 1 || recordCount(refused) != 0 {
		t.Fatal("prune removed protected data or did not remove abandoned staging")
	}

	// An older generation pinned by an unfinished activity cannot be rebuilt
	// even if a legacy failure allowed the operator to retry its job.
	runSQL("UPDATE plugin_jobs SET state='queued',result_json=NULL WHERE id=?", first)
	readCount = reads.Load()
	s.execute(ctx, job{ID: first, Kind: "sync_contexts", Request: map[string]any{"__grant_id": grantIdentity(g)}})
	if reads.Load() != readCount || recordCount(first) != 1 || publication(last, current) != lastAt {
		t.Fatal("retry replaced a pinned generation")
	}
	assertReceipt(first, "failed")
}
