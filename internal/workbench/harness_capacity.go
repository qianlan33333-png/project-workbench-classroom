package workbench

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

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

// Claim and Execute use the same reservation. Preparing a large history must
// not make its unstarted container disappear from the memory budget.
type harnessPool struct {
	sync.Mutex
	slots map[*harnessSlot]struct{}
}
type harnessSlot struct {
	pool                 *harnessPool
	kib                  int64
	started              time.Time
	finished, quarantine bool
}

var hostPool = harnessPool{slots: make(map[*harnessSlot]struct{})}
var errHarnessAdmission = errors.New("执行容量暂满，请保留排队")

const lightStartKiB = 192 * 1024
const heavyStartKiB = 384 * 1024

func hostSlotCost(in RuntimeInput) int64 {
	if !in.Routing && in.Run.Gateway == nil && in.Project.ID != personalProjectID {
		return heavyStartKiB
	}
	return lightStartKiB
}
func tryHostSlot(cost int64) *harnessSlot { return hostPool.try(time.Now(), hostStartHeadroom(), cost) }
func (p *harnessPool) availableStartKiB() int64 {
	p.Lock()
	defer p.Unlock()
	return p.remainingLocked(time.Now(), hostStartHeadroom())
}
func (p *harnessPool) remainingLocked(now time.Time, headroom int64) int64 {
	if len(p.slots) >= maxConcurrentRuns {
		return -1
	}
	for s := range p.slots {
		if s.started.IsZero() || now.Sub(s.started) < 5*time.Second {
			headroom -= s.kib
		}
	}
	return headroom
}
func (p *harnessPool) try(now time.Time, headroom, cost int64) *harnessSlot {
	p.Lock()
	defer p.Unlock()
	if p.remainingLocked(now, headroom) < cost {
		return nil
	}
	s := &harnessSlot{pool: p, kib: cost}
	p.slots[s] = struct{}{}
	return s
}
func (s *harnessSlot) markStarted() {
	if s == nil {
		return
	}
	s.pool.Lock()
	defer s.pool.Unlock()
	s.started = time.Now()
}
func (s *harnessSlot) finish() {
	if s == nil {
		return
	}
	s.pool.Lock()
	defer s.pool.Unlock()
	s.finished = true
	if !s.quarantine {
		delete(s.pool.slots, s)
	}
}
func (s *harnessSlot) setQuarantine(active bool) {
	s.pool.Lock()
	defer s.pool.Unlock()
	s.quarantine = active
	if !active && s.finished {
		delete(s.pool.slots, s)
	}
}
func waitHostSlot(ctx context.Context, in RuntimeInput) (*harnessSlot, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if s := tryHostSlot(hostSlotCost(in)); s != nil {
			return s, nil
		}
		if in.Routing && in.Purpose == "" {
			return nil, errHarnessAdmission
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// Direct routes and archive children have no worker cleanup retry of their own.
// A failed removal retains its slot until the same container is confirmed gone.
func (h *Harness) cleanupProcessSlot(ctx context.Context, run string, s *harnessSlot) error {
	err := h.Cleanup(ctx, run)
	if err != nil && s != nil {
		s.setQuarantine(true)
		go h.retrySlotCleanup(s, run)
	}
	return err
}
func (h *Harness) retrySlotCleanup(s *harnessSlot, run string) {
	for delay := time.Second; ; delay = min(delay*2, time.Minute) {
		time.Sleep(delay)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := h.Cleanup(ctx, run)
		cancel()
		if err == nil {
			s.setQuarantine(false)
			return
		}
	}
}
