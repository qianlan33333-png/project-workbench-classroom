package workbench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeferredRouteKeepsEntryFocusOrder(t *testing.T) {
	s := testStore(t)
	a := testProject(t, s, "alpha")
	b := testProject(t, s, "beta")
	in := Incoming{Source: "web", ConversationID: "entry", EventID: newID(), Text: "请安排这项独立任务"}
	if e := s.SetFocus(in, a.DefaultSessionID); e != nil {
		t.Fatal(e)
	}
	first, e := s.Submit(in)
	if e != nil {
		t.Fatal(e)
	}
	in.EventID = newID()
	in.Text = "继续"
	second, e := s.Submit(in)
	if e != nil {
		t.Fatal(e)
	}
	// Simultaneous timestamps must still follow persisted arrival order.
	_, e = s.DB.Exec("UPDATE inbox SET created_at=? WHERE id IN (?,?)", first.CreatedAt, first.ID, second.ID)
	if e != nil {
		t.Fatal(e)
	}
	var ready atomic.Bool
	route := func(context.Context, Incoming, RouteCatalog) (RouteDecision, error) {
		if !ready.Load() {
			return RouteDecision{}, errHarnessAdmission
		}
		return RouteDecision{ProjectID: b.ID}, nil
	}
	ex := &admissionExecutor{started: make(chan admittedExecution, 8)}
	w := NewWorker(s, ex, route)
	ctx, stop := context.WithCancel(t.Context())
	w.Start(ctx)
	defer func() { stop(); w.Wait() }()
	waitFor(t, func() bool {
		var delay int
		return s.DB.QueryRow("SELECT coalesce(json_extract(data,'$.routeAfter'),0) FROM inbox WHERE id=?", first.ID).Scan(&delay) == nil && delay > 0
	})
	independent, e := s.Submit(Incoming{Source: "web", ConversationID: "other", EventID: newID(), SessionID: a.DefaultSessionID, Text: "explicit"})
	if e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { m, _ := s.Message(independent.ID); return m.RunID != "" })
	if m, _ := s.Message(second.ID); m.RunID != "" || m.State != "unrouted" {
		t.Fatal("continuation overtook its focus dependency", m)
	}
	ready.Store(true)
	waitFor(t, func() bool { m, _ := s.Message(second.ID); return m.RunID != "" })
	for _, id := range []string{first.ID, second.ID} {
		m, _ := s.Message(id)
		if m.SessionID != b.DefaultSessionID {
			t.Fatal("wrong destination", m.SessionID)
		}
	}
}

