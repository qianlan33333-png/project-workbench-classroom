package workbench

import (
	"errors"
	"fmt"
	"github.com/robfig/cron/v3"
	"sort"
	"time"
)

type SeriesChange struct {
	From int64  `json:"from"`
	Cron string `json:"cron"`
}
type Exception struct {
	Original int64 `json:"original"`
	At       int64 `json:"at"`
} // zero means skipped
type TimeEdit struct {
	Original          int64  `json:"original"`
	At                int64  `json:"at"`
	Skip              bool   `json:"skip"`
	Cron              string `json:"cron"`
	Version           int64  `json:"version"`
	ReplaceExceptions bool   `json:"replaceExceptions"`
}
type tick struct{ Original, At int64 }

// Inspect each rule segment separately, then compare moved instances by their actual time.
func scheduleTick(sc Schedule, after int64) (tick, error) {
	var best tick
	consider := func(original, at int64) {
		if at > after && (best.At == 0 || at < best.At) {
			best = tick{original, at}
		}
	}
	exceptions := map[int64]int64{}
	for _, x := range sc.Exceptions {
		exceptions[x.Original] = x.At
		consider(x.Original, x.At)
	}
	if sc.OnceAt != 0 {
		if _, ok := exceptions[sc.OnceAt]; !ok {
			consider(sc.OnceAt, sc.OnceAt)
		}
		return best, nil
	}
	zone := sc.Timezone
	if zone == "" {
		zone = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return best, err
	}
	rules := append([]SeriesChange{{sc.CreatedAt, sc.Cron}}, sc.Changes...)
	for i, seg := range rules {
		if i+1 < len(rules) && rules[i+1].From <= after {
			continue
		}
		spec, e := cron.ParseStandard(seg.Cron)
		if e != nil {
			return best, e
		}
		cursor := after
		if seg.From-1 > cursor {
			cursor = seg.From - 1
		}
		for {
			n := spec.Next(time.UnixMilli(cursor).In(loc))
			if n.IsZero() {
				break
			}
			ts := n.UnixMilli()
			if i+1 < len(rules) && ts >= rules[i+1].From {
				break
			}
			if _, skip := exceptions[ts]; !skip {
				consider(ts, ts)
				break
			}
			cursor = ts
		}
	}
	return best, nil
}
func (sc *Schedule) advance(after int64) error {
	v, e := scheduleTick(*sc, after)
	sc.NextAt = v.At
	sc.OriginalNext = v.Original
	return e
}
func originalExists(sc Schedule, ts int64) bool {
	sc.Exceptions = nil
	v, e := scheduleTick(sc, ts-1)
	return e == nil && v.At == ts
}
func (s *Store) editTime(id, kind string, in TimeEdit) (Schedule, error) {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	sc, e := s.Schedule(id)
	if e != nil {
		return sc, e
	}
	if sc.Version != in.Version {
		return sc, ErrConflict
	}
	var handled int
	if e = s.DB.QueryRow("SELECT count(*) FROM schedule_checks WHERE schedule_id=? AND operation=?", id, fmt.Sprintf("cron:%d", in.Original)).Scan(&handled); e != nil {
		return sc, e
	}
	if handled != 0 {
		return sc, errors.New("此实例已有执行记录")
	}
	current := in.Original
	for _, x := range sc.Exceptions {
		if x.Original == in.Original && x.At != 0 {
			current = x.At
		}
	}
	if current <= now() || !originalExists(sc, in.Original) {
		return sc, errors.New("请选择尚未执行的有效实例")
	}
	if kind == "occurrence" {
		if !in.Skip && in.At <= now() {
			return sc, errors.New("新时间须在未来")
		}
		if in.Skip {
			in.At = 0
		}
		retained := make([]Exception, 0, len(sc.Exceptions)+1)
		for _, x := range sc.Exceptions {
			if x.Original != in.Original {
				retained = append(retained, x)
			}
		}
		sc.Exceptions = retained
		if in.At != 0 && in.At != in.Original {
			v, _ := scheduleTick(sc, in.At-1)
			if v.At == in.At {
				return sc, errors.New("该时间已有本任务的另一次执行")
			}
		}
		if in.At != in.Original {
			sc.Exceptions = append(sc.Exceptions, Exception{in.Original, in.At})
		}
	} else {
		if sc.OnceAt != 0 {
			return sc, errors.New("单次任务没有周期")
		}
		if _, e = cron.ParseStandard(in.Cron); e != nil {
			return sc, e
		}
		keep := []SeriesChange{}
		for _, x := range sc.Changes {
			if x.From < in.Original {
				keep = append(keep, x)
			}
		}
		sc.Changes = append(keep, SeriesChange{in.Original, in.Cron})
		var conflicts []string
		valid := []Exception{}
		for _, x := range sc.Exceptions {
			if x.Original >= in.Original && !originalExists(sc, x.Original) {
				conflicts = append(conflicts, time.UnixMilli(x.Original).UTC().Format(time.RFC3339))
			} else {
				valid = append(valid, x)
			}
		}
		if len(conflicts) > 0 && !in.ReplaceExceptions {
			return sc, fmt.Errorf("新周期影响这些单次例外，请核对后再保存：%v", conflicts)
		}
		sc.Exceptions = valid
	}
	sort.Slice(sc.Exceptions, func(i, j int) bool { return sc.Exceptions[i].Original < sc.Exceptions[j].Original })
	sc.Version++
	if e = sc.advance(now()); e != nil {
		return sc, e
	}
	if sc.NextAt == 0 {
		sc.Enabled = false
	}
	_, e = s.DB.Exec("UPDATE schedules SET next_at=?,enabled=?,data=? WHERE id=?", sc.NextAt, sc.Enabled, encode(sc), sc.ID)
	return sc, e
}
