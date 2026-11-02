package workbench

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type admittedExecution struct {
	run     Run
	release chan struct{}
}
type admissionExecutor struct {
	started    chan admittedExecution
	cleanupErr error
}

func (a *admissionExecutor) Cleanup(context.Context, string) error { return a.cleanupErr }
func (a *admissionExecutor) Execute(ctx context.Context, in RuntimeInput, hooks RuntimeHooks) (RuntimeResult, error) {
	x := admittedExecution{in.Run, make(chan struct{})}
	select {
	case a.started <- x:
	case <-ctx.Done():
		return RuntimeResult{}, ctx.Err()
	}
	select {
	case <-x.release:
		return RuntimeResult{Text: `{"action":"ignore"}`}, nil
	case <-ctx.Done():
		return RuntimeResult{}, ctx.Err()
	}
}
func nextExecution(t *testing.T, a *admissionExecutor) admittedExecution {
	t.Helper()
	select {
	case x := <-a.started:
		return x
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for execution")
		return admittedExecution{}
	}
}
func queueTestCustomer(t *testing.T, s *Store, c GatewayConnection, b GatewayBinding, customer string) Run {
	t.Helper()
	id := newID()
	receiveGateway(t, s, c, b.AccountID, customer, "message.received", id, "question", time.Now())
	return queueGatewayTest(t, s, id)
}

