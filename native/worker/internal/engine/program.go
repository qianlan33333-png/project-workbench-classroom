package engine

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/workflow"
	"workbench-native/internal/core"
)

type StepRef struct {
	RunID string
	Kind  string
}

func stepID(parent, suffix string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(parent+"/"+suffix)).String()
}
func Program(ctx workflow.Context, id string) (err error) {
	defer reportFailure(ctx, id, &err)()
	ctx = activityContext(ctx, InteractiveQueue)
	var steps []StepRef
	if err = workflow.ExecuteActivity(ctx, "ProgramSyncSteps", id).Get(ctx, &steps); err != nil {
		return err
	}
	for _, step := range steps {
		if err = programChild(ctx, step.RunID, Sync, step.RunID); err != nil {
			return err
		}
	}
	var hasGeneration bool
	if err = workflow.ExecuteActivity(ctx, "ProgramHasGeneration", id).Get(ctx, &hasGeneration); err != nil {
		return err
	}
	if !hasGeneration {
		return workflow.ExecuteActivity(ctx, "ProgramSyncFinished", id).Get(ctx, nil)
	}
	var generation string
	if err = workflow.ExecuteActivity(ctx, "ProgramGeneration", id).Get(ctx, &generation); err != nil {
		return err
	}
	if err = programChild(ctx, generation, Batch, BatchInput{RunID: generation}); err != nil {
		return err
	}
	return workflow.ExecuteActivity(ctx, "ProgramFinished", id, generation).Get(ctx, nil)
}
func (a *Activities) ProgramHasGeneration(ctx context.Context, id string) (bool, error) {
	r, e := a.Store.ExecutionRun(ctx, id)
	return r.Destination != "", failure(e)
}
func (a *Activities) ProgramSyncFinished(ctx context.Context, id string) error {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return failure(e)
	}
	steps, e := a.Store.RunSteps(ctx, r.Principal, id)
	if e != nil {
		return failure(e)
	}
	refs := []core.Ref{}
	count := 0
	for _, step := range steps {
		v, err := a.Store.Snapshot(ctx, step.Destination, step.ID)
		if err != nil {
			return err
		}
		if v.State != "published" {
			return failure(core.ErrInvalid)
		}
		count += v.Count
		for _, section := range v.Sections {
			refs = append(refs, core.Ref{ResourceID: step.Destination, Version: step.ID, Section: section.ID})
		}
	}
	if e = a.Store.PinRunSources(ctx, r.Principal, id, refs); e != nil {
		return failure(e)
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: id + "-published", SessionID: r.Principal.SessionID, RunID: id, Kind: "resource.published", Text: fmt.Sprintf("本轮 %d 项资料更新与校验已完成，共发布 %d 条记录。", len(steps), count)})
}
func programChild(ctx workflow.Context, id string, fn any, input any) error {
	child := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: id, TaskQueue: BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE, WaitForCancellation: true, ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL})
	future := workflow.ExecuteChildWorkflow(child, fn, input)
	e := future.Get(ctx, nil)
	wait, _ := workflow.NewDisconnectedContext(ctx)
	_ = future.Get(wait, nil)
	return e
}
func (a *Activities) ProgramSyncSteps(ctx context.Context, id string) ([]StepRef, error) {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return nil, failure(e)
	}
	if r.Program == nil {
		return nil, failure(core.ErrInvalid)
	}
	steps := []StepRef{}
	for _, source := range r.Program.Sources {
		if !source.Refresh {
			continue
		}
		child := core.Run{ID: stepID(id, "sync/"+source.ResourceID), Principal: r.Principal, ParentRunID: id, Operation: "sync", Title: "更新资料 · " + source.ResourceID, Destination: source.ResourceID, Lineage: r.Lineage}
		if e = a.Store.SaveRun(ctx, child); e != nil {
			return nil, failure(e)
		}
		steps = append(steps, StepRef{child.ID, "sync"})
	}
	return steps, nil
}
func (a *Activities) ProgramGeneration(ctx context.Context, id string) (string, error) {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return "", failure(e)
	}
	if r.Program == nil {
		return "", failure(core.ErrInvalid)
	}
	childID := stepID(id, "generate")
	// An explicit continuation reuses the originally frozen input versions.
	if old, e := a.Store.Run(ctx, r.Principal, childID); e == nil {
		if e = a.Store.PinRunSources(ctx, r.Principal, id, core.UnionRefs(old.Sources, old.Lineage)); e != nil {
			return "", failure(e)
		}
		return old.ID, nil
	} else if !core.IsMissing(e) {
		return "", failure(e)
	}
	refs := []core.Ref{}
	for _, source := range r.Program.Sources {
		version := source.Version
		if source.Refresh {
			version = stepID(id, "sync/"+source.ResourceID)
		}
		if version == "" {
			resource, e := a.Store.Resource(ctx, source.ResourceID)
			if e != nil {
				return "", e
			}
			version = resource.CurrentVersion
		}
		for _, section := range source.Sections {
			ref := core.Ref{ResourceID: source.ResourceID, Version: version, Section: section}
			if e = core.Authorize(ctx, a.Store, r.Principal, ref); e != nil {
				return "", failure(e)
			}
			refs = append(refs, ref)
		}
	}
	if e = a.Store.RememberRead(ctx, r.Principal, refs); e != nil {
		return "", failure(e)
	}
	child := core.Run{ID: childID, Principal: r.Principal, ParentRunID: id, Operation: "generate", Title: r.Title, Destination: r.Destination, Sources: refs, Lineage: r.Lineage, Method: r.Method, Selection: r.Selection}
	if e = a.Store.SaveRun(ctx, child); e != nil {
		return "", failure(e)
	}
	if e = a.Store.PinRunSources(ctx, r.Principal, id, core.UnionRefs(child.Sources, child.Lineage)); e != nil {
		return "", failure(e)
	}
	return childID, nil
}
func (a *Activities) ProgramFinished(ctx context.Context, id, generation string) error {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return failure(e)
	}
	child, e := a.Store.Run(ctx, r.Principal, generation)
	if e != nil {
		return failure(e)
	}
	refs, e := a.Store.ActualRunSources(ctx, generation)
	if e != nil {
		return e
	}
	for _, ref := range core.UnionRefs(child.Sources, refs) {
		if _, e = a.Store.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_run_sources VALUES(?,?,?,?)`, id, ref.ResourceID, ref.Version, ref.Section); e != nil {
			return e
		}
	}
	stats, e := a.Store.Stats(ctx, r.Principal, generation)
	if e != nil {
		return e
	}
	kind := "batch.completed"
	if stats.Failed > 0 {
		kind = "batch.partial"
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: id + "-" + kind, SessionID: r.Principal.SessionID, RunID: id, Kind: kind, Text: fmt.Sprintf("本轮资料更新与生成已结束：共 %d 条，成功 %d 条，失败 %d 条。", stats.Total, stats.Succeeded, stats.Failed), Details: map[string]any{"generationRunId": generation, "stats": stats}})
}
