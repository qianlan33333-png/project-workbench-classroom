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
	"go.temporal.io/sdk/temporal"
	"net/http"
	"sync"
	"time"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type ProgramSchedule struct {
	ID      string         `json:"id"`
	Program ProgramRequest `json:"program"`
	Hours   []int          `json:"hours"`
	Minute  int            `json:"minute"`
	Paused  bool           `json:"paused"`
}
type scheduleView struct {
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	SessionID      string      `json:"sessionId"`
	Hours          []int       `json:"hours"`
	Minute         int         `json:"minute"`
	Paused         bool        `json:"paused"`
	Next           []time.Time `json:"next"`
	State          string      `json:"state"`
	ProjectID      string      `json:"projectId,omitempty"`
	Strategy       string      `json:"strategy,omitempty"`
	Last           time.Time   `json:"last,omitzero"`
	Result         string      `json:"result,omitempty"`
	Source         string      `json:"source,omitempty"`
	SourceResource string      `json:"sourceResource,omitempty"`
	Experts        []string    `json:"experts,omitempty"`
	Versions       []int       `json:"versions,omitempty"`
}

func (a *Server) configureProgram(ctx context.Context, p core.Principal, in ProgramSchedule) (string, error) {
	return a.configureProgramInputs(ctx, p, in, false)
}

func (a *Server) configureProgramInputs(ctx context.Context, p core.Principal, in ProgramSchedule, explicit bool) (string, error) {
	if !core.ValidID(in.ID) || len(in.Hours) == 0 || len(in.Hours) > 24 || in.Minute < 0 || in.Minute > 59 {
		return "", core.ErrInvalid
	}
	hours := []client.ScheduleRange{}
	seen := map[int]bool{}
	for _, h := range in.Hours {
		if h < 0 || h > 23 || seen[h] {
			return "", core.ErrInvalid
		}
		seen[h] = true
		hours = append(hours, client.ScheduleRange{Start: h})
	}
	in.Program.OperationKey = in.ID
	r, e := a.programRun(ctx, p, in.Program)
	if e != nil {
		return "", e
	}
	r.ExplicitInputs = explicit
	if !explicit {
		r.Lineage, e = a.Store.SessionSources(ctx, p)
		if e != nil {
			return "", e
		}
		if e = core.AuthorizeAll(ctx, a.Store, p, r.Lineage); e != nil {
			return "", e
		}
	}
	id := "program-" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(p.SessionID+"/"+in.ID)).String()
	spec := client.ScheduleSpec{TimeZoneName: "Asia/Shanghai", Calendars: []client.ScheduleCalendarSpec{{Hour: hours, Minute: []client.ScheduleRange{{Start: in.Minute}}}}}
	action := &client.ScheduleWorkflowAction{ID: id, Workflow: engine.ScheduledProgram, TaskQueue: engine.BatchQueue, Args: []any{r}}
	_, e = a.Temporal.ScheduleClient().Create(ctx, client.ScheduleOptions{ID: id, Spec: spec, Action: action, Overlap: enums.SCHEDULE_OVERLAP_POLICY_SKIP, CatchupWindow: time.Hour, Paused: in.Paused})
	var exists *serviceerror.AlreadyExists
	if errors.As(e, &exists) || errors.Is(e, temporal.ErrScheduleAlreadyRunning) {
		e = a.Temporal.ScheduleClient().GetHandle(ctx, id).Update(ctx, client.ScheduleUpdateOptions{DoUpdate: func(v client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			s := v.Description.Schedule
			s.Spec = &spec
			s.Action = action
			s.State.Paused = in.Paused
			return &client.ScheduleUpdate{Schedule: &s}, nil
		}})
	}
	if e != nil {
		return "", e
	}
	v := scheduleView{ID: id, Title: r.Title, SessionID: p.SessionID, Hours: in.Hours, Minute: in.Minute, Paused: in.Paused}
	b, _ := json.Marshal(v)
	_, e = a.Store.DB.ExecContext(ctx, `INSERT INTO wb_settings VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)`, id, string(b))
	return id, e
}
func (a *Server) scheduleTools(s *mcp.Server, p core.Principal) {
	mcp.AddTool(s, &mcp.Tool{Name: "program_schedule", Description: "Configure a typed resource program on Temporal daily at selected Beijing hours. Skips overlap, returns to this Session. Empty destination means refresh/validate/publish only. Does not send externally."}, func(ctx context.Context, _ *mcp.CallToolRequest, in ProgramSchedule) (*mcp.CallToolResult, struct {
		ID string `json:"id"`
	}, error) {
		id, e := a.configureProgram(ctx, p, in)
		return nil, struct {
			ID string `json:"id"`
		}{id}, e
	})
}
func (a *Server) describeSchedules(ctx context.Context, schedules []scheduleView) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	jobs := make(chan int, len(schedules))
	for i := range schedules {
		schedules[i].State = "unavailable"
		jobs <- i
	}
	close(jobs)
	if a.Temporal == nil {
		return
	}
	var readers sync.WaitGroup
	for range min(4, len(schedules)) {
		readers.Go(func() {
			for i := range jobs {
				if ctx.Err() != nil {
					return
				}
				a.describeSchedule(ctx, &schedules[i])
			}
		})
	}
	readers.Wait()
}

