package engine

import (
	"context"
	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/workflow"
	"workbench-native/internal/core"
)

// A typed Temporal entry point, not a user-defined workflow interpreter.
func ScheduledProgram(ctx workflow.Context, template core.Run) error {
	id := workflow.GetInfo(ctx).WorkflowExecution.RunID
	if e := workflow.ExecuteActivity(activityContext(ctx, InteractiveQueue), "CreateScheduledRun", id, template).Get(ctx, nil); e != nil {
		return e
	}
	child := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: id, TaskQueue: BatchQueue, WaitForCancellation: true, ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL})
	f := workflow.ExecuteChildWorkflow(child, WriteTask, WriteInput{RunID: id, ProjectID: template.Principal.ProjectID, Operation: template.Operation})
	e := f.Get(ctx, nil)
	wait, _ := workflow.NewDisconnectedContext(ctx)
	_ = f.Get(wait, nil)
	return e
}
func (a *Activities) CreateScheduledRun(ctx context.Context, id string, r core.Run) error {
	if _, e := uuid.Parse(id); e != nil {
		return failure(core.ErrInvalid)
	}
	r.ID = id
	return failure(a.Store.SaveRun(ctx, r))
}
