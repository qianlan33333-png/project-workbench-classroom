package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"net/http"
	"time"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type PersonalConfig struct {
	SessionID string `json:"sessionId"`
	Hour      int    `json:"hour"`
	Minute    int    `json:"minute"`
	Paused    bool   `json:"paused"`
}

func (a *Server) personalConfig(ctx context.Context) (PersonalConfig, error) {
	var b []byte
	var v PersonalConfig
	e := a.Store.DB.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id='personal'`).Scan(&b)
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(b, &v)
	return v, e
}
func (a *Server) configurePersonal(ctx context.Context, v PersonalConfig) error {
	a.personalMu.Lock()
	defer a.personalMu.Unlock()
	if !core.ValidID(v.SessionID) || v.Hour < 0 || v.Hour > 23 || v.Minute < 0 || v.Minute > 59 {
		return core.ErrInvalid
	}
	var kind string
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT kind FROM wb_sessions WHERE id=?`, v.SessionID).Scan(&kind); e != nil || kind != "personal" {
		return core.ErrDenied
	}
	if old, e := a.personalConfig(ctx); e == nil && old.SessionID != v.SessionID {
		return core.ErrConflict
	} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	spec := client.ScheduleSpec{TimeZoneName: "Asia/Shanghai", Calendars: []client.ScheduleCalendarSpec{{Hour: []client.ScheduleRange{{Start: v.Hour}}, Minute: []client.ScheduleRange{{Start: v.Minute}}}}}
	action := &client.ScheduleWorkflowAction{ID: "personal-daily", Workflow: engine.Daily, TaskQueue: engine.InteractiveQueue, Args: []any{core.Principal{Kind: "personal", SessionID: v.SessionID}}}
	_, e := a.Temporal.ScheduleClient().Create(ctx, client.ScheduleOptions{ID: "personal-daily", Spec: spec, Action: action, Overlap: enums.SCHEDULE_OVERLAP_POLICY_SKIP, CatchupWindow: 2 * time.Hour, Paused: v.Paused})
	var exists *serviceerror.AlreadyExists
	if errors.As(e, &exists) || errors.Is(e, temporal.ErrScheduleAlreadyRunning) {
		e = a.Temporal.ScheduleClient().GetHandle(ctx, "personal-daily").Update(ctx, client.ScheduleUpdateOptions{DoUpdate: func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			s := input.Description.Schedule
			s.Spec = &spec
			s.Action = action
			s.State.Paused = v.Paused
			return &client.ScheduleUpdate{Schedule: &s}, nil
		}})
	}
	if e != nil {
		return e
	}
	b, _ := json.Marshal(v)
	_, e = a.Store.DB.ExecContext(ctx, `INSERT INTO wb_settings VALUES('personal',?) ON DUPLICATE KEY UPDATE body=VALUES(body)`, string(b))
	return e
}
func (a *Server) personalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /personal/session", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		if p.Kind != "personal" {
			respond(w, nil, core.ErrDenied)
			return
		}
		v, e := a.personalConfig(r.Context())
		if e == sql.ErrNoRows {
			v = PersonalConfig{SessionID: p.SessionID, Hour: 9}
			e = a.configurePersonal(r.Context(), v)
		}
		respond(w, v, e)
	})
	mux.HandleFunc("GET /personal/settings", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		v, e := a.personalConfig(r.Context())
		respond(w, v, e)
	})
	mux.HandleFunc("POST /personal/settings", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		var v PersonalConfig
		if decode(w, r, &v) {
			respond(w, v, a.configurePersonal(r.Context(), v))
		}
	})
}
