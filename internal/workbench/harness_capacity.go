package workbench

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// Production has one workbench process. The router and archive paths also
// enter Execute, though they do not necessarily have a row in runs.
var hostHarnessSlots = make(chan struct{}, maxConcurrentRuns)

func hostMemoryReady() bool {
	return hostStartHeadroom() >= 0
}

// MemAvailable does not reflect a container that has only just been launched.
func hostStartHeadroom() int64 {
	if runtime.GOOS != "linux" {
		return 1 << 50
	}
	info, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	return harnessHeadroom(info)
}

// Leave a quarter of a small host free, capped at 896 MiB on the pilot machine.
func harnessHeadroom(info []byte) int64 {
	var available, total int64
	for _, line := range bytes.Split(info, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte("MemAvailable:")) && !bytes.HasPrefix(line, []byte("MemTotal:")) {
			continue
		}
		fields := bytes.Fields(line)
		if len(fields) != 3 || string(fields[2]) != "kB" {
			return -1
		}
		value, err := strconv.ParseInt(string(fields[1]), 10, 64)
		if err != nil || value <= 0 {
			return -1
		}
		if string(fields[0]) == "MemTotal:" {
			total = value
		} else {
			available = value
		}
	}
	if total <= 0 || available <= 0 {
		return -1
	}
	return available - min(total/4, 896*1024)
}

// A short startup allowance avoids admitting a burst against the same free
// pages while Podman is still creating the children's cgroups.
type coldStart struct {
	at  time.Time
	kib int64
}
type launchWindow struct {
	sync.Mutex
	recent []coldStart
}

var containerStarts launchWindow

func (w *launchWindow) admit(at time.Time, headroom, cost int64) bool {
	w.Lock()
	defer w.Unlock()
	keep := w.recent[:0]
	for _, x := range w.recent {
		if at.Sub(x.at) < 5*time.Second {
			keep = append(keep, x)
			headroom -= x.kib
		}
	}
	w.recent = keep
	if headroom < cost {
		return false
	}
	w.recent = append(w.recent, coldStart{at, cost})
	return true
}

func acquireHarnessSlot(ctx context.Context, slots chan struct{}, ready func() bool) (func(), error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case slots <- struct{}{}:
		}
		release := func() { <-slots }
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		if ready() {
			return release, nil
		}
		release()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
