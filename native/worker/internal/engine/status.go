package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"workbench-native/internal/core"
)

type RunOverview struct {
	ID          string              `json:"id"`
	Title       string              `json:"title"`
	Operation   string              `json:"operation"`
	SessionID   string              `json:"sessionId"`
	ProjectID   string              `json:"projectId"`
	CreatedAt   time.Time           `json:"createdAt"`
	ClosedAt    *time.Time          `json:"closedAt,omitempty"`
	Execution   string              `json:"execution"`
	Stats       core.RunStats       `json:"stats"`
	Destination string              `json:"destination,omitempty"`
	Published   bool                `json:"published"`
	Deliveries  *core.DeliveryStats `json:"deliveries,omitempty"`
}

const (
	overviewWorkers      = 8
	overviewFactReaders  = 4
	overviewStatusBudget = 3 * time.Second
)

// MySQL supplies the business catalog and counters. Temporal supplies execution
// evidence within a shared deadline so an unavailable backend cannot stall the list.
func Overview(ctx context.Context, s *core.Store, c client.Client, p core.Principal) ([]RunOverview, error) {
	return overview(ctx, s, c, p, "")
}

func overview(ctx context.Context, s *core.Store, c client.Client, p core.Principal, exclude string) ([]RunOverview, error) {
	catalog, err := s.RunCatalog(ctx, p, exclude)
	if err != nil {
		return nil, err
	}
	type row struct {
		view RunOverview
		err  error
	}
	facts := make([]row, len(catalog))
	readOverviewRows(ctx, len(catalog), overviewFactReaders, func(i int) {
		run := catalog[i]
		facts[i].view, facts[i].err = readRunOverview(ctx, s, p, run.Run, run.CreatedAt)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]RunOverview, 0, len(catalog))
	runs := make([]core.Run, 0, len(catalog))
	for i, fact := range facts {
		if errors.Is(fact.err, core.ErrDenied) || core.IsMissing(fact.err) {
			continue
		}
		if fact.err != nil {
			return nil, fact.err
		}
		runs = append(runs, catalog[i].Run)
		out = append(out, fact.view)
	}
	if c != nil {
		readOverviewExecutions(ctx, c, runs, out, overviewStatusBudget)
	}
	return out, ctx.Err()
}

func InspectRun(ctx context.Context, s *core.Store, c client.Client, p core.Principal, id string) (RunOverview, error) {
	r, err := s.Run(ctx, p, id)
	if err != nil {
		return RunOverview{}, err
	}
	var created time.Time
	if err = s.DB.QueryRowContext(ctx, `SELECT created_at FROM wb_runs WHERE id=?`, r.ID).Scan(&created); err != nil {
		return RunOverview{}, err
	}
	v, err := readRunOverview(ctx, s, p, r, created)
	if err == nil && c != nil {
		readRunExecution(ctx, c, r, &v)
	}
	return v, err
}

func readRunOverview(ctx context.Context, s *core.Store, p core.Principal, r core.Run, created time.Time) (RunOverview, error) {
	v := RunOverview{ID: r.ID, Title: r.Title, Operation: r.Operation, SessionID: r.Principal.SessionID,
		ProjectID: r.Principal.ProjectID, CreatedAt: created, Execution: "Unknown", Destination: r.Destination}
	var err error
	v.Stats, v.Deliveries, err = s.RunOverviewFacts(ctx, p, r)
	if err != nil {
		return v, err
	}
	if r.Destination != "" {
		version := r.ID
		if r.Operation == "pipeline" {
			version = stepID(r.ID, "generate")
		}
		snap, err := s.Snapshot(ctx, r.Destination, version)
		v.Published = err == nil && snap.State == "published"
	}
	return v, nil
}

func readOverviewExecutions(ctx context.Context, c client.Client, runs []core.Run, out []RunOverview, budget time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	readOverviewRows(ctx, len(runs), overviewWorkers, func(i int) {
		readRunExecution(ctx, c, runs[i], &out[i])
	})
}

func readOverviewRows(ctx context.Context, count, concurrency int, read func(int)) {
	jobs := make(chan int, count)
	for i := range count {
		jobs <- i
	}
	close(jobs)
	var readers sync.WaitGroup
	for range min(count, concurrency) {
		readers.Go(func() {
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				read(i)
			}
		})
	}
	readers.Wait()
}

func readRunExecution(ctx context.Context, c client.Client, r core.Run, v *RunOverview) {
	read, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// Scheduled daily summaries store the execution's run ID as their business
	// ID. A later trigger sharing the Workflow ID must not relabel old summaries.
	executionRunID := r.ExecutionRunID()
	desc, err := c.DescribeWorkflowExecution(read, r.ExecutionID(), executionRunID)
	info := desc.GetWorkflowExecutionInfo()
	if err != nil || info == nil || info.GetStatus() == enums.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED {
		return
	}
	v.Execution = info.Status.String()
	if started := info.GetStartTime(); started != nil {
		v.CreatedAt = started.AsTime()
	}
	if first := info.GetFirstRunId(); executionRunID == "" && first != "" && first != info.GetExecution().GetRunId() {
		initial, err := c.DescribeWorkflowExecution(read, r.ExecutionID(), first)
		if started := initial.GetWorkflowExecutionInfo().GetStartTime(); err == nil && started != nil {
			v.CreatedAt = started.AsTime()
		}
	}
	if closed := info.GetCloseTime(); closed != nil {
		at := closed.AsTime()
		v.ClosedAt = &at
	}
}
