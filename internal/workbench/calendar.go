package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/robfig/cron/v3"
	"os/exec"

	"sort"
	"strconv"
	"strings"
	"time"
)

type CalendarEvent struct {
	RemoteSource string `json:"remoteSource,omitempty"`
	LocalWriteAt int64  `json:"localWriteAt,omitempty"`
	SourceHint   string `json:"-"`
	ID           string `json:"id"`
	Operation    string `json:"operation,omitempty"`
	Version      int64  `json:"version"`
	ProjectID    string `json:"projectId,omitempty"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Start        int64  `json:"start"`
	End          int64  `json:"end"`
	AllDay       bool   `json:"allDay,omitempty"`
	Destination  string `json:"destination"`
	RemoteID     string `json:"remoteId,omitempty"`
	Link         string `json:"link,omitempty"`
	Sync         string `json:"sync,omitempty"`
	Error        string `json:"error,omitempty"`
	Pending      string `json:"pending,omitempty"`
	Deleted      bool   `json:"deleted,omitempty"`
	RemoteHash   string `json:"remoteHash,omitempty"`
	BeforeHash   string `json:"beforeHash,omitempty"`
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
	Items         []CalendarItem     `json:"items"`
	ProviderError string             `json:"providerError,omitempty"`
	Sync          CalendarSyncStatus `json:"sync"`
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
	Recurrence  string
}

var shanghaiCalendarZone, _ = time.LoadLocation("Asia/Shanghai")

func calendarZone() *time.Location { return shanghaiCalendarZone }
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

	return CalendarEvent{SourceHint: strings.TrimPrefix(v.Source, "pwb:"), ID: id, RemoteID: v.ID, Title: v.Title, Description: v.Description, Start: v.Start.millis(), End: end, AllDay: v.Start.Date != "", Destination: "feishu", Link: v.Link, Sync: "ready", Deleted: v.Status == "cancelled"}
}
func (s *Store) calendarID() string {
	if s.feishuCalendarID != "" {
		return s.feishuCalendarID
	}
	return "primary"
}

type calendarRejected struct{ error }

func (h *Harness) calendarCommand(ctx context.Context, t CLITool, args []string) ([]byte, error) {
	in := RuntimeInput{Project: Project{ID: personalProjectID}}
	argv, name, err := h.personalExecArgs(in, "/tools/bin/"+t.Command, args, "", &t)
	if err != nil {
		return nil, calendarRejected{err}
	}
	defer h.cleanupExternal(name)
	cmd := exec.CommandContext(ctx, h.Config.Podman, argv...)
	cmd.WaitDelay = 2 * time.Second
	var output, diagnostic limitedOutput
	output.maxBytes = 8 << 20
	cmd.Stdout = &output
	cmd.Stderr = &diagnostic
	err = cmd.Run()
	if output.clipped {
		return nil, errors.New("飞书响应超出上限，请缩小时间窗口")
	}
	if err != nil && diagnostic.b.Len() > 0 {
		err = fmt.Errorf("隔离飞书调用失败: %s", safeRuntimeError(diagnostic.b.String()))
	}
	if !json.Valid(output.b.Bytes()) && json.Valid(diagnostic.b.Bytes()) {
		return diagnostic.b.Bytes(), err
	}
	return output.b.Bytes(), err
}
func remoteHash(v CalendarEvent) string {
	return digest([]byte(encode([]any{v.Title, v.Description, v.Start / 1000, v.End / 1000, v.AllDay, v.Deleted})))
}
func (s *Store) calendarCLI(ctx context.Context, result any, args ...string) error {
	return s.calendarCall(ctx, result, append([]string{"calendar", "events"}, args...)...)
}
func (s *Store) calendarCall(ctx context.Context, result any, args ...string) error {
	t, e := s.CLITool("lark")
	if e != nil || !t.Enabled || !t.Installed {
		return calendarRejected{errors.New("飞书 CLI 未启用或未安装")}
	}
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	if s.calendarRunner == nil {
		return calendarRejected{errors.New("日历隔离执行器不可用")}
	}
	data, e := s.calendarRunner(ctx, t, append(args, "--as", "user", "--format", "json"))
	var reply struct {
		OK    bool
		Data  json.RawMessage
		Error struct {
			Message, Type, Subtype string
			Code                   json.RawMessage
		}
	}
	decodeErr := json.Unmarshal(data, &reply)
	if e != nil || !reply.OK || decodeErr != nil {
		if ctx.Err() != nil {
			return errors.New("飞书请求超时，下次同步会重试；已有日程仍保留")
		}
		if reply.Error.Message != "" {
			rawMessage := strings.ToLower(reply.Error.Message)
			invalid := strings.Contains(rawMessage, "sync_token") || strings.Contains(rawMessage, "sync token") || strings.Contains(rawMessage, "page_token") || strings.Contains(rawMessage, "page token")
			msg := safeRuntimeError(reply.Error.Message)
			auth := !invalid && (reply.Error.Type == "authentication" || strings.Contains(reply.Error.Subtype, "token"))
			if auth {
				msg = "飞书用户授权已失效，请重新登录；已同步日程仍可查看"
			}
			if invalid {
				msg = "飞书增量同步游标已失效"
			}
			return calendarAPIError{message: msg, auth: auth, invalidCursor: invalid}
		}
		if e != nil {
			return fmt.Errorf("飞书调用失败: %s", safeRuntimeError(e.Error()))
		}
		return errors.New("飞书返回格式异常；已同步日程仍保留")
	}
	if result != nil {
		return json.Unmarshal(reply.Data, result)
	}
	return nil
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
				old.RemoteHash = remoteHash(old)
				if v.RemoteHash == "" || v.RemoteHash != old.RemoteHash {
					return old, ErrConflict
				}
				old.Operation = "import:" + old.RemoteID
			}
		}
		if e != nil {
			return CalendarEvent{}, e
		}
		if old.RemoteSource != "" {
			status, err := s.CalendarSyncStatus()
			if err != nil {
				return old, err
			}
			if status.Source != "" && old.RemoteSource != status.Source {
				return old, errors.New("此日程属于另一飞书账号，请切回原账号后操作")
			}
		}
		if scope != "" && old.ProjectID != scope {
			return CalendarEvent{}, ErrNotFound
		}
		if v.Version != old.Version {
			return old, ErrConflict
		}
		if intent == "accept_remote" {
			if old.RemoteID == "" {
				return old, errors.New("尚无飞书事件标识，请先核对创建结果")
			}
			var obj struct{ Event larkEvent }
			if err := s.calendarCLI(ctx, &obj, "get", "--calendar-id", s.calendarID(), "--event-id", old.RemoteID); err != nil {
				return old, err
			}
			actual := obj.Event.event()
			actual.ID, actual.ProjectID, actual.Operation, actual.Destination = old.ID, old.ProjectID, old.Operation, old.Destination
			actual.Version = old.Version + 1
			actual.RemoteHash = remoteHash(actual)
			actual.LocalWriteAt = now()
			actual.RemoteSource = old.RemoteSource
			s.RequestCalendarSync()
			return actual, s.storeEvent(ctx, actual)
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
				if !applied && (v.BeforeHash == "" || remoteHash(remote) != v.BeforeHash) {
					return v, errors.New("飞书日程已被其他操作修改，请在飞书核对后处理；未重放旧操作")
				}
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
			if old.RemoteID != "" {
				var observed struct{ Event larkEvent }
				if err := s.calendarCLI(ctx, &observed, "get", "--calendar-id", s.calendarID(), "--event-id", old.RemoteID); err != nil {
					return old, err
				}
				actual := observed.Event.event()
				baseline := remoteHash(old)
				if v.RemoteHash != "" {
					baseline = v.RemoteHash
				}
				if remoteHash(actual) != baseline {
					return old, ErrConflict
				}
				v.BeforeHash = remoteHash(actual)
			}
			if intent == "delete" {
				v = old
			}
			if intent == "delete" {
				v.BeforeHash = remoteHash(old)
			}
			v.Destination = old.Destination
			v.RemoteID = old.RemoteID
			v.Operation = old.Operation
			v.ProjectID = old.ProjectID
			v.Version = old.Version + 1
			v.Link = old.Link
		}
	}
	if intent == "edit" && old.Sync == "failed" && old.RemoteID == "" && old.Destination != "cloud" {
		intent = "create"
		v.Operation = newID()
	}
	if scope != "" {
		v.ProjectID = scope
	}
	if v.ProjectID != "" {
		p, e := s.Project(v.ProjectID)
		if (e != nil || p.Archived) && intent != "delete" {
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
	v.LocalWriteAt = now()
	status, err := s.CalendarSyncStatus()
	if err != nil {
		return v, err
	}
	v.RemoteSource = status.Source
	defer s.RequestCalendarSync()
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
		var rejected calendarRejected
		if errors.As(e, &rejected) {
			v.Sync = "failed"
			v.Pending = ""
		}
		v.Error = e.Error()
	} else {
		v.Sync = "ready"
		v.Pending = ""
		if res.Event.ID != "" {
			v.RemoteID = res.Event.ID
			v.Link = res.Event.Link
		}
		v.Deleted = intent == "delete"
		v.RemoteHash = remoteHash(v)
		v.BeforeHash = ""
	}
	return v, s.storeEvent(context.Background(), v)
}
func (s *Store) calendarView(ctx context.Context, lo, hi int64, scope string, remote bool) (CalendarView, error) {
	if lo <= 0 || hi <= lo || hi-lo > 45*24*3600000 {
		return CalendarView{}, errors.New("时间窗口须在45天以内")
	}
	if remote {
		_ = s.SyncCalendar(ctx, true)
	}
	status, err := s.CalendarSyncStatus()
	if err != nil {
		return CalendarView{}, err
	}
	view := CalendarView{Items: []CalendarItem{}, Sync: status, ProviderError: status.Error}
	if lo <= 0 || hi <= lo || hi-lo > 45*24*3600000 {
		return view, errors.New("时间窗口须在45天以内")
	}
	byRemote, known, e := s.cachedCalendarEvents(ctx, status.Source, lo, hi)
	if e != nil {
		return view, e
	}
	events, e := listJSON[CalendarEvent](s.DB, "SELECT data FROM calendar_events WHERE (?='' OR json_extract(data,'$.projectId')=?) AND (coalesce(json_extract(data,'$.remoteId'),'')<>'' OR (json_extract(data,'$.start')<? AND json_extract(data,'$.end')>?))", scope, scope, hi, lo)
	if e != nil {
		return view, e
	}
	for _, v := range events {
		if v.RemoteSource != "" && status.Source != "" && v.RemoteSource != status.Source {
			continue
		}
		if v.RemoteID != "" {
			// Raw source data is authoritative once synced, except a pending or
			// later local write that the snapshot could not have observed.
			base, _ := splitEventID(v.RemoteID)
			if known[v.RemoteID] || known[base+"_0"] {
				if v.Pending == "" && v.LocalWriteAt <= status.SnapshotAt {
					if actual, ok := byRemote[v.RemoteID]; ok {
						actual.ID, actual.ProjectID, actual.Operation, actual.Destination = v.ID, v.ProjectID, v.Operation, v.Destination
						actual.Version = v.Version
						if scope != "" {
							view.Items = append(view.Items, CalendarItem{CalendarEvent: actual, Kind: "event"})
							delete(byRemote, v.RemoteID)
						} else {
							byRemote[v.RemoteID] = actual
						}
					}
					continue
				}
			}
			delete(byRemote, v.RemoteID)
		}
		if !v.Deleted && v.Start < hi && v.End > lo {
			view.Items = append(view.Items, CalendarItem{CalendarEvent: v, Kind: "event"})
		}
	}
	if scope == "" {
		for _, v := range byRemote {
			if !v.Deleted && v.Start < hi && v.End > lo {
				view.Items = append(view.Items, CalendarItem{CalendarEvent: v, Kind: "event"})
			}
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
		if at < hi && day.AddDate(0, 0, 1).UnixMilli() > lo {
			view.Items = append(view.Items, CalendarItem{Kind: "todo", State: t.State, CalendarEvent: CalendarEvent{ID: t.ID, Title: label + t.Title, ProjectID: t.ProjectID, Description: t.Description, Start: at, End: day.AddDate(0, 0, 1).UnixMilli(), AllDay: true}})
		}
	}
	schedules, e := s.Schedules(scope)
	if e != nil {
		return view, e
	}
	compiled := map[string]cron.Schedule{}
	for _, rule := range schedules {
		if !rule.Enabled {
			continue
		}
		for cursor := max(now(), lo-1); cursor < hi; {
			v, e := nextTick(rule, cursor, compiled)
			if len(view.Items) > 80000 {
				return view, errors.New("该周期内执行次数过多，请缩小查询时间范围")
			}
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
