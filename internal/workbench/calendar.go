package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CalendarEvent struct {
	ID          string `json:"id"`
	Operation   string `json:"operation,omitempty"`
	Version     int64  `json:"version"`
	ProjectID   string `json:"projectId,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Start       int64  `json:"start"`
	End         int64  `json:"end"`
	AllDay      bool   `json:"allDay,omitempty"`
	Destination string `json:"destination"`
	RemoteID    string `json:"remoteId,omitempty"`
	Link        string `json:"link,omitempty"`
	Sync        string `json:"sync,omitempty"`
	Error       string `json:"error,omitempty"`
	Pending     string `json:"pending,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
}
type CalendarItem struct {
	CalendarEvent
	Kind         string `json:"kind"`
	Original     int64  `json:"original,omitempty"`
	Repeated     bool   `json:"repeated,omitempty"`
	State        string `json:"state,omitempty"`
	RunID        string `json:"runId,omitempty"`
	OperationKey string `json:"operationKey,omitempty"`
}
type CalendarView struct {
	Items         []CalendarItem `json:"items"`
	ProviderError string         `json:"providerError,omitempty"`
}
type larkTime struct {
	Date      string
	Timestamp string
	Timezone  string
}
type larkEvent struct {
	Source      string `json:"source"`
	ID          string `json:"event_id"`
	Title       string `json:"summary"`
	Description string
	Start       larkTime `json:"start_time"`
	End         larkTime `json:"end_time"`
	Link        string   `json:"app_link"`
	Status      string
}

