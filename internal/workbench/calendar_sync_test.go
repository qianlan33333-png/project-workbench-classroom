package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func syncPage(items []larkEvent, token, page string, more bool) []byte {
	return []byte(encode(map[string]any{"ok": true, "data": map[string]any{"items": items, "sync_token": token, "page_token": page, "has_more": more}}))
}
func syncEvent(id, title string, start int64) larkEvent {
	return larkEvent{ID: id, Title: title, Status: "confirmed", Start: larkTime{Timestamp: strconv.FormatInt(start/1000, 10), Timezone: "Asia/Shanghai"}, End: larkTime{Timestamp: strconv.FormatInt(start/1000+3600, 10), Timezone: "Asia/Shanghai"}}
}
func syncFixture(t *testing.T) (*Store, int64) {
	s := testStore(t)
	s.feishuCalendarID = "test"
	return s, time.Date(2026, 11, 2, 9, 0, 0, 0, calendarZone()).UnixMilli()
}
func TestCalendarSyncDurableIncrementalOfflineAndCancellation(t *testing.T) {
	s, at := syncFixture(t)
	ctx := context.Background()
	calls := 0
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		calls++
		if args[0] != "api" {
			t.Fatal(args)
		}
		var p map[string]any
		json.Unmarshal([]byte(args[4]), &p)
		if p["anchor_time"] != "0" || p["start_time"] != nil {
			t.Fatal(p)
		}
		return syncPage([]larkEvent{syncEvent("one_0", "会议", at)}, "cursor-1", "", false), nil
	})
	if e := s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	restart, e := NewStore(s.DB, s.Root)
	if e != nil {
		t.Fatal(e)
	}
	view, e := restart.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || len(view.Items) != 1 || calls != 1 || view.Sync.LastSuccess == 0 {
		t.Fatal(view, e, calls)
	}
	raw := encode(view)
	if strings.Contains(raw, "cursor-1") || strings.Contains(raw, "userId") {
		t.Fatal("private cursor leaked")
	}
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		var p map[string]any
		json.Unmarshal([]byte(args[4]), &p)
		if p["sync_token"] != "cursor-1" {
			t.Fatal(p)
		}
		return syncPage(nil, "cursor-2", "", false), nil
	})
	if e = s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	view, e = s.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || len(view.Items) != 1 {
		t.Fatal(view, e)
	}
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		return []byte(`{"ok":false,"error":{"type":"authentication","subtype":"token_missing","message":"need user auth"}}`), errors.New("exit 3")
	})
	if e = s.SyncCalendar(ctx, true); e == nil {
		t.Fatal("auth missing accepted")
	}
	count := 0
	s.calendarRunner = func(context.Context, CLITool, []string) ([]byte, error) {
		count++
		return nil, errors.New("should not call")
	}
	if e = s.SyncCalendar(ctx, false); e != nil || count != 0 {
		t.Fatal(e, count)
	}
	view, e = s.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || len(view.Items) != 1 || !view.Sync.Paused || view.ProviderError == "" {
		t.Fatal(view, e)
	}
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		v := larkEvent{ID: "one_0", Status: "cancelled"}
		return syncPage([]larkEvent{v}, "cursor-3", "", false), nil
	})
	if e = s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	view, e = s.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || len(view.Items) != 0 || view.Sync.Paused {
		t.Fatal(view, e)
	}
}
func TestCalendarSyncPartialPagesAndPublishFailureKeepCursor(t *testing.T) {
	s, at := syncFixture(t)
	ctx := context.Background()
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		return syncPage([]larkEvent{syncEvent("old_0", "旧", at)}, "original", "", false), nil
	})
	if e := s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	st, _ := s.calendarState()
	i := 0
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		i++
		if i == 1 {
			return syncPage([]larkEvent{syncEvent("new_0", "未完成", at)}, "", "page2", true), nil
		}
		return nil, errors.New("network failure")
	})
	if e := s.SyncCalendar(ctx, true); e == nil {
		t.Fatal("partial success")
	}
	after, _ := s.calendarState()
	if after.Token != st.Token || after.LastSuccess != st.LastSuccess {
		t.Fatal(after)
	}
	v, e := s.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || len(v.Items) != 1 || v.Items[0].Title != "旧" {
		t.Fatal(v, e)
	}
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		return syncPage([]larkEvent{syncEvent("new_0", "未发布", at)}, "unpublished", "", false), nil
	})
	if _, e = s.DB.Exec("CREATE TRIGGER fail_publish BEFORE INSERT ON calendar_remote_events BEGIN SELECT RAISE(FAIL,'injected commit error'); END"); e != nil {
		t.Fatal(e)
	}
	if e = s.SyncCalendar(ctx, true); e == nil {
		t.Fatal("publish failure swallowed")
	}
	after, _ = s.calendarState()
	if after.Token != "original" {
		t.Fatal("cursor advanced despite rollback", after.Token)
	}
	s.DB.Exec("DROP TRIGGER fail_publish")
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		t.Fatal("completed staging should publish without fetching again")
		return nil, nil
	})
	if e = s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	st, _ = s.calendarState()
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		return []byte(`{"ok":false,"error":{"type":"authentication","message":"invalid sync_token"}}`), errors.New("exit")
	})
	if e = s.SyncCalendar(ctx, true); e == nil {
		t.Fatal("cursor error swallowed")
	}
	after, _ = s.calendarState()
	if after.Token != "" || after.LastSuccess != st.LastSuccess || after.Paused {
		t.Fatal(after)
	}
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		var p map[string]any
		json.Unmarshal([]byte(args[4]), &p)
		if p["sync_token"] != nil {
			t.Fatal(p)
		}
		return syncPage(nil, "rebuilt", "", false), nil
	})
	if e = s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	v, e = s.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || len(v.Items) != 2 {
		t.Fatal(v, e)
	}
}
func TestCalendarSyncRecurrenceExceptionsAndAllDay(t *testing.T) {
	s, at := syncFixture(t)
	ctx := context.Background()
	day := int64(86400000)
	parent := syncEvent("weekly_0", "例会", at)
	parent.Recurrence = "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR"
	cancel := larkEvent{ID: "weekly_" + strconv.FormatInt((at+day)/1000, 10), Status: "cancelled"}
	moved := syncEvent("weekly_"+strconv.FormatInt((at+2*day)/1000, 10), "改到下周", at+7*day)
	allDay := larkEvent{ID: "day_0", Title: "全天", Status: "confirmed", Start: larkTime{Date: "2026-11-03"}, End: larkTime{Date: "2026-11-03"}}
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		return syncPage([]larkEvent{parent, cancel, moved, allDay}, "cursor", "", false), nil
	})
	if e := s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	v, e := s.calendarView(ctx, at-1, at+6*day, "", false)
	if e != nil || len(v.Items) != 4 {
		t.Fatal(v, e)
	}
	for _, item := range v.Items {
		if item.Title == "改到下周" {
			t.Fatal("moved exception still in original window")
		}
		if item.AllDay && item.End-item.Start != day {
			t.Fatal(item)
		}
	}
	v, e = s.calendarView(ctx, at+7*day-1, at+13*day, "", false)
	if e != nil || len(v.Items) != 6 {
		t.Fatal(v, e)
	}
	for _, item := range v.Items {
		if item.RemoteID == parent.ID {
			t.Fatal("series rendered without instance identity")
		}
	}
}
func TestCalendarSyncIdentityAndLocalWriteIsolation(t *testing.T) {
	s, at := syncFixture(t)
	ctx := context.Background()
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		return syncPage([]larkEvent{syncEvent("one_0", "原值", at)}, "c1", "", false), nil
	})
	if e := s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	state, _ := s.calendarState()
	local := CalendarEvent{ID: newID(), Operation: newID(), RemoteID: "one_0", RemoteSource: state.Source, Title: "新改动", Start: at, End: at + 3600000, Destination: "both", Pending: "edit", LocalWriteAt: now() + 1, Version: 3}
	if e := s.storeEvent(ctx, local); e != nil {
		t.Fatal(e)
	}
	v, e := s.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || len(v.Items) != 1 || v.Items[0].Title != "新改动" {
		t.Fatal(v, e)
	}
	s.feishuCalendarID = "another-account-calendar"
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		return syncPage([]larkEvent{syncEvent("other_0", "另一日历", at)}, "c2", "", false), nil
	})
	if e = s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	v, e = s.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || len(v.Items) != 1 || v.Items[0].Title != "另一日历" {
		t.Fatal(v, e)
	}
	if _, e = s.changeEvent(ctx, "delete", local, ""); e == nil {
		t.Fatal("cross-account write accepted")
	}
}
func TestCalendarSyncConcurrentRefreshAndReadDoNotBlock(t *testing.T) {
	s, at := syncFixture(t)
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		calls.Add(1)
		close(entered)
		<-release
		return syncPage(nil, "done", "", false), nil
	})
	finished := make(chan error, 1)
	go func() { finished <- s.SyncCalendar(ctx, true) }()
	<-entered
	second, e := NewStore(s.DB, s.Root)
	if e != nil {
		t.Fatal(e)
	}
	second.feishuCalendarID = "test"
	second.calendarRunner = s.calendarRunner
	if e = second.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	v, e := s.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || !v.Sync.Syncing {
		t.Fatal(v, e)
	}
	if e = s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e = <-finished; e != nil || calls.Load() != 1 {
		t.Fatal(e, calls.Load())
	}
	if e = s.SyncCalendar(ctx, false); e != nil || calls.Load() != 1 {
		t.Fatal("ten minute interval ignored", e, calls.Load())
	}
}

