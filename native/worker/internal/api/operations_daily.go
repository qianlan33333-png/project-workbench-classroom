package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

var originalOperationsStrategy = regexp.MustCompile(`^hxc:[a-z0-9_-]{1,40}$`)

type dailyOperationsRoster struct {
	State   string
	Run     string
	Package string
	Week    string
	Scope   []struct {
		Key             string
		ID              string
		Reason          string
		ExpiresAt       *time.Time
		Expert          string
		StrategyVersion int
	}
}

func (r dailyOperationsRoster) originalBinding(run core.Run) (packageID, week string) {
	if r.Package != "" {
		return r.Package, r.Week
	}
	return run.Shared["package"].(string), run.Shared["week"].(string)
}

func (a *Server) originalScheduleEvidence(ctx context.Context, schedule *scheduleView) {
	var body []byte
	var runResource string
	err := a.Store.DB.QueryRowContext(ctx, `SELECT JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.text')),
		JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.destination')) FROM wb_items i
		JOIN wb_runs r ON r.id=i.run_id WHERE r.project_id=? AND r.session_id=? AND i.item_id=?
		AND JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.shared.strategy'))=?
		AND JSON_LENGTH(JSON_EXTRACT(JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.text')),'$.scope'))>0
		ORDER BY r.created_at DESC LIMIT 1`, schedule.ProjectID, schedule.SessionID, engine.OriginalOperationsRoster, schedule.Strategy).Scan(&body, &runResource)
	resourceID := schedule.SourceResource
	if resourceID == "" {
		resourceID = runResource
	}
	if resourceID != "" {
		if resource, err := a.Store.Resource(ctx, resourceID); err == nil {
			schedule.Source = resource.ConnectionID
		}
	}
	var latest dailyOperationsRoster
	if err != nil || json.Unmarshal(body, &latest) != nil {
		return
	}
	for _, member := range latest.Scope {
		if member.Expert != "" && !slices.Contains(schedule.Experts, member.Expert) {
			schedule.Experts = append(schedule.Experts, member.Expert)
		}
		if member.StrategyVersion > 0 && !slices.Contains(schedule.Versions, member.StrategyVersion) {
			schedule.Versions = append(schedule.Versions, member.StrategyVersion)
		}
	}
}

func (a *Server) dailyOperationsRoster(ctx context.Context, p core.Principal, id string) (core.Run, dailyOperationsRoster, error) {
	var roster dailyOperationsRoster
	run, err := a.Store.Run(ctx, p, id)
	if err != nil || run.Operation != "operations_package" || run.Shared["day"] != operationsDay(time.Now()) {
		return run, roster, core.ErrDenied
	}
	item, err := a.Store.Item(ctx, run.ID, engine.OriginalOperationsRoster)
	if err != nil {
		return run, roster, err
	}
	err = json.Unmarshal([]byte(item.Text), &roster)
	if err == nil && roster.State != "empty" && (roster.State != "ready" || !operationsCopyRun.MatchString(roster.Run) ||
		((roster.Package != "" || roster.Week != "") && !validOperationsPackage(roster.Package, roster.Week))) {
		err = core.ErrInvalid
	}
	return run, roster, err
}

