package toolplugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestActivityCatalogAndCandidatesAreDurableJobs(t *testing.T) {
	dsn := os.Getenv("PW_PLUGIN_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	requests := 0
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/api/local/v3/work-packages":
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "week": "2026-W45", "packages": []any{map[string]any{"package_id": "published", "label": "source"}}})
		case "/api/local/v3/codex-copy-jobs":
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "run_id": "draft"})
		case "/api/local/copy/batch-runs":
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "run": map[string]any{"items": []any{map[string]any{"item_key": "usr_000000000001", "request": map[string]any{"safe_user_ref": "usr_000000000001"}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fixture.Close()
	g := Grant{Token: strings.Repeat("r", 40), ID: "read-grant", ProjectID: "read-project", Actions: []string{"activity_catalog", "activity_candidates", "lookup", "status"}}
	s, e := Open(Config{DSN: dsn, SourceBase: fixture.URL, Grants: []Grant{g}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	s.HTTP = fixture.Client()
	s.epoch = 1
	for _, table := range []string{"plugin_items", "plugin_jobs", "plugin_worker_epoch"} {
		s.DB.Exec("DELETE FROM " + table)
	}
	s.DB.Exec("INSERT INTO plugin_worker_epoch VALUES(1,1)")
	for _, action := range []string{"activity_catalog", "activity_candidates"} {
		args := map[string]any{"project_id": g.ProjectID, "session_id": "read-session", "invocation_key": "durable-" + action, "package_id": "published"}
		before := requests
		v, e := s.call(context.Background(), g, action, args, false)
		if e != nil {
			t.Fatal(e)
		}
		if requests != before {
			t.Fatal("slow original request ran inline")
		}
		_ = v
		j, e := scanJob(s.DB.QueryRow(jobSelect+" WHERE invocation_key=?", args["invocation_key"]))
		if e != nil || j.State != "queued" {
			t.Fatal(j, e)
		}
		s.execute(context.Background(), j)
		j, _ = scanJob(s.DB.QueryRow(jobSelect+" WHERE id=?", j.ID))
		if j.State != "succeeded" {
			t.Fatal(j.State, j.Error)
		}
		result, e := s.jobControl(context.Background(), g, "lookup", args)
		if e != nil || result.(map[string]any)["job_ref"] != j.ID {
			t.Fatal("read response cannot be reconciled", e)
		}
	}
}
func TestActivityCatalogRealSource(t *testing.T) {
	path := os.Getenv("PW_PLUGIN_SOURCE_CONFIG")
	dsn := os.Getenv("PW_PLUGIN_TEST_DSN")
	if path == "" || dsn == "" {
		t.Skip("explicit credentialed read-only source acceptance")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var cfg Config
	if e = json.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	cfg.DSN = dsn
	g := Grant{Token: strings.Repeat("q", 40), ID: "real-catalog-read", ProjectID: "catalog-read", Actions: []string{"activity_catalog", "lookup", "status"}}
	cfg.Grants = []Grant{g}
	s, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	s.epoch = 1
	for _, table := range []string{"plugin_items", "plugin_jobs", "plugin_worker_epoch"} {
		s.DB.Exec("DELETE FROM " + table)
	}
	s.DB.Exec("INSERT INTO plugin_worker_epoch VALUES(1,1)")
	args := map[string]any{"project_id": g.ProjectID, "session_id": "isolated-catalog", "invocation_key": "real-source-catalog"}
	if _, e = s.call(context.Background(), g, "activity_catalog", args, false); e != nil {
		t.Fatal(e)
	}
	j, e := scanJob(s.DB.QueryRow(jobSelect+" WHERE invocation_key=?", args["invocation_key"]))
	if e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	s.execute(context.Background(), j)
	j, _ = scanJob(s.DB.QueryRow(jobSelect+" WHERE id=?", j.ID))
	if j.State != "succeeded" {
		t.Fatal(j.State, j.Error)
	}
	var result map[string]any
	json.Unmarshal(j.Result, &result)
	rows, _ := result["packages"].([]any)
	if len(rows) == 0 {
		t.Fatal("no source package metadata")
	}
	t.Logf("real original package catalog read through durable job: packages=%d elapsed=%s; no generation or send", len(rows), time.Since(started))
}
