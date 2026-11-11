package engine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"workbench-native/internal/core"
)

// Arguments of this single fixed sequence, not a user-defined workflow graph.
type OperationsCycleInput struct {
	Principal           core.Principal            `json:"principal"`
	Sources             []core.ResourceSelection  `json:"sources"`
	CleanDestination    string                    `json:"cleanDestination"`
	Strategies          core.Ref                  `json:"strategies"`
	History             core.Ref                  `json:"history"`
	HistorySource       *core.ResourceSelection   `json:"historySource,omitempty"`
	StrategySource      *core.ResourceSelection   `json:"strategySource,omitempty"`
	MethodSource        *core.ResourceSelection   `json:"methodSource,omitempty"`
	StrategyKeys        []string                  `json:"strategyKeys,omitempty"`
	Limit               int                       `json:"limit,omitempty"`
	SelectedDestination string                    `json:"selectedDestination"`
	DraftDestination    string                    `json:"draftDestination"`
	MethodRef           core.RecordRef            `json:"methodRef"`
	MethodRefs          map[string]core.RecordRef `json:"methodRefs,omitempty"`
	Section             string                    `json:"section"`
	Resume              bool                      `json:"resume,omitempty"`
}
type OperationCall struct {
	Principal core.Principal
	Name      string
	Arguments map[string]any
}
type OperationReply struct {
	RunID     string        `json:"runId"`
	Execution string        `json:"execution"`
	Stats     core.RunStats `json:"stats"`
}

// Schedule workflow IDs contain timestamps. Reuse the existing scheduled-run
// pattern so business run IDs stay valid UUIDs and cancellation joins the child.
func ScheduledOperationsCycle(ctx workflow.Context, in OperationsCycleInput) error {
	id := workflow.GetInfo(ctx).WorkflowExecution.RunID
	child := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: id, TaskQueue: InteractiveQueue, WaitForCancellation: true, ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL})
	f := workflow.ExecuteChildWorkflow(child, OperationsCycle, in)
	e := f.Get(ctx, nil)
	wait, _ := workflow.NewDisconnectedContext(ctx)
	_ = f.Get(wait, nil)
	return e
}

type operationsAuth struct {
	token string
	p     core.Principal
}

func (t operationsAuth) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+t.token)
	copy.Header.Set("X-Workbench-Kind", t.p.Kind)
	copy.Header.Set("X-Workbench-Project", t.p.ProjectID)
	copy.Header.Set("X-Workbench-Session", t.p.SessionID)
	return http.DefaultTransport.RoundTrip(copy)
}
func (a *Activities) OperationsInvoke(ctx context.Context, in OperationCall) (json.RawMessage, error) {
	allowed := map[string]bool{"data_refresh": true, "strategy_select": true, "batch_generate": true, "crm_push": true, "crm_receipts": true, "run_get": true, "run_resume": true}
	if !allowed[in.Name] || in.Principal.Kind != "project" {
		return nil, failure(core.ErrDenied)
	}
	address := os.Getenv("WORKBENCH_LISTEN")
	if address == "" {
		address = "127.0.0.1:7381"
	}
	if !strings.HasPrefix(address, "127.0.0.1:") {
		return nil, failure(core.ErrInvalid)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "workbench-operations-cycle", Version: "1"}, nil)
	session, e := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: "http://" + address + "/mcp", HTTPClient: &http.Client{Timeout: 40 * time.Second, Transport: operationsAuth{os.Getenv("WORKBENCH_HOST_TOKEN"), in.Principal}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil)
	if e != nil {
		return nil, e
	}
	defer session.Close()
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: in.Name, Arguments: in.Arguments})
	if e != nil {
		return nil, e
	}
	if result.IsError {
		return nil, temporal.NewNonRetryableApplicationError("step rejected input; inspect original task", "Invalid", nil)
	}
	data, e := json.Marshal(result.StructuredContent)
	if e != nil {
		return nil, e
	}

	return data, nil
}
func (a *Activities) OperationsCreateCycle(ctx context.Context, id string, in OperationsCycleInput) (string, error) {
	if in.Principal.Kind != "project" || in.Section == "" || in.Limit < 0 || in.Limit > 5000 {
		return "", failure(core.ErrInvalid)
	}
	for _, source := range []*core.ResourceSelection{in.HistorySource, in.StrategySource, in.MethodSource} {
		if source != nil && (!source.Refresh || len(source.Sections) != 1) {
			return "", failure(core.ErrInvalid)
		}
	}
	r := core.Run{ID: id, Principal: in.Principal, ExplicitInputs: true, Operation: "operations_cycle", Title: "更新资料→选单→话术→CRM→回执"}
	if e := a.Store.SaveRun(ctx, r); e != nil {
		return "", failure(e)
	}
	frozen := in
	frozen.Resume = false
	b, _ := json.Marshal(frozen)
	var input map[string]any
	_ = json.Unmarshal(b, &input)
	if _, e := a.Store.FreezeCheckpoint(ctx, r, "operations-cycle", core.Checkpoint{Input: input}); e != nil {
		return "", failure(e)
	}
	var at time.Time
	e := a.Store.DB.QueryRowContext(ctx, `SELECT created_at FROM wb_runs WHERE id=?`, id).Scan(&at)
	return at.UTC().Format(time.RFC3339Nano), e
}

