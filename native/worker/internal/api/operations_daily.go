package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
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
	State string
	Run   string
	Scope []struct {
		Key       string
		ID        string
		Reason    string
		ExpiresAt *time.Time
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
	if err == nil && roster.State != "empty" && (roster.State != "ready" || !operationsCopyRun.MatchString(roster.Run)) {
		err = core.ErrInvalid
	}
	return run, roster, err
}

func (a *Server) dailyOperationsPackage(ctx context.Context, p core.Principal, cfg operationsConfig, id, item string) (map[string]any, int, error) {
	run, roster, err := a.dailyOperationsRoster(ctx, p, id)
	if err != nil || roster.State != "ready" {
		return nil, 0, errors.Join(err, core.ErrConflict)
	}
	allowed := make(map[string]int, len(roster.Scope))
	now := time.Now()
	for i, member := range roster.Scope {
		if member.ExpiresAt == nil || now.Before(*member.ExpiresAt) {
			allowed[member.Key] = i
		}
	}
	if _, ok := allowed[item]; item != "" && !ok {
		return nil, 0, core.ErrConflict
	}
	data, err := a.Store.ReadOperationsDirectory(ctx, p, cfg.Input.ResourceID, "package_daily", map[string]string{
		"package": run.Shared["package"].(string), "week": run.Shared["week"].(string), "run": roster.Run, "item": item,
		"strategy": run.Shared["strategy"].(string), "day": run.Shared["day"].(string),
	})
	if err != nil {
		return nil, 0, err
	}
	var doc map[string]any
	if err = json.Unmarshal(data, &doc); err != nil || doc["run"] != roster.Run {
		return nil, 0, core.ErrInvalid
	}
	rows, ok := doc["rows"].([]any)
	if !ok {
		return nil, 0, core.ErrInvalid
	}
	filtered := make([]any, 0, len(allowed))
	pending := 0
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			return nil, 0, core.ErrInvalid
		}
		key, _ := row["key"].(string)
		i, exists := allowed[key]
		if !exists {
			continue
		}
		if row["id"] != roster.Scope[i].ID {
			return nil, 0, core.ErrConflict
		}
		row["reason"] = roster.Scope[i].Reason
		filtered = append(filtered, row)
		if row["status"] == "待核对" && doc["formal"] == "" {
			pending++
		}
	}
	if item != "" && len(filtered) != 1 {
		return nil, 0, core.ErrConflict
	}
	doc["rows"] = filtered
	doc["sendEnabled"] = originalSendEnabled(cfg)
	if doc["sendEnabled"] == true {
		doc["sendAccount"] = cfg.Staff
		doc["sendTransport"] = cfg.Transport
	}
	return doc, pending, nil
}

