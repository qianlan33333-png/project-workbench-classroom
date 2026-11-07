package engine

import (
	"context"
	"fmt"
	"go.temporal.io/sdk/workflow"
	"time"
	"workbench-native/internal/core"
)

func Daily(ctx workflow.Context, p core.Principal) error {
	id := workflow.GetInfo(ctx).WorkflowExecution.RunID
	ctx = activityContext(ctx, InteractiveQueue)
	return workflow.ExecuteActivity(ctx, "DailySummary", id, p, workflow.GetInfo(ctx).WorkflowExecution.ID).Get(ctx, nil)
}
func (a *Activities) DailySummary(ctx context.Context, id string, p core.Principal, workflowID string) error {
	stop := keepAlive(ctx)
	defer stop()
	if p.Kind != "personal" {
		return failure(core.ErrDenied)
	}
	r := core.Run{ID: id, WorkflowID: workflowID, Principal: p, Operation: "daily", Title: "今日安排"}
	if e := a.Store.SaveRun(ctx, r); e != nil {
		return failure(e)
	}
	projects, e := a.Store.Projects(ctx)
	if e != nil {
		return e
	}
	runs, e := Overview(ctx, a.Store, a.Temporal, p)
	if e != nil {
		return e
	}
	decisions, e := a.Store.Decisions(ctx, p)
	if e != nil {
		return e
	}
	memories, e := a.Store.Memories(ctx, p)
	if e != nil {
		return e
	}
	zone, _ := time.LoadLocation("Asia/Shanghai")
	facts := map[string]any{"asOf": time.Now().In(zone).Format(time.RFC3339), "timeZone": "Asia/Shanghai", "projects": projects, "recentRuns": runs, "waitingForOwner": decisions, "personalRules": memories}
	checkpoint, e := a.Store.Checkpoint(ctx, r, "daily-input")
	if core.IsMissing(e) {
		refs, err := a.Store.SessionSources(ctx, p)
		if err != nil {
			return err
		}
		checkpoint, e = a.Store.FreezeCheckpoint(ctx, r, "daily-input", core.Checkpoint{Refs: refs, Input: facts})
	}
	if e != nil {
		return failure(e)
	}
	method := core.Method{Name: "daily-summary", Instructions: "你是个人助理。仅根据任务记录、本人规则和待决定事项，给出今天的简短安排。区分已有事实、未核实状态与建议；不能把任务存在当作执行成功。不要执行或承诺已派发任何工作。资料不足就说明。", MaxInputBytes: 128 * 1024, MaxOutputTokens: 2000}
	v, e := a.Store.Item(ctx, id, "summary")
	if core.IsMissing(e) {
		result, err := a.Model.Generate(ctx, ModelRequest{RunID: id, ItemID: "summary", Method: method, Input: checkpoint.Input})
		if err != nil {
			return err
		}
		v = core.ItemResult{ID: "summary", State: "succeeded", Text: result.Text, InputHash: core.Digest(id)}
		if e = a.Store.RecordItem(ctx, id, v); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	v, e = a.Store.Item(ctx, id, "summary")
	if e != nil {
		return e
	}
	if _, e = a.Store.Checkpoint(ctx, r, "daily-input"); e != nil {
		return failure(e)
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: fmt.Sprintf("%s-daily", id), SessionID: p.SessionID, RunID: id, Kind: "personal.daily", Text: v.Text})
}
