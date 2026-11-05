package toolplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestActivityMySQL2000FrozenFinalCopyAndReceipts(t *testing.T) {
	runActivityWorkflow(t, 2000, false, false)
}
func TestActivityRealProviderFromMySQL(t *testing.T) {
	if os.Getenv("PW_PLUGIN_PROVIDER_CONFIG") == "" {
		t.Skip("explicit credentialed provider acceptance")
	}
	runActivityWorkflow(t, 2, true, false)
}

type activityTestTransport func(*http.Request) (*http.Response, error)

func (f activityTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestActivityDefiniteRejectionCanRepairWithoutBlockingOthers(t *testing.T) {
	runActivityWorkflow(t, 3, false, true)
}
func runActivityWorkflow(t *testing.T, size int, real, rejectOnce bool) {
	rejectPending := rejectOnce
	dsn := os.Getenv("PW_PLUGIN_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	ctx := context.Background()
	var mu sync.Mutex
	refs := []any{}
	draft := map[string]any{}
	outputs := map[string]string{}
	modelCounts := map[string]int{}
	approvals := 0
	receiptState := "mobile_pending"
	write := func(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var q map[string]any
		if r.Method == "POST" {
			json.NewDecoder(r.Body).Decode(&q)
		}
		switch r.URL.Path {
		case "/model":
			msgs := q["messages"].([]any)
			var input map[string]any
			json.Unmarshal([]byte(str(msgs[1].(map[string]any), "content")), &input)
			ref := str(input, "safe_user_ref")
			if str(object(input, "context"), "mysql_only_fact") != "MYSQL:"+ref {
				t.Error("model did not read new MySQL", ref)
			}
			modelCounts[ref]++
			write(w, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "MODEL-RAW:" + ref}}}})
		case "/api/local/v3/codex-copy-jobs":
			switch str(q, "action") {
			case "prepare":
				refs = q["selected_user_refs"].([]any)
				write(w, map[string]any{"ok": true, "run_id": "source-template"})
			case "export":
				jobs := []any{}
				for i := number(q, "offset", 0); i < min(number(q, "offset", 0)+20, len(refs)); i++ {
					ref := refs[i].(string)
					if i == 0 && !real {
						outputs[ref] = "FIXED:" + ref
						continue
					}
					jobs = append(jobs, map[string]any{"item_key": ref, "prompt": "Original validated method", "prompt_sha256": strings.Repeat("a", 64), "request_hash": strings.Repeat("b", 64)})
				}
				write(w, map[string]any{"ok": true, "jobs": jobs})
			case "import":
				ref := str(q, "item_key")
				if rejectPending && ref == refs[1].(string) {
					rejectPending = false
					w.WriteHeader(409)
					write(w, map[string]any{"ok": false, "error": "external_job_dependency_changed"})
					return
				}
				outputs[ref] = "FINAL-REPAIRED:" + str(q, "output")
				write(w, map[string]any{"ok": true, "status": "success"})
			case "formal_status":
				write(w, map[string]any{"ok": true, "pending": false})
			}
		case "/api/local/copy/batch-runs":
			if r.Method == "POST" {
				if str(q, "action") == "create_or_resume" {
					draft = object(q, "config")
				}
				write(w, map[string]any{"ok": true, "run": map[string]any{"run_id": "source-own", "config": draft}})
				return
			}
			items := []any{}
			for i, v := range refs {
				ref := v.(string)
				row := map[string]any{"item_key": ref, "request": map[string]any{"safe_user_ref": ref}}
				if i == 0 && !real && outputs[ref] != "" {
					row["status"] = "success"
					row["response"] = map[string]any{"generated_by": "owner_approved_fixed_template"}
				}
				items = append(items, row)
			}
			write(w, map[string]any{"ok": true, "run": map[string]any{"config": map[string]any{}, "items": items}})
		case "/api/local/v3/copy-package":
			if r.URL.Query().Get("status_only") == "1" {
				sent := 0
				if receiptState == "sent" {
					sent = len(refs)
				}
				write(w, map[string]any{"ok": true, "delivery_summary": map[string]any{"version": "original-review-delivery-status.v1", "run_id": "source-own", "digest": "source-digest", "status": receiptState, "requested_count": len(refs), "sent_count": sent, "failed_count": 0}})
				return
			}
			items := []any{}
			for _, v := range refs {
				ref := v.(string)
				items = append(items, map[string]any{"item_key": ref, "safe_user_ref": ref, "text": outputs[ref], "attachments": []any{map[string]any{"type": "image", "sha256": "fixed-image"}}, "usable": true, "reviewable": true})
			}
			pkg := map[string]any{"digest": "source-digest", "items": items}
			if approvals > 0 {
				pkg["review"] = map[string]any{"decision": "approved", "digest": "source-digest"}
				pkg["handoff"] = map[string]any{"id": "handoff-one"}
			}
			write(w, map[string]any{"ok": true, "package": pkg})
		case "/api/local/v3/copy-package-review":
			approvals++
			write(w, map[string]any{"ok": true, "pending": true, "job_id": "formal-one"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fixture.Close()
	g := Grant{Token: strings.Repeat("a", 40), ID: "activity-grant", ProjectID: "activity-project", Actions: []string{"activity_generate", "activity_dispatch", "status", "cancel", "retry"}}
	receiptCode := `import json,sys;q=json.load(sys.stdin);print(json.dumps({"version":"workbench-item-receipts.v1","run_id":q["run_id"],"digest":q["digest"],"state":"verified","items":{"usr_%%012d"%%i:{"state":"sent"} for i in range(%d)}}))`
	receiptCode = fmt.Sprintf(receiptCode, size)
	model := Model{Endpoint: fixture.URL + "/model", Name: "fictional-model", Key: "fictional-key"}
	if real {
		var cfg Config
		raw, er := os.ReadFile(os.Getenv("PW_PLUGIN_PROVIDER_CONFIG"))
		if er != nil {
			t.Fatal(er)
		}
		if er = json.Unmarshal(raw, &cfg); er != nil {
			t.Fatal(er)
		}
		model = cfg.Model
	}
	s, e := Open(Config{ReceiptCommand: []string{"python3", "-c", receiptCode}, DSN: dsn, SourceBase: fixture.URL, Grants: []Grant{g}, Model: model})
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	s.HTTP = fixture.Client()
	if real {
		fixtureTransport := s.HTTP.Transport
		s.HTTP.Transport = activityTestTransport(func(r *http.Request) (*http.Response, error) {
			if strings.HasPrefix(r.URL.String(), fixture.URL) {
				return fixtureTransport.RoundTrip(r)
			}
			raw, er := io.ReadAll(r.Body)
			if er != nil {
				return nil, er
			}
			r.Body = io.NopCloser(strings.NewReader(string(raw)))
			var q map[string]any
			json.Unmarshal(raw, &q)
			msgs := q["messages"].([]any)
			var input map[string]any
			json.Unmarshal([]byte(str(msgs[1].(map[string]any), "content")), &input)
			ref := str(input, "safe_user_ref")
			if str(object(input, "context"), "mysql_only_fact") != "MYSQL:"+ref {
				t.Error("real model did not get pinned MySQL facts")
			}
			mu.Lock()
			modelCounts[ref]++
			mu.Unlock()
			return http.DefaultTransport.RoundTrip(r)
		})
	}
	s.epoch = 1
	for _, table := range []string{"plugin_activities", "plugin_items", "plugin_jobs", "context_aliases", "context_records", "context_state", "plugin_worker_epoch"} {
		if _, e = s.DB.Exec("DELETE FROM " + table); e != nil {
			t.Fatal(e)
		}
	}
	s.DB.Exec("INSERT INTO plugin_worker_epoch VALUES(1,1)")
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < size; i++ {
		ref := fmt.Sprintf("usr_%012d", i)
		raw := encode(map[string]any{"safe_user_ref": ref, "mysql_only_fact": "MYSQL:" + ref})
		if _, e = tx.Exec("INSERT INTO context_records VALUES('new-mysql',?,?,?,?)", ref, hash(raw), raw, raw); e != nil {
			t.Fatal(e)
		}
		refs = append(refs, ref)
	}
	tx.Exec("INSERT INTO context_state VALUES('users','new-mysql','',?,'{}',1)", size)
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	enqueue := func(kind, key string, extra map[string]any) job {
		t.Helper()
		q := map[string]any{"project_id": g.ProjectID, "session_id": "one-activity-session", "invocation_key": key}
		for k, v := range extra {
			q[k] = v
		}
		if _, e = s.enqueue(ctx, g, kind, q); e != nil {
			t.Fatal(e)
		}
		j, er := scanJob(s.DB.QueryRow(jobSelect+" WHERE invocation_key=?", key))
		if er != nil {
			t.Fatal(er)
		}
		return j
	}
	gen := enqueue("activity_generate", "generate-2000", map[string]any{"name": "合成活动", "package_id": "published-package", "items": refs, "instruction": "仅处理当前用户。输出context.mysql_only_fact的原值，最多一行，不编造其他内容。"})
	for n := 0; n < 110; n++ {
		s.execute(ctx, gen)
		j, _ := scanJob(s.DB.QueryRow(jobSelect+" WHERE id=?", gen.ID))
		if j.State == "succeeded" {
			break
		}
		if j.State == "failed" && rejectOnce {
			break
		}
		if j.State != "queued" {
			t.Fatalf("generation %s %s", j.State, j.Error)
		}
	}
	j, _ := scanJob(s.DB.QueryRow(jobSelect+" WHERE id=?", gen.ID))
	if rejectOnce {
		if j.State != "failed" {
			t.Fatal("definite rejection did not settle", j.State)
		}
		var succeeded int
		s.DB.QueryRow("SELECT count(*) FROM plugin_items WHERE job_id=? AND state='succeeded'", gen.ID).Scan(&succeeded)
		if succeeded != size-1 {
			t.Fatal("rejected item starved subsequent users", succeeded)
		}
		if _, e = s.jobControl(ctx, g, "retry", map[string]any{"session_id": gen.Session, "task_ref": gen.ID}); e != nil {
			t.Fatal(e)
		}
		s.execute(ctx, gen)
		j, _ = scanJob(s.DB.QueryRow(jobSelect+" WHERE id=?", gen.ID))
	}
	if j.State != "succeeded" {
		t.Fatal(j.State, j.Error)
	}
	a, e := s.activity(ctx, g, map[string]any{"task_ref": gen.ID, "session_id": gen.Session})
	if e != nil {
		t.Fatal(e)
	}
	raw := encode(a.Data["samples"])
	if !strings.Contains(raw, "FINAL-REPAIRED:") || !strings.Contains(raw, "fixed-image") {
		t.Fatal("review did not freeze actual final text and attachments")
	}
	mu.Lock()
	expected := size - 1
	if real {
		expected = size
	}
	if len(modelCounts) != expected {
		t.Error("fixed template was generated", len(modelCounts))
	}
	for ref, n := range modelCounts {
		want := 1
		if rejectOnce && ref == refs[1].(string) {
			want = 2
		}
		if n != want {
			t.Error("duplicate model call", ref, n)
		}
	}
	mu.Unlock()
	dispatch := enqueue("activity_dispatch", "dispatch-exact", map[string]any{"task_ref": a.ID, "digest": a.Digest, "approval_event": "owner-inbox-event"})
	s.execute(ctx, dispatch)
	a, _ = s.activity(ctx, g, map[string]any{"task_ref": gen.ID, "session_id": gen.Session})
	if a.State != "awaiting_phone" {
		t.Fatal("phone accepted counted as sent", a.State)
	}
	if _, e = s.jobControl(ctx, g, "cancel", map[string]any{"task_ref": dispatch.ID, "session_id": dispatch.Session}); e != nil {
		t.Fatal(e)
	}
	var cancel bool
	s.DB.QueryRow("SELECT cancel_requested FROM plugin_jobs WHERE id=?", dispatch.ID).Scan(&cancel)
	if cancel {
		t.Fatal("dispatch receipt polling cancelled")
	}
	mu.Lock()
	receiptState = "sent"
	mu.Unlock()
	s.execute(ctx, dispatch)
	a, _ = s.activity(ctx, g, map[string]any{"task_ref": gen.ID, "session_id": gen.Session})
	if a.State != "sent" {
		t.Fatal(a.State)
	}
	if approvals != 1 {
		t.Fatal("approval repeated", approvals)
	}
	if _, e = s.jobControl(ctx, g, "retry", map[string]any{"task_ref": dispatch.ID, "session_id": dispatch.Session}); e == nil {
		t.Fatal("dispatch retried")
	}
	// Old lease is rejected before an item checkpoint.
	s.DB.Exec("UPDATE plugin_worker_epoch SET epoch=2 WHERE id=1")
	if _, e = s.activityItemExec(ctx, gen.ID, "UPDATE plugin_items SET state='failed' WHERE job_id=?", gen.ID); e == nil {
		t.Fatal("stale worker wrote results")
	}
	t.Logf("%d pinned MySQL contexts, %d model inputs, realProvider=%t: final repairs/attachments, phone boundary, immutable approval and item receipt reconciliation passed", size, expected, real)
}
