package workbench

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHarnessCapacityCancelAndRelease(t *testing.T) {
	slots := make(chan struct{}, 1)
	ready := func() bool { return true }
	release, err := acquireHarnessSlot(t.Context(), slots, ready)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Millisecond)
	defer cancel()
	if _, err = acquireHarnessSlot(ctx, slots, ready); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	release()
	ctx, stop := context.WithCancel(t.Context())
	_, err = acquireHarnessSlot(ctx, slots, func() bool { stop(); return false })
	if !errors.Is(err, context.Canceled) || len(slots) != 0 {
		t.Fatal("waiting retained an execution slot", err)
	}
}

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

func TestConcurrentColdStartsCannotSpendSameHeadroom(t *testing.T) {
	var w launchWindow
	at := time.Now()
	if !w.admit(at, 400, 300) || w.admit(at, 400, 300) || !w.admit(at.Add(6*time.Second), 400, 300) {
		t.Fatal("cold-start memory allowance did not bound burst and recover")
	}
}
