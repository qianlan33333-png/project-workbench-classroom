package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewPauseIgnoresBrokenBindingAndNewRulesStayOff(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "暂停")
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "旧规则", Instruction: "work", Cron: "0 9 * * *", Enabled: true, SessionID: p.DefaultSessionID})
	if e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("UPDATE sessions SET data=json_set(data,'$.recycledAt',1) WHERE id=?", p.DefaultSessionID)
	raw := json.RawMessage(`{"id":"` + sc.ID + `","enabled":false}`)
	got, _, e := s.saveChatSchedule(RuntimeInput{Project: p}, raw, false)
	if e != nil || got.Enabled {
		t.Fatal(got, e)
	}
	created, _, e := s.saveChatSchedule(RuntimeInput{Project: p, Run: Run{ID: newID()}}, json.RawMessage(`{"name":"整理","instruction":"整理资料","cron":"0 9 * * *","condition":"changed","output":"project_context"}`), false)
	if e != nil || created.Enabled {
		t.Fatal(created, e)
	}
	created.ID = ""
	created.SessionID = p.DefaultSessionID
	if _, e = s.SaveSchedule(created); e == nil {
		t.Fatal("allowed scratch summary to reuse native session")
	}
}

func TestReviewSeriesEarlierAndCollision(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "周期")
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "周期", Instruction: "work", Cron: "0 18 * * 2", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	original := sc.NextAt
	// Move the preceding instance to Wednesday in the following week, then change the series there.
	next := original + 7*24*3600000
	moved := next + 26*3600000
	sc, e = s.editTime(sc.ID, "occurrence", TimeEdit{Original: original, At: moved, Version: sc.Version})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.editTime(sc.ID, "series", TimeEdit{Original: next, Cron: "0 20 * * 3", Version: sc.Version}); e == nil {
		t.Fatal("collision silently accepted")
	}
	fresh, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "提前", Instruction: "work", Cron: "0 18 * * 2"})
	if e != nil {
		t.Fatal(e)
	}
	first := fresh.NextAt
	if first-9*3600000 <= now() {
		first += 7 * 24 * 3600000
	}
	fresh, e = s.editTime(fresh.ID, "series", TimeEdit{Original: first, Cron: "0 9 * * 2", Version: fresh.Version})
	if e != nil {
		t.Fatal(e)
	}
	tick, e := scheduleTick(fresh, first-10*3600000)
	if e != nil || tick.At != first-9*3600000 {
		t.Fatal(tick, e)
	}
	interval, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "间隔", Instruction: "work", Cron: "@every 1h"})
	if e != nil {
		t.Fatal(e)
	}
	tick, e = scheduleTick(interval, interval.CreatedAt+300000)
	if e != nil || tick.At != interval.NextAt || !originalExists(interval, tick.At) {
		t.Fatal(interval, tick, e)
	}
}

func fakeCalendar(t *testing.T, s *Store, fn func([]string) ([]byte, error)) {
	t.Helper()
	tool := toolPresets()[0]
	os.MkdirAll(filepath.Dir(s.toolBinary(tool)), 0700)
	os.WriteFile(s.toolBinary(tool), []byte("#!/bin/sh\nexit 0\n"), 0700)
	tool.Installed = true
	tool.Enabled = true
	if _, e := s.SaveCLITool(tool); e != nil {
		t.Fatal(e)
	}
	s.calendarRunner = func(_ context.Context, _ CLITool, args []string) ([]byte, error) { return fn(args) }
}
func eventReply(v larkEvent) []byte {
	return []byte(encode(map[string]any{"ok": true, "data": map[string]any{"event": v}}))
}

func TestReviewRemoteSourceCannotOverwriteLocalAndRejectedCreateRepairable(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	start := now() + 3600000
	local, e := s.changeEvent(ctx, "create", CalendarEvent{Operation: newID(), Title: "本地", Start: start, End: start + 3600000, Destination: "cloud"}, "")
	if e != nil {
		t.Fatal(e)
	}
	remote := larkEvent{ID: "remote-copy", Source: "pwb:" + local.ID, Title: "外部", Start: larkTime{Timestamp: "2000000000"}, End: larkTime{Timestamp: "2000003600"}}
	fakeCalendar(t, s, func(args []string) ([]byte, error) { return eventReply(remote), nil })
	imported := remote.event()
	imported.RemoteHash = remoteHash(imported)
	if _, e = s.changeEvent(ctx, "delete", imported, ""); e != nil {
		t.Fatal(e)
	}
	after, _ := s.calendarRecord(local.ID)
	if after.Deleted || after.Title != local.Title {
		t.Fatal("unrelated local record overwritten", after)
	}
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		return []byte(`{"ok":false,"error":{"message":"invalid title"}}`), errors.New("exit 1")
	})
	failed, e := s.changeEvent(ctx, "create", CalendarEvent{Operation: newID(), Title: "bad", Start: start, End: start + 3600000, Destination: "both"}, "")
	if e != nil || failed.Pending != "" || failed.Sync != "failed" {
		t.Fatal(failed, e)
	}
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		if args[2] != "create" {
			t.Fatalf("repair was %s", args[2])
		}
		return eventReply(larkEvent{ID: "fixed"}), nil
	})
	failed.Title = "fixed"
	fixed, e := s.changeEvent(ctx, "edit", failed, "")
	if e != nil || fixed.RemoteID != "fixed" || fixed.Pending != "" {
		t.Fatal(fixed, e)
	}
}

