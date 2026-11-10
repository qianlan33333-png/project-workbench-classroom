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
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	SessionID string      `json:"sessionId"`
	Hours     []int       `json:"hours"`
	Minute    int         `json:"minute"`
	Paused    bool        `json:"paused"`
	Next      []time.Time `json:"next"`
	State     string      `json:"state"`
	ProjectID string      `json:"projectId,omitempty"`
	Strategy  string      `json:"strategy,omitempty"`
	Last      time.Time   `json:"last,omitzero"`
	Result    string      `json:"result,omitempty"`
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
		for i, v := range out {
			d, err := a.Temporal.ScheduleClient().GetHandle(r.Context(), v.ID).Describe(r.Context())
			if err != nil {
				out[i].State = "unavailable"
				continue
			}
			out[i].State = "ready"
			out[i].Paused = d.Schedule.State.Paused
			out[i].Next = d.Info.NextActionTimes
			if v.Strategy != "" && len(d.Schedule.Spec.Calendars) == 1 {
				calendar := d.Schedule.Spec.Calendars[0]
				if len(calendar.Hour) == 1 && len(calendar.Minute) == 1 {
					out[i].Hours = []int{calendar.Hour[0].Start}
					out[i].Minute = calendar.Minute[0].Start
				}
			}
			if len(d.Info.RecentActions) > 0 {
				last := d.Info.RecentActions[len(d.Info.RecentActions)-1]
				out[i].Last = last.ActualTime
				if last.StartWorkflowResult != nil {
					out[i].Result = a.operationsExecution(r.Context(), last.StartWorkflowResult.WorkflowID)
				}
			}
		}
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
		var b []byte
		if e := a.Store.DB.QueryRowContext(r.Context(), `SELECT body FROM wb_settings WHERE id=? AND id LIKE 'program-%'`, v.ID).Scan(&b); e != nil {
			respond(w, nil, e)
			return
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
