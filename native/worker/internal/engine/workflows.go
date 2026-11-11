package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"workbench-native/internal/core"
)

const BatchQueue = "workbench-batch"
const InteractiveQueue = "workbench-interactive"

type BatchInput struct {
	Fence     core.WriteFence `json:"fence"`
	RunID     string          `json:"runId"`
	Cursor    string          `json:"cursor"`
	Processed int             `json:"processed"`
}
type BatchOutput struct {
	RunID     string `json:"runId"`
	Processed int    `json:"processed"`
}
type TaskInput struct {
	RunID string
	Fence core.WriteFence
}
type PageInput struct {
	Fence  core.WriteFence
	RunID  string
	Cursor string
}
type ItemInput struct {
	Fence core.WriteFence
	RunID string
	ID    string
}
type PreparedPage struct {
	IDs  []string
	Next string
}
type FailedItem struct {
	Fence core.WriteFence
	RunID string
	ID    string
	Error string
}
type FollowupInput struct {
	Round      int
	Manual     bool
	RunID      string
	DecisionID string
	Checks     int
	NextAt     time.Time
	Waiting    bool
}
type Decision struct {
	ID       string `json:"id"`
	Answer   string `json:"answer"`
	Revision int64  `json:"revision"`
	Digest   string `json:"digest"`
}

func activityContext(ctx workflow.Context, queue string) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{TaskQueue: queue, StartToCloseTimeout: 3 * time.Minute, HeartbeatTimeout: 45 * time.Second, WaitForCancellation: true, RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, BackoffCoefficient: 2, MaximumInterval: 30 * time.Second, MaximumAttempts: 3, NonRetryableErrorTypes: []string{"Denied", "Invalid", "Conflict", "UnknownDelivery"}}})
}

// Batch coordinates bounded pages; actual content is stored in MySQL, not History.
func Batch(ctx workflow.Context, in BatchInput) (out BatchOutput, err error) {
	defer reportFailure(ctx, in.RunID, &err)()
	ctx = activityContext(ctx, BatchQueue)
	execution := in.Fence.Execution
	if execution == "" {
		execution = workflow.GetInfo(ctx).WorkflowExecution.RunID
	}
	defer finishWrite(ctx, in.RunID, execution, &err)()
	if in.Fence.Epoch == 0 {
		if e := workflow.ExecuteActivity(ctx, "BeginWrite", in.RunID, workflow.GetInfo(ctx).WorkflowExecution.RunID).Get(ctx, &in.Fence); e != nil {
			return BatchOutput{}, e
		}
	}
	var page PreparedPage
	if e := workflow.ExecuteActivity(ctx, "PreparePage", PageInput{RunID: in.RunID, Cursor: in.Cursor, Fence: in.Fence}).Get(ctx, &page); e != nil {
		return BatchOutput{}, e
	}
	// Only stable item identifiers cross the durable history boundary.
	for start := 0; start < len(page.IDs); start += 4 {
		end := start + 4
		if end > len(page.IDs) {
			end = len(page.IDs)
		}
		futures := []workflow.Future{}
		group, cancelGroup := workflow.WithCancel(ctx)
		defer func() {
			cancelGroup()
			wait, _ := workflow.NewDisconnectedContext(ctx)
			for _, pending := range futures {
				_ = pending.Get(wait, nil)
			}
		}()
		for _, id := range page.IDs[start:end] {
			futures = append(futures, workflow.ExecuteActivity(group, "GenerateItem", ItemInput{RunID: in.RunID, ID: id, Fence: in.Fence}))
		}
		for i, f := range futures {
			if e := f.Get(ctx, nil); e != nil {
				var appErr *temporal.ApplicationError
				if temporal.IsCanceledError(e) || (errors.As(e, &appErr) && appErr.Type() == "Denied") {
					cancelGroup()
					wait, _ := workflow.NewDisconnectedContext(ctx)
					for _, pending := range futures {
						_ = pending.Get(wait, nil)
					}
					return BatchOutput{}, e
				}
				if e = workflow.ExecuteActivity(ctx, "RecordFailure", FailedItem{RunID: in.RunID, ID: page.IDs[start+i], Error: e.Error(), Fence: in.Fence}).Get(ctx, nil); e != nil {
					return BatchOutput{}, e
				}
			}
			in.Processed++
		}
		cancelGroup()
	}
	if page.Next != "" {
		in.Cursor = page.Next
		return BatchOutput{}, workflow.NewContinueAsNewError(ctx, Batch, in)
	}
	if e := workflow.ExecuteActivity(ctx, "FinishBatch", TaskInput{in.RunID, in.Fence}).Get(ctx, nil); e != nil {
		return BatchOutput{}, e
	}
	return BatchOutput{RunID: in.RunID, Processed: in.Processed}, nil
}