func OperationsCycle(ctx workflow.Context, in OperationsCycleInput) (err error) {
	id := workflow.GetInfo(ctx).WorkflowExecution.ID
	defer reportFailure(ctx, id, &err)()
	ctx = activityContext(ctx, InteractiveQueue)
	active := ""
	defer func() {
		if ctx.Err() != nil && active != "" {
			cleanup, _ := workflow.NewDisconnectedContext(ctx)
			cleanup = activityContext(cleanup, InteractiveQueue)
			if e := workflow.ExecuteActivity(cleanup, "OperationsCancelStep", in.Principal, active).Get(cleanup, nil); e != nil {
				err = e
			}
		}
	}()
	var stamp string
	if err = workflow.ExecuteActivity(ctx, "OperationsCreateCycle", id, in).Get(ctx, &stamp); err != nil {
		return err
	}
	invoke := func(name string, args map[string]any) (json.RawMessage, error) {
		var out json.RawMessage
		e := workflow.ExecuteActivity(ctx, "OperationsInvoke", OperationCall{in.Principal, name, args}).Get(ctx, &out)
		return out, e
	}
	wait := func(run string) error {
		resumed := false
		for polls := 0; ; polls++ {
			if polls == 200 {
				return workflow.NewContinueAsNewError(ctx, OperationsCycle, in)
			}
			data, e := invoke("run_get", map[string]any{"runId": run})
			if e != nil {
				return e
			}
			var status OperationReply
			if e = json.Unmarshal(data, &status); e != nil {
				return e
			}
			switch status.Execution {
			case "Completed":
				if status.Stats.Failed > 0 {
					if !in.Resume || resumed {
						return failure(core.ErrInvalid)
					}
					if _, e = invoke("run_resume", map[string]any{"runId": run}); e != nil {
						return e
					}
					resumed = true
					break
				}
				return nil
			case "Failed", "Canceled", "Terminated", "TimedOut":
				if !in.Resume || resumed {
					return failure(core.ErrInvalid)
				}
				if _, e = invoke("run_resume", map[string]any{"runId": run}); e != nil {
					return e
				}
				resumed = true
			}
			if e = workflow.Sleep(ctx, 5*time.Second); e != nil {
				return e
			}
		}
	}
	step := func(name string, args map[string]any) (string, error) {
		active = uuid.NewSHA1(uuid.NameSpaceURL, []byte(in.Principal.SessionID+"/"+args["operationKey"].(string))).String()
		data, e := invoke(name, args)
		if e != nil {
			return "", e
		}
		var out OperationReply
		if e = json.Unmarshal(data, &out); e != nil {
			return "", e
		}
		if out.RunID == "" {
			return "", failure(core.ErrInvalid)
		}
		if out.RunID != active {
			return "", failure(core.ErrConflict)
		}
		e = wait(out.RunID)
		if e == nil {
			active = ""
		}
		return out.RunID, e
	}
	sources := append([]core.ResourceSelection{}, in.Sources...)
	if in.HistorySource != nil {
		sources = append(sources, *in.HistorySource)
	}
	if in.StrategySource != nil {
		sources = append(sources, *in.StrategySource)
	}
	if in.MethodSource != nil {
		sources = append(sources, *in.MethodSource)
	}
	clean, e := step("data_refresh", map[string]any{"operationKey": id + "-data", "sources": sources, "destination": in.CleanDestination})
	if e != nil {
		return e
	}
	if e = workflow.ExecuteActivity(ctx, "OperationsRefreshClock", in.Principal, clean).Get(ctx, &stamp); e != nil {
		return e
	}
	ref := func(resource, version string) core.Ref {
		return core.Ref{ResourceID: resource, Version: version, Section: in.Section}
	}
	history := in.History
	if in.HistorySource != nil {
		history = ref(in.HistorySource.ResourceID, stepID(clean, "sync/"+in.HistorySource.ResourceID))
		history.Section = in.HistorySource.Sections[0]
	}
	strategies := in.Strategies
	if in.StrategySource != nil {
		strategies = ref(in.StrategySource.ResourceID, stepID(clean, "sync/"+in.StrategySource.ResourceID))
		strategies.Section = in.StrategySource.Sections[0]
	}
	selectArgs := map[string]any{"operationKey": id + "-select", "data": ref(in.CleanDestination, clean), "strategies": strategies, "history": history, "strategyKeys": in.StrategyKeys, "destination": in.SelectedDestination, "asOf": stamp}
	if in.Limit > 0 {
		selectArgs["limit"] = in.Limit
	}
	selected, e := step("strategy_select", selectArgs)
	if e != nil {
		return e
	}
	var count int
	if e = workflow.ExecuteActivity(ctx, "OperationsSelectedCount", in.Principal, ref(in.SelectedDestination, selected)).Get(ctx, &count); e != nil {
		return e
	}
	if count > 5000 {
		return failure(core.ErrInvalid)
	}
	if count == 0 {
		return workflow.ExecuteActivity(ctx, "OperationsCycleSubmitted", id, "").Get(ctx, nil)
	}
	generateArgs := map[string]any{"operationKey": id + "-generate", "title": "按原策略生成话术", "sources": []core.Ref{ref(in.SelectedDestination, selected)}, "destination": in.DraftDestination}
	methodRefInput := in.MethodRef
	methodRefsInput := in.MethodRefs
	if in.MethodSource != nil {
		methodRef := ref(in.MethodSource.ResourceID, stepID(clean, "sync/"+in.MethodSource.ResourceID))
		methodRef.Section = in.MethodSource.Sections[0]
		methodRefInput.Ref = methodRef
		methods := map[string]core.RecordRef{}
		for key, item := range in.MethodRefs {
			item.Ref = methodRef
			methods[key] = item
		}
		methodRefsInput = methods
	}
	if len(methodRefsInput) > 0 {
		generateArgs["methodRefs"] = methodRefsInput
		generateArgs["methodPointer"] = "/actions/0/generation/skill_key"
	} else {
		generateArgs["methodRef"] = methodRefInput
	}

	drafts := []core.Ref{}
	after := ""
	total := 0
	for part := 0; ; part++ {
		var page core.SelectionInfo
		if e = workflow.ExecuteActivity(ctx, "OperationsGenerationPage", in.Principal, ref(in.SelectedDestination, selected), after).Get(ctx, &page); e != nil {
			return e
		}
		if page.SelectedCount == 0 {
			return failure(core.ErrConflict)
		}
		generateArgs["operationKey"] = id + "-generate-" + strconv.Itoa(part)
		generateArgs["selection"] = core.Selection{After: after, Limit: 2000}
		generated, err := step("batch_generate", generateArgs)
		if err != nil {
			return err
		}
		drafts = append(drafts, ref(in.DraftDestination, generated))
		total += page.SelectedCount
		if page.NextAfter == "" {
			break
		}
		after = page.NextAfter
	}
	if total != count {
		return failure(core.ErrConflict)
	}
	pushed, e := step("crm_push", map[string]any{"operationKey": id + "-crm", "title": "原策略整批稿件", "review": core.OperationsDrafts{Selection: ref(in.SelectedDestination, selected), Drafts: drafts}})
	if e != nil {
		return e
	}
	return workflow.ExecuteActivity(ctx, "OperationsCycleSubmitted", id, pushed).Get(ctx, nil)
}
func (a *Activities) OperationsGenerationPage(ctx context.Context, p core.Principal, ref core.Ref, after string) (core.SelectionInfo, error) {
	return a.Store.SelectionInfo(ctx, p, ref, &core.Selection{After: after, Limit: 2000})
}
func (a *Activities) OperationsSelectedCount(ctx context.Context, p core.Principal, ref core.Ref) (int, error) {
	if e := core.Authorize(ctx, a.Store, p, ref); e != nil {
		return 0, failure(e)
	}
	snap, e := a.Store.Snapshot(ctx, ref.ResourceID, ref.Version)
	return snap.Count, failure(e)
}
func (a *Activities) OperationsDeliveryIDs(ctx context.Context, p core.Principal, id, after string) (DispatchPage, error) {
	rows, e := a.Store.Deliveries(ctx, p, id, after)
	if e != nil {
		return DispatchPage{}, failure(e)
	}
	page := DispatchPage{}
	for _, row := range rows {
		page.IDs = append(page.IDs, row.ID)
		page.Next = row.ItemID
	}
	if len(rows) < 200 {
		page.Next = ""
	}
	return page, nil
}