func (a *Server) operationsToday(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(ctx)
	cfg, err := a.readOperations(ctx, p.ProjectID)
	if err != nil || p != operationsPrincipal(p.ProjectID) || !cfg.ReadSource {
		respond(w, nil, core.ErrDenied)
		return
	}
	resource, err := a.Store.Resource(ctx, cfg.Input.ResourceID)
	if err != nil || resource.OwnerProject != p.ProjectID && resource.MaintainerProject != p.ProjectID {
		respond(w, nil, core.ErrDenied)
		return
	}
	q := r.URL.Query()
	day := operationsDay(time.Now())
	if id := q.Get("daily"); id != "" {
		doc, err := a.operationsReviewView(ctx, p, cfg, id, q.Get("item"), q.Get("after"))
		respond(w, doc, err)
		return
	}
	history := q.Get("history") == "1"
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT r.body, COALESCE(JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.text')),''),
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(roster.body,'$.text')),'')
		FROM wb_runs r LEFT JOIN wb_items i ON i.run_id=r.id AND i.item_id=?
		LEFT JOIN wb_items roster ON roster.run_id=r.id AND roster.item_id=?
		WHERE r.project_id=? AND r.session_id=? AND JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.operation'))='operations_package'
		AND (JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.shared.day'))=? OR (? AND i.item_id IS NOT NULL))
		ORDER BY r.created_at DESC,r.id DESC`, core.OperationsReviewItem, engine.OriginalOperationsRoster, p.ProjectID, p.SessionID, day, history)
	if err != nil {
		respond(w, nil, err)
		return
	}
	defer rows.Close()
	packages := []map[string]any{}
	var unresolved []string
	attention, running := false, false
	seen := map[string]bool{}
	for rows.Next() {
		var raw, header, roster string
		var run core.Run
		var review core.OperationsReview
		if err = rows.Scan(&raw, &header, &roster); err != nil {
			respond(w, nil, err)
			return
		}
		if json.Unmarshal([]byte(raw), &run) != nil || run.Destination != cfg.Input.ResourceID {
			continue
		}
		if header == "" {
			if !history {
				var initial dailyOperationsRoster
				_ = json.Unmarshal([]byte(roster), &initial)
				if initial.State != "empty" {
					unresolved = append(unresolved, run.ID)
				}
			}
			continue
		}
		if json.Unmarshal([]byte(header), &review) != nil {
			attention = true
			continue
		}
		if !history {
			key := core.Digest([]any{run.Shared["strategy"], run.Shared["scene"]})
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		processed := review.Confirm != "" || run.Shared["day"] != day
		if history != processed || review.Count == 0 {
			continue
		}
		packages = append(packages, map[string]any{"daily": run.ID, "run_id": review.Run, "package_id": review.Package,
			"label": run.Title, "scene_label": run.Shared["scene"], "week": review.Week, "day": run.Shared["day"], "pending": review.Count})
	}
	if err = rows.Err(); err != nil {
		respond(w, nil, err)
		return
	}
	rows.Close()
	for _, id := range unresolved {
		var ended bool
		err = a.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM wb_events WHERE run_id=? AND event_kind IN ('run.failed','run.canceled'))`, id).Scan(&ended)
		if err != nil {
			respond(w, nil, err)
			return
		}
		running = running || !ended
		attention = attention || ended
	}
	respond(w, map[string]any{"day": day, "packages": packages, "attention": attention, "running": running}, nil)
}

func (a *Server) operationsReviewView(ctx context.Context, p core.Principal, cfg operationsConfig, id, item, after string) (map[string]any, error) {
	run, err := a.Store.Run(ctx, p, id)
	if err != nil || run.Operation != "operations_package" || run.Destination != cfg.Input.ResourceID {
		return nil, core.ErrDenied
	}
	head, err := a.Store.OperationsReview(ctx, id)
	if err != nil {
		return nil, err
	}
	receipts := core.OperationsBatchStatus{}
	if head.Confirm != "" {
		receipts, err = a.Store.OperationsBatchResult(ctx, head.Confirm)
		if err != nil && !core.IsMissing(err) {
			return nil, err
		}
	}
	name := operationsStaffName(cfg)
	strategy, _ := run.Shared["strategy"].(string)
	editable := head.Confirm == "" && head.Edit == "" && run.Shared["day"] == operationsDay(time.Now())
	canSend := editable && head.Count > 0 && name != "" && cfg.Transport != "" && engine.OperationsBatchEnabled(strategy, cfg.Staff)
	view := map[string]any{"state": "ready", "run": head.Run, "digest": head.Digest,
		"reviewVersion": core.OperationsReviewVersion(head, cfg.Transport, cfg.Staff, name), "sendEnabled": canSend,
		"sendAccount": name, "pending": head.Count, "reviewed": head.Confirm != "", "syncing": head.Edit != "", "plan": receipts.Plan}
	show := func(row core.OperationsReviewRow, detail bool) map[string]any {
		state, progress := operationsDeliveryLabel(head, receipts.Entries[row.Key], row)
		out := map[string]any{"key": row.Key, "id": row.ID, "nickname": row.Nickname, "reason": row.Reason, "status": state}
		if detail {
			out["message"], out["progress"], out["revision"] = row.Message, progress, row.Revision
			out["edited"], out["editable"] = row.Edited, editable && row.Editable
			out["judgment"], out["attachments"] = row.Judgment, row.Attachments
		}
		return out
	}
	result := []map[string]any{}
	if item != "" {
		row, err := a.Store.OperationsReviewRow(ctx, id, item)
		if err != nil {
			return nil, err
		}
		result = append(result, show(row, true))
	} else {
		if after != "" && !core.ValidID(after) {
			return nil, core.ErrInvalid
		}
		rows, err := a.Store.OperationsReviewRows(ctx, id, after, 101)
		if err != nil {
			return nil, err
		}
		if len(rows) > 100 {
			rows = rows[:100]
			view["next"] = rows[99].Key
		}
		for _, row := range rows {
			result = append(result, show(row, false))
		}
		if after == "" && len(rows) > 0 {
			first, err := a.Store.OperationsReviewRow(ctx, id, rows[0].Key)
			if err != nil {
				return nil, err
			}
			view["first"] = show(first, true)
		}
	}
	if current, err := a.Store.OperationsReview(ctx, id); err != nil || current != head {
		return nil, errors.Join(err, core.ErrConflict)
	}
	view["rows"] = result
	return view, nil
}

