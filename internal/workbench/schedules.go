package workbench

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

func nextSchedule(s Schedule, after time.Time) (int64, error) {
	if s.Timezone == "" {
		s.Timezone = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return 0, errors.New("时区无效")
	}
	spec, err := cron.ParseStandard(s.Cron)
	if err != nil {
		return 0, errors.New("时间规则无效，请使用五段 cron 表达式")
	}
	return spec.Next(after.In(loc)).UnixMilli(), nil
}
func (s *Store) Schedules(project string) ([]Schedule, error) {
	return listJSON[Schedule](s.DB, "SELECT data FROM schedules WHERE (?='' OR project_id=?) ORDER BY next_at", project, project)
}
func (s *Store) SaveSchedule(v Schedule) (Schedule, error) {
	p, err := s.Project(v.ProjectID)
	if err != nil {
		return v, err
	}
	if p.Archived {
		return v, errors.New("项目已归档")
	}
	if strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.Instruction) == "" {
		return v, errors.New("请填写任务名称和指令")
	}
	if v.ExpertID != "" && !contains(p.ExpertIDs, v.ExpertID) {
		return v, errors.New("专家尚未加入项目")
	}
	if v.Timezone == "" {
		v.Timezone = "Asia/Shanghai"
	}
	if v.NotifyConnectionID != "" {
		c, err := s.Connection(v.NotifyConnectionID)
		if err != nil || !c.Enabled {
			return v, errors.New("通知连接不可用")
		}
		if v.NotifyConversationID == "" {
			return v, errors.New("请先选择该本人连接的聊天目标")
		}
		var count int
		if err = s.DB.QueryRow("SELECT COUNT(*) FROM inbox WHERE connection_id=? AND json_extract(data,'$.incoming.conversationId')=? AND json_extract(data,'$.incoming.actorId')=?", c.ID, v.NotifyConversationID, c.OwnerID).Scan(&count); err != nil {
			return v, err
		}
		if count == 0 {
			return v, errors.New("通知只能发送到已验证的本人私聊")
		}
	}
	if v.ID == "" {
		v.ID = newID()
		v.CreatedAt = now()
	} else {
		old, err := getJSON[Schedule](s.DB, "SELECT data FROM schedules WHERE id=?", v.ID)
		if err != nil {
			return v, err
		}
		if old.ProjectID != v.ProjectID {
			return v, errors.New("定时任务不能更换所属项目")
		}
		v.CreatedAt = old.CreatedAt
	}
	v.NextAt, err = nextSchedule(v, time.Now())
	if err != nil {
		return v, err
	}
	_, err = s.DB.Exec("INSERT INTO schedules VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,next_at=excluded.next_at,data=excluded.data", v.ID, v.ProjectID, v.Enabled, v.NextAt, encode(v))
	return v, err
}
func (s *Store) FireSchedules(at time.Time) error {
	due, err := listJSON[Schedule](s.DB, "SELECT data FROM schedules WHERE enabled=1 AND next_at<=? ORDER BY next_at", at.UnixMilli())
	if err != nil {
		return err
	}
	for _, schedule := range due {
		p, e := s.Project(schedule.ProjectID)
		if e != nil {
			return e
		}
		if p.Archived {
			schedule.Enabled = false
			_, e = s.DB.Exec("UPDATE schedules SET enabled=0,data=? WHERE id=?", encode(schedule), schedule.ID)
			if e != nil {
				return e
			}
			continue
		}
		planned := schedule.NextAt
		next, e := nextSchedule(schedule, at)
		if e != nil {
			return e
		}
		// Session, inbox, run and occurrence are committed together. Missed ticks coalesce here.
		e = s.Transaction(context.Background(), func(tx *sql.Tx) error {
			var count int
			if e := tx.QueryRow("SELECT COUNT(*) FROM schedule_occurrences WHERE schedule_id=? AND scheduled_at=?", schedule.ID, planned).Scan(&count); e != nil {
				return e
			}
			if count > 0 {
				return nil
			}
			ss := Session{ID: newID(), ProjectID: p.ID, Title: schedule.Name + " · " + at.In(time.FixedZone("CST", 8*3600)).Format("01-02 15:04"), Kind: "automation", ExpertID: schedule.ExpertID, CreatedAt: now(), UpdatedAt: now()}
			ss.HarnessID = "pw-" + ss.ID
			m := Message{ID: newID(), Incoming: Incoming{Source: "automation", EventID: schedule.ID + ":" + time.UnixMilli(planned).UTC().Format(time.RFC3339Nano), SessionID: ss.ID, Text: schedule.Instruction, ExpertID: schedule.ExpertID}, State: "queued", SessionID: ss.ID, CreatedAt: now()}
			r := Run{ID: newID(), ProjectID: p.ID, SessionID: ss.ID, MessageID: m.ID, State: "queued", Source: "automation", Text: schedule.Instruction, ExpertID: schedule.ExpertID, ScheduleID: schedule.ID, CreatedAt: now()}
			m.RunID = r.ID
			if r.ExpertID != "" {
				var raw string
				if e := tx.QueryRow("SELECT data FROM experts WHERE id=?", r.ExpertID).Scan(&raw); e != nil {
					return e
				}
				ex, e := decodeJSON[Expert](raw)
				if e != nil {
					return e
				}
				r.ExpertDigest = ex.Digest
			}
			if _, e := tx.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?)", ss.ID, p.ID, ss.HarnessID, ss.Kind, ss.UpdatedAt, encode(ss)); e != nil {
				return e
			}
			if _, e := tx.Exec("INSERT INTO inbox VALUES(?,?,?,?,?,?,?,?,?)", m.ID, "automation", "", m.Incoming.EventID, entryKey(m.Incoming), m.State, ss.ID, m.CreatedAt, encode(m)); e != nil {
				return e
			}
			if _, e := tx.Exec("INSERT INTO runs VALUES(?,?,?,?,?,?,?,?)", r.ID, p.ID, ss.ID, m.ID, r.State, 10, r.CreatedAt, encode(r)); e != nil {
				return e
			}
			if _, e := tx.Exec("INSERT INTO schedule_occurrences VALUES(?,?,?)", schedule.ID, planned, m.ID); e != nil {
				return e
			}
			schedule.NextAt = next
			_, e := tx.Exec("UPDATE schedules SET next_at=?,data=? WHERE id=?", next, encode(schedule), schedule.ID)
			return e
		})
		if e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) Schedule(id string) (Schedule, error) {
	return getJSON[Schedule](s.DB, "SELECT data FROM schedules WHERE id=?", id)
}
