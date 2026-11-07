package engine

import (
	"context"
	"go.temporal.io/sdk/client"
	"time"
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

// The business catalog is not proof of execution. Temporal supplies the current
// lifecycle; a separately checked snapshot supplies publication status.
func Overview(ctx context.Context, s *core.Store, c client.Client, p core.Principal) ([]RunOverview, error) {
	runs, e := s.RunCatalog(ctx, p)
	if e != nil {
		return nil, e
	}
	out := make([]RunOverview, 0, len(runs))
	for _, r := range runs {
		v, err := InspectRun(ctx, s, c, p, r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func InspectRun(ctx context.Context, s *core.Store, c client.Client, p core.Principal, id string) (RunOverview, error) {
	r, e := s.Run(ctx, p, id)
	if e != nil {
		return RunOverview{}, e
	}
	if e = s.RememberRunRead(ctx, p, r); e != nil {
		return RunOverview{}, e
	}

	v := RunOverview{ID: r.ID, Title: r.Title, Operation: r.Operation, SessionID: r.Principal.SessionID, ProjectID: r.Principal.ProjectID, Execution: "Unknown", Destination: r.Destination}
	if e = s.DB.QueryRowContext(ctx, `SELECT created_at FROM wb_runs WHERE id=?`, r.ID).Scan(&v.CreatedAt); e != nil {
		return RunOverview{}, e
	}
	v.Stats, e = s.Stats(ctx, p, r.ID)
	if e != nil {
		return RunOverview{}, e
	}
	if r.Operation == "dispatch_batch" || r.Operation == "message" || r.Operation == "reply" {
		stats, err := s.DeliveryStats(ctx, p, r.ID)
		if err != nil {
			return RunOverview{}, err
		}
		v.Deliveries = &stats
	}
	if c != nil {
		read, cancel := context.WithTimeout(ctx, 2*time.Second)
		desc, err := c.DescribeWorkflowExecution(read, r.ExecutionID(), "")
		cancel()
		if err == nil && desc.WorkflowExecutionInfo != nil {
			info := desc.WorkflowExecutionInfo
			if info.StartTime != nil {
				v.CreatedAt = info.StartTime.AsTime()
			}
			if first := info.GetFirstRunId(); first != "" && first != info.GetExecution().GetRunId() {
				read, cancel := context.WithTimeout(ctx, 2*time.Second)
				initial, firstErr := c.DescribeWorkflowExecution(read, r.ExecutionID(), first)
				cancel()
				if firstErr == nil && initial.GetWorkflowExecutionInfo().GetStartTime() != nil {
					v.CreatedAt = initial.GetWorkflowExecutionInfo().GetStartTime().AsTime()
				}
			}
			v.Execution = info.Status.String()
			if info.CloseTime != nil {
				at := info.CloseTime.AsTime()
				v.ClosedAt = &at
			}
		}
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