func calendarZone() *time.Location { z, _ := time.LoadLocation("Asia/Shanghai"); return z }
func (v larkTime) millis() int64 {
	if v.Date != "" {
		d, _ := time.ParseInLocation("2006-01-02", v.Date, calendarZone())
		return d.UnixMilli()
	}
	n, _ := strconv.ParseInt(v.Timestamp, 10, 64)
	return n * 1000
}
func (v larkEvent) event() CalendarEvent {
	end := v.End.millis()
	if v.Start.Date != "" {
		end = time.UnixMilli(end).In(calendarZone()).AddDate(0, 0, 1).UnixMilli()
	}
	id := "fs:" + v.ID
	if strings.HasPrefix(v.Source, "pwb:") {
		id = strings.TrimPrefix(v.Source, "pwb:")
	}
	return CalendarEvent{ID: id, RemoteID: v.ID, Title: v.Title, Description: v.Description, Start: v.Start.millis(), End: end, AllDay: v.Start.Date != "", Destination: "feishu", Link: v.Link, Sync: "ready", Deleted: v.Status == "cancelled"}
}
func (s *Store) calendarID() string {
	if s.feishuCalendarID != "" {
		return s.feishuCalendarID
	}
	return "primary"
}
func (s *Store) calendarCLI(ctx context.Context, result any, args ...string) error {
	t, e := s.CLITool("lark")
	if e != nil || !t.Enabled || !t.Installed {
		return errors.New("飞书 CLI 未启用或未安装")
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	argv := append([]string{"calendar", "events"}, args...)
	argv = append(argv, "--as", "user", "--format", "json")
	cmd := exec.CommandContext(ctx, filepath.Join(s.Root, "tools", "bin", t.Command), argv...)
	data, e := cmd.Output()
	var reply struct {
		OK    bool
		Data  json.RawMessage
		Error struct{ Message string }
	}
	_ = json.Unmarshal(data, &reply)
	if e != nil || !reply.OK {
		if ctx.Err() != nil {
			return errors.New("飞书回执超时，请核对后重试")
		}
		if reply.Error.Message != "" {
			return errors.New(safeRuntimeError(reply.Error.Message))
		}
		return errors.New("飞书请求失败，请检查授权及日历编辑权限")
	}
	if result != nil && len(reply.Data) > 0 {
		return json.Unmarshal(reply.Data, result)
	}
	return nil
}
func (s *Store) remoteEvents(ctx context.Context, lo, hi int64) ([]CalendarEvent, error) {
	var result []CalendarEvent
	for start := lo; start < hi; {
		end := min(hi, start+7*24*3600000)
		var page struct{ Items []larkEvent }
		e := s.calendarCLI(ctx, &page, "instance_view", "--calendar-id", s.calendarID(), "--start-time", strconv.FormatInt(start/1000, 10), "--end-time", strconv.FormatInt((end+999)/1000, 10))
		if e != nil {
			return nil, e
		}
		for _, r := range page.Items {
			v := r.event()
			if !v.Deleted {
				result = append(result, v)
			}
		}
		start = end
	}
	return result, nil
}
func (s *Store) calendarRecord(id string) (CalendarEvent, error) {
	return getJSON[CalendarEvent](s.DB, "SELECT data FROM calendar_events WHERE id=?", id)
}
func (s *Store) storeEvent(ctx context.Context, v CalendarEvent) error {
	_, e := s.DB.ExecContext(ctx, "INSERT INTO calendar_events VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", v.ID, v.Operation, encode(v))
	return e
}
func (s *Store) changeEvent(ctx context.Context, intent string, v CalendarEvent, scope string) (CalendarEvent, error) {
	s.calendarMu.Lock()
	defer s.calendarMu.Unlock()
	old, e := s.calendarRecord(v.ID)
	if intent == "create" {
		if len(v.Operation) < 8 || len(v.Operation) > 180 {
			return v, errors.New("缺少稳定的操作标识")
		}
		exists, err := getJSON[CalendarEvent](s.DB, "SELECT data FROM calendar_events WHERE operation=?", v.Operation)
		if err == nil {
			if scope != "" && exists.ProjectID != scope {
				return CalendarEvent{}, ErrNotFound
			}
			return exists, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return v, err
		}
		v.ID = newID()
		v.RemoteID = ""
		v.Sync = ""
		v.Pending = ""
		v.Error = ""
		v.Deleted = false
		v.Version = 1
	} else {
		if errors.Is(e, ErrNotFound) && strings.HasPrefix(v.ID, "fs:") && scope == "" {
			var obj struct{ Event larkEvent }
			e = s.calendarCLI(ctx, &obj, "get", "--calendar-id", s.calendarID(), "--event-id", strings.TrimPrefix(v.ID, "fs:"))
			if e == nil {
				old = obj.Event.event()
				old.Operation = "import:" + old.RemoteID
			}
		}
		if e != nil {
			return CalendarEvent{}, e
		}
		if scope != "" && old.ProjectID != scope {
			return CalendarEvent{}, ErrNotFound
		}
		if v.Version != old.Version {
			return old, ErrConflict
		}
		if intent == "reconcile" {
			if old.Pending == "" {
				return old, nil
			}
			intent = old.Pending
			v = old
			if intent != "create" && v.RemoteID != "" {
				var observed struct{ Event larkEvent }
				if e = s.calendarCLI(ctx, &observed, "get", "--calendar-id", s.calendarID(), "--event-id", v.RemoteID); e != nil {
					return v, e
				}
				remote := observed.Event.event()
				applied := intent == "delete" && remote.Deleted || intent == "edit" && !remote.Deleted && remote.Title == v.Title && remote.Description == v.Description && remote.Start/1000 == v.Start/1000 && remote.End/1000 == v.End/1000
				if applied {
					v.Sync = "ready"
					v.Error = ""
					v.Pending = ""
					v.Deleted = intent == "delete"
					return v, s.storeEvent(ctx, v)
				}
			}
		} else {
			if old.Pending != "" {
				return old, errors.New("请先核对上一次飞书操作")
			}
			if old.Deleted {
				return old, ErrNotFound
			}
			if intent == "delete" {
				v = old
			}
			v.Destination = old.Destination
			v.RemoteID = old.RemoteID
			v.Operation = old.Operation
			v.ProjectID = old.ProjectID
			v.Version = old.Version + 1
			v.Link = old.Link
		}
	}
	if scope != "" {
		v.ProjectID = scope
	}
	if v.ProjectID != "" {
		p, e := s.Project(v.ProjectID)
		if e != nil || p.Archived {
			return v, errors.New("日程所属项目无效")
		}
	}
	if strings.TrimSpace(v.Title) == "" || len(v.Title) > 800 || v.Start <= 0 || v.End <= v.Start {
		return v, errors.New("请输入标题和有效的起止时间")
	}
	if v.Destination != "cloud" && v.Destination != "feishu" && v.Destination != "both" {
		return v, errors.New("请选择云端、飞书或两处")
	}
	if v.Destination == "cloud" {
		v.Deleted = intent == "delete"
		v.Sync = "ready"
		return v, s.storeEvent(ctx, v)
	}
	// Commit the desired state before crossing the network; a process crash leaves a recoverable intent.
	v.Pending = intent
	v.Sync = "pending"
	v.Error = ""
	if e = s.storeEvent(ctx, v); e != nil {
		return v, e
	}
	ltime := func(ms int64, end bool) map[string]string {
		if v.AllDay {
			if end {
				ms--
			}
			return map[string]string{"date": time.UnixMilli(ms).In(calendarZone()).Format("2006-01-02")}
		}
		return map[string]string{"timestamp": strconv.FormatInt(ms/1000, 10), "timezone": "Asia/Shanghai"}
	}
	payload := map[string]any{"summary": v.Title, "description": v.Description, "start_time": ltime(v.Start, false), "end_time": ltime(v.End, true)}
	if intent == "create" {
		payload["source"] = "pwb:" + v.ID
	}
	body := encode(payload)
	args := []string{"--calendar-id", s.calendarID()}
	var res struct{ Event larkEvent }
	switch intent {
	case "create":
		args = append([]string{"create"}, args...)
		args = append(args, "--idempotency-key", digest([]byte(v.Operation))[:32], "--data", body)
	case "edit":
		args = append([]string{"patch"}, args...)
		args = append(args, "--event-id", v.RemoteID, "--data", body)
	case "delete":
		args = append([]string{"delete"}, args...)
		args = append(args, "--event-id", v.RemoteID)
	default:
		return v, errors.New("不支持的日程操作")
	}
	e = s.calendarCLI(ctx, &res, args...)
	if e == nil && intent == "create" && res.Event.ID == "" {
		e = errors.New("飞书未返回日程标识")
	}
	if e != nil {
		v.Sync = "unknown"
		v.Error = e.Error()
	} else {
		v.Sync = "ready"
		v.Pending = ""
		if res.Event.ID != "" {
			v.RemoteID = res.Event.ID
			v.Link = res.Event.Link
		}
		v.Deleted = intent == "delete"
	}
	return v, s.storeEvent(context.Background(), v)
}
func (s *Store) calendarView(ctx context.Context, lo, hi int64, scope string, remote bool) (CalendarView, error) {
	view := CalendarView{Items: []CalendarItem{}}
	if lo <= 0 || hi <= lo || hi-lo > 45*24*3600000 {
		return view, errors.New("时间窗口须在 45 天以内")
	}
	events, e := listJSON[CalendarEvent](s.DB, "SELECT data FROM calendar_events WHERE (?='' OR json_extract(data,'$.projectId')=?)", scope, scope)
	if e != nil {
		return view, e
	}
	byRemote := map[string]CalendarEvent{}
	byID := map[string]CalendarEvent{}
	for _, v := range events {
		if v.RemoteID != "" {
			byRemote[v.RemoteID] = v
		}
		if !v.Deleted {
			byID[v.ID] = v
		}
	}
	if remote {
		rs, err := s.remoteEvents(ctx, lo, hi)
		if err != nil {
			view.ProviderError = err.Error()
		} else {
			// A successful complete range also removes items moved out of the window on Feishu.
			for _, v := range byRemote {
				if v.Sync == "ready" {
					delete(byID, v.ID)
				}
			}
			for _, v := range rs {
				local, ok := byRemote[v.RemoteID]
				if !ok {
					if pending, exists := byID[v.ID]; exists && pending.Pending == "create" {
						continue
					}
				}
				if ok {
					if local.Deleted || local.Pending != "" {
						continue
					}
					v.ID = local.ID
					v.Version = local.Version
					v.ProjectID = local.ProjectID
					v.Operation = local.Operation
					v.Destination = local.Destination
				} else if scope != "" {
					continue
				} else {
					v.ID = "fs:" + v.RemoteID
				}
				byID[v.ID] = v
			}
		}
	}
	for _, v := range byID {
		if v.Start < hi && v.End > lo {
			view.Items = append(view.Items, CalendarItem{CalendarEvent: v, Kind: "event"})
		}
	}
	ts, e := s.Tasks(scope)
	if e != nil {
		return view, e
	}
	for _, t := range ts {
		d := t.ScheduledDate
		label := ""
		if d == "" {
			d = t.DueDate
			label = "截止 · "
		}
		day, err := time.ParseInLocation("2006-01-02", d, calendarZone())
		if err != nil {
			continue
		}
		at := day.UnixMilli()
		if at >= lo && at < hi {
			view.Items = append(view.Items, CalendarItem{Kind: "todo", State: t.State, CalendarEvent: CalendarEvent{ID: t.ID, Title: label + t.Title, ProjectID: t.ProjectID, Description: t.Description, Start: at, End: day.AddDate(0, 0, 1).UnixMilli(), AllDay: true}})
		}
	}
	schedules, e := s.Schedules(scope)
	if e != nil {
		return view, e
	}
	for _, rule := range schedules {
		if !rule.Enabled {
			continue
		}
		for cursor := max(now(), lo-1); cursor < hi; {
			v, e := scheduleTick(rule, cursor)
			if e != nil {
				return view, e
			}
			if v.At <= cursor || v.At >= hi {
				break
			}
			cursor = v.At
			view.Items = append(view.Items, CalendarItem{Kind: "ai", Repeated: rule.OnceAt == 0, Original: v.Original, CalendarEvent: CalendarEvent{ID: rule.ID, Title: rule.Name, Version: rule.Version, ProjectID: rule.ProjectID, Start: v.At}})
		}
	}
	rows, e := s.DB.Query("SELECT c.schedule_id,c.operation,c.checked_at,c.outcome,c.run_id,c.detail,s.data,coalesce(json_extract(r.data,'$.scheduledFor'),0) FROM schedule_checks c JOIN schedules s ON s.id=c.schedule_id LEFT JOIN runs r ON r.id=c.run_id WHERE (?='' OR s.project_id=?) AND coalesce(nullif(json_extract(r.data,'$.scheduledFor'),0),c.checked_at)>=? AND coalesce(nullif(json_extract(r.data,'$.scheduledFor'),0),c.checked_at)<?", scope, scope, lo, hi)
	if e != nil {
		return view, e
	}
	defer rows.Close()
	for rows.Next() {
		var id, op, state, run, detail, raw string
		var at, planned int64
		if e = rows.Scan(&id, &op, &at, &state, &run, &detail, &raw, &planned); e != nil {
			return view, e
		}
		rule, _ := decodeJSON[Schedule](raw)
		if planned > 0 {
			at = planned
		}
		view.Items = append(view.Items, CalendarItem{Kind: "ai", State: state, RunID: run, OperationKey: op, CalendarEvent: CalendarEvent{ID: id, Start: at, Title: rule.Name, Description: detail, ProjectID: rule.ProjectID}})
	}
	sort.SliceStable(view.Items, func(i, j int) bool { return view.Items[i].Start < view.Items[j].Start })
	return view, rows.Err()
}