func TestCalendarSyncPaginationResumesAcrossRestart(t *testing.T) {
	s, at := syncFixture(t)
	ctx := context.Background()
	page := 0
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		var p map[string]any
		json.Unmarshal([]byte(args[4]), &p)
		if page > 0 && p["page_token"] != strconv.Itoa(page) {
			t.Fatal("lost pagination checkpoint", p, page)
		}
		page++
		return syncPage([]larkEvent{syncEvent("event"+strconv.Itoa(page)+"_0", "会议", at)}, "", strconv.Itoa(page), true), nil
	})
	if e := s.SyncCalendar(ctx, true); e == nil {
		t.Fatal("expected bounded initial batch")
	}
	if page != 10 {
		t.Fatal(page)
	}
	v, e := s.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || len(v.Items) != 0 {
		t.Fatal("partial history published", v, e)
	}
	pending, _ := s.calendarState()
	local := CalendarEvent{ID: newID(), Operation: newID(), RemoteID: "event1_0", Title: "分页期间的修改", Start: at, End: at + 3600000, Destination: "both", LocalWriteAt: pending.BatchStarted + 1}
	if e = s.storeEvent(ctx, local); e != nil {
		t.Fatal(e)
	}
	restarted, e := NewStore(s.DB, s.Root)
	if e != nil {
		t.Fatal(e)
	}
	restarted.feishuCalendarID = "test"
	fakeCalendar(t, restarted, func(args []string) ([]byte, error) {
		var p map[string]any
		json.Unmarshal([]byte(args[4]), &p)
		if p["page_token"] != "10" {
			t.Fatal(p)
		}
		return syncPage([]larkEvent{syncEvent("last_0", "最后一页", at)}, "complete", "", false), nil
	})
	if e = restarted.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	v, e = restarted.calendarView(ctx, at-1, at+7200000, "", false)
	if e != nil || len(v.Items) != 11 {
		t.Fatal(v, e)
	}
	kept := false
	for _, item := range v.Items {
		if item.Title == "分页期间的修改" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("resumed snapshot overwrote a newer local edit")
	}
}

