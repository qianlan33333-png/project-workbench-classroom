package engine

import (
	"context"
	"fmt"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/workflow"
	"slices"
	"time"
	"workbench-native/internal/core"
)

type DispatchBatchInput struct {
	RunID, After       string
	Prepared, Approved bool
}
type DispatchPage struct {
	Next string
	IDs  []string
}

func DispatchBatch(ctx workflow.Context, in DispatchBatchInput) (err error) {
	defer reportFailure(ctx, in.RunID, &err)()
	ctx = activityContext(ctx, InteractiveQueue)
	if !in.Prepared {
		for pages := 0; pages < 10; pages++ {
			var page DispatchPage
			if err = workflow.ExecuteActivity(ctx, "PrepareDispatchPage", in.RunID, in.After).Get(ctx, &page); err != nil {
				return err
			}
			in.After = page.Next
			if page.Next == "" {
				in.Prepared = true
				in.After = ""
				break
			}
		}
		if !in.Prepared {
			return workflow.NewContinueAsNewError(ctx, DispatchBatch, in)
		}
	}
	if !in.Approved {
		if err = workflow.ExecuteActivity(ctx, "PrepareDispatchReview", in.RunID).Get(ctx, &in.Approved); err != nil {
			return err
		}
		for !in.Approved {
			var answer Decision
			if err = receiveSignal(ctx, "decision", &answer); err != nil {
				return err
			}
			if err = workflow.ExecuteActivity(ctx, "ConfirmMessage", in.RunID, answer).Get(ctx, &in.Approved); err != nil {
				return err
			}
		}
	}
	// Four bounded child sends at a time; each preserves its own real receipt.
	for pages := 0; pages < 5; pages++ {
		var page DispatchPage
		if err = workflow.ExecuteActivity(ctx, "DispatchDeliveryPage", in.RunID, in.After).Get(ctx, &page); err != nil {
			return err
		}
		for start := 0; start < len(page.IDs); start += 4 {
			group, cancel := workflow.WithCancel(ctx)
			futures := []workflow.ChildWorkflowFuture{}
			for _, id := range page.IDs[start:min(start+4, len(page.IDs))] {
				child := workflow.WithChildOptions(group, workflow.ChildWorkflowOptions{WorkflowID: id, TaskQueue: InteractiveQueue, WaitForCancellation: true, ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL})
				futures = append(futures, workflow.ExecuteChildWorkflow(child, SubmitMessage, id))
			}
			for _, f := range futures {
				if e := f.Get(ctx, nil); e != nil && err == nil {
					err = e
					cancel()
				}
			}
			cancel()
			wait, _ := workflow.NewDisconnectedContext(ctx)
			for _, f := range futures {
				_ = f.Get(wait, nil)
			}
			if err != nil {
				return err
			}
		}
		in.After = page.Next
		if in.After == "" {
			return workflow.ExecuteActivity(ctx, "DispatchSubmitted", in.RunID).Get(ctx, nil)
		}
	}
	return workflow.NewContinueAsNewError(ctx, DispatchBatch, in)
}
func SubmitMessage(ctx workflow.Context, id string) error {
	ctx = activityContext(ctx, InteractiveQueue)
	for checks := 0; checks < 100; checks++ {
		var receipt Receipt
		if err := workflow.ExecuteActivity(ctx, "SendDelivery", id).Get(ctx, &receipt); err != nil {
			return err
		}
		if receipt.State != "prepared" {
			// A fast gateway callback can precede persistence of its provider ID.
			// Check that same ID briefly; the sweep remains the fallback.
			for delay := 2 * time.Second; receipt.AwaitPartReceipt && delay <= 8*time.Second; delay *= 2 {
				if err := workflow.Sleep(ctx, delay); err != nil {
					return err
				}
				receipt = Receipt{}
				if err := workflow.ExecuteActivity(ctx, "ReconcileDelivery", id).Get(ctx, &receipt); err != nil {
					return err
				}
			}
			return nil
		}
		// No external call has been claimed. Timer-based waiting preserves the
		// delivery identity and does not consume the Activity failure budget.
		if err := workflow.Sleep(ctx, 30*time.Second); err != nil {
			return err
		}
	}
	return workflow.NewContinueAsNewError(ctx, SubmitMessage, id)
}
func (a *Activities) PrepareDispatchPage(ctx context.Context, id, after string) (DispatchPage, error) {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return DispatchPage{}, failure(e)
	}
	if r.Dispatch == nil {
		return DispatchPage{}, failure(core.ErrInvalid)
	}
	plan := r.Dispatch
	page, e := a.Store.Read(ctx, r.Principal, plan.Source, after, 200)
	if e != nil {
		return DispatchPage{}, failure(e)
	}
	validator, e := (core.Method{OutputSchema: plan.EligibilitySchema}).ResolveOutput()
	if e != nil {
		return DispatchPage{}, failure(e)
	}
	ids := []string{}
	for _, v := range page.Records {
		ids = append(ids, v.ID)
	}
	eligibility, e := a.Store.ReadIDs(ctx, r.Principal, plan.Eligibility, ids)
	if e != nil {
		return DispatchPage{}, failure(e)
	}
	byID := map[string]core.Record{}
	for _, v := range eligibility {
		byID[v.ID] = v
	}
	for _, v := range page.Records {
		if len(plan.RecordIDs) > 0 && !slices.Contains(plan.RecordIDs, v.ID) {
			continue
		}
		eligible, ok := byID[v.ID]
		valid := ok && validator.Validate(eligible.Fields) == nil
		if !valid {
			if e = a.Store.RecordItem(ctx, r.ID, core.ItemResult{ID: v.ID, State: "excluded", InputHash: core.Digest(v), Error: "固定来源资料未满足资格 Schema，缺失资料也排除。"}); e != nil {
				return DispatchPage{}, e
			}
			continue
		}
		text, target, e := plan.Content(v.Fields, eligible.Fields)
		if e != nil {
			return DispatchPage{}, failure(fmt.Errorf("%w: manifest record %s", e, v.ID))
		}
		if e = a.Store.PutDelivery(ctx, r, core.Delivery{ID: core.DeliveryItemID(id, v.ID), RunID: id, ItemID: v.ID, ConnectionID: plan.ConnectionID, Target: target, Text: text}); e != nil {
			return DispatchPage{}, failure(e)
		}
	}
	return DispatchPage{Next: page.Next}, nil
}
func (a *Activities) PrepareDispatchReview(ctx context.Context, id string) (bool, error) {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return false, failure(e)
	}
	d, e := a.Store.DispatchReview(ctx, r)
	if e != nil {
		return false, failure(e)
	}
	if d.State == "answered" {
		return a.ConfirmMessage(ctx, id, Decision{ID: d.ID, Revision: d.Revision, Digest: d.Digest})
	}
	if d.State != "waiting" {
		return false, failure(core.ErrConflict)
	}
	return false, nil
}
func (a *Activities) DispatchDeliveryPage(ctx context.Context, id, after string) (DispatchPage, error) {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return DispatchPage{}, failure(e)
	}
	rows, e := a.Store.Deliveries(ctx, r.Principal, id, after)
	if e != nil {
		return DispatchPage{}, failure(e)
	}
	out := DispatchPage{}
	for _, v := range rows {
		out.IDs = append(out.IDs, v.ID)
	}
	if len(rows) == 200 {
		out.Next = rows[len(rows)-1].ItemID
	}
	return out, nil
}
func (a *Activities) DispatchSubmitted(ctx context.Context, id string) error {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return failure(e)
	}
	stats, e := a.Store.DeliveryStats(ctx, r.Principal, id)
	if e != nil {
		return e
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: id + "-submitted", SessionID: r.Principal.SessionID, RunID: id, Kind: "message.batch_submitted", Text: fmt.Sprintf("本批 %d 条已完成提交处理。实际发送与送达请看逐条渠道回执；待手机确认与未知结果仍未视为发送成功。", stats.Total), Details: stats})
}
