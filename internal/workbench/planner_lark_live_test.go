package workbench

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPlannerLiveFeishuCalendar(t *testing.T) {
	binary := os.Getenv("PWB_ACCEPT_LARK")
	if binary == "" {
		t.Skip("requires designated provider acceptance")
	}
	s := testStore(t)
	token := newID()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "calendar", "calendars", "create", "--as", "user", "--data", encode(map[string]string{"summary": "工作台隔离验收 " + token[:8], "permissions": "private"}))
	data, e := cmd.Output()
	if e != nil {
		t.Fatal("创建隔离日历失败", e)
	}
	var envelope struct {
		Data struct {
			Calendar struct {
				ID string `json:"calendar_id"`
			}
		}
	}
	if e = json.Unmarshal(data, &envelope); e != nil || envelope.Data.Calendar.ID == "" {
		t.Fatal("未获得隔离日历标识")
	}
	s.feishuCalendarID = envelope.Data.Calendar.ID
	defer func() {
		if e := exec.Command(binary, "calendar", "calendars", "delete", "--calendar-id", s.feishuCalendarID, "--as", "user", "--yes").Run(); e != nil {
			t.Errorf("cleanup calendar: %v", e)
		}
	}()
	tool := toolPresets()[0]
	if e = os.MkdirAll(filepath.Dir(s.toolBinary(tool)), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(s.toolBinary(tool), []byte("#!/bin/sh\nexec "+binary+" \"$@\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveCLITool(tool); e != nil {
		t.Fatal(e)
	}
	start := time.Now().Add(48 * time.Hour).Truncate(time.Minute).UnixMilli()
	v, e := s.changeEvent(ctx, "create", CalendarEvent{Operation: token, Title: "验收 " + token, Destination: "both", Start: start, End: start + 1800000}, "")
	if e != nil || v.Sync != "ready" {
		t.Fatal(e, v.Error)
	}
	v.Title = "已调整 " + token
	v, e = s.changeEvent(ctx, "edit", v, "")
	if e != nil || v.Sync != "ready" {
		t.Fatal(e, v.Error)
	}
	matched := false
	for i := 0; i < 6; i++ {
		view, e := s.calendarView(ctx, start-3600000, start+7200000, "", true)
		if e != nil || view.ProviderError != "" {
			t.Fatal(e, view.ProviderError)
		}
		count := 0
		for _, event := range view.Items {
			if event.Title == v.Title {
				count++
				if event.ID != v.ID {
					t.Fatal("duplicate identity")
				}
			}
		}
		if count > 1 {
			t.Fatal("duplicate")
		}
		if count == 1 {
			matched = true
			break
		}
		time.Sleep(time.Second)
	}
	if !matched {
		t.Fatal("remote event did not appear")
	}
	v, e = s.changeEvent(ctx, "delete", v, "")
	if e != nil || v.Sync != "ready" || !v.Deleted {
		t.Fatal(e, v.Error)
	}
}
