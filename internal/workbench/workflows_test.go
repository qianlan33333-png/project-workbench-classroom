package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func workflowFixture(t *testing.T, s *Store, p Project) Plugin {
	t.Helper()
	plugin := componentFixture(t, s, p)
	plugin.Component.Key = "context.operations"
	plugin.Component.Actions = append(plugin.Component.Actions,
		ComponentAction{Name: "sync_contexts"}, ComponentAction{Name: "retry"},
		ComponentAction{Name: "configuration", ReadOnly: true}, ComponentAction{Name: "activity_catalog", ReadOnly: true})
	v, err := s.SavePlugin(plugin)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWorkflowScheduleRecoveryAndScopedEvidence(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "工作流试点")
	plugin := workflowFixture(t, s, p)
	ctx := context.Background()
	c, err := s.SaveComponentConfig(ComponentRunConfig{SessionID: p.DefaultSessionID, PluginID: plugin.ID, PluginVersion: plugin.Component.Version, Mode: "program", Action: "sync_contexts"})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := s.SaveSchedule(Schedule{ProjectID: p.ID, SessionID: p.DefaultSessionID, Name: "同步", Instruction: "按固定配置同步", Cron: "20 */6 * * *", Timezone: "Asia/Shanghai", Enabled: true, Component: &c})
	if err != nil {
		t.Fatal(err)
	}
	nowAt := time.UnixMilli(sc.NextAt)
	if err := s.FireSchedules(nowAt); err != nil {
		t.Fatal(err)
	}
	if err := s.FireSchedules(nowAt); err != nil {
		t.Fatal(err)
	}
	views, err := s.workflowViews(p.DefaultSessionID, nowAt.Add(time.Minute).UnixMilli())
	if err != nil || len(views) != 1 || views[0].Source != "schedule" {
		t.Fatalf("occurrence not unique: %+v %v", views, err)
	}
	id := views[0].ID
	copy := plugin
	copy.ID, copy.Name = "", "同项目其他组件入口"
	component := *plugin.Component
	component.Key = "other-key"
	copy.Component = &component
	copy, err = s.SavePlugin(copy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitComponent(ctx, c.SessionID, ComponentRequest{PluginID: copy.ID, Action: "sync_contexts", OperationKey: "duplicate-sync"}); err == nil {
		t.Fatal("another component installation bypassed the active work")
	}
	if err := s.FireSchedules(nowAt.Add(6 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	views, err = s.workflowViews(p.DefaultSessionID, nowAt.Add(7*time.Hour).UnixMilli())
	if err != nil || len(views) != 2 || views[0].State != "skipped" {
		t.Fatalf("overlap not skipped: %+v %v", views, err)
	}
	var submits, retries, originalChecks int
	jobState := "failed"
	s.componentRunner = func(_ context.Context, _ Plugin, action string, args map[string]any) (json.RawMessage, error) {
		if args["session_id"] != p.DefaultSessionID || args["project_id"] != p.ID {
			t.Fatalf("unexpected component scope: %v", args)
		}
		switch action {
		case "sync_contexts":
			submits++
			return json.RawMessage(`{"status":"queued","job_ref":"sync-one"}`), nil
		case "retry":
			retries++
			if args["job_ref"] != "sync-one" {
				t.Fatal("retried a different job")
			}
			jobState = "succeeded"
			return nil, errors.New("retry acknowledgement lost")
		case "status":
			if args["job_ref"] == "older" {
				return json.RawMessage(`{"status":"succeeded","job_ref":"older","result":{"published":true,"generation":"older","source_metadata":{"crm_source_snapshot_id":"same-source"}}}`), nil
			}
			originalChecks++
			if jobState == "failed" {
				return json.RawMessage(`{"status":"failed","job_ref":"sync-one","summary":"源暂时不可读"}`), nil
			}
			return json.RawMessage(`{"status":"succeeded","job_ref":"sync-one","result":{"published":true,"generation":"sync-one","previous_generation":"older","users":25126,"published_at":1793938896564,"source_metadata":{"crm_source_snapshot_id":"same-source"}}}`), nil
		default:
			t.Fatalf("unexpected action %s", action)
			return nil, nil
		}
	}
	poll := func() {
		t.Helper()
		if _, e := s.DB.Exec("UPDATE workflow_runs SET next_at=1 WHERE id=? AND next_at>0", id); e != nil {
			t.Fatal(e)
		}
		if _, e := s.DB.Exec("UPDATE component_calls SET next_poll=0"); e != nil {
			t.Fatal(e)
		}
		s.pollWorkflows(ctx)
		s.pollComponents(ctx)
	}
	get := func() WorkflowRun {
		t.Helper()
		v, e := s.workflowRun(p.DefaultSessionID, id)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	for range 4 {
		poll()
	}
	failed := get()
	if failed.State != "failed" || failed.SummaryRunID == "" || !workflowResumable(failed) {
		t.Fatalf("missing failed feedback and resume: %+v", failed)
	}
	if _, err := s.resumeWorkflow(ctx, p.DefaultSessionID, id); err != nil {
		t.Fatal(err)
	}
	// The retry reached the plugin, but its response was lost. Restart with
	// only durable state and the original plugin receipt.
	runner := s.componentRunner
	root := s.Root
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	s.componentRunner = runner
	for range 8 {
		poll()
	}
	final := get()
	if final.State != "succeeded" || final.SummaryRunID == failed.SummaryRunID || len(final.Prior) != 1 ||
		submits != 1 || retries != 1 || originalChecks < 3 || !strings.Contains(workflowFactsText(final), "源业务版本与上一批相同") {
		t.Fatalf("incorrect recovery: %+v submissions=%d retries=%d checks=%d", final, submits, retries, originalChecks)
	}
	if final.Steps[0].CallID != failed.Steps[0].CallID {
		t.Fatal("recovery replaced original component call")
	}
	summary, err := s.Run(final.SummaryRunID)
	if err != nil || summary.WorkflowID != id || summary.Kind != workflowSummaryKind {
		t.Fatalf("wrong summary run: %+v %v", summary, err)
	}
	evidence, err := s.workflowPrompt(summary)
	if err != nil || !strings.Contains(evidence, "25126") || !strings.Contains(evidence, id) {
		t.Fatalf("summary lacks evidence: %s %v", evidence, err)
	}
	other := testProject(t, s, "其他项目")
	if _, err := s.Submit(Incoming{Source: "web", SessionID: other.DefaultSessionID, WorkflowID: id, Text: "这轮结果？", EventID: "wrong-workflow"}); err == nil {
		t.Fatal("cross-session reference accepted")
	}
	m, err := s.Submit(Incoming{Source: "web", SessionID: p.DefaultSessionID, WorkflowID: id, Text: "这轮结果？", EventID: "right-workflow"})
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.Route(ctx, m.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	question, err := s.Run(m.RunID)
	if err != nil || question.WorkflowID != id {
		t.Fatalf("question lost the work reference: %+v %v", question, err)
	}
}

func TestWorkflowCatalogUsesSameExecutorAndPreservesSuccess(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "目录流程试点")
	plugin := workflowFixture(t, s, p)
	ctx := context.Background()
	c, err := s.SaveComponentConfig(ComponentRunConfig{SessionID: p.DefaultSessionID, PluginID: plugin.ID, PluginVersion: plugin.Component.Version, Mode: "program", Action: "activity_catalog"})
	if err != nil {
		t.Fatal(err)
	}
	var configuration, catalog int
	s.componentRunner = func(_ context.Context, _ Plugin, action string, _ map[string]any) (json.RawMessage, error) {
		switch action {
		case "configuration":
			configuration++
			return json.RawMessage(`{"status":"succeeded","result":{}}`), nil
		case "activity_catalog":
			catalog++
			if catalog == 1 {
				return json.RawMessage(`{"status":"failed","summary":"上游暂时不可读"}`), nil
			}
			return json.RawMessage(`{"status":"succeeded","result":{"packages":[{},{}]}}`), nil
		default:
			t.Fatalf("unexpected action %s", action)
			return nil, nil
		}
	}
	v, err := s.RunComponentConfig(ctx, c.SessionID, "manual-catalog")
	if err != nil {
		t.Fatal(err)
	}
	id := v.(workflowView).ID
	for phase := 0; phase < 2; phase++ {
		for range 6 {
			s.DB.Exec("UPDATE workflow_runs SET next_at=1 WHERE id=? AND next_at>0", id)
			s.DB.Exec("UPDATE component_calls SET next_poll=0")
			s.pollWorkflows(ctx)
			s.pollComponents(ctx)
		}
		r, err := s.workflowRun(c.SessionID, id)
		if err != nil {
			t.Fatal(err)
		}
		if phase == 0 {
			if r.State != "failed" || r.Steps[0].State != "succeeded" {
				t.Fatalf("did not retain the successful first step: %+v", r)
			}
			if _, err := s.resumeWorkflow(ctx, c.SessionID, id); err != nil {
				t.Fatal(err)
			}
		} else if r.State != "succeeded" || configuration != 1 || catalog != 2 || !strings.Contains(workflowFactsText(r), "2 个活动工作包") {
			t.Fatalf("second flow repeated a successful step or lost evidence: %+v", r)
		} else {
			report, err := s.Run(r.SummaryRunID)
			if err != nil {
				t.Fatal(err)
			}
			report.State = "failed"
			if err := s.saveRun(report); err != nil {
				t.Fatal(err)
			}
			if _, err := s.retryWorkflowSummary(c.SessionID, id); err != nil {
				t.Fatal(err)
			}
			s.pollWorkflows(ctx)
			s.pollComponents(ctx)
			after, err := s.workflowRun(c.SessionID, id)
			if err != nil || after.SummaryRunID == r.SummaryRunID || after.State != "succeeded" || configuration != 1 || catalog != 2 {
				t.Fatalf("retrying feedback changed business execution: %+v %v", after, err)
			}
		}
	}
}
