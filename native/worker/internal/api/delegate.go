package api

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type DelegateInput struct {
	OperationKey string     `json:"operationKey"`
	ProjectID    string     `json:"targetProjectId"`
	Instructions string     `json:"instructions,omitempty"`
	Sources      []core.Ref `json:"sources,omitempty"`
}

func (a *Server) delegationTools(s *mcp.Server, bound core.Principal) {
	mcp.AddTool(s, &mcp.Tool{Name: "task_delegate", Description: "SuperAgent 派发到目标项目的新 Session。默认转交最近一条本人原始要求；可提供 instructions，但它继承 SuperAgent 已读资料的全部读取要求。任务始终按目标项目的资源范围执行，返回接收回执，不代表业务已完成。"}, func(ctx context.Context, _ *mcp.CallToolRequest, in DelegateInput) (*mcp.CallToolResult, RunRef, error) {
		if bound.Kind != "personal" || !core.ValidID(in.ProjectID) || !core.ValidID(in.OperationKey) || len(in.Instructions) > 16000 {
			return nil, RunRef{}, core.ErrDenied
		}
		var n int
		if e := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_projects WHERE id=?`, in.ProjectID).Scan(&n); e != nil || n != 1 {
			return nil, RunRef{}, core.ErrInvalid
		}
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(bound.SessionID+"/"+in.OperationKey)).String()
		if old, e := a.Store.Run(ctx, bound, id); e == nil {
			if old.Operation != "dispatch" || old.NotifySession != bound.SessionID || old.Principal.ProjectID != in.ProjectID || core.Digest(old.Sources) != core.Digest(in.Sources) || (in.Instructions != "" && old.Goal != in.Instructions) || (in.Instructions == "" && old.OriginMessage == "") {
				return nil, RunRef{}, core.ErrConflict
			}
			_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.Dispatch, id)
			return nil, RunRef{RunID: id}, accepted(e)
		} else if !core.IsMissing(e) {
			return nil, RunRef{}, e
		}
		target := core.Principal{Kind: "project", ProjectID: in.ProjectID, SessionID: uuid.NewSHA1(uuid.NameSpaceURL, []byte(id+"/session")).String()}
		r := core.Run{ID: id, Principal: target, Operation: "dispatch", Title: "来自 SuperAgent 的项目任务", Sources: in.Sources, NotifySession: bound.SessionID}
		if in.Instructions != "" {
			refs, e := a.Store.SessionSources(ctx, bound)
			if e != nil {
				return nil, RunRef{}, e
			}
			if e = core.AuthorizeAll(ctx, a.Store, target, refs); e != nil {
				return nil, RunRef{}, fmt.Errorf("%w: SuperAgent 已读资料超出目标项目范围，不能转交改写后的 instructions；如只需转交本人要求，请省略 instructions，由宿主直接传本人最近原话。不需要扩大目标项目权限", e)
			}
			r.Goal = in.Instructions
			r.Lineage = refs
		} else {
			request, e := a.Host.OwnerRequest(ctx, bound)
			if e != nil {
				return nil, RunRef{}, e
			}
			if request.ID == "" || request.Text == "" {
				return nil, RunRef{}, core.ErrInvalid
			}
			r.Goal = request.Text
			r.OriginMessage = request.ID
		}
		if e := a.Store.SaveRun(ctx, r); e != nil {
			return nil, RunRef{}, e
		}
		_, e := a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: engine.InteractiveQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.Dispatch, id)
		return nil, RunRef{RunID: id}, accepted(e)
	})
}