// MCP calls keep each tool independently invocable. Cancellation still joins
// the current original task before the cycle is allowed to finish stopping.
func (a *Activities) OperationsCancelStep(ctx context.Context, p core.Principal, id string) error {
	stop := keepAlive(ctx)
	defer stop()
	r, e := a.Store.Run(ctx, p, id)
	if core.IsMissing(e) {
		return nil
	}
	if e != nil {
		return failure(e)
	}
	if e = a.Temporal.CancelWorkflow(ctx, r.ExecutionID(), ""); e != nil {
		var missing *serviceerror.NotFound
		if errors.As(e, &missing) {
			return nil
		}
		return e
	}
	_ = a.Temporal.GetWorkflow(ctx, r.ExecutionID(), "").Get(ctx, nil)
	return ctx.Err()
}
func (a *Activities) OperationsCycleSubmitted(ctx context.Context, id, batch string) error {
	r, e := a.Store.ExecutionRun(ctx, id)
	if e != nil {
		return failure(e)
	}
	text := "本轮备稿完成，等待一次整批确认；实际发送以 CRM 逐人回执为准。"
	if batch == "" {
		text = "本轮没有满足原策略的可执行名单，未生成或外发。"
	}
	return a.Store.Emit(ctx, core.RunEvent{ID: id + "-submitted", SessionID: r.Principal.SessionID, RunID: id, Kind: "operations.submitted", Text: text, Details: map[string]any{"crmRunId": batch}})
}