func TestReviewReconcileDoesNotOverwriteThirdStateAndCanAdoptIt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	old := CalendarEvent{ID: newID(), Operation: newID(), Title: "old", Start: 2000000000000, End: 2000003600000, Destination: "both", RemoteID: "ext", Version: 1, Sync: "ready"}
	s.storeEvent(ctx, old)
	actual := larkEvent{ID: "ext", Title: "old", Start: larkTime{Timestamp: "2000000000"}, End: larkTime{Timestamp: "2000003600"}}
	calls := 0
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		calls++
		if args[2] == "get" {
			return eventReply(actual), nil
		}
		return nil, errors.New("unknown")
	})
	desired := old
	desired.Title = "desired"
	pending, e := s.changeEvent(ctx, "edit", desired, "")
	if e != nil || pending.Pending != "edit" {
		t.Fatal(pending, e)
	}
	actual.Title = "changed on Feishu"
	calls = 0
	if _, e = s.changeEvent(ctx, "reconcile", pending, ""); e == nil || calls != 1 {
		t.Fatal("replayed conflict", e, calls)
	}
	adopted, e := s.changeEvent(ctx, "accept_remote", pending, "")
	if e != nil || adopted.Title != actual.Title || adopted.Pending != "" {
		t.Fatal(adopted, e)
	}
}

func TestReviewArchivedProjectCanCancelAndRemoteMovePersists(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := testProject(t, s, "日程")
	start := int64(2000000000000)
	local, e := s.changeEvent(ctx, "create", CalendarEvent{Operation: newID(), ProjectID: p.ID, Title: "本地", Start: start, End: start + 3600000, Destination: "cloud"}, "")
	if e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("UPDATE projects SET archived=1,data=json_set(data,'$.archived',json('true')) WHERE id=?", p.ID)
	if v, e := s.changeEvent(ctx, "delete", local, ""); e != nil || !v.Deleted {
		t.Fatal(v, e)
	}
	remote := CalendarEvent{ID: newID(), Operation: newID(), Title: "飞书", Start: start, End: start + 3600000, Destination: "both", RemoteID: "moved", Version: 1, Sync: "ready"}
	s.storeEvent(ctx, remote)
	s.feishuCalendarID = "test-calendar"
	fakeCalendar(t, s, func(args []string) ([]byte, error) {
		event := larkEvent{ID: "moved", Title: "飞书", Start: larkTime{Timestamp: "2001000000"}, End: larkTime{Timestamp: "2001003600"}}
		return []byte(encode(map[string]any{"ok": true, "data": map[string]any{"items": []larkEvent{event}, "sync_token": "next"}})), nil
	})
	if _, e = s.calendarView(ctx, start-1, start+7200000, "", true); e != nil {
		t.Fatal(e)
	}
	view, e := s.calendarView(ctx, start-1, start+7200000, "", false)
	if e != nil || len(view.Items) != 0 {
		t.Fatal(view, e)
	}
}

func TestReviewFailureVisibilityAndSafetySurvivePurge(t *testing.T) {
	s := testStore(t)
	p := testProject(t, s, "失败")
	sc, e := s.SaveSchedule(Schedule{ProjectID: p.ID, Name: "失败", Instruction: "work", Cron: "0 9 * * *", SessionID: p.DefaultSessionID})
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := s.RunSchedule(sc.ID, "review-failure")
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.Run(receipt.RunID)
	if e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("INSERT INTO events(session_id,run_id,type,data,created_at) VALUES(?,?,'harness.tool/call','{}',?)", r.SessionID, r.ID, now())
	r.State = "failed"
	r.Error = "error"
	r.FinishedAt = now()
	if e = s.saveRun(r); e != nil {
		t.Fatal(e)
	}
	var id string
	s.DB.QueryRow("SELECT id FROM schedule_failures WHERE run=?", r.ID).Scan(&id)
	s.DB.Exec("UPDATE sessions SET data=json_set(data,'$.recycledAt',1) WHERE id=?", r.SessionID)
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	hash, _ := PasswordHash("review-password")
	(&API{Store: s, Config: Config{PasswordHash: hash}}).Mount(router)
	req := httptest.NewRequest("GET", "/api/v1/failures/"+id+"/tools", nil)
	req.SetBasicAuth("owner", "review-password")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.DB.Exec("DELETE FROM events WHERE run_id=?", r.ID)
	f, e := s.failure(id)
	if e != nil || !f.CheckExternal {
		t.Fatal(f, e)
	}
	// The sandbox adapter refuses native execution before invoking any configured binary.
	h := Harness{Store: s, Config: Config{RuntimeMode: "native"}}
	if _, e = h.calendarCommand(context.Background(), CLITool{ID: "lark"}, []string{"calendar"}); e == nil || !strings.Contains(e.Error(), "Podman") {
		t.Fatal(e)
	}
}
