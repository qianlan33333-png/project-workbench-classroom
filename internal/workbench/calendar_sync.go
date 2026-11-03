package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	rrule "github.com/teambition/rrule-go"
)

const calendarSyncSchema = `
CREATE TABLE IF NOT EXISTS calendar_sync(id TEXT PRIMARY KEY,data TEXT NOT NULL,owner TEXT NOT NULL DEFAULT '',lease_until INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO calendar_sync(id,data) VALUES('feishu','{}');
CREATE TABLE IF NOT EXISTS calendar_remote_events(source TEXT NOT NULL,event_id TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(source,event_id));
CREATE TABLE IF NOT EXISTS calendar_stage(batch TEXT NOT NULL,event_id TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(batch,event_id));
`
const calendarInterval = int64(10 * time.Minute / time.Millisecond)

type CalendarSyncStatus struct {
	Source     string `json:"-"`
	CalendarID string `json:"calendarId,omitempty"`
	UserID     string `json:"-"`
	Token      string `json:"-"`
	// Private fields are explicitly persisted by calendarState, never returned by HTTP.
	LastSuccess int64  `json:"lastSuccess"`
	LastAttempt int64  `json:"lastAttempt"`
	SnapshotAt  int64  `json:"snapshotAt"`
	NextAt      int64  `json:"nextAt"`
	Paused      bool   `json:"paused"`
	Syncing     bool   `json:"syncing"`
	Error       string `json:"error,omitempty"`
}
type calendarState struct {
	CalendarSyncStatus
	Source        string `json:"source"`
	UserID        string `json:"userId"`
	Token         string `json:"token"`
	Batch         string `json:"batch,omitempty"`
	BatchStarted  int64  `json:"batchStarted,omitempty"`
	Page          string `json:"page,omitempty"`
	BaseToken     string `json:"baseToken,omitempty"`
	PendingSource string `json:"pendingSource,omitempty"`
	ReadyToken    string `json:"readyToken,omitempty"`
}
type calendarAPIError struct {
	message             string
	auth, invalidCursor bool
}

