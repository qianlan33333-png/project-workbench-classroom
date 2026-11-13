package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"net/http"
	"strconv"
	"time"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

func (a *Server) configRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /session/resolve-sources", func(w http.ResponseWriter, r *http.Request) {
		var refs []core.Ref
		if decode(w, r, &refs) {
			v, e := a.Store.ResolveExternalSources(r.Context(), principal(r.Context()), refs)
			respond(w, v, e)
		}
	})

	mux.HandleFunc("GET /delegation/pending", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.PendingDelegations(r.Context(), principal(r.Context()), r.URL.Query().Get("after"))
		respond(w, v, e)
	})
	mux.HandleFunc("POST /delegation/report", func(w http.ResponseWriter, r *http.Request) {
		var v core.DelegationReport
		if decode(w, r, &v) {
			respond(w, map[string]bool{"recorded": true}, a.Store.ReportDelegation(r.Context(), principal(r.Context()), v))
		}
	})
	mux.HandleFunc("POST /session/external-sources", func(w http.ResponseWriter, r *http.Request) {
		var refs []core.Ref
		if !decode(w, r, &refs) {
			return
		}
		if len(refs) == 0 || len(refs) > 32 {
			respond(w, nil, core.ErrInvalid)
			return
		}
		p := principal(r.Context())
		if p.Kind == "project" {
			_, e := a.Store.SessionRoute(r.Context(), p)
			if e == nil {
				for _, ref := range refs {
					if e = a.Store.CheckMessageKnowledge(r.Context(), p, ref); e != nil {
						respond(w, nil, e)
						return
					}
				}
			}
			if e != nil && !core.IsMissing(e) {
				respond(w, nil, e)
				return
			}
		}
		if e := core.AuthorizeAll(r.Context(), a.Store, p, refs); e != nil {
			respond(w, nil, e)
			return
		}
		respond(w, map[string]bool{"recorded": true}, a.Store.RememberRead(r.Context(), p, refs))
	})
	mux.HandleFunc("GET /session/check", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		object, practice := false, false
		if p.Kind == "project" {
			route, e := a.Store.SessionRoute(r.Context(), p)
			if e == nil {
				object = true
				practice = core.IsReplyPracticeConnection(route.ConnectionID)
			} else if !core.IsMissing(e) {
				respond(w, nil, e)
				return
			}
		}
		respond(w, map[string]bool{"allowed": true, "objectSession": object, "replyPractice": practice}, a.Store.CheckSession(r.Context(), p))
	})
	mux.HandleFunc("GET /events/pending", func(w http.ResponseWriter, r *http.Request) {
		after, e := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		if r.URL.Query().Get("after") == "" {
			e = nil
		}
		if e != nil {
			respond(w, nil, core.ErrInvalid)
			return
		}
		v, e := a.Store.PendingEvents(r.Context(), principal(r.Context()), after)
		respond(w, v, e)
	})
	mux.HandleFunc("POST /events/ack", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			ID string `json:"id"`
		}
		if decode(w, r, &v) {
			respond(w, v, a.Store.AckEvent(r.Context(), principal(r.Context()), v.ID))
		}
	})
	mux.HandleFunc("GET /runs", func(w http.ResponseWriter, r *http.Request) {
		v, e := engine.Overview(r.Context(), a.Store, a.Temporal, principal(r.Context()))
		respond(w, v, e)
	})
	mux.HandleFunc("GET /decisions", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Decisions(r.Context(), principal(r.Context()))
		respond(w, v, e)
	})
	mux.HandleFunc("GET /connections", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Connections(r.Context(), principal(r.Context()))
		respond(w, v, e)
	})
	mux.HandleFunc("POST /connections", func(w http.ResponseWriter, r *http.Request) {
		var v core.Connection
		if decode(w, r, &v) {
			respond(w, v, a.Store.PutConnection(r.Context(), principal(r.Context()), v))
		}
	})
	mux.HandleFunc("POST /connection-test", func(w http.ResponseWriter, r *http.Request) {
		var v core.Connection
		if decode(w, r, &v) {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			respond(w, map[string]bool{"verified": true}, core.TestMySQLConnection(ctx, principal(r.Context()), v))
		}
	})
	mux.HandleFunc("GET /grants", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Grants(r.Context(), principal(r.Context()))
		respond(w, v, e)
	})
	mux.HandleFunc("GET /memory", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Memories(r.Context(), principal(r.Context()))
		respond(w, v, e)
	})
	mux.HandleFunc("POST /memory", func(w http.ResponseWriter, r *http.Request) {
		var v core.Memory
		if decode(w, r, &v) {
			respond(w, v, a.Store.PutMemory(r.Context(), principal(r.Context()), v))
		}
	})
	mux.HandleFunc("POST /decisions/answer", func(w http.ResponseWriter, r *http.Request) {
		var v core.Decision
		if !decode(w, r, &v) {
			return
		}
		d, e := a.Store.Answer(r.Context(), principal(r.Context()), v)
		respond(w, d, e)
	})
}

