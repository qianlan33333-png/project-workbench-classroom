package engine

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"workbench-native/internal/core"
)

type OperationsScheduleInput struct {
	Principal core.Principal
	Resource  string
	Strategy  string
}

type originalCatalog struct {
	Week     string `json:"week"`
	Packages []struct {
		Package  string `json:"package_id"`
		Strategy string `json:"strategy_key"`
		Scene    string `json:"scene_label"`
		Title    string `json:"label"`
	} `json:"packages"`
}

func originalOperationsDay(t time.Time) string {
	return t.In(time.FixedZone("CST", 8*60*60)).Format(time.DateOnly)
}

// Reuse the project writer and original copy workflow. The day is tied to the
// Temporal occurrence, so a late start cannot silently generate yesterday's work.
func ScheduledOperations(ctx workflow.Context, in OperationsScheduleInput) error {
	started := workflow.Now(ctx)
	if scheduled, ok := workflow.GetTypedSearchAttributes(ctx).GetTime(temporal.NewSearchAttributeKeyTime("TemporalScheduledStartTime")); ok {
		started = scheduled
	}
	day := originalOperationsDay(started)
	var runs []core.Run
	if err := workflow.ExecuteActivity(activityContext(ctx, InteractiveQueue), "OriginalOperationsRuns", in, day).Get(ctx, &runs); err != nil {
		return err
	}
	var failures []error
	for _, run := range runs {
		if err := workflow.ExecuteActivity(activityContext(ctx, InteractiveQueue), "CreateScheduledRun", run.ID, run).Get(ctx, nil); err != nil {
			failures = append(failures, err)
			continue
		}
		child := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: run.ID, TaskQueue: BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
			WaitForCancellation: true, ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
			RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2, InitialInterval: time.Minute},
		})
		future := workflow.ExecuteChildWorkflow(child, WriteTask, WriteInput{RunID: run.ID, ProjectID: in.Principal.ProjectID, Operation: run.Operation})
		err := future.Get(ctx, nil)
		wait, _ := workflow.NewDisconnectedContext(ctx)
		_ = future.Get(wait, nil)
		if err != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(err) {
			failures = append(failures, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return errors.Join(failures...)
}

func (a *Activities) OriginalOperationsRuns(ctx context.Context, in OperationsScheduleInput, day string) ([]core.Run, error) {
	defer keepAlive(ctx)()
	if day != originalOperationsDay(time.Now()) {
		return nil, failure(core.ErrConflict)
	}
	data, err := a.Store.ReadOperationsDirectory(ctx, in.Principal, in.Resource, "catalog", map[string]string{})
	if err != nil {
		return nil, err
	}
	var catalog originalCatalog
	if err = json.Unmarshal(data, &catalog); err != nil || catalog.Week == "" || catalog.Packages == nil {
		return nil, failure(core.ErrInvalid)
	}
	var runs []core.Run
	for _, pack := range catalog.Packages {
		if pack.Strategy != in.Strategy {
			continue
		}
		key := in.Principal.ProjectID + "/" + in.Strategy + "/" + pack.Scene + "/" + day
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(key)).String()
		// Recover only an already settled empty projection from the first
		// rollout that did prepare original rows. Keep healthy and in-flight
		// identities, as well as actual zero-candidate results.
		stored, err := a.Store.Item(ctx, id, OriginalOperationsRoster)
		if err == nil {
			var batch originalPackage
			if err = json.Unmarshal([]byte(stored.Text), &batch); err != nil {
				return nil, failure(err)
			}
			if batch.State == "empty" && batch.Total > 0 && originalRunID.MatchString(batch.Run) {
				status, err := a.Temporal.DescribeWorkflowExecution(ctx, id, "")
				if err != nil {
					return nil, err
				}
				if status.GetWorkflowExecutionInfo().GetStatus() == enums.WORKFLOW_EXECUTION_STATUS_COMPLETED {
					id = uuid.NewSHA1(uuid.NameSpaceURL, []byte(key+"/original-draft-rebind")).String()
				}
			}
		} else if !core.IsMissing(err) {
			return nil, err
		}
		runs = append(runs, core.Run{
			ID: id, Principal: in.Principal, Operation: "operations_package", Destination: in.Resource,
			Title: pack.Title, Quiet: true, ExplicitInputs: true,
			Shared: map[string]any{"package": pack.Package, "week": catalog.Week, "day": day,
				"strategy": in.Strategy, "scene": pack.Scene},
		})
	}
	return runs, nil
}
