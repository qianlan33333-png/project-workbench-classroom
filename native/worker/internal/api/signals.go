package api

import (
	"context"
	"go.temporal.io/api/enums/v1"
	"time"
	"workbench-native/internal/engine"
)

// This outbox only delivers a recorded human answer. Temporal still owns all
// execution and recovery; a duplicate signal is harmless to the decision ID.
func (a *Server) DeliverSignals(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	cursor := ""
	for {
		if ctx.Err() != nil {
			return
		}
		attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
		pending, e := a.Store.PendingSignals(attempt, cursor)
		if e == nil {
			for _, d := range pending {
				cursor = d.ID
				state, err := a.Temporal.DescribeWorkflowExecution(attempt, d.RunID, "")
				if err != nil {
					continue
				}
				if state.WorkflowExecutionInfo.Status == enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
					if err = a.Temporal.SignalWorkflow(attempt, d.RunID, "", "decision", engine.Decision{ID: d.ID, Revision: d.Revision, Digest: d.Digest}); err != nil {
						continue
					}
				}
				_ = a.Store.AckSignal(attempt, d.ID)
			}
		}
		if len(pending) < 100 {
			cursor = ""
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