type SyncInput struct {
	OperationKey string `json:"operationKey"`
	Title        string `json:"title"`
	Destination  string `json:"destination"`
}
type FollowupRequest struct {
	TargetRunIDs []string   `json:"targetRunIds,omitempty"`
	OperationKey string     `json:"operationKey"`
	Title        string     `json:"title"`
	Goal         string     `json:"goal"`
	Sources      []core.Ref `json:"sources"`
	NextAt       time.Time  `json:"nextAt"`
}
type ResumeRequest struct {
	RunID string `json:"runId"`
}

func accepted(e error) error {
	var exists *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(e, &exists) {
		return nil
	}
	return e
}
func (a *Server) moreTools(s *mcp.Server, bound core.Principal) {
	mcp.AddTool(s, &mcp.Tool{Name: "run_list", Description: "Read recent tasks with real Temporal state, timestamps, counts and separate publication status."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
		Runs []engine.RunOverview `json:"runs"`
	}, error) {
		v, e := engine.Overview(ctx, a.Store, a.Temporal, bound)
		return nil, struct {
			Runs []engine.RunOverview `json:"runs"`
		}{v}, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "decision_list", Description: "Read matters waiting for the owner. Does not answer or approve anything."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
		Decisions []core.Decision `json:"decisions"`
	}, error) {
		v, e := a.Store.Decisions(ctx, bound)
		return nil, struct {
			Decisions []core.Decision `json:"decisions"`
		}{v}, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "project_list", Description: "Read the project directory, without loading project documents or inheriting project write capabilities."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
		Projects []core.Project `json:"projects"`
	}, error) {
		v, e := a.Store.Projects(ctx)
		return nil, struct {
			Projects []core.Project `json:"projects"`
		}{v}, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "resource_sync", Description: "Run the registered resource source reader and validator, then atomically publish. Uses fixed owner configuration; no ad-hoc SQL."}, func(ctx context.Context, _ *mcp.CallToolRequest, in SyncInput) (*mcp.CallToolResult, RunRef, error) {
		return a.syncResource(ctx, bound, in)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "followup_create", Description: "Persist one bounded follow-up. At most three timed checks; targetRunIds (at most 5) are checked against actual execution/publication/receipts each round. Source pages are only samples. Questions wait for a real owner answer. Never sends to customers."}, func(ctx context.Context, _ *mcp.CallToolRequest, in FollowupRequest) (*mcp.CallToolResult, RunRef, error) {
		if !core.ValidID(in.OperationKey) || in.Goal == "" || len(in.Goal) > 16000 || in.NextAt.IsZero() {
			return nil, RunRef{}, core.ErrInvalid
		}
		if len(in.TargetRunIDs) > 5 {
			return nil, RunRef{}, core.ErrInvalid
		}
		seen := map[string]bool{}
		for _, id := range in.TargetRunIDs {
			if !core.ValidID(id) || seen[id] {
				return nil, RunRef{}, core.ErrInvalid
			}
			seen[id] = true
			target, e := a.Store.Run(ctx, bound, id)
			if e != nil {
				return nil, RunRef{}, e
			}
			if e = a.Store.RememberRunRead(ctx, bound, target); e != nil {
				return nil, RunRef{}, e
			}
		}
		r := core.Run{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(bound.SessionID+"/"+in.OperationKey)).String(), Principal: bound, Operation: "followup", Title: in.Title, Goal: in.Goal, Sources: in.Sources, TargetRunIDs: in.TargetRunIDs}
		if e := a.Store.SaveRun(ctx, r); e != nil {
			return nil, RunRef{}, e
		}
		_, e := a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.Followup, engine.FollowupInput{RunID: r.ID, NextAt: in.NextAt})
		return nil, RunRef{RunID: r.ID}, accepted(e)
	})
	mcp.AddTool(s, &mcp.Tool{Name: "memory_list", Description: "Read personal memories and their sources. Available only in the SuperAgent session."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
		Memories []core.Memory `json:"memories"`
	}, error) {
		v, e := a.Store.Memories(ctx, bound)
		return nil, struct {
			Memories []core.Memory `json:"memories"`
		}{v}, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "memory_save", Description: "Save, correct or forget a personal memory with its source and expected revision; cannot mark an inference as owner-confirmed."}, func(ctx context.Context, _ *mcp.CallToolRequest, in core.Memory) (*mcp.CallToolResult, core.Memory, error) {
		in.Confirmed = false
		e := a.Store.PutMemory(ctx, bound, in)
		if e != nil {
			return nil, core.Memory{}, e
		}
		saved, e := a.Store.Memory(ctx, bound, in.ID)
		return nil, saved, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "run_resume", Description: "Explicitly continue failed items of the original batch. Succeeded items are reused. Does not retry unknown external sends."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ResumeRequest) (*mcp.CallToolResult, RunRef, error) {
		r, e := a.Store.Run(ctx, bound, in.RunID)
		if e != nil {
			return nil, RunRef{}, e
		}
		if r.Operation == "operations_batch_confirm" {
			if !bound.IsOwner() {
				return nil, RunRef{}, core.ErrDenied
			}
			transport, _ := r.Shared["transport"].(string)
			allowed, err := a.operationsBatchWriteAllowed(ctx, r.ID, transport)
			if err != nil || !allowed {
				return nil, RunRef{}, errors.Join(err, core.ErrDenied)
			}
			if err = a.Store.RestoreUnsubmittedOperationsBatch(ctx, r); err != nil {
				return nil, RunRef{}, err
			}
			_, err = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID + "-delivery", TaskQueue: engine.InteractiveQueue,
				WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE}, engine.OperationsBatchDelivery, r.ID)
			return nil, RunRef{RunID: r.ID}, accepted(err)
		}
		if r.Operation == "dispatch_batch" {
			_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE}, engine.DispatchBatch, engine.DispatchBatchInput{RunID: r.ID})
			return nil, RunRef{RunID: r.ID}, accepted(e)
		}
		if r.Operation == "operations_cycle" {
			checkpoint, e := a.Store.Checkpoint(ctx, r, "operations-cycle")
			if e != nil {
				return nil, RunRef{}, e
			}
			raw, _ := json.Marshal(checkpoint.Input)
			var input engine.OperationsCycleInput
			if json.Unmarshal(raw, &input) != nil {
				return nil, RunRef{}, core.ErrInvalid
			}
			input.Resume = true
			_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE}, engine.OperationsCycle, input)
			return nil, RunRef{RunID: r.ID}, accepted(e)
		}
		if core.IsLocalOperations(r) {
			_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: engine.BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE}, engine.WriteTask, engine.WriteInput{RunID: r.ID, ProjectID: r.Principal.ProjectID, Operation: "operations_local_review"})
			return nil, RunRef{RunID: r.ID}, accepted(e)
		}
		if r.Operation != "generate" && r.Operation != "pipeline" && r.Operation != "sync" && r.Operation != "data_refresh" && r.Operation != "strategy_select" && r.Operation != engine.OperationsProfileRefresh {
			return nil, RunRef{}, core.ErrInvalid
		}
		_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: engine.BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE}, engine.WriteTask, engine.WriteInput{RunID: r.ID, ProjectID: r.WriteScope(), Operation: r.Operation})
		return nil, RunRef{RunID: r.ID}, accepted(e)
	})
}

func (a *Server) syncResource(ctx context.Context, p core.Principal, in SyncInput) (*mcp.CallToolResult, RunRef, error) {
	if (p.Kind != "project" && p.Kind != "personal") || !core.ValidID(in.OperationKey) {
		return nil, RunRef{}, core.ErrDenied
	}
	resource, e := a.Store.Resource(ctx, in.Destination)
	if e != nil {
		return nil, RunRef{}, e
	}
	if resource.ConnectionID == "" {
		return nil, RunRef{}, core.ErrDenied
	}
	r := core.Run{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/"+in.OperationKey)).String(), Principal: p, Operation: "sync", Destination: in.Destination, Title: in.Title}
	if e := a.Store.SaveRun(ctx, r); e != nil {
		return nil, RunRef{}, e
	}
	_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: engine.BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.WriteTask, engine.WriteInput{RunID: r.ID, ProjectID: r.WriteScope(), Operation: r.Operation})
	return nil, RunRef{RunID: r.ID}, accepted(e)
}
