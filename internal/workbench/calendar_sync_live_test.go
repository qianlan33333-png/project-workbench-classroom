package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"
)

func TestCalendarSyncLivePrimaryOfflinePersistence(t *testing.T) {
	file := os.Getenv("PWB_ACCEPT_SANDBOX_CONFIG")
	if file == "" {
		t.Skip("requires authorized live calendar sandbox")
	}
	raw, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	var cfg Config
	if e = json.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	s := testStore(t)
	original := cfg.DataDir
	cfg.DataDir = s.Root
	tool := toolPresets()[0]
	for _, part := range []string{"packages/lark/" + tool.Version, "state/lark"} {
		dst := filepath.Join(s.Root, "tools", part)
		if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.Symlink(filepath.Join(original, "tools", part), dst); e != nil {
			t.Fatal(e)
		}
	}
	tool.Installed = true
	if _, e = s.SaveCLITool(tool); e != nil {
		t.Fatal(e)
	}
	h := &Harness{Store: s, Config: cfg}
	calls := 0
	s.calendarRunner = func(ctx context.Context, tool CLITool, args []string) ([]byte, error) {
		calls++
		return h.calendarCommand(ctx, tool, args)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	started := time.Now()
	if e = s.SyncCalendar(ctx, true); e != nil {
		t.Fatal(e)
	}
	st, _ := s.calendarState()
	var count int
	s.DB.QueryRow("SELECT count(*) FROM calendar_remote_events WHERE source=?", st.Source).Scan(&count)
	nowTime := time.Now().In(calendarZone())
	d := time.Date(nowTime.Year(), nowTime.Month(), nowTime.Day(), 0, 0, 0, 0, calendarZone()).AddDate(0, 0, -int(nowTime.Weekday()))
	lo, hi := d.UnixMilli(), d.AddDate(0, 0, 7).UnixMilli()
	view, e := s.calendarView(ctx, lo, hi, "", false)
	if e != nil {
		t.Fatal(e)
	}
	var remote struct{ Items []larkEvent }
	if e = s.calendarCLI(ctx, &remote, "instance_view", "--calendar-id", st.CalendarID, "--start-time", strconv.FormatInt(lo/1000, 10), "--end-time", strconv.FormatInt(hi/1000, 10)); e != nil {
		t.Fatal(e)
	}
	want := []string{}
	got := []string{}
	for _, r := range remote.Items {
		v := r.event()
		if !v.Deleted && v.Start < hi && v.End > lo {
			want = append(want, v.RemoteID)
		}
	}
	for _, v := range view.Items {
		if v.Kind == "event" && v.RemoteID != "" {
			got = append(got, v.RemoteID)
		}
	}
	sort.Strings(want)
	sort.Strings(got)
	if encode(want) != encode(got) {
		t.Fatalf("native rules differ from Feishu instance view: expected %v got %v", want, got)
	}
	if e = s.SyncCalendar(ctx, true); e != nil {
		t.Fatal("incremental sync", e)
	}
	s.calendarRunner = func(context.Context, CLITool, []string) ([]byte, error) {
		return []byte(`{"ok":false,"error":{"type":"authentication","subtype":"token_missing","message":"injected offline"}}`), errors.New("injected")
	}
	if e = s.SyncCalendar(ctx, true); e == nil {
		t.Fatal("offline not detected")
	}
	restarted, e := NewStore(s.DB, s.Root)
	if e != nil {
		t.Fatal(e)
	}
	offline, e := restarted.calendarView(ctx, lo, hi, "", false)
	if e != nil || len(offline.Items) != len(view.Items) || !offline.Sync.Paused {
		t.Fatal("offline persistence", e, len(offline.Items), len(view.Items))
	}
	t.Logf("full raw records=%d, current week=%d, live CLI requests=%d, elapsed=%s; offline restart preserved all visible events", count, len(want), calls, time.Since(started).Round(time.Millisecond))
}
