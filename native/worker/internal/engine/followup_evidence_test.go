package engine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	enums "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	service "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/testsuite"
	"os"
	"strings"
	"testing"
	"workbench-native/internal/core"
)

type evidenceModel struct{ Inputs []map[string]any }

func (m *evidenceModel) Generate(_ context.Context, in ModelRequest) (ModelResult, error) {
	m.Inputs = append(m.Inputs, in.Input)
	return ModelResult{Text: `{"state":"followup","summary":"目标仍需核实","nextHours":1}`}, nil
}

// Actual MySQL evidence and checkpoints; only the model and Temporal describe
// response are fixtures. Workflow timer/restart is covered separately.
func textJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestMySQLFollowupReadsTargetAndFreezesEachRound(t *testing.T) {
	dsn := os.Getenv("WORKBENCH_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL not configured")
	}
	if !strings.Contains(dsn, "/workbench_native_acceptance") {
		t.Fatal("isolated DB required")
	}
	ctx := context.Background()
	s, err := core.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Migrate(ctx))
	id := uuid.NewString()
	owner := core.Principal{Kind: "owner", SessionID: id + "o"}
	p := core.Principal{Kind: "project", ProjectID: id, SessionID: id + "s"}
	other := core.Principal{Kind: "project", ProjectID: id + "b", SessionID: id + "bs"}
	must(s.PutProject(ctx, owner, core.Project{ID: id, Name: "target evidence"}))
	must(s.PutProject(ctx, owner, core.Project{ID: other.ProjectID, Name: "outside"}))
	source := core.Resource{ID: id + "source", Name: "evidence", OwnerProject: other.ProjectID, Sections: []core.Section{{ID: "main", Fields: []string{"name"}}}}
	must(s.PutResource(ctx, owner, source))
	must(s.BeginSnapshot(ctx, other, core.Snapshot{ResourceID: source.ID, Version: "v1"}))
	must(s.Stage(ctx, other, source.ID, "v1", []core.Record{{ID: "1", Fields: map[string]any{"name": "sample"}}}))
	_, err = s.Publish(ctx, other, source.ID, "v1", 1)
	must(err)
	must(s.SetGrant(ctx, owner, core.Grant{ResourceID: source.ID, ProjectID: p.ProjectID, Sections: []string{"main"}}))
	ref := core.Ref{ResourceID: source.ID, Version: "v1", Section: "main"}
	target := core.Run{ID: id + "target", Principal: p, Operation: "dispatch", Title: "delegation accepted"}
	must(s.SaveRun(ctx, target))
	child := core.Run{ID: id + "child", Principal: p, Operation: "generate", Title: "actual work", Sources: []core.Ref{ref}}
	must(s.SaveRun(ctx, child))
	follow := core.Run{ID: id + "follow", Principal: p, Operation: "followup", Title: "verify original work", TargetRunIDs: []string{target.ID}}
	must(s.SaveRun(ctx, follow))
	temporal := &mocks.Client{}
	status := enums.WORKFLOW_EXECUTION_STATUS_RUNNING
	temporal.On("DescribeWorkflowExecution", mock.Anything, mock.Anything, "").Return(func(_ context.Context, runID, _ string) (*service.DescribeWorkflowExecutionResponse, error) {
		state := status
		if runID == target.ID {
			state = enums.WORKFLOW_EXECUTION_STATUS_COMPLETED
		}
		return &service.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: state}}, nil
	})
	model := &evidenceModel{}
	a := &Activities{Store: s, Model: model, Temporal: temporal}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a)
	review := func(round int) {
		_, err := env.ExecuteActivity(a.ReviewFollowup, FollowupInput{RunID: follow.ID, Round: round})
		must(err)
	}
	review(1)
	first, err := s.Checkpoint(ctx, follow, "review-1")
	must(err)
	if strings.Contains(textJSON(first.Input), follow.ID) {
		t.Fatal("followup depends on itself")
	}
	raw := core.Digest(first.Input)
	text := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	body := text(first.Input)
	if !strings.Contains(body, `"execution":"Running"`) || !strings.Contains(body, "仅代表目标Session接收") || len(first.Refs) != 1 {
		t.Fatal("missing real child state, dispatch boundary or provenance", body)
	}
	status = enums.WORKFLOW_EXECUTION_STATUS_COMPLETED
	must(s.RecordItem(ctx, child.ID, core.ItemResult{ID: "1", State: "succeeded", InputHash: "fixture", Text: "result"}))
	review(1)
	again, err := s.Checkpoint(ctx, follow, "review-1")
	must(err)
	if core.Digest(again.Input) != raw || len(model.Inputs) != 1 {
		t.Fatal("retry changed evidence or regenerated")
	}
	review(2)
	second, err := s.Checkpoint(ctx, follow, "review-2")
	must(err)
	if strings.Contains(text(second.Input), `"execution":"Running"`) || !strings.Contains(text(second.Input), `"succeeded":1`) {
		t.Fatal("new round ignored changed target")
	}
	if _, err = InspectRun(ctx, s, temporal, other, target.ID); !errors.Is(err, core.ErrDenied) {
		t.Fatal("wrong project accessed target", err)
	}
	must(s.SetGrant(ctx, owner, core.Grant{ResourceID: source.ID, ProjectID: p.ProjectID}))
	if _, err = s.Checkpoint(ctx, follow, "review-1"); !errors.Is(err, core.ErrDenied) {
		t.Fatal("revoked checkpoint still readable", err)
	}
}