func (a *Server) describeSchedule(ctx context.Context, v *scheduleView) {
	if v.Strategy != "" {
		a.originalScheduleEvidence(ctx, v)
	}
	d, err := a.Temporal.ScheduleClient().GetHandle(ctx, v.ID).Describe(ctx)
	if err != nil || d == nil || d.Schedule.State == nil {
		return
	}
	v.State = "ready"
	v.Paused = d.Schedule.State.Paused
	v.Next = d.Info.NextActionTimes
	if v.Strategy != "" && d.Schedule.Spec != nil && len(d.Schedule.Spec.Calendars) == 1 {
		calendar := d.Schedule.Spec.Calendars[0]
		if len(calendar.Hour) == 1 && len(calendar.Minute) == 1 {
			v.Hours = []int{calendar.Hour[0].Start}
			v.Minute = calendar.Minute[0].Start
		}
	}
	if len(d.Info.RecentActions) > 0 {
		last := d.Info.RecentActions[len(d.Info.RecentActions)-1]
		v.Last = last.ActualTime
		if run := last.StartWorkflowResult; run != nil {
			v.Result = a.describeOperationsExecution(ctx, run.WorkflowID, run.FirstExecutionRunID)
		}
	}
}

func (a *Server) scheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /schedules", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		rows, e := a.Store.DB.QueryContext(r.Context(), `SELECT body FROM wb_settings WHERE id LIKE 'program-%' ORDER BY id`)
		if e != nil {
			respond(w, nil, e)
			return
		}
		out := []scheduleView{}
		for rows.Next() {
			var v scheduleView
			var b []byte
			if e = rows.Scan(&b); e != nil {
				break
			}
			if e = json.Unmarshal(b, &v); e != nil {
				break
			}
			out = append(out, v)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			respond(w, nil, e)
			return
		}
		a.describeSchedules(r.Context(), out)
		respond(w, out, nil)
	})
	mux.HandleFunc("POST /schedules/pause", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		var v struct {
			ID     string `json:"id"`
			Paused bool   `json:"paused"`
		}
		if !decode(w, r, &v) {
			return
		}
		a.operationsMu.Lock()
		defer a.operationsMu.Unlock()
		var b []byte
		if e := a.Store.DB.QueryRowContext(r.Context(), `SELECT body FROM wb_settings WHERE id=? AND id LIKE 'program-%'`, v.ID).Scan(&b); e != nil {
			respond(w, nil, e)
			return
		}
		var saved scheduleView
		if e := json.Unmarshal(b, &saved); e != nil {
			respond(w, nil, e)
			return
		}
		if !v.Paused && originalOperationsStrategy.MatchString(saved.Strategy) && saved.SessionID == operationsPrincipal(saved.ProjectID).SessionID {
			cfg, e := a.readOperations(r.Context(), saved.ProjectID)
			if e != nil || (cfg.Cycle != nil && cfg.Strategy.ID != v.ID) {
				respond(w, nil, errors.Join(e, core.ErrConflict))
				return
			}
		}
		h := a.Temporal.ScheduleClient().GetHandle(r.Context(), v.ID)
		var e error
		if v.Paused {
			e = h.Pause(r.Context(), client.SchedulePauseOptions{Note: "本人暂停"})
		} else {
			e = h.Unpause(r.Context(), client.ScheduleUnpauseOptions{Note: "本人恢复"})
		}
		respond(w, v, e)
	})
}
