package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-openapi/jsonpointer"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type Server struct {
	Store                    *core.Store
	Temporal                 client.Client
	Token                    string
	CustomerClients          []CustomerClient
	CustomerStore            *core.Store
	personalMu               sync.Mutex
	operationsMu             sync.Mutex
	libraryMu                sync.Mutex
	libraryEvidence          libraryCommand
	LibraryPermissionsLocked bool
	Host                     engine.AgentHost
	VaultRoot                string
	BackgroundReady          func() bool
}
type principalKey struct{}

func principal(ctx context.Context) core.Principal {
	p, _ := ctx.Value(principalKey{}).(core.Principal)
	return p
}
func (a *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	a.configRoutes(mux)
	a.expertRoutes(mux)
	a.activationRoutes(mux)
	a.personalRoutes(mux)
	a.messageRoutes(mux)
	a.businessPolicyRoutes(mux)
	a.libraryRoutes(mux)
	a.replyRoutes(mux)
	a.scheduleRoutes(mux)
	a.resourceRoutes(mux)
	a.vaultRoutes(mux)
	a.operationsRoutes(mux)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if e := a.Store.DB.PingContext(ctx); e != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		if a.Temporal != nil {
			if _, e := a.Temporal.WorkflowService().GetSystemInfo(ctx, &workflowservice.GetSystemInfoRequest{}); e != nil {
				http.Error(w, "execution service unavailable", 503)
				return
			}
		}
		backgroundReady := a.BackgroundReady == nil || a.BackgroundReady()
		write(w, map[string]any{"ready": true, "backgroundReady": backgroundReady})
	})
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if access, ok := r.Context().Value(customerAccessKey{}).(core.CustomerAccess); ok {
			return a.customerMCP(access)
		}
		return a.MCP(principal(r.Context()))
	}, &mcp.StreamableHTTPOptions{Stateless: true}))
	mux.HandleFunc("GET /resources", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Resources(r.Context(), principal(r.Context()))
		respond(w, v, e)
	})
	mux.HandleFunc("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		var v core.Project
		if !decode(w, r, &v) {
			return
		}
		respond(w, v, a.Store.PutProject(r.Context(), principal(r.Context()), v))
	})
	mux.HandleFunc("POST /resources", func(w http.ResponseWriter, r *http.Request) {
		var v core.Resource
		if !decode(w, r, &v) {
			return
		}
		respond(w, v, a.Store.RegisterResource(r.Context(), principal(r.Context()), v))
	})

	mux.HandleFunc("POST /snapshots", func(w http.ResponseWriter, r *http.Request) {
		var v core.Snapshot
		if !decode(w, r, &v) {
			return
		}
		respond(w, v, a.Store.BeginSnapshot(r.Context(), principal(r.Context()), v))
	})
	mux.HandleFunc("POST /stage", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			ResourceID string
			Version    string
			Records    []core.Record
		}
		if !decode(w, r, &v) {
			return
		}
		respond(w, map[string]bool{"staged": true}, a.Store.Stage(r.Context(), principal(r.Context()), v.ResourceID, v.Version, v.Records))
	})
	mux.HandleFunc("POST /publish", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			ResourceID string
			Version    string
			Count      int
		}
		if !decode(w, r, &v) {
			return
		}
		out, e := a.Store.Publish(r.Context(), principal(r.Context()), v.ResourceID, v.Version, v.Count)
		respond(w, out, e)
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.Events(r.Context(), principal(r.Context()), r.URL.Query().Get("after"))
		respond(w, v, e)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			mux.ServeHTTP(w, r)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if access, ok := a.customerAccess(token); ok {
			if r.URL.Path != "/mcp" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			mux.ServeHTTP(w, r.WithContext(context.WithValue(ctx, customerAccessKey{}, access)))
			return
		}
		if len(a.Token) < 32 || subtle.ConstantTimeCompare([]byte(token), []byte(a.Token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		p := core.Principal{Kind: r.Header.Get("X-Workbench-Kind"), ProjectID: r.Header.Get("X-Workbench-Project"), SessionID: r.Header.Get("X-Workbench-Session")}
		if p.Kind == "personal" {
			config, err := a.personalConfig(r.Context())
			if err != nil && !core.IsMissing(err) {
				http.Error(w, "personal session unavailable", http.StatusServiceUnavailable)
				return
			}
			if err == nil && p.SessionID != config.SessionID {
				http.Error(w, "SuperAgent session required", http.StatusForbidden)
				return
			}
		}
		if e := a.Store.BindSession(r.Context(), p); e != nil {
			http.Error(w, "invalid session binding", 403)
			return
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func respond(w http.ResponseWriter, v any, e error) {
	if e == nil {
		write(w, v)
		return
	}
	status := 400
	var source *core.MySQLSourceError
	if errors.As(e, &source) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": source.Code, "error": source.Text})
		return
	}
	if errors.Is(e, core.ErrDenied) {
		status = 403
	}
	if errors.Is(e, core.ErrConflict) {
		status = 409
	}
	http.Error(w, e.Error(), status)
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeLimited(w, r, v, 8*1024*1024)
}

func decodeLimited(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		http.Error(w, "invalid request", 400)
		return false
	}
	if e := d.Decode(new(any)); e != io.EOF {
		http.Error(w, "invalid request", 400)
		return false
	}
	return true
}

type Empty struct{}
type Catalog struct {
	Resources []core.Resource `json:"resources"`
}
type ReadInput struct {
	Ref   core.Ref `json:"ref"`
	After string   `json:"after,omitempty"`
	Limit int      `json:"limit,omitempty"`
}
type BatchInput struct {
	MethodRefs    map[string]core.RecordRef `json:"methodRefs,omitempty"`
	MethodPointer string                    `json:"methodPointer,omitempty"`
	Selection     *core.Selection           `json:"selection,omitempty"`
	OperationKey  string                    `json:"operationKey"`
	Title         string                    `json:"title"`
	Sources       []core.Ref                `json:"sources"`
	Destination   string                    `json:"destination"`
	Method        core.Method               `json:"method,omitempty"`
	MethodRef     *core.RecordRef           `json:"methodRef,omitempty"`
}
type RunRef struct {
	InputRefs []core.Ref          `json:"inputRefs,omitempty"`
	RunID     string              `json:"runId"`
	Selection *core.SelectionInfo `json:"selection,omitempty"`
}
type RunStatus struct {
	Selection *core.SelectionInfo `json:"selection,omitempty"`
	RunID     string              `json:"runId"`
	Execution string              `json:"execution"`
	Stats     core.RunStats       `json:"stats"`
	Title     string              `json:"title"`
	Published bool                `json:"published"`
}
type ItemsInput struct {
	RunID string `json:"runId"`
	After string `json:"after,omitempty"`
	Limit int    `json:"limit,omitempty"`
}
type ItemsResult struct {
	Items []core.ItemResult `json:"items"`
	Next  string            `json:"next,omitempty"`
}

func (a *Server) MCP(bound core.Principal) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "workbench-tools", Version: "0.1.0"}, nil)
	if bound.Kind == "personal" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := a.Store.CheckLibraryPersonal(ctx, bound)
		cancel()
		if err != nil {
			return s
		}
	}
	if bound.Kind == "project" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, e := a.Store.SessionRoute(ctx, bound)
		cancel()
		if e == nil {
			a.messageTools(s, bound)
			if a.Store.HasBusinessPolicies(bound.ProjectID) {
				a.businessPolicyTool(s, bound)
			}
			a.resourceTools(s, bound)
			s.RemoveTools("message_prepare")
			return s
		}
		if !core.IsMissing(e) {
			return s
		}
	}
	a.attachmentTools(s, bound)
	a.programTools(s, bound)
	a.crmTools(s, bound)
	a.strategyTools(s, bound)
	a.resourceTools(s, bound)
	a.dispatchBatchTools(s, bound)
	a.scheduleTools(s, bound)
	a.moreTools(s, bound)
	a.delegationTools(s, bound)
	if bound.Kind == "personal" {
		a.replyActivityTool(s, bound)
		a.feishuCLITool(s, bound)
	}
	a.messageTools(s, bound)
	a.libraryTools(s, bound)
	mcp.AddTool(s, &mcp.Tool{Name: "resource_list", Description: "Discover registered databases and corpora by name, purpose and current access. A library with access none needs a project grant; it exposes no sections, schema or content."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, struct {
		Resources []core.ResourceDiscovery `json:"resources"`
	}, error) {
		v, e := a.Store.ResourceDirectory(ctx, bound)
		return nil, struct {
			Resources []core.ResourceDiscovery `json:"resources"`
		}{v}, e
	})

	mcp.AddTool(s, &mcp.Tool{Name: "resource_read", Description: "Read a small page from a fixed resource snapshot and section. Use batch_generate for bulk processing."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReadInput) (*mcp.CallToolResult, core.Page, error) {
		if in.Limit <= 0 {
			in.Limit = 5
		}
		if in.Limit > 20 {
			return nil, core.Page{}, core.ErrInvalid
		}
		v, e := a.Store.Read(ctx, bound, in.Ref, in.After, in.Limit)
		if e == nil {
			b, _ := json.Marshal(v)
			if len(b) > 48000 {
				return nil, core.Page{}, fmt.Errorf("%w: selected records exceed conversation budget; choose a smaller section/page or submit batch references", core.ErrInvalid)
			}
		}
		return nil, v, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "batch_generate", Description: "Submit durable per-record generation using fixed resource references. selection accepts either recordIds or limit, at most 2000; omission defaults to the first 2000 records. Select the intended population explicitly. Prefer methodRef to a published fixed method. Returns immediately; no customer sending."}, func(ctx context.Context, _ *mcp.CallToolRequest, in BatchInput) (*mcp.CallToolResult, RunRef, error) {
		p := bound
		if p.Kind != "project" {
			return nil, RunRef{}, core.ErrDenied
		}
		if e := a.validateOperationsGeneration(ctx, p, in); e != nil {
			return nil, RunRef{}, e
		}
		selection, e := core.BoundedSelection(in.Selection)
		if e != nil {
			return nil, RunRef{}, e
		}
		if in.MethodRef != nil {
			if len(in.MethodRefs) > 0 {
				return nil, RunRef{}, core.ErrInvalid
			}
			if in.Method.Instructions != "" {
				return nil, RunRef{}, core.ErrInvalid
			}
			records, e := a.Store.ReadIDs(ctx, p, in.MethodRef.Ref, []string{in.MethodRef.ID})
			if e != nil {
				return nil, RunRef{}, e
			}
			if len(records) != 1 {
				return nil, RunRef{}, core.ErrInvalid
			}
			b, e := json.Marshal(records[0].Fields["method"])
			if e != nil {
				return nil, RunRef{}, e
			}
			if e = json.Unmarshal(b, &in.Method); e != nil {
				return nil, RunRef{}, core.ErrInvalid
			}
		}
		methods := map[string]core.Method{}
		methodSources := []core.Ref{}
		if in.MethodRef != nil {
			methodSources = append(methodSources, in.MethodRef.Ref)
		}
		if len(in.MethodRefs) > 0 {
			if in.Method.Instructions != "" || len(in.MethodRefs) > 64 {
				return nil, RunRef{}, core.ErrInvalid
			}
			if _, e := jsonpointer.New(in.MethodPointer); e != nil {
				return nil, RunRef{}, core.ErrInvalid
			}
			keys := make([]string, 0, len(in.MethodRefs))
			for key := range in.MethodRefs {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if !core.ValidID(key) {
					return nil, RunRef{}, core.ErrInvalid
				}
				ref := in.MethodRefs[key]
				rows, e := a.Store.ReadIDs(ctx, p, ref.Ref, []string{ref.ID})
				if e != nil {
					return nil, RunRef{}, e
				}
				if len(rows) != 1 {
					return nil, RunRef{}, core.ErrInvalid
				}
				b, _ := json.Marshal(rows[0].Fields["method"])
				var method core.Method
				if e = json.Unmarshal(b, &method); e != nil || method.Instructions == "" || len(method.Instructions) > 64000 {
					return nil, RunRef{}, core.ErrInvalid
				}
				if _, e = method.ResolveOutput(); e != nil {
					return nil, RunRef{}, e
				}
				method.Digest = ""
				method.Digest = core.Digest(method)
				methods[key] = method
				methodSources = append(methodSources, ref.Ref)
			}
			in.Method = core.Method{Name: "固定逐条方法", Digest: core.Digest(methods)}
		}
		if !core.ValidID(in.OperationKey) || len(in.Sources) == 0 || len(in.Sources) > 16 || (in.Method.Instructions == "" && len(methods) == 0) || len(in.Method.Instructions) > 64000 {
			return nil, RunRef{}, core.ErrInvalid
		}
		if _, e := in.Method.ResolveOutput(); e != nil {
			return nil, RunRef{}, e
		}
		in.Method.Digest = ""
		in.Method.Digest = core.Digest(in.Method)
		selectionInfo, e := a.Store.SelectionInfo(ctx, p, in.Sources[0], selection)
		if e != nil {
			return nil, RunRef{}, e
		}
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/"+in.OperationKey)).String()
		r := core.Run{ID: id, Principal: p, Operation: "generate", ExplicitInputs: len(in.MethodRefs) > 0, Sources: in.Sources, Destination: in.Destination, Method: in.Method, Methods: methods, MethodRefs: in.MethodRefs, MethodPointer: in.MethodPointer, Lineage: core.UnionRefs(methodSources), Title: in.Title, Selection: selection}
		if e := a.Store.SaveRun(ctx, r); e != nil {
			return nil, RunRef{}, e
		}
		_, e = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: engine.BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.WriteTask, engine.WriteInput{RunID: r.ID, ProjectID: r.Principal.ProjectID, Operation: r.Operation})
		var already *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(e, &already) {
			e = nil
		}
		return nil, RunRef{RunID: id, Selection: &selectionInfo}, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "run_get", Description: "Read the real execution status and item counts of an existing run."}, func(ctx context.Context, _ *mcp.CallToolRequest, in RunRef) (*mcp.CallToolResult, RunStatus, error) {
		v, e := a.runStatus(ctx, bound, in.RunID)
		return nil, v, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "run_items", Description: "Read saved results without regenerating. Permission is checked again for every source."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ItemsInput) (*mcp.CallToolResult, ItemsResult, error) {
		if in.Limit <= 0 {
			in.Limit = 5
		}
		if in.Limit > 20 {
			return nil, ItemsResult{}, core.ErrInvalid
		}
		v, e := a.Store.Items(ctx, bound, in.RunID, in.After)
		out := ItemsResult{Items: v}
		if len(v) > in.Limit {
			out.Items = v[:in.Limit]
			out.Next = out.Items[len(out.Items)-1].ID
		}
		return nil, out, e
	})
	mcp.AddTool(s, &mcp.Tool{Name: "run_cancel", Description: "Request cancellation of the original run; does not undo completed external actions."}, func(ctx context.Context, _ *mcp.CallToolRequest, in RunRef) (*mcp.CallToolResult, RunRef, error) {
		r, e := a.Store.Run(ctx, bound, in.RunID)
		if e != nil {
			return nil, in, e
		}
		if r.Operation == "operations_batch_confirm" {
			if e = a.Store.StopOperationsBatch(ctx, bound, r.ID); e != nil {
				return nil, in, e
			}
			_ = a.Temporal.CancelWorkflow(ctx, r.ID+"-delivery", "")
		}
		e = a.Temporal.CancelWorkflow(ctx, r.ExecutionID(), r.ExecutionRunID())
		return nil, in, e
	})
	return s
}

