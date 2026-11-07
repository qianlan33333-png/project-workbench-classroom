package api

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type ProgramRequest struct {
	Selection    *core.Selection          `json:"selection,omitempty"`
	OperationKey string                   `json:"operationKey"`
	Title        string                   `json:"title"`
	Sources      []core.ResourceSelection `json:"sources"`
	Destination  string                   `json:"destination"`
	MethodRef    *core.RecordRef          `json:"methodRef,omitempty"`
}

func (a *Server) programRun(ctx context.Context, p core.Principal, in ProgramRequest) (core.Run, error) {
	r := core.Run{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/"+in.OperationKey)).String(), Principal: p, Operation: "pipeline", Title: in.Title, Destination: in.Destination, Program: &core.PipelinePlan{Sources: in.Sources}}
	if p.Kind != "project" || !core.ValidID(in.OperationKey) || len(in.Sources) == 0 || len(in.Sources) > 16 {
		return r, core.ErrInvalid
	}
	seen := map[string]bool{}
	sections := 0
	for _, source := range in.Sources {
		if !core.ValidID(source.ResourceID) || seen[source.ResourceID] || len(source.Sections) == 0 {
			return r, core.ErrInvalid
		}
		seen[source.ResourceID] = true
		sections += len(source.Sections)
		resource, e := a.Store.Resource(ctx, source.ResourceID)
		if e != nil {
			return r, e
		}
		if source.Refresh && resource.OwnerProject != p.ProjectID && resource.MaintainerProject != p.ProjectID {
			return r, core.ErrDenied
		}
		sectionSeen := map[string]bool{}
		for _, section := range source.Sections {
			if sectionSeen[section] {
				return r, core.ErrInvalid
			}
			sectionSeen[section] = true
			found := false
			for _, s := range resource.Sections {
				if s.ID == section {
					found = true
				}
			}
			if !found {
				return r, core.ErrInvalid
			}
		}
		if !source.Refresh {
			version := source.Version
			if version == "" {
				version = resource.CurrentVersion
			}
			for _, section := range source.Sections {
				if e = core.Authorize(ctx, a.Store, p, core.Ref{ResourceID: source.ResourceID, Version: version, Section: section}); e != nil {
					return r, e
				}
			}
		}
	}
	if sections > 16 {
		return r, core.ErrInvalid
	}
	if in.Destination == "" {
		if in.Selection != nil {
			return r, core.ErrInvalid
		}
		if in.MethodRef != nil {
			return r, core.ErrInvalid
		}
		for _, s := range in.Sources {
			if !s.Refresh {
				return r, core.ErrInvalid
			}
		}
		return r, nil
	}
	selection, e := core.BoundedSelection(in.Selection)
	if e != nil {
		return r, e
	}
	r.Selection = selection
	if in.MethodRef == nil {
		return r, core.ErrInvalid
	}
	records, e := a.Store.ReadIDs(ctx, p, in.MethodRef.Ref, []string{in.MethodRef.ID})
	if e != nil {
		return r, e
	}
	if len(records) != 1 {
		return r, core.ErrInvalid
	}
	bytes, e := json.Marshal(records[0].Fields["method"])
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(bytes, &r.Method); e != nil {
		return r, core.ErrInvalid
	}
	if r.Method.Instructions == "" || len(r.Method.Instructions) > 64000 {
		return r, core.ErrInvalid
	}
	if _, e = r.Method.ResolveOutput(); e != nil {
		return r, e
	}
	r.Method.Digest = ""
	r.Method.Digest = core.Digest(r.Method)
	return r, nil
}
func (a *Server) programTools(s *mcp.Server, p core.Principal) {
	mcp.AddTool(s, &mcp.Tool{Name: "run_steps", Description: "Read the steps of one grouped program with real execution states. It never reruns work."}, func(ctx context.Context, _ *mcp.CallToolRequest, in RunRef) (*mcp.CallToolResult, struct {
		Steps []RunStatus `json:"steps"`
	}, error) {
		out := struct {
			Steps []RunStatus `json:"steps"`
		}{Steps: []RunStatus{}}
		steps, e := a.Store.RunSteps(ctx, p, in.RunID)
		if e != nil {
			return nil, out, e
		}
		for _, step := range steps {
			v, err := a.runStatus(ctx, p, step.ID)
			if err != nil {
				return nil, out, err
			}
			out.Steps = append(out.Steps, v)
		}
		return nil, out, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "program_run", Description: "Run the standard refresh→validate→publish→generate program as one durable task. Select resource sections and a published method. refresh reads only a configured source; generation freezes the resulting versions. Returns one runId; no external sending."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ProgramRequest) (*mcp.CallToolResult, RunRef, error) {
		r, e := a.programRun(ctx, p, in)
		if e != nil {
			return nil, RunRef{}, e
		}
		if e = a.Store.SaveRun(ctx, r); e != nil {
			return nil, RunRef{}, e
		}
		_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: r.ID, TaskQueue: engine.BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.WriteTask, engine.WriteInput{RunID: r.ID, ProjectID: p.ProjectID, Operation: r.Operation})
		return nil, RunRef{RunID: r.ID}, accepted(e)
	})
}
