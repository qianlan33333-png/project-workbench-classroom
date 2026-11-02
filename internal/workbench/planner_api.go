package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"strconv"
)

type Notice struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Link    string `json:"link"`
	Created int64  `json:"created"`
	Seen    bool   `json:"seen"`
}
type Failure struct {
	ID            string           `json:"id"`
	Schedule      string           `json:"scheduleId"`
	Name          string           `json:"name"`
	Operation     string           `json:"operation"`
	Run           string           `json:"runId"`
	Detail        string           `json:"detail"`
	At            int64            `json:"at"`
	CheckExternal bool             `json:"checkExternal"`
	Retry         *ScheduleReceipt `json:"retry,omitempty"`
}

func (s *Store) failure(id string) (Failure, error) {
	v := Failure{ID: id}
	e := s.DB.QueryRow("SELECT schedule,operation,name,run,detail,at FROM schedule_failures WHERE id=?", id).Scan(&v.Schedule, &v.Operation, &v.Name, &v.Run, &v.Detail, &v.At)
	if e != nil {
		return v, ErrNotFound
	}
	if v.Run != "" {
		var count int
		e = s.DB.QueryRow("SELECT count(*) FROM events WHERE run_id=? AND type='harness.tool/call'", v.Run).Scan(&count)
		if e != nil {
			return v, e
		}
		v.CheckExternal = count > 0
	}
	var r ScheduleReceipt
	if e = s.DB.QueryRow("SELECT operation,checked_at,outcome,run_id,detail FROM schedule_checks WHERE schedule_id=? AND operation=?", v.Schedule, "manual:retry-"+id).Scan(&r.Operation, &r.At, &r.Outcome, &r.RunID, &r.Detail); e == nil {
		v.Retry = &r
	}
	return v, nil
}
func (a *API) mountPlanner(g *gin.RouterGroup) {
	s := a.Store
	g.GET("/planner/personal-space", func(c *gin.Context) { p, e := s.PersonalSpace(); reply(c, p, e) })
	g.GET("/calendar", func(c *gin.Context) {
		lo, _ := strconv.ParseInt(c.Query("start"), 10, 64)
		hi, _ := strconv.ParseInt(c.Query("end"), 10, 64)
		v, e := s.calendarView(c.Request.Context(), lo, hi, c.Query("projectId"), c.Query("remote") == "1")
		reply(c, v, e)
	})
	for _, action := range []string{"create", "edit", "delete", "reconcile"} {
		g.POST("/calendar/"+action, func(c *gin.Context) {
			bind(c, func(v CalendarEvent) (any, error) { return s.changeEvent(c.Request.Context(), action, v, "") })
		})
	}
	for _, kind := range []string{"occurrence", "series"} {
		g.POST("/schedules/:id/"+kind, func(c *gin.Context) {
			bind(c, func(v TimeEdit) (any, error) { return s.editTime(c.Param("id"), kind, v) })
		})
	}
	g.POST("/schedules/preview", func(c *gin.Context) {
		bind(c, func(sc Schedule) (any, error) {
			times := []int64{}
			after := now()
			for len(times) < 3 {
				v, e := scheduleTick(sc, after)
				if e != nil {
					return nil, e
				}
				if v.At == 0 {
					break
				}
				times = append(times, v.At)
				after = v.At
			}
			return times, nil
		})
	})
	g.GET("/notices", func(c *gin.Context) {
		rs, e := s.DB.Query("SELECT id,kind,title,summary,link,created,seen FROM user_notices WHERE cleared=0 ORDER BY created DESC")
		if e != nil {
			reply(c, nil, e)
			return
		}
		defer rs.Close()
		vs := []Notice{}
		for rs.Next() {
			var v Notice
			if e = rs.Scan(&v.ID, &v.Kind, &v.Title, &v.Summary, &v.Link, &v.Created, &v.Seen); e != nil {
				reply(c, nil, e)
				return
			}
			vs = append(vs, v)
		}
		reply(c, vs, rs.Err())
	})
	g.GET("/notices/count", func(c *gin.Context) {
		var n int
		e := s.DB.QueryRow("SELECT count(*) FROM user_notices WHERE seen=0 AND cleared=0").Scan(&n)
		reply(c, gin.H{"unread": n}, e)
	})
	g.POST("/notices/:id/read", func(c *gin.Context) {
		_, e := s.DB.Exec("UPDATE user_notices SET seen=1 WHERE id=?", c.Param("id"))
		reply(c, true, e)
	})
	g.POST("/notices/clear", func(c *gin.Context) {
		bind(c, func(v struct {
			Through int64 `json:"through"`
		}) (any, error) {
			_, e := s.DB.Exec("UPDATE user_notices SET cleared=1,seen=1 WHERE created<=?", v.Through)
			return true, e
		})
	})
	g.GET("/failures/:id/tools", func(c *gin.Context) {
		f, e := s.failure(c.Param("id"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		rows, e := s.DB.Query("SELECT type,data FROM events WHERE run_id=? AND type LIKE 'harness.tool/%' ORDER BY seq LIMIT 500", f.Run)
		if e != nil {
			reply(c, nil, e)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var kind, raw string
			if e = rows.Scan(&kind, &raw); e != nil {
				reply(c, nil, e)
				return
			}
			out = append(out, map[string]any{"type": kind, "data": json.RawMessage(raw), "runId": f.Run})
		}
		reply(c, out, rows.Err())
	})
	g.GET("/failures/:id", func(c *gin.Context) { v, e := s.failure(c.Param("id")); reply(c, v, e) })
	g.POST("/failures/:id/retry", func(c *gin.Context) {
		bind(c, func(q struct {
			Checked bool `json:"checked"`
		}) (any, error) {
			f, e := s.failure(c.Param("id"))
			if e != nil {
				return nil, e
			}
			if f.Retry != nil {
				return f.Retry, nil
			}
			if f.CheckExternal && !q.Checked {
				return nil, errors.New("请先核对上次工具调用和外部回执，再按当前配置重跑")
			}
			return s.RunSchedule(f.Schedule, "retry-"+f.ID)
		})
	})
}
func (s *Store) plannerTool(ctx context.Context, in RuntimeInput, tool string, raw json.RawMessage) (any, error) {
	var q struct {
		Project      string `json:"projectId"`
		ID           string
		Action       string
		Start, End   int64
		Remote       *bool
		Event        CalendarEvent
		Change       TimeEdit
		Schedule     json.RawMessage
		NotifySource bool
	}
	if e := json.Unmarshal(raw, &q); e != nil {
		return nil, e
	}
	scope := in.Project.ID
	if scope == personalProjectID {
		scope = q.Project
	} else if q.Project != "" && q.Project != scope {
		return nil, ErrNotFound
	}
	switch tool {
	case "query_calendar":
		return s.calendarView(ctx, q.Start, q.End, scope, q.Remote == nil || *q.Remote)
	case "change_calendar":
		if q.Action == "create" {
			q.Event.ProjectID = scope
			if q.Event.Operation == "" {
				q.Event.Operation = digest([]byte(in.Run.ID + encode(q.Event)))
			}
		}
		return s.changeEvent(ctx, q.Action, q.Event, scope)
	case "list_schedules":
		return s.Schedules(scope)
	}
	if in.Run.Source == "automation" {
		return nil, errors.New("自动执行不能自行修改调度")
	}
	if scope == "" {
		scope = personalProjectID
	}
	if tool == "save_schedule" {
		in.Project.ID = scope
		v, ids, e := s.saveChatSchedule(in, q.Schedule, q.NotifySource)
		if e != nil {
			return nil, e
		}
		return v, s.stopScheduleRuns(ids)
	}
	sc, e := s.Schedule(q.ID)
	if e != nil || sc.ProjectID != scope {
		return nil, ErrNotFound
	}
	switch tool {
	case "update_occurrence":
		return s.editTime(q.ID, "occurrence", q.Change)
	case "update_series":
		return s.editTime(q.ID, "series", q.Change)
	case "delete_schedule":
		return true, s.deleteSchedule(q.ID)
	}
	return nil, errors.New("未知操作")
}
func (s *Store) deleteSchedule(id string) error {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	res, e := s.DB.Exec("DELETE FROM schedules WHERE id=? AND NOT EXISTS(SELECT 1 FROM runs WHERE json_extract(data,'$.scheduleId')=? AND state IN ('queued','running','waiting','stopping'))", id, id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("规则不存在或仍有执行，请先停止")
	}
	return nil
}