func (a *Server) runStatus(ctx context.Context, p core.Principal, id string) (RunStatus, error) {
	r, e := a.Store.Run(ctx, p, id)
	if e != nil {
		return RunStatus{}, e
	}
	stats, e := a.Store.Stats(ctx, p, id)
	if e != nil {
		return RunStatus{}, e
	}
	out := RunStatus{RunID: id, Title: r.Title, Stats: stats, Execution: "Unknown"}
	if r.Selection != nil && len(r.Sources) > 0 {
		info, err := a.Store.SelectionInfo(ctx, p, r.Sources[0], r.Selection)
		if err != nil {
			return out, err
		}
		out.Selection = &info
	}
	version := r.ID
	if r.Operation == "pipeline" {
		version = uuid.NewSHA1(uuid.NameSpaceURL, []byte(r.ID+"/generate")).String()
	}
	if r.Destination != "" {
		snap, err := a.Store.Snapshot(ctx, r.Destination, version)
		out.Published = err == nil && snap.State == "published"
	}
	if a.Temporal == nil {
		return out, nil
	}
	read, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	v, e := a.Temporal.DescribeWorkflowExecution(read, r.ExecutionID(), r.ExecutionRunID())
	var missing *serviceerror.NotFound
	if errors.As(e, &missing) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if info := v.GetWorkflowExecutionInfo(); info != nil && info.GetStatus() != enums.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED {
		out.Execution = info.Status.String()
	}
	return out, nil
}
