package engine

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	enums "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	service "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/types/known/timestamppb"
	"workbench-native/internal/core"
)

type overviewTemporal struct {
	client.Client
	describe func(context.Context, string, string) (*service.DescribeWorkflowExecutionResponse, error)
}

func (c overviewTemporal) DescribeWorkflowExecution(ctx context.Context, id, run string) (*service.DescribeWorkflowExecutionResponse, error) {
	return c.describe(ctx, id, run)
}

func TestOverviewPinsDailyExecution(t *testing.T) {
	now := time.Now().UTC()
	run := core.Run{ID: "old-daily", WorkflowID: "personal-daily-shared", Operation: "daily"}
	c := overviewTemporal{describe: func(_ context.Context, id, attempt string) (*service.DescribeWorkflowExecutionResponse, error) {
		if id != run.WorkflowID || attempt != run.ID {
			t.Fatalf("read %q/%q instead of original attempt", id, attempt)
		}
		return &service.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Status: enums.WORKFLOW_EXECUTION_STATUS_COMPLETED, StartTime: timestamppb.New(now), CloseTime: timestamppb.New(now.Add(time.Minute)),
		}}, nil
	}}
	v := RunOverview{Execution: "Unknown"}
	readRunExecution(t.Context(), c, run, &v)
	if v.Execution != "Completed" || !v.CreatedAt.Equal(now) || v.ClosedAt == nil || !v.ClosedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("original execution lost: %+v", v)
	}
}

func TestOverviewLimitsConcurrencyAndPreservesFactsOnDeadline(t *testing.T) {
	const count = 100
	runs := make([]core.Run, count)
	out := make([]RunOverview, count)
	for i := range runs {
		runs[i] = core.Run{ID: fmt.Sprint(i)}
		out[i] = RunOverview{ID: runs[i].ID, Title: "retained", Execution: "Unknown", Stats: core.RunStats{Total: 1, Succeeded: 1}}
	}
	var active, peak, calls atomic.Int32
	c := overviewTemporal{describe: func(ctx context.Context, id, run string) (*service.DescribeWorkflowExecutionResponse, error) {
		calls.Add(1)
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		defer active.Add(-1)
		if id == "0" {
			return &service.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
				Status: enums.WORKFLOW_EXECUTION_STATUS_FAILED,
			}}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	started := time.Now()
	readOverviewExecutions(t.Context(), c, runs, out, 100*time.Millisecond)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("shared deadline not respected: %v", elapsed)
	}
	if peak.Load() != overviewWorkers || active.Load() != 0 || calls.Load() != overviewWorkers+1 {
		t.Fatalf("peak=%d, active=%d, calls=%d", peak.Load(), active.Load(), calls.Load())
	}
	for i, v := range out {
		want := "Unknown"
		if i == 0 {
			want = "Failed"
		}
		if v.ID != runs[i].ID || v.Title != "retained" || v.Execution != want || v.Stats.Succeeded != 1 {
			t.Fatalf("wrong row %d: %+v", i, v)
		}
	}
}

func BenchmarkOverviewExecutionReads(b *testing.B) {
	runs := make([]core.Run, 100)
	c := overviewTemporal{describe: func(ctx context.Context, _, _ string) (*service.DescribeWorkflowExecutionResponse, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
			return &service.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
				Status: enums.WORKFLOW_EXECUTION_STATUS_COMPLETED,
			}}, nil
		}
	}}
	b.Run("serial", func(b *testing.B) {
		for b.Loop() {
			out := make([]RunOverview, len(runs))
			for i := range runs {
				readRunExecution(b.Context(), c, runs[i], &out[i])
			}
		}
	})
	b.Run("bounded", func(b *testing.B) {
		for b.Loop() {
			out := make([]RunOverview, len(runs))
			readOverviewExecutions(b.Context(), c, runs, out, overviewStatusBudget)
		}
	})
}