func (e calendarAPIError) Error() string { return e.message }
func (e calendarAPIError) Unwrap() error { return calendarRejected{errors.New(e.message)} }
func (s *Store) calendarState() (calendarState, error) {
	return getJSON[calendarState](s.DB, "SELECT data FROM calendar_sync WHERE id='feishu'")
}
func (s *Store) CalendarSyncStatus() (CalendarSyncStatus, error) {
	st, e := s.calendarState()
	if e != nil {
		return st.CalendarSyncStatus, e
	}
	st.CalendarSyncStatus.Source = st.Source
	st.CalendarSyncStatus.UserID = st.UserID
	st.CalendarSyncStatus.Token = st.Token
	var until int64
	if e = s.DB.QueryRow("SELECT lease_until FROM calendar_sync WHERE id='feishu'").Scan(&until); e != nil {
		return st.CalendarSyncStatus, e
	}
	st.Syncing = until > now() || len(s.calendarWake) > 0
	return st.CalendarSyncStatus, nil
}
func (s *Store) RequestCalendarSync() {
	select {
	case s.calendarWake <- struct{}{}:
	default:
	}
}
func (s *Store) CalendarSyncLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		manual := false
		select {
		case <-ctx.Done():
			return
		case <-s.calendarWake:
			manual = true
		case <-timer.C:
		}
		if err := s.SyncCalendar(ctx, manual); err != nil {
			log.Printf("日历同步: %s", safeRuntimeError(err.Error()))
		}
		state, err := s.calendarState()
		delay := time.Minute
		if err == nil && !state.Paused && state.NextAt > now() {
			delay = time.Until(time.UnixMilli(state.NextAt))
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(max(delay, time.Millisecond))
	}
}
func (s *Store) SyncCalendar(parent context.Context, manual bool) (result error) {
	if !s.calendarSyncMu.TryLock() {
		return nil
	}
	defer s.calendarSyncMu.Unlock()
	st, e := s.calendarState()
	if e != nil {
		return e
	}
	if !manual && (st.Paused || st.NextAt > now()) {
		return nil
	}
	owner := newID()
	started := now()
	res, e := s.DB.Exec("UPDATE calendar_sync SET owner=?,lease_until=? WHERE id='feishu' AND lease_until<=?", owner, started+4*60000, started)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil || n == 0 {
		return e
	}
	defer func() {
		// Drain clicks received before completion, so concurrent refreshes share one attempt.
		select {
		case <-s.calendarWake:
		default:
		}
		_, _ = s.DB.Exec("UPDATE calendar_sync SET owner='',lease_until=0 WHERE id='feishu' AND owner=?", owner)
	}()
	// Re-read only after acquiring the database lease: another process may
	// have published a newer cursor between our first read and acquisition.
	st, e = s.calendarState()
	if e != nil {
		return e
	}
	if !manual && (st.Paused || st.NextAt > now()) {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	st.LastAttempt = started
	defer func() {
		if result != nil {
			st.Error = safeRuntimeError(result.Error())
			st.NextAt = now() + calendarInterval
			var ce calendarAPIError
			st.Paused = errors.As(result, &ce) && ce.auth
			_, e := s.DB.Exec("UPDATE calendar_sync SET data=? WHERE id='feishu' AND owner=?", encode(st), owner)
			if e != nil {
				result = fmt.Errorf("%w; 保存同步状态失败: %v", result, e)
			}
		}
	}()
	var primary struct {
		Calendars []struct {
			Calendar struct {
				ID string `json:"calendar_id"`
			}
			UserID string `json:"user_id"`
		}
	}
	cid, uid := s.feishuCalendarID, "acceptance"
	if cid == "" {
		if e = s.calendarCall(ctx, &primary, "calendar", "calendars", "primary"); e != nil {
			return e
		}
		if len(primary.Calendars) != 1 {
			return errors.New("无法确定本人主日历")
		}
		cid, uid = primary.Calendars[0].Calendar.ID, primary.Calendars[0].UserID
	}
	if cid == "" || uid == "" {
		return errors.New("飞书未返回日历或用户标识")
	}
	source := cid + ":" + uid
	token := st.Token
	if source != st.Source {
		token = ""
	}
	if st.Batch == "" || st.PendingSource != source {
		st.Batch = newID()
		st.BatchStarted = started
		st.Page = ""
		st.BaseToken = token
		st.PendingSource = source
		st.ReadyToken = ""
	}
	batch := st.Batch
	if _, e = s.DB.Exec("DELETE FROM calendar_stage WHERE batch<>?", batch); e != nil {
		return e
	}
	pageToken := st.Page
	token = st.BaseToken
	seen := map[string]bool{}
	nextToken := st.ReadyToken
	for page := 0; page < 10 && nextToken == ""; page++ {
		params := map[string]any{"page_size": 200, "anchor_time": "0"}
		if token != "" {
			params["sync_token"] = token
		}
		if pageToken != "" {
			params["page_token"] = pageToken
		}
		var response struct {
			Items []json.RawMessage `json:"items"`
			Page  string            `json:"page_token"`
			Sync  string            `json:"sync_token"`
			More  bool              `json:"has_more"`
		}
		e = s.calendarCall(ctx, &response, "api", "GET", "/open-apis/calendar/v4/calendars/"+url.PathEscape(cid)+"/events", "--params", encode(params))
		if e != nil {
			var ce calendarAPIError
			if (token != "" || pageToken != "") && errors.As(e, &ce) && ce.invalidCursor {
				// Reset once, then let the next ten-minute tick perform a full merge.
				st.Token = ""
				st.Batch = ""
				st.BatchStarted = 0
				st.Page = ""
				st.BaseToken = ""
				st.PendingSource = ""
				st.ReadyToken = ""
				return fmt.Errorf("同步游标失效，下一轮重新补齐: %w", e)
			}
			return e
		}
		progress := st
		progress.Page = response.Page
		if !response.More {
			progress.ReadyToken = response.Sync
		}
		if e = s.Transaction(ctx, func(tx *sql.Tx) error {
			for _, raw := range response.Items {
				var v larkEvent
				if e := json.Unmarshal(raw, &v); e != nil {
					return e
				}
				if e := validateRemoteEvent(v); e != nil {
					return e
				}
				if _, e := tx.ExecContext(ctx, "INSERT INTO calendar_stage VALUES(?,?,?) ON CONFLICT(batch,event_id) DO UPDATE SET data=excluded.data", batch, v.ID, string(raw)); e != nil {
					return e
				}
			}
			_, e := tx.ExecContext(ctx, "UPDATE calendar_sync SET data=? WHERE id='feishu' AND owner=?", encode(progress), owner)
			return e
		}); e != nil {
			return e
		}
		st = progress
		if !response.More {
			if response.Sync == "" {
				return errors.New("飞书未返回同步游标，保留上次完整数据")
			}
			nextToken = response.Sync
			break
		}
		if response.Page == "" || seen[response.Page] {
			return errors.New("飞书分页游标无效，保留上次完整数据")
		}
		seen[response.Page] = true
		pageToken = response.Page
	}
	if nextToken == "" {
		return errors.New("日程正在分批补齐，下轮从已保存进度继续；已有日程仍保留")
	}
	// Publishing is short and shares the calendar write lock. Network calls never
	// hold it, so opening cached records remains responsive during synchronization.
	s.calendarMu.Lock()
	defer s.calendarMu.Unlock()
	next := st
	next.Source, next.UserID, next.Token = source, uid, nextToken
	next.Batch = ""
	next.BatchStarted = 0
	next.Page = ""
	next.BaseToken = ""
	next.PendingSource = ""
	next.ReadyToken = ""
	next.CalendarID = cid
	next.LastSuccess = now()
	next.SnapshotAt = st.BatchStarted
	if next.SnapshotAt == 0 {
		next.SnapshotAt = started
	}
	next.NextAt = next.LastSuccess + calendarInterval
	next.Paused = false
	next.Error = ""
	publishErr := s.Transaction(ctx, func(tx *sql.Tx) error {
		binding := st.Source
		if binding == "" {
			binding = source
		}
		if _, e := tx.ExecContext(ctx, "UPDATE calendar_events SET data=json_set(data,'$.remoteSource',?) WHERE coalesce(json_extract(data,'$.remoteId'),'')<>'' AND coalesce(json_extract(data,'$.remoteSource'),'')=''", binding); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO calendar_remote_events SELECT ?,event_id,data FROM calendar_stage WHERE batch=? ON CONFLICT(source,event_id) DO UPDATE SET data=excluded.data", source, batch); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "UPDATE calendar_sync SET data=? WHERE id='feishu' AND owner=?", encode(next), owner)
		return e
	})
	if publishErr == nil {
		_, _ = s.DB.Exec("DELETE FROM calendar_stage WHERE batch=?", batch)
	}
	return publishErr
}
func validateRemoteEvent(v larkEvent) error {
	if v.ID == "" {
		return errors.New("飞书返回缺少ID的日程")
	}
	if v.Status == "cancelled" {
		return nil
	}
	if v.Start.millis() <= 0 || v.End.millis() <= 0 {
		return fmt.Errorf("日程%s的时间无效", v.ID)
	}
	if v.Recurrence != "" {
		_, e := remoteRule(v)
		return e
	}
	return nil
}
func splitEventID(id string) (string, int64) {
	i := strings.LastIndexByte(id, '_')
	if i < 0 {
		return id, 0
	}
	n, _ := strconv.ParseInt(id[i+1:], 10, 64)
	return id[:i], n
}
func remoteRule(v larkEvent) (*rrule.RRule, error) {
	loc := calendarZone()
	if v.Start.Timezone != "" {
		var e error
		loc, e = time.LoadLocation(v.Start.Timezone)
		if e != nil {
			return nil, e
		}
	}
	opt, e := rrule.StrToROptionInLocation(strings.TrimPrefix(v.Recurrence, "RRULE:"), loc)
	if e != nil {
		return nil, e
	}
	if opt.Freq > rrule.DAILY {
		return nil, errors.New("日程重复频率超出支持范围")
	}
	opt.Dtstart = time.UnixMilli(v.Start.millis()).In(loc)
	return rrule.NewRRule(*opt)
}
func (s *Store) cachedCalendarEvents(ctx context.Context, source string, lo, hi int64) (map[string]CalendarEvent, map[string]bool, error) {
	events := map[string]CalendarEvent{}
	known := map[string]bool{}
	if source == "" {
		return events, known, nil
	}
	raw, e := listJSON[larkEvent](s.DB, "SELECT data FROM calendar_remote_events WHERE source=?", source)
	if e != nil {
		return nil, nil, e
	}
	for _, v := range raw {
		known[v.ID] = true
	}
	add := func(v CalendarEvent) {
		if !v.Deleted && v.Start < hi && v.End > lo {
			v.RemoteHash = remoteHash(v)
			events[v.RemoteID] = v
		}
	}
	for _, v := range raw {
		if e = ctx.Err(); e != nil {
			return nil, nil, e
		}
		if v.Status == "cancelled" {
			continue
		}
		if v.Recurrence == "" {
			add(v.event())
			continue
		}
		rule, e := remoteRule(v)
		if e != nil {
			return nil, nil, e
		}
		base := v.event()
		uid, _ := splitEventID(v.ID)
		// Include an occurrence that starts before the window but overlaps it.
		duration := base.End - base.Start
		count := 0
		iter := rule.Iterator()
		scanned := 0
		for at, ok := iter(); ok; at, ok = iter() {
			scanned++
			if scanned > 1000000 {
				return nil, nil, errors.New("重复日程跨度过大，请检查重复规则")
			}
			if scanned%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
			}
			if at.UnixMilli() >= hi {
				break
			}
			if at.UnixMilli() <= lo-duration {
				continue
			}
			count++
			if count > 80000 || len(events) > 80000 {
				return nil, nil, errors.New("日程实例过多，请缩小查询范围")
			}

			id := uid + "_" + strconv.FormatInt(at.Unix(), 10)
			if known[id] {
				continue
			} // Explicit exception, including cancellation, wins.
			instance := base
			instance.RemoteID = id
			instance.ID = "fs:" + id
			instance.Start = at.UnixMilli()
			if base.AllDay {
				days := int(time.UnixMilli(base.End).Sub(time.UnixMilli(base.Start)).Hours() / 24)
				instance.End = at.AddDate(0, 0, days).UnixMilli()
			} else {
				instance.End = instance.Start + duration
			}
			add(instance)
		}
	}
	return events, known, nil
}