func TestSixConcurrentReadersThroughWorkerStart(t *testing.T) {
	s := testStore(t)
	c, b := gatewaySetup(t, s, newGatewayProvider(t))
	p, e := s.SavePlugin(Plugin{Name: "trusted knowledge", Transport: "http", ProjectID: b.ProjectID, URL: "https://kb.fixture/mcp"})
	if e != nil {
		t.Fatal(e)
	}
	b.PluginIDs = []string{p.ID}
	if b, e = s.SaveGatewayBinding(b); e != nil {
		t.Fatal(e)
	}
	var queued []Run
	for i := 0; i < 7; i++ {
		queued = append(queued, queueTestCustomer(t, s, c, b, fmt.Sprintf("customer-%d", i)))
	}
	second := queueTestCustomer(t, s, c, b, "customer-0")
	ex := &admissionExecutor{started: make(chan admittedExecution, 12)}
	w := NewWorker(s, ex, nil)
	w.readMCPGrants = []GatewayReadMCPGrant{{b.ProjectID, p.ID, p.URL}}
	ctx, stop := context.WithCancel(t.Context())
	w.Start(ctx)
	defer func() { stop(); w.Wait() }()
	active := map[string]admittedExecution{}
	for range 6 {
		x := nextExecution(t, ex)
		if x.run.Gateway == nil || !x.run.Gateway.SharedRead {
			t.Fatal("expected a shared run")
		}
		active[x.run.ID] = x
	}
	for _, r := range queued[:6] {
		if _, ok := active[r.ID]; !ok {
			t.Fatalf("oldest distinct customer was not admitted: %s", r.ID)
		}
	}
	var leases, uniqueSessions, baselines int
	if err := s.DB.QueryRow("SELECT count(*),count(DISTINCT session_id) FROM workbench_run_leases WHERE shared=1").Scan(&leases, &uniqueSessions); err != nil || leases != 6 || uniqueSessions != 6 {
		t.Fatal(leases, uniqueSessions, err)
	}
	if err := s.DB.QueryRow("SELECT count(*) FROM baselines").Scan(&baselines); err != nil || baselines != 0 {
		t.Fatal("readers copied project files", baselines, err)
	}
	if r, _ := s.Run(queued[6].ID); r.State != "queued" {
		t.Fatal("seventh customer did not wait")
	}
	if r, _ := s.Run(second.ID); r.State != "queued" {
		t.Fatal("same conversation ran concurrently")
	}
	personal, e := s.PersonalSpace()
	if e != nil {
		t.Fatal(e)
	}
	owner := submitRoute(t, s, Incoming{Source: "web", SessionID: personal.DefaultSessionID, Text: "owner", EventID: newID()})
	close(active[queued[1].ID].release)
	delete(active, queued[1].ID)
	next := nextExecution(t, ex)
	if next.run.ID != owner.RunID {
		t.Fatal("personal assistant did not get next free slot", next.run.Source)
	}
	close(next.release)
	next = nextExecution(t, ex)
	if next.run.ID != queued[6].ID {
		t.Fatal("seventh customer did not start on release")
	}
	active[next.run.ID] = next
	project, _ := s.Project(b.ProjectID)
	writer := submitRoute(t, s, Incoming{Source: "web", SessionID: project.DefaultSessionID, Text: "write project", EventID: newID()})
	late := queueTestCustomer(t, s, c, b, "late")
	for id, x := range active {
		if id != queued[0].ID {
			close(x.release)
		}
	}
	waitFor(t, func() bool {
		var n int
		return s.DB.QueryRow("SELECT count(*) FROM workbench_run_leases").Scan(&n) == nil && n == 1
	})
	if r, _ := s.Run(late.ID); r.State != "queued" {
		t.Fatal("new reader bypassed waiting writer")
	}
	close(active[queued[0].ID].release)
	for _, expected := range []string{second.ID, writer.RunID, late.ID} {
		x := nextExecution(t, ex)
		if x.run.ID != expected {
			t.Fatal("incorrect serial/writer-fair order", x.run.ID, expected)
		}
		close(x.release)
	}
}
func TestGatewayReaderScheduling(t *testing.T) {
	// Only an immutable scoped HTTP destination with no CLI can be shared.
	for _, tc := range []struct {
		name                       string
		transport                  string
		scope, changed, cli, allow bool
		want                       bool
	}{
		{"approved", "http", true, false, false, true, true}, {"unlisted", "http", true, false, false, false, false},
		{"retargeted", "http", true, true, false, true, false}, {"global", "http", false, false, false, true, false},
		{"stdio", "stdio", true, false, false, true, false}, {"additional-cli", "http", true, false, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			c, b := gatewaySetup(t, s, newGatewayProvider(t))
			p := Plugin{Name: "support", Transport: tc.transport, URL: "https://trusted.test/mcp", Command: "mcp"}
			if tc.scope {
				p.ProjectID = b.ProjectID
			}
			p, e := s.SavePlugin(p)
			if e != nil {
				t.Fatal(e)
			}
			b.PluginIDs = []string{p.ID}
			if tc.cli {
				b.ToolIDs = []string{capabilityFixture(t, s).ID}
			}
			if b, e = s.SaveGatewayBinding(b); e != nil {
				t.Fatal(e)
			}
			_ = queueTestCustomer(t, s, c, b, "a")
			_ = queueTestCustomer(t, s, c, b, "b")
			w := NewWorker(s, nil, nil)
			g := GatewayReadMCPGrant{b.ProjectID, p.ID, p.URL}
			if tc.allow {
				w.readMCPGrants = []GatewayReadMCPGrant{g}
			}
			if tc.changed {
				p.URL = "https://altered.test/mcp"
				if _, e = s.SavePlugin(p); e != nil {
					t.Fatal(e)
				}
			}
			first, e := w.claim()
			if e != nil || first.Gateway.SharedRead != tc.want {
				t.Fatal(tc.name, e)
			}
			_, e = w.claim()
			if (e == nil) != tc.want {
				t.Fatal(tc.name, e)
			}
			if tc.want {
				h := &Harness{Store: s, Config: Config{GatewayConcurrentReadMCP: w.readMCPGrants}}
				in := RuntimeInput{Run: first, Project: Project{ID: b.ProjectID}}
				if _, ok := h.gatewayPlugin(in, p.ID, "http"); !ok {
					t.Fatal("approved tool unavailable")
				}
				p.Headers = map[string]string{"Authorization": "new credential"}
				if _, e = s.SavePlugin(p); e != nil {
					t.Fatal(e)
				}
				if _, ok := h.gatewayPlugin(in, p.ID, "http"); !ok {
					t.Fatal("credential rotation revoked the approved destination")
				}
				p.URL = "https://other.test/mcp"
				if _, e = s.SavePlugin(p); e != nil {
					t.Fatal(e)
				}
				if _, ok := h.gatewayPlugin(in, p.ID, "http"); ok {
					t.Fatal("active shared run followed a changed destination")
				}
			}
		})
	}
}
func TestGatewayReaderRecoveryAndMaintenance(t *testing.T) {
	s := testStore(t)
	c, b := gatewaySetup(t, s, newGatewayProvider(t))
	ex := &admissionExecutor{cleanupErr: errors.New("executor busy")}
	w := NewWorker(s, ex, nil)
	for _, name := range []string{"first", "second"} {
		_ = queueTestCustomer(t, s, c, b, name)
	}
	for range 2 {
		if _, e := w.claim(); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.CheckUnlocked(b.ProjectID); !errors.Is(e, ErrLocked) {
		t.Fatal("project mutation allowed with readers", e)
	}
	if e := s.CheckNoRunningTools(); !errors.Is(e, ErrLocked) {
		t.Fatal("shared package update allowed with readers", e)
	}
	if e := s.DeleteProject(b.ProjectID); !errors.Is(e, ErrLocked) {
		t.Fatal("project delete allowed with readers", e)
	}
	if e := w.Recover(t.Context()); e == nil {
		t.Fatal("cleanup failure ignored")
	}
	if e := s.CheckUnlocked(b.ProjectID); !errors.Is(e, ErrLocked) {
		t.Fatal("released reader before cleanup", e)
	}
	ex.cleanupErr = nil
	if e := w.Recover(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckUnlocked(b.ProjectID); e != nil {
		t.Fatal("reader remained after recovery", e)
	}
	_ = queueTestCustomer(t, s, c, b, "new")
	if _, e := s.DB.Exec("INSERT INTO project_locks VALUES(?,?,?)", b.ProjectID, "maintenance:fixture", now()); e != nil {
		t.Fatal(e)
	}
	if _, e := w.claim(); !errors.Is(e, ErrNotFound) {
		t.Fatal("maintenance did not block readers", e)
	}
}
