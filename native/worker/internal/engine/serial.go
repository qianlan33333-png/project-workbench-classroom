package engine

import (
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"time"
	"workbench-native/internal/core"
)

type WriteInput struct{ RunID, ProjectID, Operation string }

func ProjectWriterID(project string) string { return "writer-" + project }

// Temporal itself enforces one open child ID per project. Waiting uses durable
// timers and bounded histories; there is no separate lease or recovery engine.
func WriteTask(ctx workflow.Context, in WriteInput) (err error) {
	childStarted := false
	defer func() {
		if !childStarted {
			reportFailure(ctx, in.RunID, &err)()
		}
	}()
	if !core.ValidID(in.ProjectID) {
		return failure(core.ErrInvalid)
	}
	child := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: ProjectWriterID(in.ProjectID), TaskQueue: BatchQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		WaitForCancellation:   true, ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
	})
	for attempt := 0; attempt < 40; attempt++ {
		var future workflow.ChildWorkflowFuture
		switch in.Operation {
		case "generate":
			future = workflow.ExecuteChildWorkflow(child, Batch, BatchInput{RunID: in.RunID})
		case "pipeline":
			future = workflow.ExecuteChildWorkflow(child, Program, in.RunID)
		case "data_refresh", "strategy_select":
			future = workflow.ExecuteChildWorkflow(child, OperationsTask, in.RunID)
		case "sync":
			future = workflow.ExecuteChildWorkflow(child, Sync, in.RunID)
		case "operations_local_review":
			future = workflow.ExecuteChildWorkflow(child, LocalOperationsReview, in.RunID)
		case "operations_package":
			future = workflow.ExecuteChildWorkflow(child, OperationsPackage, in.RunID)
		case "operations_package_confirm":
			future = workflow.ExecuteChildWorkflow(child, OperationsPackageConfirm, in.RunID)
		case "operations_batch_confirm":
			future = workflow.ExecuteChildWorkflow(child, ConfirmOperationsBatch, in.RunID)
		default:
			return failure(core.ErrInvalid)
		}
		err = future.GetChildWorkflowExecution().Get(ctx, nil)
		if err != nil {
			if temporal.IsWorkflowExecutionAlreadyStartedError(err) {
				if err = workflow.Sleep(ctx, 15*time.Second); err != nil {
					return err
				}
				continue
			}
			// A cancellation may race with child-start acknowledgement. Wait for this
			// attempted child to settle; a failed start does not wait on another run.
			wait, _ := workflow.NewDisconnectedContext(ctx)
			_ = future.Get(wait, nil)
			return err
		}
		childStarted = true
		err = future.Get(ctx, nil)
		wait, _ := workflow.NewDisconnectedContext(ctx)
		_ = future.Get(wait, nil)
		return err
	}
	return workflow.NewContinueAsNewError(ctx, WriteTask, in)
}

func receiveSignal(ctx workflow.Context, name string, value any) error {
	received := false
	selector := workflow.NewSelector(ctx)
	selector.AddReceive(workflow.GetSignalChannel(ctx, name), func(ch workflow.ReceiveChannel, _ bool) { ch.Receive(ctx, value); received = true })
	selector.AddReceive(ctx.Done(), func(workflow.ReceiveChannel, bool) {})
	selector.Select(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !received {
		return failure(core.ErrInvalid)
	}
	return nil
}
