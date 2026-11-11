package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"strings"
	"time"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

const replacedOperationsSchedule = "已接入同一运营备稿流程"

// Reconfigure the existing daily schedule, not a second scheduler or a general
// program language. Credentials, connection locations and rules stay registered.
func (a *Server) configureLocalOperationsSchedule(ctx context.Context, cfg operationsConfig, strategy string, at time.Time, in engine.OperationsCycleInput) (scheduleView, error) {
	var view scheduleView
	p := operationsPrincipal(cfg.ProjectID)
	if in.Principal != p || len(in.Sources) != 1 || len(in.StrategyKeys) != 1 || in.StrategyKeys[0] != strategy || in.Limit != 0 || in.Section == "" || len(in.MethodRefs) != 6 {
		return view, core.ErrInvalid
	}
	sources := append([]core.ResourceSelection{}, in.Sources...)
	for _, r := range []*core.ResourceSelection{in.HistorySource, in.StrategySource, in.MethodSource} {
		if r == nil {
			return view, core.ErrInvalid
		}
		sources = append(sources, *r)
	}
	seen := map[string]bool{}
	for _, source := range sources {
		if seen[source.ResourceID] || !source.Refresh || len(source.Sections) != 1 || source.Sections[0] != in.Section {
			return view, core.ErrInvalid
		}
		seen[source.ResourceID] = true
		if e := a.Store.RequireSourceKind(ctx, p, source.ResourceID, "operations-original"); e != nil {
			return view, e
		}
	}
	destinations := []string{in.CleanDestination, in.SelectedDestination, in.DraftDestination}
	if batch := in.AdditionalBatch; batch != nil {
		if batch.Strategy == strategy || !originalOperationsStrategy.MatchString(batch.Strategy) {
			return view, core.ErrInvalid
		}
		destinations = append(destinations, batch.SelectedDestination, batch.DraftDestination)
	}
	for _, id := range destinations {
		if seen[id] {
			return view, core.ErrInvalid
		}
		seen[id] = true
		r, e := a.Store.Resource(ctx, id)
		if e != nil || r.OwnerProject != p.ProjectID {
			return view, core.ErrDenied
		}
	}
	for key, ref := range in.MethodRefs {
		if !core.ValidID(key) || ref.ID != key || ref.Ref.ResourceID != in.MethodSource.ResourceID || ref.Ref.Section != in.Section {
			return view, core.ErrInvalid
		}
	}
	id := "program-" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/original/"+strategy)).String()
	spec := client.ScheduleSpec{TimeZoneName: "Asia/Shanghai", Calendars: []client.ScheduleCalendarSpec{{Hour: []client.ScheduleRange{{Start: at.Hour()}}, Minute: []client.ScheduleRange{{Start: at.Minute()}}}}}
	action := &client.ScheduleWorkflowAction{ID: id, Workflow: engine.ScheduledOperationsCycle, TaskQueue: engine.InteractiveQueue, Args: []any{in}}
	wasPaused := false
	installNote := "operations-cycle-installing:running"
	handle := a.Temporal.ScheduleClient().GetHandle(ctx, id)
	if previous, e := handle.Describe(ctx); e == nil {
		wasPaused = previous.Schedule.State.Paused
		if previous.Schedule.State.Note == replacedOperationsSchedule {
			wasPaused = false
		}
		if strings.HasPrefix(previous.Schedule.State.Note, "operations-cycle-installing:") {
			wasPaused = previous.Schedule.State.Note == "operations-cycle-installing:paused"
		}
	} else {
		var missing *serviceerror.NotFound
		if !errors.As(e, &missing) {
			return view, e
		}
	}
	if wasPaused {
		installNote = "operations-cycle-installing:paused"
	}
	_, e := a.Temporal.ScheduleClient().Create(ctx, client.ScheduleOptions{ID: id, Spec: spec, Action: action, Overlap: enums.SCHEDULE_OVERLAP_POLICY_SKIP, CatchupWindow: time.Hour, PauseOnFailure: true, Paused: true, Note: installNote})
	var exists *serviceerror.AlreadyExists
	if errors.As(e, &exists) || errors.Is(e, temporal.ErrScheduleAlreadyRunning) {
		e = a.Temporal.ScheduleClient().GetHandle(ctx, id).Update(ctx, client.ScheduleUpdateOptions{DoUpdate: func(u client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			s := u.Description.Schedule
			s.Spec = &spec
			s.Action = action
			s.State.Paused = true
			s.State.Note = installNote
			return &client.ScheduleUpdate{Schedule: &s}, nil
		}})
	}
	if e != nil {
		return view, e
	}
	// Older original schedules were saved separately from cfg.Strategy.ID.
	// Replace original preparation jobs for this exact operations Session only;
	// an unrelated resource program in the same Session remains active.
	oldIDs := map[string]bool{cfg.Profile.ID: true, cfg.Strategy.ID: true}
	rows, e := a.Store.DB.QueryContext(ctx, `SELECT id FROM wb_settings WHERE id LIKE 'program-%' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.sessionId'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.projectId'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.strategy')) LIKE 'hxc:%'`, p.SessionID, p.ProjectID)
	if e != nil {
		return view, e
	}
	for rows.Next() {
		var old string
		if e = rows.Scan(&old); e != nil {
			rows.Close()
			return view, e
		}
		oldIDs[old] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return view, e
	}
	for old := range oldIDs {
		if old == "" || old == id {
			continue
		}
		if e = a.Temporal.ScheduleClient().GetHandle(ctx, old).Update(ctx, client.ScheduleUpdateOptions{DoUpdate: func(u client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			s := u.Description.Schedule
			if !s.State.Paused {
				s.State.Paused = true
				s.State.Note = replacedOperationsSchedule
			}
			return &client.ScheduleUpdate{Schedule: &s}, nil
		}}); e != nil {
			var missing *serviceerror.NotFound
			if !errors.As(e, &missing) {
				return view, e
			}
		}
	}
	// Release migration drains the old worker before changing source ownership.
	// A live in-flight project writer must settle before configuration changes.
	if active, e := a.Temporal.DescribeWorkflowExecution(ctx, engine.ProjectWriterID(p.ProjectID), ""); e == nil {
		if active.WorkflowExecutionInfo.Status == enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			return view, core.ErrConflict
		}
	} else {
		var missing *serviceerror.NotFound
		if !errors.As(e, &missing) {
			return view, e
		}
	}
	cfg.Cycle = &in
	cfg.Strategy.ID = id
	cfg.Strategy.Time = at.Format("15:04")
	if e = a.saveOperations(ctx, cfg); e != nil {
		return view, e
	}
	view = scheduleView{ID: id, Title: "资料清洗、原策略选单与备稿", SessionID: p.SessionID, ProjectID: p.ProjectID, Strategy: strategy, SourceResource: in.Sources[0].ResourceID, Hours: []int{at.Hour()}, Minute: at.Minute(), Paused: wasPaused}
	data, _ := json.Marshal(view)
	_, e = a.Store.DB.ExecContext(ctx, `INSERT INTO wb_settings VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)`, id, string(data))
	if e != nil {
		return view, e
	}
	if !wasPaused {
		e = handle.Unpause(ctx, client.ScheduleUnpauseOptions{Note: "本地运营配置和资料已保存"})
	}
	return view, e
}
