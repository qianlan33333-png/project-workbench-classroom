package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type mysqlSyncStatus struct {
	Run            *engine.RunOverview `json:"run,omitempty"`
	Problem        string              `json:"problem,omitempty"`
	CanRetry       bool                `json:"canRetry"`
	ConnectionID   string              `json:"connectionId"`
	CurrentVersion string              `json:"currentVersion,omitempty"`
	Revision       int64               `json:"revision"`
}

func (a *Server) mysqlResource(ctx context.Context, p core.Principal, id string) (core.Resource, error) {
	if !p.IsOwner() {
		return core.Resource{}, core.ErrDenied
	}
	if err := a.Store.RequireSourceKind(ctx, p, id, "mysql"); err != nil {
		return core.Resource{}, err
	}
	return a.Store.Resource(ctx, id)
}

func (a *Server) resourceSyncRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /resource-sync-status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		p := principal(ctx)
		resource, err := a.mysqlResource(ctx, p, r.URL.Query().Get("resourceId"))
		if err != nil {
			respond(w, nil, err)
			return
		}
		view := mysqlSyncStatus{ConnectionID: resource.ConnectionID, CurrentVersion: resource.CurrentVersion, Revision: resource.Revision}
		var id string
		err = a.Store.DB.QueryRowContext(ctx, `SELECT id FROM wb_runs
			WHERE body->>'$.destination'=? AND body->>'$.operation'='sync' AND JSON_EXTRACT(body,'$.parentRunId') IS NULL
			ORDER BY created_at DESC, id DESC LIMIT 1`, resource.ID).Scan(&id)
		if core.IsMissing(err) {
			respond(w, view, nil)
			return
		}
		if err != nil {
			respond(w, nil, err)
			return
		}
		run, err := engine.InspectRun(ctx, a.Store, a.Temporal, p, id)
		if err != nil {
			respond(w, nil, err)
			return
		}
		view.Run = &run
		switch run.Execution {
		case "NotStarted":
			view.CanRetry = !run.Published
			if view.CanRetry {
				view.Problem = "任务已保存，执行服务查不到启动记录。可以重试原任务。"
			}
		case "Failed", "TimedOut", "Canceled", "Terminated":
			view.CanRetry = !run.Published
			if view.CanRetry {
				view.Problem = "本次同步未发布。请检查来源连接和资料设置，修好后重试原任务。"
				var failure *temporal.ApplicationError
				// Follow typed failures from the closed workflow; never relay arbitrary
				// activity messages, which may contain upstream configuration.
				err := a.Temporal.GetWorkflow(ctx, id, "").Get(ctx, nil)
				for err != nil && errors.As(err, &failure) {
					if failure.Type() == "MySQLSource" {
						view.Problem = failure.Message()
						break
					}
					err = errors.Unwrap(failure)
				}
			}
		}
		respond(w, view, nil)
	})
	mux.HandleFunc("POST /resource-sync-start", func(w http.ResponseWriter, r *http.Request) {
		var in SyncInput
		if !decode(w, r, &in) {
			return
		}
		ctx := r.Context()
		resource, err := a.mysqlResource(ctx, principal(ctx), in.Destination)
		if err != nil {
			respond(w, nil, err)
			return
		}
		config, err := a.personalConfig(ctx)
		if err != nil {
			respond(w, nil, err)
			return
		}
		in.Title = "同步 " + resource.Name
		_, result, err := a.syncResource(ctx, core.Principal{Kind: "personal", SessionID: config.SessionID}, in)
		respond(w, result, err)
	})
	mux.HandleFunc("POST /resource-sync-retry", func(w http.ResponseWriter, r *http.Request) {
		var in ResumeRequest
		if !decode(w, r, &in) {
			return
		}
		ctx := r.Context()
		p := principal(ctx)
		if !p.IsOwner() || !core.ValidID(in.RunID) {
			respond(w, nil, core.ErrDenied)
			return
		}
		run, err := a.Store.Run(ctx, p, in.RunID)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if run.Operation != "sync" || run.ParentRunID != "" {
			respond(w, nil, core.ErrInvalid)
			return
		}
		if _, err := a.mysqlResource(ctx, p, run.Destination); err != nil {
			respond(w, nil, err)
			return
		}
		view, err := engine.InspectRun(ctx, a.Store, a.Temporal, p, run.ID)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if view.Published {
			respond(w, nil, core.ErrConflict)
			return
		}
		var reuse enums.WorkflowIdReusePolicy
		switch view.Execution {
		case "NotStarted":
			reuse = enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
		case "Failed", "TimedOut", "Canceled", "Terminated":
			reuse = enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY
		default:
			respond(w, nil, core.ErrConflict)
			return
		}
		_, err = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID: run.ID, TaskQueue: engine.BatchQueue,
			WorkflowIDReusePolicy:                    reuse,
			WorkflowExecutionErrorWhenAlreadyStarted: true,
		}, engine.WriteTask, engine.WriteInput{RunID: run.ID, ProjectID: run.WriteScope(), Operation: run.Operation})
		respond(w, RunRef{RunID: run.ID}, accepted(err))
	})
}