// A followup sleeps durably; waiting never occupies a Worker process or Agent turn.
func Followup(ctx workflow.Context, in FollowupInput) (err error) {
	defer reportFailure(ctx, in.RunID, &err)()
	ctx = activityContext(ctx, InteractiveQueue)
	for {
		if in.Waiting {
			var decision Decision
			if e := receiveSignal(ctx, "decision", &decision); e != nil {
				return e
			}
			var next FollowupInput
			if e := workflow.ExecuteActivity(ctx, "ContinueFollowup", in, decision).Get(ctx, &next); e != nil {
				return e
			}
			in = next
		} else if !in.NextAt.IsZero() {
			if in.Checks >= 3 && !in.Manual {
				var waiting FollowupInput
				if e := workflow.ExecuteActivity(ctx, "FollowupLimit", in).Get(ctx, &waiting); e != nil {
					return e
				}
				in = waiting
				continue
			}
			if delay := in.NextAt.Sub(workflow.Now(ctx)); delay > 0 {
				if e := workflow.Sleep(ctx, delay); e != nil {
					return e
				}
			}
			if !in.Manual {
				in.Checks++
			}
			in.Round++
			in.Manual = false
			var next FollowupInput
			if e := workflow.ExecuteActivity(ctx, "ReviewFollowup", in).Get(ctx, &next); e != nil {
				return e
			}
			in = next
		} else {
			return nil
		}
	}
}

type ModelRequest struct {
	Lane   string         `json:"lane,omitempty"`
	RunID  string         `json:"runId"`
	ItemID string         `json:"itemId"`
	Method core.Method    `json:"method"`
	Input  map[string]any `json:"input"`
}
type ModelResult struct {
	Text  string         `json:"text"`
	Usage map[string]int `json:"usage,omitempty"`
}
type Model interface {
	Generate(context.Context, ModelRequest) (ModelResult, error)
}
type Activities struct {
	Store    *core.Store
	Model    Model
	Host     AgentHost
	Temporal client.Client
}