func TestCalendarCachedRecurrenceTimezoneAndOverlap(t *testing.T) {
	s, _ := syncFixture(t)
	ny, e := time.LoadLocation("America/New_York")
	if e != nil {
		t.Fatal(e)
	}
	start := time.Date(2026, 10, 26, 9, 0, 0, 0, ny)
	event := syncEvent("dst_0", "时区", start.UnixMilli())
	event.Start.Timezone = "America/New_York"
	event.End.Timezone = "America/New_York"
	event.Recurrence = "FREQ=WEEKLY;COUNT=3"
	fakeCalendar(t, s, func(args []string) ([]byte, error) { return syncPage([]larkEvent{event}, "c", "", false), nil })
	if e = s.SyncCalendar(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	second := time.Date(2026, 11, 2, 9, 0, 0, 0, ny)
	v, e := s.calendarView(context.Background(), second.Add(30*time.Minute).UnixMilli(), second.Add(2*time.Hour).UnixMilli(), "", false)
	if e != nil || len(v.Items) != 1 || v.Items[0].Start != second.UnixMilli() {
		t.Fatal(v, e)
	}
}

func TestCalendarSyncLoopWakesAfterDueTimeAndAuthorization(t *testing.T) {
	s, _ := syncFixture(t)
	state, _ := s.calendarState()
	state.NextAt = now() + 120
	s.DB.Exec("UPDATE calendar_sync SET data=? WHERE id='feishu'", encode(state))
	delivered := make(chan struct{}, 4)
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		delivered <- struct{}{}
		return syncPage(nil, "cursor", "", false), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.CalendarSyncLoop(ctx) }()
	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("persisted due time not scheduled")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		v, _ := s.CalendarSyncStatus()
		if v.LastSuccess > 0 && !v.Syncing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first sync did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	// Manual refresh after reauthorization wakes a loop currently sleeping ten minutes.
	s.RequestCalendarSync()
	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("manual authorization wake lost")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sync loop did not stop")
	}
}