func (a *Server) operationsToday(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(ctx)
	cfg, err := a.readOperations(ctx, p.ProjectID)
	if err != nil || p != operationsPrincipal(p.ProjectID) || !cfg.ReadSource {
		respond(w, nil, core.ErrDenied)
		return
	}
	if id := r.URL.Query().Get("daily"); id != "" {
		if a.operationsExecution(ctx, id) != "Completed" {
			respond(w, nil, core.ErrConflict)
			return
		}
		doc, _, err := a.dailyOperationsPackage(ctx, p, cfg, id, r.URL.Query().Get("item"))
		respond(w, doc, err)
		return
	}
	day := operationsDay(time.Now())
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT id FROM wb_runs WHERE project_id=? AND session_id=?
		AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.operation'))='operations_package'
		AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.shared.day'))=? ORDER BY created_at,id`, p.ProjectID, p.SessionID, day)
	if err != nil {
		respond(w, nil, err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		respond(w, nil, err)
		return
	}
	packages := []map[string]any{}
	attention := false
	running := false
	for _, id := range ids {
		switch a.operationsExecution(ctx, id) {
		case "Completed":
		case "Running":
			running = true
			continue
		default:
			attention = true
			continue
		}
		run, roster, err := a.dailyOperationsRoster(ctx, p, id)
		if err != nil {
			attention = true
			continue
		}
		if roster.State == "empty" {
			continue
		}
		_, pending, err := a.dailyOperationsPackage(ctx, p, cfg, id, "")
		if err != nil {
			attention = true
			continue
		}
		if pending > 0 {
			packages = append(packages, map[string]any{"daily": id, "run_id": roster.Run, "package_id": run.Shared["package"],
				"label": run.Title, "scene_label": run.Shared["scene"], "week": run.Shared["week"], "pending": pending})
		}
	}
	respond(w, map[string]any{"day": day, "packages": packages, "attention": attention, "running": running}, nil)
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
	v := scheduleView{ID: id, Title: title, SessionID: p.SessionID, Hours: hours, Minute: t.Minute(), ProjectID: p.ProjectID, Strategy: in.Strategy}
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
	if err != nil || p != operationsPrincipal(p.ProjectID) || !originalSendEnabled(cfg) {
		respond(w, nil, core.ErrDenied)
		return
	}
	var in struct {
		Daily, Digest, Account, Transport string
		Keys                              []string
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Account != cfg.Staff || in.Transport != cfg.Transport || !operationsDigest.MatchString(in.Digest) ||
		len(in.Keys) == 0 || len(in.Keys) > 2500 || a.operationsExecution(ctx, in.Daily) != "Completed" {
		respond(w, nil, core.ErrInvalid)
		return
	}
	parent, roster, err := a.dailyOperationsRoster(ctx, p, in.Daily)
	if err != nil || roster.State != "ready" || parent.Destination != cfg.Input.ResourceID {
		respond(w, nil, core.ErrConflict)
		return
	}
	allowed := make(map[string]bool, len(roster.Scope))
	for _, member := range roster.Scope {
		allowed[member.Key] = member.ExpiresAt == nil || time.Now().Before(*member.ExpiresAt)
	}
	for _, key := range in.Keys {
		if !allowed[key] {
			respond(w, nil, core.ErrConflict)
			return
		}
		delete(allowed, key)
	}
	slices.Sort(in.Keys)
	keys, _ := json.Marshal(in.Keys)
	base := p.ProjectID + "/original-confirm/" + roster.Run
	a.operationsMu.Lock()
	defer a.operationsMu.Unlock()
	var id string
	for attempt := 0; attempt < 10; attempt++ {
		candidate := uuid.NewSHA1(uuid.NameSpaceURL, []byte(base+"/"+strconv.Itoa(attempt))).String()
		old, err := a.Store.Run(ctx, p, candidate)
		if core.IsMissing(err) {
			id = candidate
			break
		}
		if err != nil {
			respond(w, nil, err)
			return
		}
		result, err := a.Store.Item(ctx, old.ID, engine.OriginalOperationsConfirmation)
		if err == nil && result.Text == "source_changed" {
			continue
		}
		if err != nil && !core.IsMissing(err) {
			respond(w, nil, err)
			return
		}
		if old.Operation != "operations_package_confirm" || old.Shared["keys"] != string(keys) || old.Shared["digest"] != in.Digest {
			respond(w, nil, core.ErrConflict)
			return
		}
		id = candidate
		break
	}
	if id == "" {
		respond(w, nil, core.ErrConflict)
		return
	}
	run := core.Run{ID: id, Principal: p, Operation: "operations_package_confirm", Destination: parent.Destination,
		Title: "确认原工作包", ExplicitInputs: true, Quiet: true,
		Shared: map[string]any{"package": parent.Shared["package"], "week": parent.Shared["week"], "day": parent.Shared["day"],
			"strategy": parent.Shared["strategy"], "run": roster.Run, "digest": in.Digest, "keys": string(keys),
			"staff": cfg.Staff, "transport": cfg.Transport}}
	if err = a.Store.SaveRun(ctx, run); err != nil {
		respond(w, nil, err)
		return
	}
	_, err = a.Temporal.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: engine.BatchQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, engine.WriteTask,
		engine.WriteInput{RunID: id, ProjectID: p.ProjectID, Operation: run.Operation})
	respond(w, RunRef{RunID: id}, accepted(err))
}

func originalSendEnabled(cfg operationsConfig) bool {
	return cfg.ReadSource && cfg.Transport != "" && cfg.Staff != "" &&
		os.Getenv("WORKBENCH_OPERATIONS_ORIGINAL_SEND") == "1" &&
		os.Getenv("WORKBENCH_OPERATIONS_ORIGINAL_STAFF_ID") == cfg.Staff
}
