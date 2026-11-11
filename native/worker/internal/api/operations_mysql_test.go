package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/types/known/durationpb"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func TestRealOperationsRecoveryKeepsOriginalReceipts(t *testing.T) {
	address, dsn := os.Getenv("WORKBENCH_TEST_TEMPORAL"), os.Getenv("WORKBENCH_TEST_DSN")
	if address == "" || dsn == "" {
		t.Skip("real Temporal/MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated database required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := core.Open(ctx, dsn)
	must(err)
	defer s.DB.Close()
	must(s.Migrate(ctx))
	id := uuid.NewString()
	namespace := "operations-" + id
	temporal, err := client.Dial(client.Options{HostPort: address, Namespace: namespace})
	must(err)
	defer temporal.Close()
	_, err = temporal.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: namespace, WorkflowExecutionRetentionPeriod: durationpb.New(24 * time.Hour)})
	must(err)
	p := operationsPrincipal(id)
	owner := core.Principal{Kind: "owner", SessionID: id + "-owner"}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "isolated operations fixture"}))
	must(s.BindSession(ctx, p))
	a := Server{Store: s, Temporal: temporal, Token: strings.Repeat("x", 40)}
	activities := engine.Activities{Store: s, Temporal: temporal}
	publish := func(ref core.Ref, records []core.Record, deps ...core.Ref) {
		t.Helper()
		must(s.BeginSnapshot(ctx, owner, core.Snapshot{ResourceID: ref.ResourceID, Version: ref.Version, Dependencies: deps}))
		must(s.Stage(ctx, owner, ref.ResourceID, ref.Version, records))
		_, err := s.Publish(ctx, owner, ref.ResourceID, ref.Version, len(records))
		must(err)
	}
	resource := func(ref core.Ref, fields ...string) core.Resource {
		return core.Resource{ID: ref.ResourceID, Name: ref.ResourceID, OwnerProject: id, Sections: []core.Section{{ID: ref.Section, Name: ref.Section, Fields: fields}}}
	}
	input := core.Ref{ResourceID: id + "-facts", Section: "facts", Version: "v1"}
	must(s.PutResource(ctx, owner, resource(input, "nickname", "evidence")))
	publish(input, []core.Record{
		{ID: "unknown", Fields: map[string]any{"nickname": "unknown", "evidence": map[string]any{"fact": "observed"}}},
		{ID: "prepared", Fields: map[string]any{"nickname": "prepared", "evidence": map[string]any{"fact": "observed"}}},
	})
	root := core.Run{ID: id + "-history", Principal: p, Operation: "pipeline", Title: onboardingTitle, Destination: operationsResource(id, "strategy"), ExplicitInputs: true}
	manifest := core.Ref{ResourceID: root.Destination, Section: "text", Version: id + "-drafts"}
	must(s.PutResource(ctx, owner, resource(manifest, "text")))
	must(s.SaveRun(ctx, root))
	_, err = s.DB.ExecContext(ctx, `UPDATE wb_runs SET created_at=UTC_TIMESTAMP()-INTERVAL 2 DAY WHERE id=?`, root.ID)
	must(err)
	must(s.SaveRun(ctx, core.Run{ID: manifest.Version, Principal: p, ParentRunID: root.ID, Operation: "generate", Title: root.Title, Sources: []core.Ref{input}, ExplicitInputs: true}))
	draft := `{"interested":true,"focus":"observed","reason":"verified fact","evidence":["fact"],"message":"isolated draft"}`
	publish(manifest, []core.Record{{ID: "unknown", Fields: map[string]any{"text": draft}}, {ID: "prepared", Fields: map[string]any{"text": draft}}}, input)
	eligibility := core.Ref{ResourceID: id + "-eligibility", Section: "eligible", Version: "v1"}
	check := core.RecordCheck{ID: "allowed", Name: "eligible", Section: eligibility.Section, Schema: map[string]any{"type": "object", "required": []any{"customerId", "staffId", "businessSlot"}}, Bindings: map[string]string{"customerId": "/customerId", "staffId": "/staffId", "businessSlot": "/businessSlot"}}
	qualified := resource(eligibility, "customerId", "staffId", "businessSlot")
	qualified.ConnectionID = id + "-source"
	qualified.Checks = []core.RecordCheck{check}
	must(s.PutConnection(ctx, owner, core.Connection{ID: qualified.ConnectionID, Name: "isolated", Kind: "jsonl", Location: t.TempDir()}))
	must(s.PutResource(ctx, owner, qualified))
	eligible := []core.Record{{ID: "unknown", Fields: map[string]any{"customerId": "c1", "staffId": "staff", "businessSlot": "slot1"}}, {ID: "prepared", Fields: map[string]any{"customerId": "c2", "staffId": "staff", "businessSlot": "slot2"}}}
	publish(eligibility, eligible)
	transport := id + "-crm"
	must(s.PutTransport(ctx, owner, core.Transport{ID: transport, Name: "isolated CRM", Kind: "crm-cli", Enabled: true, Projects: []string{id}, Endpoint: "http://127.0.0.1:9/mcp", TokenEnv: "WORKBENCH_CONNECTOR_OPERATIONS_TEST"}))
	must(a.saveOperations(ctx, operationsConfig{ProjectID: id, Input: input, Eligibility: core.Ref{ResourceID: eligibility.ResourceID, Section: eligibility.Section}, Staff: "staff", Check: check.ID, Transport: transport}))
	first := uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/ops-send-"+root.ID)).String()
	plan := core.DispatchPlan{Source: manifest, Eligibility: eligibility, Check: check, EligibilitySchema: check.Schema, ConnectionID: transport, TextPointer: "/text/message", TargetPointers: check.Bindings, DecodeJSONField: "text", RecordIDs: []string{"prepared", "unknown"}}
	// Simulate persistence succeeding before the original workflow start.
	must(s.SaveRun(ctx, core.Run{ID: first, Principal: p, ParentRunID: root.ID, Operation: "dispatch_batch", Title: root.Title, Dispatch: &plan, Sources: []core.Ref{manifest, eligibility}, ExplicitInputs: true}))
	call := func(method, path string, body string, out any) {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+a.Token)
		req.Header.Set("X-Workbench-Kind", p.Kind)
		req.Header.Set("X-Workbench-Project", p.ProjectID)
		req.Header.Set("X-Workbench-Session", p.SessionID)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		must(json.Unmarshal(w.Body.Bytes(), out))
	}
	var started RunRef
	request := `{"runId":"` + root.ID + `"}`
	call("POST", "/operations/send", request, &started)
	if started.RunID != first || a.operationsExecution(ctx, first) != "Running" {
		t.Fatal("historical run did not resume its original workflow", started)
	}
	_, err = activities.PrepareDispatchPage(ctx, first, "")
	must(err)
	_, err = activities.PrepareDispatchReview(ctx, first)
	must(err)
	decision, err := s.Decision(ctx, p, first+"-d1")
	must(err)
	decision.Answer = "发 " + first[:8]
	_, err = s.Answer(ctx, owner, decision)
	must(err)
	unknown := core.DeliveryItemID(first, "unknown")
	_, claimed, err := s.ClaimDelivery(ctx, unknown)
	must(err)
	if !claimed {
		t.Fatal("fixture did not claim the first recipient")
	}
	_, err = s.FinishDelivery(ctx, unknown, "unknown", "original-plan", "isolated unknown result")
	must(err)
	eligibility.Version = "v2"
	publish(eligibility, eligible)
	prepared := core.DeliveryItemID(first, "prepared")
	if _, _, err := s.ClaimDelivery(ctx, prepared); !errors.Is(err, core.ErrConflict) {
		t.Fatal("outdated eligibility was accepted", err)
	}
	must(temporal.TerminateWorkflow(ctx, first, "", "isolated eligibility-change fixture"))
	var view struct{ Run operationsRun }
	call("GET", "/operations/view?runId="+root.ID, "", &view)
	if view.Run.Pending != 1 || !view.Run.RetryReview {
		t.Fatal("only the unsubmitted recipient may be reviewed again", view.Run)
	}
	var retry RunRef
	call("POST", "/operations/send", request, &retry)
	if retry.RunID == first {
		t.Fatal("the outdated decision was reused")
	}
	call("POST", "/operations/send", request, &started)
	if started.RunID != retry.RunID {
		t.Fatal("duplicate click created another manifest", started, retry)
	}
	newRun, err := s.Run(ctx, p, retry.RunID)
	must(err)
	if newRun.ParentRunID != root.ID || newRun.Dispatch.Eligibility != eligibility || strings.Join(newRun.Dispatch.RecordIDs, ",") != "prepared" {
		t.Fatal("wrong scope for the replacement plan", newRun.Dispatch)
	}
	oldPrepared, claimed, err := s.ClaimDelivery(ctx, prepared)
	must(err)
	if claimed || oldPrepared.State != "superseded" {
		t.Fatal("the old manifest can still submit the replaced recipient")
	}
	oldReceipt, err := s.Delivery(ctx, unknown)
	must(err)
	if oldReceipt.State != "unknown" || oldReceipt.Receipt != "original-plan" {
		t.Fatal("the original receipt was lost or replaced")
	}
	_, err = activities.PrepareDispatchPage(ctx, retry.RunID, "")
	must(err)
	if _, _, err := s.ClaimDelivery(ctx, core.DeliveryItemID(retry.RunID, "prepared")); !errors.Is(err, core.ErrDenied) {
		t.Fatal("replacement did not require a fresh owner decision", err)
	}
	must(temporal.TerminateWorkflow(ctx, retry.RunID, "", "isolated acceptance finished"))

	oldLibrary := core.Ref{ResourceID: id + "-old-library", Section: "text", Version: "v1"}
	newLibrary := core.Ref{ResourceID: id + "-new-library", Section: "text", Version: "v1"}
	for _, ref := range []core.Ref{oldLibrary, newLibrary} {
		library := resource(ref, "text")
		library.OwnerProject = ""
		must(s.PutResource(ctx, owner, library))
		publish(ref, []core.Record{{ID: "excerpt", Fields: map[string]any{"text": ref.ResourceID}}})
		must(s.SetGrant(ctx, owner, core.Grant{ResourceID: ref.ResourceID, ProjectID: id, Sections: []string{ref.Section}}))
	}
	_, err = s.Read(ctx, p, oldLibrary, "", 1)
	must(err)
	oldRun := core.Run{ID: id + "-old-inputs", Principal: p, Operation: "pipeline", Title: "old inputs", ExplicitInputs: true, Sources: []core.Ref{oldLibrary}}
	must(s.SaveRun(ctx, oldRun))
	must(s.SetGrant(ctx, owner, core.Grant{ResourceID: oldLibrary.ResourceID, ProjectID: id}))
	if _, err = s.Run(ctx, p, oldRun.ID); !errors.Is(err, core.ErrDenied) {
		t.Fatal("historical dependencies must still honor revocation", err)
	}
	cfg, err := a.readOperations(ctx, id)
	must(err)
	method, err := a.publishOperationsMethod(ctx, owner, cfg, "strategy", "expert", "Use authorized facts only.")
	must(err)
	schedule, err := a.configureProgramInputs(ctx, p, ProgramSchedule{ID: "replacement-knowledge", Hours: []int{9}, Paused: true, Program: ProgramRequest{
		Title: onboardingTitle, Destination: root.Destination, Sources: []core.ResourceSelection{{ResourceID: input.ResourceID, Sections: []string{input.Section}}},
		LatestMethod: &method, Knowledge: &newLibrary,
	}}, true)
	must(err)
	must(temporal.ScheduleClient().GetHandle(ctx, schedule).Delete(ctx))
	pipeline, err := a.programRun(ctx, p, ProgramRequest{OperationKey: "new-inputs", Title: onboardingTitle, Destination: root.Destination,
		Sources: []core.ResourceSelection{{ResourceID: input.ResourceID, Sections: []string{input.Section}}}, LatestMethod: &method, Knowledge: &newLibrary})
	must(err)
	pipeline.ExplicitInputs = true
	must(s.SaveRun(ctx, pipeline))
	generationID, err := activities.ProgramGeneration(ctx, pipeline.ID)
	must(err)
	generation, err := s.Run(ctx, p, generationID)
	must(err)
	if len(generation.Lineage) != 2 || len(generation.Shared) != 2 {
		t.Fatal("new run must pin only its actual method and replacement library", generation.Lineage)
	}
	must(s.SetGrant(ctx, owner, core.Grant{ResourceID: newLibrary.ResourceID, ProjectID: id}))
	if _, err = s.Run(ctx, p, generationID); !errors.Is(err, core.ErrDenied) {
		t.Fatal("new run failed to retain authorization for the library it used", err)
	}

	// Production's old original schedule is not stored in cfg.Strategy.ID.
	// Installing the fixed cycle must still retire it, and only its Session.
	oldSchedule, otherSchedule, resourceSchedule := "program-"+uuid.NewString(), "program-"+uuid.NewString(), "program-"+uuid.NewString()
	for _, old := range []string{oldSchedule, otherSchedule, resourceSchedule} {
		_, err = temporal.ScheduleClient().Create(ctx, client.ScheduleOptions{ID: old,
			Spec:   client.ScheduleSpec{Calendars: []client.ScheduleCalendarSpec{{Year: []client.ScheduleRange{{Start: 2099}}}}},
			Action: &client.ScheduleWorkflowAction{ID: old, Workflow: "ScheduledOperations", TaskQueue: engine.BatchQueue}})
		must(err)
		defer temporal.ScheduleClient().GetHandle(ctx, old).Delete(context.Background())
		session := p.SessionID
		if old == otherSchedule {
			session = "another-session"
		}
		strategy := "hxc:onboarding"
		if old == resourceSchedule {
			strategy = ""
		}
		body, _ := json.Marshal(scheduleView{ID: old, SessionID: session, ProjectID: id, Strategy: strategy})
		_, err = s.DB.ExecContext(ctx, `INSERT INTO wb_settings VALUES(?,?)`, old, string(body))
		must(err)
	}
	connection := id + "-original"
	must(s.PutConnection(ctx, owner, core.Connection{ID: connection, Name: "isolated original", Kind: "operations-original", SecretEnv: "WORKBENCH_SOURCE_ISOLATED_ORIGINAL"}))
	selections := []core.ResourceSelection{}
	for _, name := range []string{"raw", "history", "strategies", "methods"} {
		ref := core.Ref{ResourceID: id + "-" + name, Section: "main"}
		v := resource(ref, "value")
		v.ConnectionID = connection
		must(s.PutResource(ctx, owner, v))
		selections = append(selections, core.ResourceSelection{ResourceID: ref.ResourceID, Sections: []string{"main"}, Refresh: true})
	}
	cycle := engine.OperationsCycleInput{Principal: p, Sources: selections[:1], HistorySource: &selections[1], StrategySource: &selections[2], MethodSource: &selections[3], Section: "main", StrategyKeys: []string{"hxc:ongoing"}, MethodRefs: map[string]core.RecordRef{}}
	for _, destination := range []*string{&cycle.CleanDestination, &cycle.SelectedDestination, &cycle.DraftDestination} {
		*destination = id + "-" + uuid.NewString()[:8]
		must(s.PutResource(ctx, owner, resource(core.Ref{ResourceID: *destination, Section: "main"}, "value")))
	}
	for _, key := range []string{"personal_followup", "new_user_onboarding", "weekly_review", "content_service", "activation_recall", "sales_assist"} {
		cycle.MethodRefs[key] = core.RecordRef{Ref: core.Ref{ResourceID: selections[3].ResourceID, Section: "main"}, ID: key}
	}
	cfg.Strategy.ID = ""
	cfg.Profile.ID = "program-" + uuid.NewString() // Already deleted legacy job.
	cfg.ReadSource = true
	at, err := time.Parse("15:04", "23:59")
	must(err)
	installed, err := a.configureLocalOperationsSchedule(ctx, cfg, "hxc:ongoing", at, cycle)
	must(err)
	defer temporal.ScheduleClient().GetHandle(ctx, installed.ID).Delete(context.Background())
	for schedule, paused := range map[string]bool{oldSchedule: true, otherSchedule: false, resourceSchedule: false, installed.ID: false} {
		state, err := temporal.ScheduleClient().GetHandle(ctx, schedule).Describe(ctx)
		must(err)
		if state.Schedule.State.Paused != paused {
			t.Fatalf("wrong migration state for %s: paused=%v", schedule, state.Schedule.State.Paused)
		}
	}
	// Two owner changes use the same lock as the old separate schedule editor.
	// Their final saved configuration must describe the sole active cycle.
	ownerCall := func(path string, input any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(input)
		req := httptest.NewRequest("POST", path, bytes.NewReader(body)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+a.Token)
		req.Header.Set("X-Workbench-Kind", "owner")
		req.Header.Set("X-Workbench-Session", owner.SessionID)
		out := httptest.NewRecorder()
		a.Handler().ServeHTTP(out, req)
		return out
	}
	configure := func(strategy string) *httptest.ResponseRecorder {
		input := cycle
		input.StrategyKeys = []string{strategy}
		return ownerCall("/operations/original-schedule", map[string]any{"projectId": id, "strategy": strategy, "time": "23:59", "cycle": input})
	}
	start, results := make(chan struct{}), make(chan *httptest.ResponseRecorder, 2)
	for _, strategy := range []string{"hxc:onboarding", "hxc:expiry"} {
		go func(strategy string) {
			<-start
			results <- configure(strategy)
		}(strategy)
	}
	close(start)
	var changed []string
	for i := 0; i < 2; i++ {
		response := <-results
		if response.Code != 200 {
			t.Fatalf("concurrent configuration: %d %s", response.Code, response.Body.String())
		}
		var view scheduleView
		must(json.Unmarshal(response.Body.Bytes(), &view))
		changed = append(changed, view.ID)
		defer temporal.ScheduleClient().GetHandle(ctx, view.ID).Delete(context.Background())
	}
	final, err := a.readOperations(ctx, id)
	must(err)
	for _, schedule := range append(changed, installed.ID) {
		state, err := temporal.ScheduleClient().GetHandle(ctx, schedule).Describe(ctx)
		must(err)
		if state.Schedule.State.Paused == (schedule == final.Strategy.ID) {
			t.Fatal("concurrent configuration left a second active cycle")
		}
	}
	if response := ownerCall("/schedules/pause", map[string]any{"id": installed.ID, "paused": false}); response.Code != 409 {
		t.Fatalf("replaced cycle was directly resumed: %d %s", response.Code, response.Body.String())
	}
	if response := ownerCall("/schedules/pause", map[string]any{"id": final.Strategy.ID, "paused": false}); response.Code != 200 {
		t.Fatalf("current cycle cannot resume: %d %s", response.Code, response.Body.String())
	}
	if response := ownerCall("/schedules/pause", map[string]any{"id": resourceSchedule, "paused": false}); response.Code != 200 {
		t.Fatalf("ordinary resource program cannot resume: %d %s", response.Code, response.Body.String())
	}
	if response := configure("hxc:ongoing"); response.Code != 200 {
		t.Fatalf("switch back: %d %s", response.Code, response.Body.String())
	}
	restored, err := temporal.ScheduleClient().GetHandle(ctx, installed.ID).Describe(ctx)
	must(err)
	if restored.Schedule.State.Paused {
		t.Fatal("switching back retained a migration pause")
	}
	if response := ownerCall("/schedules/pause", map[string]any{"id": installed.ID, "paused": true}); response.Code != 200 {
		t.Fatalf("owner pause: %d %s", response.Code, response.Body.String())
	}
	if response := configure("hxc:onboarding"); response.Code != 200 {
		t.Fatalf("switch after owner pause: %d %s", response.Code, response.Body.String())
	}
	restored, err = temporal.ScheduleClient().GetHandle(ctx, installed.ID).Describe(ctx)
	must(err)
	if !restored.Schedule.State.Paused || restored.Schedule.State.Note != "本人暂停" {
		t.Fatal("migration overwrote the owner's pause")
	}
	response := configure("hxc:ongoing")
	var pausedView scheduleView
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &pausedView) != nil || !pausedView.Paused {
		t.Fatalf("owner pause must be retained and reported: %d %s", response.Code, response.Body.String())
	}
	restored, err = temporal.ScheduleClient().GetHandle(ctx, installed.ID).Describe(ctx)
	must(err)
	if !restored.Schedule.State.Paused {
		t.Fatal("reconfiguration resumed an owner-paused schedule")
	}
}