func operationsDeliveryLabel(head core.OperationsReview, receipt core.OperationsBatchReceipt, row core.OperationsReviewRow) (string, string) {
	if head.Confirm == "" {
		return "待核对", row.Progress
	}
	switch receipt.State {
	case "sent":
		return "已发送", receipt.At
	case "failed":
		return "发送失败", receipt.Error
	case "unknown":
		return "回执待核实", "正在核对原 CRM 计划。"
	case "pending_confirmation":
		return "待发送", "企微已受理，等待员工在企微端确认。"
	case "accepted":
		return "待发送", "CRM 已受理。"
	default:
		return "待发送", "正在提交 CRM 批量计划。"
	}
}

func operationsStaffName(cfg operationsConfig) string {
	if cfg.Staff == "" || os.Getenv("WORKBENCH_OPERATIONS_ORIGINAL_STAFF_ID") != cfg.Staff {
		return ""
	}
	return strings.TrimSpace(os.Getenv("WORKBENCH_OPERATIONS_STAFF_NAME"))
}

func (a *Server) configureOriginalOperationsSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !principal(ctx).IsOwner() {
		respond(w, nil, core.ErrDenied)
		return
	}
	var in struct{ ProjectID, Strategy, Time string }
	if !decode(w, r, &in) {
		return
	}
	cfg, err := a.readOperations(ctx, in.ProjectID)
	if err != nil || !cfg.ReadSource || !originalOperationsStrategy.MatchString(in.Strategy) {
		respond(w, nil, core.ErrDenied)
		return
	}
	t, err := time.Parse("15:04", in.Time)
	if err != nil {
		respond(w, nil, core.ErrInvalid)
		return
	}
	p := operationsPrincipal(cfg.ProjectID)
	data, err := a.Store.ReadOperationsDirectory(ctx, p, cfg.Input.ResourceID, "catalog", map[string]string{})
	var catalog struct {
		Packages []struct {
			Strategy string `json:"strategy_key"`
			Label    string `json:"label"`
		}
	}
	if err != nil || json.Unmarshal(data, &catalog) != nil {
		respond(w, nil, errors.Join(err, core.ErrInvalid))
		return
	}
	title := ""
	for _, pack := range catalog.Packages {
		if pack.Strategy == in.Strategy {
			title = pack.Label
			break
		}
	}
	if title == "" {
		respond(w, nil, core.ErrInvalid)
		return
	}
	id := "program-" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/original/"+in.Strategy)).String()
	hours := []int{t.Hour()}
	spec := client.ScheduleSpec{TimeZoneName: "Asia/Shanghai", Calendars: []client.ScheduleCalendarSpec{{
		Hour: []client.ScheduleRange{{Start: t.Hour()}}, Minute: []client.ScheduleRange{{Start: t.Minute()}},
	}}}
	action := &client.ScheduleWorkflowAction{ID: id, Workflow: engine.ScheduledOperations, TaskQueue: engine.BatchQueue,
		Args: []any{engine.OperationsScheduleInput{Principal: p, Resource: cfg.Input.ResourceID, Strategy: in.Strategy}}}
	v := scheduleView{ID: id, Title: title, SessionID: p.SessionID, Hours: hours, Minute: t.Minute(), ProjectID: p.ProjectID,
		Strategy: in.Strategy, SourceResource: cfg.Input.ResourceID}
	handle, err := a.Temporal.ScheduleClient().Create(ctx, client.ScheduleOptions{ID: id, Spec: spec, Action: action,
		Overlap: enums.SCHEDULE_OVERLAP_POLICY_SKIP, CatchupWindow: time.Hour})
	created := err == nil
	var exists *serviceerror.AlreadyExists
	if errors.As(err, &exists) || errors.Is(err, temporal.ErrScheduleAlreadyRunning) {
		handle = a.Temporal.ScheduleClient().GetHandle(ctx, id)
		err = handle.Update(ctx, client.ScheduleUpdateOptions{DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			s := in.Description.Schedule
			s.Spec, s.Action = &spec, action
			return &client.ScheduleUpdate{Schedule: &s}, nil
		}})
	}
	if err != nil {
		respond(w, nil, err)
		return
	}
	body, _ := json.Marshal(v)
	if _, err = a.Store.DB.ExecContext(ctx, `INSERT INTO wb_settings VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)`, id, string(body)); err != nil && created {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, handle.Delete(cleanup))
	}
	respond(w, v, err)
}

