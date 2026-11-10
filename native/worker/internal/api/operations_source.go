package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

var operationsWeek = regexp.MustCompile(`^\d{4}-W\d{2}$`)
var operationsCopyRun = regexp.MustCompile(`^copybatch-[a-f0-9]{32}$`)
var operationsDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (a *Server) operationsSource(w http.ResponseWriter, r *http.Request) {
	p := principal(r.Context())
	cfg, err := a.readOperations(r.Context(), p.ProjectID)
	if err != nil || p != operationsPrincipal(p.ProjectID) || !cfg.ReadSource {
		respond(w, nil, core.ErrDenied)
		return
	}
	q := r.URL.Query()
	switch q.Get("view") {
	case "catalog":
		a.operationsDirectory(w, r, p, cfg, "catalog", map[string]string{})
	case "members":
		a.operationsDirectory(w, r, p, cfg, "members", map[string]string{
			"package": q.Get("package"), "week": q.Get("week"), "after": q.Get("after"), "revision": q.Get("revision"),
		})
	case "package":
		a.operationsDirectory(w, r, p, cfg, "package", map[string]string{
			"package": q.Get("package"), "week": q.Get("week"), "run": q.Get("run"), "item": q.Get("item"),
		})
	case "package_history":
		a.operationsDirectory(w, r, p, cfg, "package_history", map[string]string{"week": q.Get("week")})
	case "package_card":
		a.operationsDirectory(w, r, p, cfg, "package_card", map[string]string{
			"package": q.Get("package"), "week": q.Get("week"), "run": q.Get("run"),
			"item": q.Get("item"), "digest": q.Get("digest"), "card": q.Get("card"),
		})
	default:
		respond(w, nil, core.ErrInvalid)
	}
}

func validOperationsPackage(packageID, week string) bool {
	return core.ValidID(packageID) && strings.HasPrefix(packageID, "weekpkg-") && operationsWeek.MatchString(week)
}

func (a *Server) startOperationsPackage(w http.ResponseWriter, r *http.Request) {
	p := principal(r.Context())
	cfg, err := a.readOperations(r.Context(), p.ProjectID)
	if err != nil || p != operationsPrincipal(p.ProjectID) || !cfg.ReadSource {
		respond(w, nil, core.ErrDenied)
		return
	}
	var in struct {
		Package, Week, RequestID, Revision string
	}
	if !decode(w, r, &in) {
		return
	}
	if !validOperationsPackage(in.Package, in.Week) {
		respond(w, nil, core.ErrInvalid)
		return
	}
	if _, err := uuid.Parse(in.RequestID); err != nil || !operationsDigest.MatchString(in.Revision) {
		respond(w, nil, core.ErrInvalid)
		return
	}
	params := map[string]any{"package": in.Package, "week": in.Week, "revision": in.Revision}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.ProjectID+"/"+in.Week+"/"+in.Package+"/generation/"+in.RequestID)).String()
	task := core.Run{ID: id, Principal: p, Operation: "operations_package", Destination: cfg.Input.ResourceID,
		Shared: params, Title: "原运营工作包", ExplicitInputs: true, Quiet: true}
	ctx := r.Context()
	a.operationsMu.Lock()
	defer a.operationsMu.Unlock()
	if latest, state, lookupErr := a.latestOperationsPackage(ctx, p, in.Package, in.Week); lookupErr != nil {
		respond(w, nil, lookupErr)
		return
	} else if state == "Running" {
		if latest.ID == id {
			respond(w, RunRef{RunID: id}, nil)
		} else {
			respond(w, nil, core.ErrConflict)
		}
		return
	}
	if err = a.Store.SaveRun(ctx, task); err != nil {
		respond(w, nil, err)
		return
	}
	_, err = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: id, TaskQueue: engine.BatchQueue, WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, engine.WriteTask, engine.WriteInput{RunID: id, ProjectID: p.ProjectID, Operation: task.Operation})
	respond(w, RunRef{RunID: id}, accepted(err))
}

func (a *Server) editOperationsPackage(w http.ResponseWriter, r *http.Request) {
	p := principal(r.Context())
	cfg, err := a.readOperations(r.Context(), p.ProjectID)
	if err != nil || p != operationsPrincipal(p.ProjectID) || !cfg.ReadSource {
		respond(w, nil, core.ErrDenied)
		return
	}
	var in struct {
		Package, Week, Run, Item, Digest, Text, Daily string
		Revision                                      int
	}
	if !decode(w, r, &in) {
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	size := utf8.RuneCountInString(in.Text)
	if !validOperationsPackage(in.Package, in.Week) || !operationsCopyRun.MatchString(in.Run) ||
		!core.ValidID(in.Item) || !operationsDigest.MatchString(in.Digest) ||
		in.Revision < 0 || in.Revision > 999999999 || size < 2 || size > 5000 {
		respond(w, nil, core.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 130*time.Second)
	defer cancel()
	if in.Daily != "" {
		parent, roster, err := a.dailyOperationsRoster(ctx, p, in.Daily)
		if err != nil || roster.Run != in.Run || parent.Shared["package"] != in.Package || parent.Shared["week"] != in.Week ||
			a.operationsExecution(ctx, in.Daily) != "Completed" {
			respond(w, nil, core.ErrConflict)
			return
		}
		allowed := false
		for _, member := range roster.Scope {
			if member.Key == in.Item && (member.ExpiresAt == nil || time.Now().Before(*member.ExpiresAt)) {
				allowed = true
				break
			}
		}
		if !allowed {
			respond(w, nil, core.ErrConflict)
			return
		}
	}
	run := core.Run{ID: uuid.NewString(), Principal: p, Operation: "operations_package_edit", Destination: cfg.Input.ResourceID,
		Title: "保存原工作包最终稿", ExplicitInputs: true, Quiet: true}
	run.Shared = map[string]any{
		"package": in.Package, "week": in.Week, "run": in.Run, "item": in.Item,
		"digest": in.Digest, "revision": strconv.Itoa(in.Revision), "text": in.Text,
	}
	if err = a.Store.SaveRun(ctx, run); err != nil {
		respond(w, nil, err)
		return
	}
	execution, err := a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: engine.ProjectWriterID(p.ProjectID), TaskQueue: engine.BatchQueue,
		WorkflowIDReusePolicy:                    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
		WorkflowExecutionTimeout:                 7 * time.Minute,
	}, engine.OperationsPackageEdit, run.ID)
	var running *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &running) {
		http.Error(w, "此项目正在处理写入，请稍后保存。", http.StatusConflict)
		return
	}
	var data json.RawMessage
	if err == nil {
		err = execution.Get(ctx, &data)
	}
	if err != nil {
		http.Error(w, "保存结果待核对。请刷新原稿后再试。", http.StatusBadGateway)
		return
	}
	respond(w, data, nil)
}

