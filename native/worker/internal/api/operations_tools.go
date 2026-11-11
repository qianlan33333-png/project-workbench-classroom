package api

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"time"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type RefreshDataInput struct {
	OperationKey string                   `json:"operationKey"`
	Sources      []core.ResourceSelection `json:"sources"`
	Destination  string                   `json:"destination"`
}
type SelectStrategyInput struct {
	StrategyKeys []string `json:"strategyKeys,omitempty"`
	Limit        int      `json:"limit,omitempty"`
	OperationKey string   `json:"operationKey"`
	Data         core.Ref `json:"data"`
	Strategies   core.Ref `json:"strategies"`
	History      core.Ref `json:"history"`
	Destination  string   `json:"destination"`
	AsOf         string   `json:"asOf"`
}

func (a *Server) submitOperation(ctx context.Context, p core.Principal, r core.Run) (*mcp.CallToolResult, RunRef, error) {
	if p.Kind != "project" || !core.ValidID(r.ID) || !core.ValidID(r.Destination) {
		return nil, RunRef{}, core.ErrDenied
	}
	if e := a.Store.SaveRun(ctx, r); e != nil {
		return nil, RunRef{}, e
	}
	operation := r.Operation
	if core.IsLocalOperations(r) {
		operation = "operations_local_review"
	}
	_, e := a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: engine.BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.WriteTask, engine.WriteInput{RunID: r.ID, ProjectID: p.ProjectID, Operation: operation})
	return nil, RunRef{RunID: r.ID}, accepted(e)
}
func (a *Server) refreshOperations(ctx context.Context, p core.Principal, in RefreshDataInput) (*mcp.CallToolResult, RunRef, error) {
	if !core.ValidID(in.OperationKey) || len(in.Sources) == 0 || len(in.Sources) > 16 {
		return nil, RunRef{}, core.ErrInvalid
	}
	seen := map[string]bool{}
	for _, source := range in.Sources {
		if !source.Refresh || seen[source.ResourceID] || len(source.Sections) != 1 {
			return nil, RunRef{}, core.ErrInvalid
		}
		seen[source.ResourceID] = true
		resource, e := a.Store.Resource(ctx, source.ResourceID)
		if e != nil {
			return nil, RunRef{}, e
		}
		if resource.OwnerProject != p.ProjectID && resource.MaintainerProject != p.ProjectID {
			return nil, RunRef{}, core.ErrDenied
		}
		found := false
		for _, section := range resource.Sections {
			if section.ID == source.Sections[0] {
				found = true
			}
		}
		if !found {
			return nil, RunRef{}, core.ErrInvalid
		}
	}
	// The first source is the keyed raw stream. Remaining registered streams
	// refresh read-only history inputs in the same frozen collection step.
	r := core.Run{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/"+in.OperationKey)).String(), Principal: p, ExplicitInputs: true, Operation: "data_refresh", Title: "更新资料", Destination: in.Destination, Operations: &core.OperationsSpec{Refresh: in.Sources}}
	result, out, e := a.submitOperation(ctx, p, r)
	if e == nil {
		for _, source := range in.Sources {
			out.InputRefs = append(out.InputRefs, core.Ref{ResourceID: source.ResourceID, Version: uuid.NewSHA1(uuid.NameSpaceURL, []byte(r.ID+"/sync/"+source.ResourceID)).String(), Section: source.Sections[0]})
		}
	}
	return result, out, e
}
func (a *Server) strategyTools(s *mcp.Server, p core.Principal) {
	mcp.AddTool(s, &mcp.Tool{Name: "strategy_select", Description: "Apply the original published strategies, cooldown and priority rules to fixed data and complete contact history. Destination contains only selected recipients; run_items retains exclusion reasons. Never calls a model or sends."}, func(ctx context.Context, _ *mcp.CallToolRequest, in SelectStrategyInput) (*mcp.CallToolResult, RunRef, error) {
		if !core.ValidID(in.OperationKey) {
			return nil, RunRef{}, core.ErrInvalid
		}
		if in.Limit < 0 || in.Limit > 5000 {
			return nil, RunRef{}, core.ErrInvalid
		}
		stamp, e := time.Parse(time.RFC3339, in.AsOf)
		if e != nil {
			return nil, RunRef{}, core.ErrInvalid
		}
		refs := []core.Ref{in.Data, in.Strategies, in.History}
		for _, ref := range refs {
			if e := ref.Validate(); e != nil {
				return nil, RunRef{}, e
			}
		}
		r := core.Run{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/"+in.OperationKey)).String(), Principal: p, ExplicitInputs: true, Operation: "strategy_select", Title: "原策略选单", Sources: refs, Destination: in.Destination, Operations: &core.OperationsSpec{AsOf: stamp.Format(time.RFC3339), StrategyKeys: in.StrategyKeys, Limit: in.Limit}}
		return a.submitOperation(ctx, p, r)
	})
}

func (a *Server) prepareLocalOperations(ctx context.Context, p core.Principal, in CRMPushInput) (*mcp.CallToolResult, RunRef, error) {
	if p.Kind != "project" || !core.ValidID(in.OperationKey) || in.Review == nil || len(in.Review.Drafts) == 0 || len(in.Review.Drafts) > 3 {
		return nil, RunRef{}, core.ErrInvalid
	}
	cfg, e := a.readOperations(ctx, p.ProjectID)
	if e != nil || !cfg.ReadSource || p != operationsPrincipal(p.ProjectID) {
		return nil, RunRef{}, core.ErrDenied
	}
	selected, e := a.Store.Run(ctx, p, in.Review.Selection.Version)
	if e != nil || selected.Operation != "strategy_select" || selected.Operations == nil || len(selected.Operations.StrategyKeys) != 1 || selected.Destination != in.Review.Selection.ResourceID {
		return nil, RunRef{}, core.ErrInvalid
	}
	if len(selected.Sources) != 3 {
		return nil, RunRef{}, core.ErrInvalid
	}
	clean, e := a.Store.Run(ctx, p, selected.Sources[0].Version)
	if e != nil || clean.Operation != "data_refresh" || clean.Operations == nil || len(clean.Operations.Refresh) == 0 || (cfg.Cycle == nil || len(cfg.Cycle.Sources) != 1 || clean.Operations.Refresh[0].ResourceID != cfg.Cycle.Sources[0].ResourceID) {
		return nil, RunRef{}, core.ErrDenied
	}
	cycle := cfg.Cycle
	if len(cycle.StrategyKeys) != 1 || selected.Operations.StrategyKeys[0] != cycle.StrategyKeys[0] || selected.Destination != cycle.SelectedDestination || clean.Destination != cycle.CleanDestination || cycle.MethodSource == nil {
		return nil, RunRef{}, core.ErrDenied
	}
	if cycle.HistorySource == nil || cycle.StrategySource == nil || in.Review.Selection.Section != cycle.Section {
		return nil, RunRef{}, core.ErrDenied
	}
	frozen, e := a.Store.Checkpoint(ctx, clean, "operations-input")
	if e != nil {
		return nil, RunRef{}, e
	}
	stamp, _ := frozen.Input["asOf"].(string)
	sourceTime, sourceErr := time.Parse(time.RFC3339Nano, stamp)
	selectedTime, selectedErr := time.Parse(time.RFC3339Nano, selected.Operations.AsOf)
	if sourceErr != nil || selectedErr != nil || selectedTime.Unix() != sourceTime.Unix() {
		return nil, RunRef{}, core.ErrDenied
	}
	expectedSources := []core.Ref{{ResourceID: cycle.CleanDestination, Version: clean.ID, Section: cycle.Section}}
	for _, source := range []*core.ResourceSelection{cycle.StrategySource, cycle.HistorySource} {
		expectedSources = append(expectedSources, core.Ref{ResourceID: source.ResourceID, Version: uuid.NewSHA1(uuid.NameSpaceURL, []byte(clean.ID+"/sync/"+source.ResourceID)).String(), Section: cycle.Section})
	}
	for i, expected := range expectedSources {
		if selected.Sources[i] != expected {
			return nil, RunRef{}, core.ErrDenied
		}
	}
	methodVersion := uuid.NewSHA1(uuid.NameSpaceURL, []byte(clean.ID+"/sync/"+cycle.MethodSource.ResourceID)).String()
	for _, ref := range in.Review.Drafts {
		generated, err := a.Store.Run(ctx, p, ref.Version)
		if err != nil || generated.Destination != cycle.DraftDestination || generated.MethodPointer != "/actions/0/generation/skill_key" || len(generated.MethodRefs) != len(cycle.MethodRefs) {
			return nil, RunRef{}, core.ErrDenied
		}
		for key, expected := range cycle.MethodRefs {
			actual, exists := generated.MethodRefs[key]
			if !exists || actual.ID != expected.ID || actual.Ref.ResourceID != expected.Ref.ResourceID || actual.Ref.Section != expected.Ref.Section || actual.Ref.Version != methodVersion {
				return nil, RunRef{}, core.ErrDenied
			}
		}
	}
	at, e := time.Parse(time.RFC3339, selected.Operations.AsOf)
	if e != nil {
		return nil, RunRef{}, e
	}
	zone, _ := time.LoadLocation("Asia/Shanghai")
	at = at.In(zone)
	year, week := at.ISOWeek()
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/"+in.OperationKey)).String()
	refs := append([]core.Ref{in.Review.Selection}, in.Review.Drafts...)
	if e = core.AuthorizeAll(ctx, a.Store, p, refs); e != nil {
		return nil, RunRef{}, e
	}
	run := core.Run{ID: id, Principal: p, Operation: "operations_package", Title: "原策略整批稿件", Quiet: true, ExplicitInputs: true, Destination: cfg.Input.ResourceID, Sources: refs, Operations: &core.OperationsSpec{Review: in.Review}, Shared: map[string]any{"local": true, "day": at.Format("2006-01-02"), "strategy": selected.Operations.StrategyKeys[0], "scene": "原策略运营", "package": "weekpkg-" + id, "week": fmt.Sprintf("%04d-W%02d", year, week)}}
	return a.submitOperation(ctx, p, run)
}
