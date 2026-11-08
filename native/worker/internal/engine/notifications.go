package engine

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"strings"
	"time"
	"workbench-native/internal/core"
)

// This scheduled delivery step only reads durable events and submits ordinary
// message workflows. Failure/retry cannot invoke the original business task.
func NotificationSweep(ctx workflow.Context) error {
	if workflow.GetVersion(ctx, "conversation-mode-notifications", workflow.DefaultVersion, 1) != workflow.DefaultVersion {
		if err := workflow.ExecuteActivity(activityContext(ctx, InteractiveQueue), "QueueModeNotifications").Get(ctx, nil); err != nil {
			return err
		}
	}
	return workflow.ExecuteActivity(activityContext(ctx, InteractiveQueue), "QueueNotifications").Get(ctx, nil)
}

func (a *Activities) QueueModeNotifications(ctx context.Context) error {
	// Bounded drain of only control events; business chatter cannot block handoff.
	for page := 0; page < 10; page++ {
		events, err := a.Store.PendingModeEvents(ctx)
		if err != nil {
			return err
		}
		for _, event := range events {
			run, e := a.Store.QueueModeNotification(ctx, event)
			if e != nil {
				return e
			}
			_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: run.ID, TaskQueue: InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY}, SendMessage, run.ID)
			if e != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(e) {
				return e
			}
			if e = a.Store.MarkModeQueued(ctx, event.ID); e != nil {
				return e
			}
		}
		if len(events) < 100 {
			return nil
		}
	}
	return nil
}
func (a *Activities) QueueNotifications(ctx context.Context) error {
	settings, e := a.Store.NotificationSettings(ctx)
	if e != nil || !settings.Enabled {
		return e
	}
	events, e := a.Store.NotificationEvents(ctx, settings.After)
	if e != nil {
		return e
	}
	p := core.Principal{Kind: "personal", SessionID: settings.SessionID}
	for _, event := range events {
		if (event.RunID == "" && event.Kind != "message.handoff") || strings.HasSuffix(event.ID, "-notify") || !notifyKind(event.Kind) {
			if e = a.Store.AdvanceNotifications(ctx, event.Sequence); e != nil {
				return e
			}
			continue
		}
		var source core.Run
		if event.Kind == "message.handoff" {
			// Handoff is a status fact, including failures before a business
			// Run could be created. No withdrawn source text is copied here.
			source = core.Run{ID: event.RunID, Title: "自动应答需要接管", Operation: "incoming"}
			source.Principal.SessionID = event.SessionID
			if e = a.Store.DB.QueryRowContext(ctx, `SELECT kind,project_id FROM wb_sessions WHERE id=?`, event.SessionID).Scan(&source.Principal.Kind, &source.Principal.ProjectID); e != nil {
				return e
			}
		} else {
			source, e = a.Store.Run(ctx, p, event.RunID)
			if e != nil {
				return e
			}
		}
		if source.Operation == "notification" || source.Operation == "control_notification" || source.ParentRunID != "" || (source.Quiet && (event.Kind == "resource.published" || event.Kind == "batch.completed")) || (source.Operation == "reply" && event.Kind != "message.unknown" && event.Kind != "message.failed") {
			if e = a.Store.AdvanceNotifications(ctx, event.Sequence); e != nil {
				return e
			}
			continue
		}
		if source.Operation == "incoming" || source.Operation == "reply" {
			route, err := a.Store.SessionRoute(ctx, source.Principal)
			if err == nil {
				cfg, err := a.Store.ReplySettingsForConnection(ctx, route.ConnectionID)
				if err == nil && !cfg.Notify {
					if e = a.Store.AdvanceNotifications(ctx, event.Sequence); e != nil {
						return e
					}
					continue
				}
			}
		}
		var refs []core.Ref
		if event.Kind != "message.handoff" {
			refs, e = a.Store.ActualRunSources(ctx, source.ID)
			if e != nil {
				return e
			}
		}
		text := source.Title + "\n" + event.CreatedAt.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02 15:04") + "\n" + event.Text
		if event.Kind == "decision.waiting" {
			details, ok := event.Details.(map[string]any)
			if ok {
				if id, ok := details["decisionId"].(string); ok {
					d, e := a.Store.Decision(ctx, p, id)
					if e != nil {
						return e
					}
					if d.State != "waiting" {
						if e = a.Store.AdvanceNotifications(ctx, event.Sequence); e != nil {
							return e
						}
						continue
					}
					text += "\n" + d.Question
				}
			}
		}
		if len([]rune(text)) > 4500 {
			text = string([]rune(text)[:4500]) + "\n其余内容请在工作台查看。"
		}
		for _, target := range settings.Targets {
			id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("notification/%s/%s", event.ID, core.Digest(target)))).String()
			run := core.Run{ID: id, Principal: p, Operation: "notification", Title: "本人通知 · " + source.Title, Goal: text, Sources: core.UnionRefs(core.UnionRefs(source.Sources, source.Lineage), refs)}
			if e = a.Store.SaveRun(ctx, run); e != nil {
				return e
			}
			if e = a.Store.PutDelivery(ctx, run, core.Delivery{ID: id, RunID: id, ItemID: "notification", ConnectionID: target.ConnectionID, Target: target.Target, Text: text}); e != nil {
				return e
			}
			_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, SendMessage, id)
			if e != nil && !temporal.IsWorkflowExecutionAlreadyStartedError(e) {
				return e
			}
		}
		if e = a.Store.AdvanceNotifications(ctx, event.Sequence); e != nil {
			return e
		}
	}
	return nil
}
func notifyKind(kind string) bool {
	switch kind {
	case "message.handoff", "project.reported", "decision.waiting", "personal.daily", "resource.published", "batch.completed", "batch.partial", "run.failed", "run.canceled", "followup.completed", "message.batch_submitted", "message.batch_receipts", "message.unknown", "message.failed", "message.pending_confirmation", "message.sent", "message.delivered":
		return true
	}
	return false
}