func TestIngressPendingIndexesExcludeLargeHistory(t *testing.T) {
	s := testStore(t)
	// A realistic terminal history must not enter either polling index.
	_, e := s.DB.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000)
 INSERT INTO inbox SELECT 'old-'||x,'web','','old-'||x,'old-entry','completed','',x,'{}' FROM n`)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		q       string
		args    []any
		indexes []string
	}{
		{gatewayInboxSQL, nil, []string{"inbox_gateway_pending"}},
		{routeInboxSQL, []any{now()}, []string{"inbox_route_ready", "inbox_route_entry"}},
	} {
		rows, e := s.DB.Query("EXPLAIN QUERY PLAN "+tc.q, tc.args...)
		if e != nil {
			t.Fatal(e)
		}
		var plan strings.Builder
		for rows.Next() {
			var a, b, c int
			var detail string
			if e = rows.Scan(&a, &b, &c, &detail); e != nil {
				t.Fatal(e)
			}
			plan.WriteString(detail + "\n")
		}
		rows.Close()
		for _, index := range tc.indexes {
			if !strings.Contains(plan.String(), index) {
				t.Fatal("pending index unused", plan.String())
			}
		}
		if strings.Contains(plan.String(), "TEMP B-TREE") {
			t.Fatal("poll sorts history", plan.String())
		}
	}
	// Delay and same-entry dependencies participate even across LIMIT pages.
	first, e := s.Submit(Incoming{Source: "web", EventID: newID(), Text: "earliest"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec("UPDATE inbox SET data=json_set(data,'$.routeAfter',?) WHERE id=?", now()+60000, first.ID)
	if e != nil {
		t.Fatal(e)
	}
	for range 25 {
		_, e = s.Submit(Incoming{Source: "web", EventID: newID(), Text: "dependent"})
		if e != nil {
			t.Fatal(e)
		}
	}
	for i := range 2 {
		_, e = s.Submit(Incoming{Source: "web", EventID: newID(), ConversationID: fmt.Sprint("other-", i), Text: "independent"})
		if e != nil {
			t.Fatal(e)
		}
	}
	got, e := listJSON[Message](s.DB, routeInboxSQL, now())
	if e != nil || len(got) != 2 {
		t.Fatal("independent entries starved", len(got), e)
	}
	// Legacy queries remain usable with the additional indexes.
	var count int
	if e = s.DB.QueryRow("SELECT count(*) FROM inbox WHERE state IN ('unrouted','gateway_pending')").Scan(&count); e != nil || count != 28 {
		t.Fatal(count, e)
	}
}

func TestDeferredRoutePreservesLaterExplicitFocus(t *testing.T) {
	s := testStore(t)
	a := testProject(t, s, "alpha")
	b := testProject(t, s, "beta")
	in := Incoming{Source: "web", ConversationID: "same-entry", EventID: newID(), Text: "new task"}
	if e := s.SetFocus(in, a.DefaultSessionID); e != nil {
		t.Fatal(e)
	}
	first, e := s.Submit(in)
	if e != nil {
		t.Fatal(e)
	}
	in.EventID = newID()
	in.SessionID = a.DefaultSessionID
	in.Text = "explicit alpha"
	explicit, e := s.Submit(in)
	if e != nil {
		t.Fatal(e)
	}
	in.EventID = newID()
	in.SessionID = ""
	in.Text = "继续"
	last, e := s.Submit(in)
	if e != nil {
		t.Fatal(e)
	}
	var ready atomic.Bool
	route := func(context.Context, Incoming, RouteCatalog) (RouteDecision, error) {
		if !ready.Load() {
			return RouteDecision{}, errHarnessAdmission
		}
		return RouteDecision{ProjectID: b.ID}, nil
	}
	ex := &admissionExecutor{started: make(chan admittedExecution, 8)}
	w := NewWorker(s, ex, route)
	ctx, stop := context.WithCancel(t.Context())
	w.Start(ctx)
	defer func() { stop(); w.Wait() }()
	waitFor(t, func() bool {
		var d int64
		return s.DB.QueryRow("SELECT coalesce(json_extract(data,'$.routeAfter'),0) FROM inbox WHERE id=?", first.ID).Scan(&d) == nil && d > 0
	})
	if m, _ := s.Message(explicit.ID); m.RunID != "" {
		t.Fatal("explicit focus overtook earlier route")
	}
	ready.Store(true)
	waitFor(t, func() bool { m, _ := s.Message(last.ID); return m.RunID != "" })
	for id, want := range map[string]string{first.ID: b.DefaultSessionID, explicit.ID: a.DefaultSessionID, last.ID: a.DefaultSessionID} {
		m, _ := s.Message(id)
		if m.SessionID != want {
			t.Fatal("arrival focus lost", id, m.SessionID, want)
		}
	}
}

func offlineCapacityHarness(t *testing.T) (*Store, *Harness, RuntimeInput) {
	t.Helper()
	s, h, in := gatewayRuntimeFixture(t)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("PW_GATEWAY_PROTOCOL_CHILD", "1")
	t.Setenv("PW_GATEWAY_TEST_EXECUTABLE", exe)
	shim := filepath.Join(t.TempDir(), "podman")
	script := `#!/bin/sh
case "$1" in
run) exec "$PW_GATEWAY_TEST_EXECUTABLE" -test.run '^TestGatewayHarnessProtocolChild$';;
rm) if [ -n "$PW_TEST_RM_FAIL" ] && [ ! -e "$PW_TEST_RM_MARKER.allow" ]; then
 if [ "$PW_TEST_RM_FAIL" = always ] || mkdir "$PW_TEST_RM_MARKER" 2>/dev/null; then exit 1; fi
 fi; exit 0;;
