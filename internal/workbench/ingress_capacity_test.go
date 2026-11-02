package workbench

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlowSemanticRouteDoesNotBlockCustomerQueue(t *testing.T) {
	s := testStore(t)
	c, b := gatewaySetup(t, s, newGatewayProvider(t))
	first, err := s.Submit(Incoming{Source: "web", EventID: newID(), Text: "please assign my message"})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	route := func(ctx context.Context, _ Incoming, _ RouteCatalog) (RouteDecision, error) {
		started <- struct{}{}
		<-ctx.Done()
		return RouteDecision{}, ctx.Err()
	}
	ex := &admissionExecutor{started: make(chan admittedExecution, 4)}
	w := NewWorker(s, ex, route)
	ctx, cancel := context.WithCancel(t.Context())
	w.Start(ctx)
	defer func() { cancel(); w.Wait() }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("semantic router did not start")
	}
	receiveGateway(t, s, c, b.AccountID, "fixture-customer", "message.received", "independent", "test", time.Now())
	x := nextExecution(t, ex)
	if x.run.Source != gatewaySource {
		t.Fatal("wrong run", x.run)
	}
	close(x.release)
	cancel()
	w.Wait()
	old, err := s.Message(first.ID)
	if err != nil || old.State != "unrouted" || old.Question != "" {
		t.Fatal("stopping the semantic router misclassified a message", old.State, err)
	}
}

func TestBusySemanticEntriesRotateAndRetryWithoutClarification(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "project-fixture")
	var batch []Message
	// The fast explicit message is beyond the first page of deferred semantic work.
	for i := 0; i < 21; i++ {
		m, err := s.Submit(Incoming{Source: "web", EventID: newID(), Text: fmt.Sprint("unknown ", i)})
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, m)
	}
	explicit, err := s.Submit(Incoming{Source: "web", EventID: newID(), SessionID: p.DefaultSessionID, Text: "known conversation"})
	if err != nil {
		t.Fatal(err)
	}
	var ready atomic.Bool
	route := func(context.Context, Incoming, RouteCatalog) (RouteDecision, error) {
		if !ready.Load() {
			return RouteDecision{}, errHarnessAdmission
		}
		return RouteDecision{ProjectID: p.ID}, nil
	}
	ex := &admissionExecutor{started: make(chan admittedExecution, 4)}
	w := NewWorker(s, ex, route)
	ctx, cancel := context.WithCancel(t.Context())
	w.Start(ctx)
	defer func() { cancel(); w.Wait() }()
	waitFor(t, func() bool { m, _ := s.Message(explicit.ID); return m.RunID != "" })
	for _, receipt := range batch {
		m, err := s.Message(receipt.ID)
		if err != nil || m.State != "unrouted" || m.Question != "" || m.RunID != "" {
			t.Fatal("busy entry lost durable ownership", m.State, err)
		}
	}
	var clarifications int
	if err = s.DB.QueryRow("SELECT count(*) FROM deliveries").Scan(&clarifications); err != nil || clarifications != 0 {
		t.Fatal("busy resources generated a user message", err, clarifications)
	}
	ready.Store(true)
	waitFor(t, func() bool {
		var n int
		return s.DB.QueryRow("SELECT count(*) FROM runs WHERE project_id=?", p.ID).Scan(&n) == nil && n == len(batch)+1
	})
}

func TestClaimRollsBackUnacquiredOrAbortedHostSlots(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "queue")
	m := submitRoute(t, s, Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: newID(), Text: "needs capacity"})
	w := NewWorker(s, &Harness{Store: s, Config: Config{RuntimeMode: "podman"}}, nil)
	var reservations []*harnessSlot
	defer func() {
		for _, slot := range reservations {
			slot.finish()
		}
	}()
	for range maxConcurrentRuns {
		slot := hostPool.try(time.Now(), 1<<40, 1)
		if slot == nil {
			t.Fatal("unexpected active host slot")
		}
		reservations = append(reservations, slot)
	}
	if _, err := w.claim(); !errors.Is(err, ErrLocked) {
		t.Fatal("expected admission backpressure", err)
	}
	if run, err := s.Run(m.RunID); err != nil || run.State != "queued" {
		t.Fatal("no-slot claim consumed run", err)
	}
	for _, slot := range reservations {
		slot.finish()
	}
	reservations = nil
	if _, err := s.DB.Exec("CREATE TRIGGER refuse_claim BEFORE UPDATE OF state ON runs WHEN NEW.state='running' BEGIN SELECT RAISE(ABORT,'injected'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.claim(); err == nil {
		t.Fatal("trigger did not abort claim")
	}
	hostPool.Lock()
	n := len(hostPool.slots)
	hostPool.Unlock()
	if n != 0 {
		t.Fatal("failed transaction leaked its reserved host slot", n)
	}
}