func (a *Server) confirmDailyOperations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(ctx)
	cfg, err := a.readOperations(ctx, p.ProjectID)
	if err != nil || p != operationsPrincipal(p.ProjectID) || !cfg.ReadSource {
		respond(w, nil, core.ErrDenied)
		return
	}
	var in struct{ Daily, ReviewVersion string }
	if !decode(w, r, &in) {
		return
	}
	if !core.ValidID(in.Daily) || !operationsDigest.MatchString(in.ReviewVersion) {
		respond(w, nil, core.ErrInvalid)
		return
	}
	parent, err := a.Store.Run(ctx, p, in.Daily)
	if err != nil || parent.Operation != "operations_package" || parent.Destination != cfg.Input.ResourceID {
		respond(w, nil, core.ErrDenied)
		return
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.ProjectID+"/operations-batch/"+in.Daily+"/"+in.ReviewVersion)).String()
	head, err := a.Store.OperationsReview(ctx, parent.ID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if head.Confirm == id {
		respond(w, RunRef{RunID: id}, nil)
		return
	}
	strategy, _ := parent.Shared["strategy"].(string)
	name := operationsStaffName(cfg)
	if !engine.OperationsBatchEnabled(strategy, cfg.Staff) || name == "" || cfg.Transport == "" {
		respond(w, nil, core.ErrDenied)
		return
	}
	if parent.Shared["day"] != operationsDay(time.Now()) || head.Confirm != "" || head.Edit != "" || head.Count == 0 ||
		in.ReviewVersion != core.OperationsReviewVersion(head, cfg.Transport, cfg.Staff, name) {
		respond(w, nil, core.ErrConflict)
		return
	}
	run := core.Run{ID: id, Principal: p, Operation: "operations_batch_confirm", ParentRunID: parent.ID,
		Title: "运营工作包确认发送", ExplicitInputs: true, Quiet: true, Destination: parent.Destination,
		Shared: map[string]any{"version": in.ReviewVersion, "staff": cfg.Staff, "transport": cfg.Transport, "strategy": strategy}}
	if err = a.Store.SaveRun(ctx, run); err != nil {
		respond(w, nil, err)
		return
	}
	_, err = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: engine.BatchQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY}, engine.WriteTask,
		engine.WriteInput{RunID: id, ProjectID: p.ProjectID, Operation: run.Operation})
	respond(w, RunRef{RunID: id}, accepted(err))
}

func (a *Server) operationsBatchManifest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(ctx)
	if !p.IsOwner() || p.SessionID != "channel-connector" {
		respond(w, nil, core.ErrDenied)
		return
	}
	q := r.URL.Query()
	if q.Get("check") == "write" {
		ok, err := a.operationsBatchWriteAllowed(ctx, q.Get("id"), q.Get("connection"))
		respond(w, map[string]bool{"sendEnabled": ok}, err)
		return
	}
	run, rows, err := a.Store.OperationsBatchManifest(ctx, q.Get("id"))
	if err != nil || run.Shared["transport"] != q.Get("connection") {
		respond(w, nil, core.ErrDenied)
		return
	}
	strategy, _ := run.Shared["strategy"].(string)
	staff, _ := run.Shared["staff"].(string)
	respond(w, map[string]any{"staff": staff, "rows": rows, "sendEnabled": engine.OperationsBatchEnabled(strategy, staff)}, nil)
}

func (a *Server) operationsBatchWriteAllowed(ctx context.Context, id, connection string) (bool, error) {
	run, err := a.Store.ExecutionRun(ctx, id)
	if err != nil || run.Operation != "operations_batch_confirm" || run.Shared["transport"] != connection {
		return false, core.ErrDenied
	}
	parent, err := a.Store.Run(ctx, run.Principal, run.ParentRunID)
	if err != nil || parent.Shared["day"] != operationsDay(time.Now()) {
		return false, err
	}
	head, err := a.Store.OperationsReview(ctx, parent.ID)
	if err != nil || head.Confirm != id {
		return false, err
	}
	cfg, err := a.readOperations(ctx, run.Principal.ProjectID)
	if err != nil {
		return false, err
	}
	strategy, _ := run.Shared["strategy"].(string)
	if !cfg.ReadSource || cfg.Input.ResourceID != parent.Destination || cfg.Transport != connection ||
		run.Shared["staff"] != cfg.Staff || !engine.OperationsBatchEnabled(strategy, cfg.Staff) {
		return false, nil
	}
	transport, err := a.Store.Transport(ctx, run.Principal, connection)
	if err != nil || transport.Kind != "crm-cli" {
		return false, err
	}
	raw, err := a.Store.ReadOperationsDirectory(ctx, run.Principal, parent.Destination, "package_review",
		map[string]string{"package": head.Package, "week": head.Week, "run": head.Run, "version": "authority"})
	if err != nil {
		return false, err
	}
	var check struct {
		State, Digest, IdentityVersion string
		Exclusive                      bool
	}
	if err = json.Unmarshal(raw, &check); err != nil {
		return false, err
	}
	return check.State == "ready" && check.Exclusive && check.Digest == head.Digest &&
		check.IdentityVersion == head.IdentityVersion, nil
}