*) exit 1;;
esac
`
	if e = os.WriteFile(shim, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	h.Config.Podman = shim
	return s, h, in
}

func TestSlowInitializeRetainsStartupReservation(t *testing.T) {
	s, h, in := offlineCapacityHarness(t)
	r, e := NewWorker(s, nil, nil).claim()
	if e != nil {
		t.Fatal(e)
	}
	in.Run = r
	gate := filepath.Join(t.TempDir(), "initialize")
	t.Setenv("PW_TEST_INITIALIZE_GATE", gate)
	pool := harnessPool{slots: make(map[*harnessSlot]struct{})}
	slot := pool.try(time.Now(), 400, 300)
	in.Run.hostSlot = slot
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := h.Execute(ctx, in, RuntimeHooks{}); done <- err }()
	waitFor(t, func() bool { _, e := os.Stat(gate + ".waiting"); return e == nil })
	// Advance the admission clock beyond the five-second launcher window.
	pool.Lock()
	remaining := pool.remainingLocked(time.Now().Add(6*time.Second), 400)
	started := slot.started
	pool.Unlock()
	if remaining != 100 || !started.IsZero() {
		t.Fatal("launcher was mistaken for a ready runtime", remaining, started)
	}
	if e = os.WriteFile(gate, nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	pool.Lock()
	started = slot.started
	pool.Unlock()
	if started.IsZero() {
		t.Fatal("successful handshake never marked runtime ready")
	}
}

func TestRecoveredCleanupPreservesModelOutcome(t *testing.T) {
	for _, modelFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(modelFailure), func(t *testing.T) {
			s, h, _ := offlineCapacityHarness(t)
			t.Setenv("PW_TEST_RM_FAIL", "once")
			t.Setenv("PW_TEST_RM_MARKER", filepath.Join(t.TempDir(), "first-rm"))
			if modelFailure {
				t.Setenv("PW_TEST_MODEL_FAILURE", "1")
			}
			w := NewWorker(s, h, nil)
			r, e := w.claim()
			if e != nil {
				t.Fatal(e)
			}
			w.execute(t.Context(), r)
			got, e := s.Run(r.ID)
			if e != nil {
				t.Fatal(e)
			}
			g, e := s.gatewaySession(r.SessionID)
			if e != nil {
				t.Fatal(e)
			}
			var deliveries int
			e = s.DB.QueryRow("SELECT count(*) FROM deliveries WHERE json_extract(data,'$.runId')=? AND json_extract(data,'$.kind')='gateway.answer'", r.ID).Scan(&deliveries)
			if e != nil {
				t.Fatal(e)
			}
			if modelFailure {
				if got.State != "failed" || g.Mode != "human" || deliveries != 0 {
					t.Fatal(got.State, g.Mode, deliveries)
				}
			} else if got.State != "completed" || g.Mode != "auto" || deliveries != 1 {
				t.Fatal(got.State, g.Mode, deliveries, got.Error)
			}
		})
	}
}

func TestUnconfirmedCleanupRetainsStoppingLease(t *testing.T) {
	s, h, _ := offlineCapacityHarness(t)
	marker := filepath.Join(t.TempDir(), "rm")
	t.Setenv("PW_TEST_RM_FAIL", "always")
	t.Setenv("PW_TEST_RM_MARKER", marker)
	w := NewWorker(s, h, nil)
	r, e := w.claim()
	if e != nil {
		t.Fatal(e)
	}
	w.execute(t.Context(), r)
	got, e := s.Run(r.ID)
	if e != nil || got.State != "stopping" {
		t.Fatal(got.State, e)
	}
	var replies, leases int
	if e = s.DB.QueryRow("SELECT count(*) FROM deliveries WHERE json_extract(data,'$.runId')=? AND json_extract(data,'$.kind')='gateway.answer'", r.ID).Scan(&replies); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow("SELECT count(*) FROM workbench_run_leases WHERE run_id=?", r.ID).Scan(&leases); e != nil {
		t.Fatal(e)
	}
	if replies != 0 || leases != 1 {
		t.Fatal("failed cleanup released durable fence", replies, leases)
	}
	r.hostSlot.pool.Lock()
	held := r.hostSlot.quarantine
	r.hostSlot.pool.Unlock()
	if !held {
		t.Fatal("orphan lost host reservation")
	}
	if e = os.WriteFile(marker+".allow", nil, 0600); e != nil {
		t.Fatal(e)
	}
	waitFor(t, func() bool { r.hostSlot.pool.Lock(); defer r.hostSlot.pool.Unlock(); return !r.hostSlot.quarantine })
}
