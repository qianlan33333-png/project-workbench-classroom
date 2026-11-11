package engine

import (
	"context"
	"encoding/json"
	"go.temporal.io/sdk/workflow"
	"time"
	"workbench-native/internal/core"
	"workbench-native/internal/operations"
)

type OperationsPage struct {
	RunID, After string
	Fence        core.WriteFence
}
type OperationsPageResult struct {
	Next  string
	Count int
}

// OperationsTask owns only a deterministic source-clean/select step. Generation,
// approval, CLI submission and receipts retain their existing implementations.
func OperationsTask(ctx workflow.Context, id string) (err error) {
	defer reportFailure(ctx, id, &err)()
	ctx = activityContext(ctx, BatchQueue)
	var syncs []StepRef
	if err = workflow.ExecuteActivity(ctx, "OperationsSyncSteps", id).Get(ctx, &syncs); err != nil {
		return err
	}
	for _, step := range syncs {
		if err = programChild(ctx, step.RunID, Sync, step.RunID); err != nil {
			return err
		}
	}
	if err = workflow.ExecuteActivity(ctx, "OperationsPinSources", id).Get(ctx, nil); err != nil {
		return err
	}
	execution := workflow.GetInfo(ctx).WorkflowExecution.RunID
	defer finishWrite(ctx, id, execution, &err)()
	var fence core.WriteFence
	if err = workflow.ExecuteActivity(ctx, "BeginWrite", id, execution).Get(ctx, &fence); err != nil {
		return err
	}
	after := ""
	for {
		var page OperationsPageResult
		if err = workflow.ExecuteActivity(ctx, "OperationsProcessPage", OperationsPage{id, after, fence}).Get(ctx, &page); err != nil {
			return err
		}
		after = page.Next
		if after == "" {
			break
		}
	}
	return workflow.ExecuteActivity(ctx, "OperationsPublish", TaskInput{id, fence}).Get(ctx, nil)
}
func (a *Activities) OperationsSyncSteps(ctx context.Context, id string) ([]StepRef, error) {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return nil, failure(e)
	}
	if r.Operations == nil {
		return nil, failure(core.ErrInvalid)
	}
	out := []StepRef{}
	for _, source := range r.Operations.Refresh {
		child := core.Run{ID: stepID(id, "sync/"+source.ResourceID), ParentRunID: id, Principal: r.Principal, ExplicitInputs: true, Operation: "sync", Destination: source.ResourceID, Title: "读取原始来源 · " + source.ResourceID, Lineage: r.Lineage}
		if r.Operations.Independent {
			child.Shared = map[string]any{"independentOperations": true}
		}
		if e = a.Store.SaveRun(ctx, child); e != nil {
			return nil, failure(e)
		}
		out = append(out, StepRef{child.ID, "sync"})
	}
	return out, nil
}
func (a *Activities) OperationsPinSources(ctx context.Context, id string) error {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return failure(e)
	}
	refs := append([]core.Ref{}, r.Sources...)
	for _, source := range r.Operations.Refresh {
		for _, section := range source.Sections {
			refs = append(refs, core.Ref{ResourceID: source.ResourceID, Version: stepID(id, "sync/"+source.ResourceID), Section: section})
		}
	}
	if len(refs) == 0 {
		return failure(core.ErrInvalid)
	}
	// Persist the input once, reusing the existing immutable checkpoint on retry.
	// Freeze normalization time after collection. Long source reads must not
	// acquire a generated_at earlier than the facts they have just collected.
	_, e = a.Store.FreezeCheckpoint(ctx, r, "operations-input", core.Checkpoint{Refs: refs, Input: map[string]any{"asOf": time.Now().UTC().Format(time.RFC3339Nano)}})
	return failure(e)
}
func (a *Activities) OperationsProcessPage(ctx context.Context, in OperationsPage) (OperationsPageResult, error) {
	ctx = core.WithFence(ctx, in.Fence)
	stop := keepAlive(ctx)
	defer stop()
	r, e := a.Store.ExecutionRun(ctx, in.RunID)
	if e != nil {
		return OperationsPageResult{}, failure(e)
	}
	if existing, err := a.Store.Snapshot(ctx, r.Destination, r.ID); err == nil && existing.State == "published" {
		return OperationsPageResult{Count: existing.Count}, nil
	} else if err != nil && !core.IsMissing(err) {
		return OperationsPageResult{}, failure(err)
	}
	frozen, e := a.Store.Checkpoint(ctx, r, "operations-input")
	if e != nil {
		return OperationsPageResult{}, failure(e)
	}
	if len(frozen.Refs) == 0 {
		return OperationsPageResult{}, failure(core.ErrInvalid)
	}
	dependencies := frozen.Refs
	if r.Operation == "data_refresh" {
		// History, strategies and methods were refreshed for the caller. The
		// archive cleaner consumes only the first raw stream; a method edit must
		// not make unchanged customer facts stale.
		dependencies = frozen.Refs[:1]
	}
	if e = a.Store.BeginSnapshot(ctx, r.Principal, core.Snapshot{ResourceID: r.Destination, Version: r.ID, Dependencies: core.UnionRefs(dependencies, r.Lineage), SourceWatermark: r.ID}); e != nil {
		return OperationsPageResult{}, failure(e)
	}
	page, e := a.Store.Read(ctx, r.Principal, frozen.Refs[0], in.After, 200)
	if e != nil {
		return OperationsPageResult{}, failure(e)
	}
	stamp := r.Operations.AsOf
	if stamp == "" {
		stamp, _ = frozen.Input["asOf"].(string)
	}
	if stamp == "" {
		var at time.Time
		if e = a.Store.DB.QueryRowContext(ctx, `SELECT created_at FROM wb_runs WHERE id=?`, r.ID).Scan(&at); e != nil {
			return OperationsPageResult{}, e
		}
		stamp = at.UTC().Format(time.RFC3339Nano)
	}
	request := map[string]any{"action": r.Operation, "records": page.Records, "asOf": stamp, "strategyKeys": r.Operations.StrategyKeys}
	if r.Operation == "strategy_select" {
		if len(frozen.Refs) != 3 {
			return OperationsPageResult{}, failure(core.ErrInvalid)
		}
		policies := []core.Record{}
		after := ""
		for {
			p, err := a.Store.Read(ctx, r.Principal, frozen.Refs[1], after, 200)
			if err != nil {
				return OperationsPageResult{}, failure(err)
			}
			policies = append(policies, p.Records...)
			if p.Next == "" {
				break
			}
			after = p.Next
		}
		ids := []string{}
		for _, record := range page.Records {
			ids = append(ids, record.ID)
		}
		history, err := a.Store.ReadIDs(ctx, r.Principal, frozen.Refs[2], ids)
		if err != nil {
			return OperationsPageResult{}, failure(err)
		}
		history, err = a.operationsHistory(ctx, r, page.Records, history, stamp)
		if err != nil {
			return OperationsPageResult{}, failure(err)
		}
		request["strategies"], request["histories"] = policies, history
	}
	var out struct {
		Records []core.Record `json:"records"`
	}
	call, done := context.WithTimeout(ctx, 2*time.Minute)
	defer done()
	if e = operations.Evaluate(call, request, &out); e != nil {
		return OperationsPageResult{}, failure(e)
	}
	if len(out.Records) != len(page.Records) {
		return OperationsPageResult{}, failure(core.ErrInvalid)
	}
	for i, record := range out.Records {
		if record.ID != page.Records[i].ID {
			return OperationsPageResult{}, failure(core.ErrInvalid)
		}
	}
	if e = core.AuthorizeAll(ctx, a.Store, r.Principal, frozen.Refs); e != nil {
		return OperationsPageResult{}, failure(e)
	}
	staged := []core.Record{}
	for _, record := range out.Records {
		if r.Operation != "strategy_select" || record.Fields["selected"] == true {
			staged = append(staged, record)
		}
	}
	if e = a.Store.Stage(ctx, r.Principal, r.Destination, r.ID, staged); e != nil {
		return OperationsPageResult{}, failure(e)
	}
	for _, record := range out.Records {
		summary, _ := json.Marshal(map[string]any{"selected": record.Fields["selected"], "reason": record.Fields["reason"], "exclusions": record.Fields["exclusions"]})
		if e = a.Store.RecordItem(ctx, r.ID, core.ItemResult{ID: record.ID, State: "succeeded", Text: string(summary), InputHash: core.Digest(record)}); e != nil {
			return OperationsPageResult{}, failure(e)
		}
	}
	return OperationsPageResult{Next: page.Next, Count: len(out.Records)}, nil
}
func (a *Activities) OperationsPublish(ctx context.Context, in TaskInput) error {
	ctx = core.WithFence(ctx, in.Fence)
	r, e := a.Store.ExecutionRun(ctx, in.RunID)
	if e != nil {
		return failure(e)
	}
	if r.Operation == "strategy_select" {
		if e = a.Store.TrimOperationsSelection(ctx, r); e != nil {
			return failure(e)
		}
	}
	var count int
	if e = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_records WHERE resource_id=? AND version=?`, r.Destination, r.ID).Scan(&count); e != nil {
		return e
	}
	v, e := a.Store.Publish(ctx, r.Principal, r.Destination, r.ID, count)
	if e != nil {
		return failure(e)
	}
	if e = a.Store.CleanupOperationsSources(ctx, r); e != nil {
		return e
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: r.ID + "-published", SessionID: r.Principal.SessionID, RunID: r.ID, Kind: "resource.published", Text: "本步骤已完成，结果可交给下一工具。", Details: v})
}

func (a *Activities) OperationsRefreshClock(ctx context.Context, p core.Principal, id string) (string, error) {
	r, e := a.Store.Run(ctx, p, id)
	if e != nil {
		return "", failure(e)
	}
	v, e := a.Store.Checkpoint(ctx, r, "operations-input")
	if e != nil {
		return "", failure(e)
	}
	stamp, ok := v.Input["asOf"].(string)
	if !ok {
		return "", failure(core.ErrInvalid)
	}
	return stamp, nil
}