func failure(e error) error {
	if errors.Is(e, core.ErrDenied) {
		return temporal.NewNonRetryableApplicationError(e.Error(), "Denied", e)
	}
	if errors.Is(e, core.ErrInvalid) || errors.Is(e, core.ErrConflict) {
		return temporal.NewNonRetryableApplicationError(e.Error(), "Invalid", e)
	}
	return e
}
func (a *Activities) PreparePage(ctx context.Context, in PageInput) (PreparedPage, error) {
	ctx = core.WithFence(ctx, in.Fence)
	r, e := a.Store.ExecutionRun(ctx, in.RunID)
	if e != nil {
		return PreparedPage{}, failure(e)
	}
	if len(r.Sources) == 0 {
		return PreparedPage{}, failure(core.ErrInvalid)
	}
	if r.Destination != "" {
		e = a.Store.BeginSnapshot(ctx, r.Principal, core.Snapshot{ResourceID: r.Destination, Version: r.ID, Dependencies: core.UnionRefs(r.Sources, r.Lineage), SourceWatermark: r.Method.Digest})
		if e != nil {
			return PreparedPage{}, failure(e)
		}
	}
	ids, next, e := a.Store.PreparePage(ctx, r, in.Cursor)
	return PreparedPage{IDs: ids, Next: next}, failure(e)
}
func (a *Activities) GenerateItem(ctx context.Context, in ItemInput) error {
	ctx = core.WithFence(ctx, in.Fence)
	r, e := a.Store.ExecutionRun(ctx, in.RunID)
	if e != nil {
		return failure(e)
	}
	if old, e := a.Store.Item(ctx, r.ID, in.ID); e == nil && old.State == "succeeded" {
		return a.stageOutput(ctx, r, old)
	} else if e != nil && !core.IsMissing(e) {
		return e
	}
	records, e := a.Store.Prepared(ctx, r.ID, in.ID)
	if e != nil {
		return e
	}
	method, e := r.MethodFor(records)
	if e != nil {
		return failure(e)
	}
	hash := core.Digest(struct {
		Method  core.Method
		Records []core.Record
	}{method, records})
	input, e := core.PrepareInput(method, records)
	if e != nil {
		return a.Store.RecordItem(ctx, r.ID, core.ItemResult{ID: in.ID, State: "failed", Error: e.Error(), InputHash: hash})
	}
	done := make(chan struct{})
	defer close(done)
	activity.RecordHeartbeat(ctx, in.ID)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				activity.RecordHeartbeat(ctx, in.ID)
			}
		}
	}()
	fixed, isFixed, e := method.FixedOutput(records)
	if e != nil {
		return failure(e)
	}
	result := ModelResult{Text: fixed}
	if !isFixed {
		result, e = a.Model.Generate(ctx, ModelRequest{Lane: "batch", RunID: r.ID, ItemID: in.ID, Method: method, Input: input})
	}
	if e != nil {
		return e
	}
	if _, e = a.Store.ExecutionRun(ctx, r.ID); e != nil {
		return failure(e)
	}
	if e = method.ValidateOutput(result.Text); e != nil {
		return e
	}
	v := core.ItemResult{ID: in.ID, State: "succeeded", Text: result.Text, InputHash: hash, Usage: result.Usage}
	if e = a.Store.RecordItem(ctx, r.ID, v); e != nil {
		return e
	}
	canonical, e := a.Store.Item(ctx, r.ID, v.ID)
	if e != nil {
		return e
	}
	return a.stageOutput(ctx, r, canonical)
}
func (a *Activities) stageOutput(ctx context.Context, r core.Run, v core.ItemResult) error {
	if r.Destination == "" {
		return nil
	}
	snap, e := a.Store.Snapshot(ctx, r.Destination, r.ID)
	if e != nil {
		return e
	}
	if snap.State == "published" {
		return nil
	}
	records, e := a.Store.Prepared(ctx, r.ID, v.ID)
	if e != nil {
		return e
	}
	method, e := r.MethodFor(records)
	if e != nil {
		return failure(e)
	}
	return failure(a.Store.Stage(ctx, r.Principal, r.Destination, r.ID, []core.Record{{ID: v.ID, Fields: map[string]any{"text": v.Text, "methodDigest": method.Digest, "inputHash": v.InputHash}}}))
}
func (a *Activities) RecordFailure(ctx context.Context, in FailedItem) error {
	ctx = core.WithFence(ctx, in.Fence)
	r, e := a.Store.ExecutionRun(ctx, in.RunID)
	if e != nil {
		return failure(e)
	}
	records, e := a.Store.Prepared(ctx, r.ID, in.ID)
	if e != nil {
		return e
	}
	method, e := r.MethodFor(records)
	if e != nil {
		return a.Store.RecordItem(ctx, r.ID, core.ItemResult{ID: in.ID, State: "failed", Error: in.Error, InputHash: core.Digest([]any{r.Methods, r.MethodPointer, records})})
	}
	hash := core.Digest(struct {
		Method  core.Method
		Records []core.Record
	}{method, records})
	return a.Store.RecordItem(ctx, r.ID, core.ItemResult{ID: in.ID, State: "failed", Error: in.Error, InputHash: hash})
}
func (a *Activities) FinishBatch(ctx context.Context, in TaskInput) error {
	ctx = core.WithFence(ctx, in.Fence)
	r, e := a.Store.ExecutionRun(ctx, in.RunID)
	if e != nil {
		return failure(e)
	}
	stats, e := a.Store.Stats(ctx, r.Principal, r.ID)
	if e != nil {
		return failure(e)
	}
	if stats.Pending != 0 {
		return fmt.Errorf("batch has %d unprocessed records", stats.Pending)
	}
	kind := "batch.completed"
	if stats.Failed > 0 {
		kind = "batch.partial"
	} else if r.Destination != "" {
		if _, e = a.Store.Publish(ctx, r.Principal, r.Destination, r.ID, stats.Succeeded); e != nil {
			return failure(e)
		}
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: r.ID + "-" + kind, SessionID: r.Principal.SessionID, RunID: r.ID, Kind: kind, Text: fmt.Sprintf("本轮处理 %d 条，成功 %d 条，失败 %d 条。", stats.Total, stats.Succeeded, stats.Failed), Details: stats})
}
