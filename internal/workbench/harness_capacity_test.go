package workbench

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHarnessMemoryFloor(t *testing.T) {
	for _, tc := range []struct {
		info  string
		ready bool
	}{
		{"MemTotal: 3812552 kB\nMemAvailable: 850000 kB", false},
		{"MemTotal: 3812552 kB\nMemAvailable: 1000000 kB", true},
		{"MemTotal: 1024000 kB\nMemAvailable: 300000 kB", true},
		{"MemTotal: 3812552 kB", false},
	} {
		if (harnessHeadroom([]byte(tc.info)) >= 0) != tc.ready {
			t.Error(tc)
		}
	}
}

func TestHarnessReservationSpansPrepareAndFailedCleanup(t *testing.T) {
	pool := harnessPool{slots: make(map[*harnessSlot]struct{})}
	at := time.Now()
	s := pool.try(at, 400, 300)
	if s == nil {
		t.Fatal("not admitted")
	}
	if pool.try(at.Add(time.Minute), 400, 300) != nil {
		t.Fatal("unstarted task lost its reservation")
	}
	s.markStarted()
	s.setQuarantine(true)
	s.finish()
	if len(pool.slots) != 1 {
		t.Fatal("released before cleanup")
	}
	s.setQuarantine(false)
	s.finish()
	if len(pool.slots) != 0 {
		t.Fatal("cleanup did not release slot")
	}
}

func TestFailedPodmanRemovalPreventsSeventhLaunch(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "podman")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\ntest -e \"$0.allow\""), 0700); err != nil {
		t.Fatal(err)
	}
	h := Harness{Config: Config{Podman: tool, RuntimeMode: "podman", DataDir: root}}
	p := harnessPool{slots: make(map[*harnessSlot]struct{})}
	var s *harnessSlot
	for range maxConcurrentRuns {
		s = p.try(time.Now(), 10000, 10)
	}
	if err := h.cleanupProcessSlot(t.Context(), "orphan", s); err == nil {
		t.Fatal("expected failed removal")
	}
	s.finish()
	if got := p.try(time.Now(), 10000, 10); got != nil {
		t.Fatal("seventh admitted with orphan")
	}
	if err := os.WriteFile(tool+".allow", nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.try(time.Now(), 10000, 10) != nil })
}