func (a *Server) latestOperationsPackage(ctx context.Context, p core.Principal, packageID, week string) (core.Run, string, error) {
	var id string
	err := a.Store.DB.QueryRowContext(ctx, `SELECT id FROM wb_runs WHERE session_id=?
		AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='operations_package'
		AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.shared.package'))=?
		AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.shared.week'))=? ORDER BY created_at DESC LIMIT 1`, p.SessionID, packageID, week).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Run{}, "", nil
	}
	if err != nil {
		return core.Run{}, "", err
	}
	run, err := a.Store.Run(ctx, p, id)
	if err != nil {
		return run, "", err
	}
	status, err := a.Temporal.DescribeWorkflowExecution(ctx, id, "")
	var missing *serviceerror.NotFound
	if errors.As(err, &missing) {
		return run, "", nil
	}
	if err != nil {
		return run, "", err
	}
	return run, status.WorkflowExecutionInfo.Status.String(), nil
}

func (a *Server) operationsPackageStatus(w http.ResponseWriter, r *http.Request) {
	p := principal(r.Context())
	if p != operationsPrincipal(p.ProjectID) {
		respond(w, nil, core.ErrDenied)
		return
	}
	q := r.URL.Query()
	if q.Get("id") == "" {
		if !validOperationsPackage(q.Get("package"), q.Get("week")) {
			respond(w, nil, core.ErrInvalid)
			return
		}
		run, state, err := a.latestOperationsPackage(r.Context(), p, q.Get("package"), q.Get("week"))
		respond(w, map[string]string{"id": run.ID, "state": state}, err)
		return
	}
	run, err := a.Store.Run(r.Context(), p, q.Get("id"))
	if err != nil || (run.Operation != "operations_package" && run.Operation != "operations_package_confirm") {
		respond(w, nil, core.ErrDenied)
		return
	}
	status, err := a.Temporal.DescribeWorkflowExecution(r.Context(), run.ID, "")
	if err != nil {
		respond(w, nil, err)
		return
	}
	out := map[string]string{"state": status.WorkflowExecutionInfo.Status.String()}
	if run.Operation == "operations_package_confirm" && out["state"] == "Completed" {
		result, err := a.Store.Item(r.Context(), run.ID, engine.OriginalOperationsConfirmation)
		if err != nil {
			respond(w, nil, err)
			return
		}
		out["result"] = result.Text
	}
	respond(w, out, nil)
}

func (a *Server) operationsDirectory(w http.ResponseWriter, r *http.Request, p core.Principal, cfg operationsConfig, view string, params map[string]string) {
	ctx, cancel := context.WithTimeout(r.Context(), 84*time.Second)
	defer cancel()
	data, err := a.Store.ReadOperationsDirectory(ctx, p, cfg.Input.ResourceID, view, params)
	if err != nil {
		http.Error(w, "上下文中心暂不可读，请重试。", http.StatusBadGateway)
		return
	}
	if view == "package" && params["item"] != "" && params["run"] != "" {
		var doc map[string]any
		if json.Unmarshal(data, &doc) == nil {
			if rows, ok := doc["rows"].([]any); ok && len(rows) == 1 {
				if item, ok := rows[0].(map[string]any); ok && item["status"] == "待生成" {
					var issue string
					err := a.Store.DB.QueryRowContext(ctx, `SELECT JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.error'))
						FROM wb_items i JOIN wb_runs run ON run.id=i.run_id
						WHERE run.project_id=? AND run.session_id=? AND i.item_id=?
						AND JSON_UNQUOTE(JSON_EXTRACT(run.body,'$.operation'))='operations_package'
						AND JSON_UNQUOTE(JSON_EXTRACT(run.body,'$.shared.package'))=?
						AND JSON_UNQUOTE(JSON_EXTRACT(run.body,'$.shared.week'))=?
						AND JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.inputHash'))=?
						ORDER BY run.created_at DESC LIMIT 1`, p.ProjectID, p.SessionID, params["item"],
						params["package"], params["week"], params["run"]).Scan(&issue)
					if err == nil && issue != "" {
						item["progress"] = issue
					}
				}
			}
			respond(w, doc, nil)
			return
		}
	}
	respond(w, data, nil)
}
