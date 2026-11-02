package workbench

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"reflect"
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
	return s.saveSchedule(v, "")
}
func (s *Store) saveSchedule(v Schedule, creationID string) (Schedule, error) {
	s.expertMu.RLock()
	defer s.expertMu.RUnlock()
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	return s.saveScheduleLocked(v, creationID, false)
}
func (s *Store) saveScheduleLocked(v Schedule, creationID string, explicitEnabled bool) (Schedule, error) {
	if v.Condition != "" && v.Condition != "changed" {
		return v, errors.New("无法识别执行条件")
	}
	if v.Output != "" && v.Output != "project_context" {
		return v, errors.New("无法识别结果位置")
	}
	for _, line := range strings.Split(v.SourceScope, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && (strings.HasPrefix(line, "/") || strings.Contains(line, "\\") || path.Clean(line) == ".." || strings.HasPrefix(path.Clean(line), "../")) {
			return v, errors.New("资料范围应填写项目内的相对路径")
		}
	}
	v.PausedReason = ""
	v.ExpertRequired = false
	if v.ProjectID == "" || v.ProjectID == personalProjectID {
		if _, e := s.PersonalSpace(); e != nil {
			return v, e
		}
		v.ProjectID = personalProjectID
	}
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
	if v.SessionID != "" {
		ss, e := s.Session(v.SessionID)
		if e != nil || ss.ProjectID != p.ID || ss.Lifecycle != "" || ss.Draft {
			return v, errors.New("请选择所属范围内的有效对话")
		}
	}
	if _, e := s.selectCapabilities(p.ID, v.Capabilities); e != nil {
		return v, e
	}
	if v.ID == "" {
		v.Version = 1
		v.ConversationOutput = true
		v.Changes = nil
		v.Exceptions = nil
		v.ID = newID()
		if creationID != "" {
			v.ID = creationID
		}
		v.CreatedAt = now()
	} else {
		old, err := getJSON[Schedule](s.DB, "SELECT data FROM schedules WHERE id=?", v.ID)
		if err != nil {
			return v, err
		}
		if old.ProjectID != v.ProjectID {
			return v, errors.New("定时任务不能更换所属项目")
		}
		if v.Version != old.Version {
			return v, ErrConflict
		}
		if (len(old.Changes) > 0 || len(old.Exceptions) > 0) && (v.Cron != old.Cron || v.OnceAt != old.OnceAt || v.Timezone != old.Timezone) {
			return v, errors.New("请在日历中选择实例，更改单次或周期")
		}
		v.Changes = old.Changes
		v.Exceptions = old.Exceptions
		v.ConversationOutput = old.ConversationOutput
		v.Version = old.Version + 1
		v.CreatedAt = old.CreatedAt
		if !explicitEnabled && (old.Condition == "changed" || old.Output != "") {
			v.Enabled = old.Enabled
		}
		if old.PausedReason != "" && !v.Enabled {
			v.PausedReason = old.PausedReason
		}
		v.ExpertRequired = old.ExpertRequired && v.ExpertID == ""
		if old.ExpertRequired && !v.ExpertRequired {
			v.PausedReason = ""
		}
		if v.Enabled && v.ExpertRequired {
			return v, errors.New("请先重新选择专家")
		}
	}
	err = v.advance(now())
	if err == nil && v.Enabled && v.NextAt == 0 {
		return v, errors.New("没有未来的执行时间")
	}
	if err != nil {
		return v, err
	}
	_, err = s.DB.Exec("INSERT INTO schedules VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,next_at=excluded.next_at,data=excluded.data", v.ID, v.ProjectID, v.Enabled, v.NextAt, encode(v))
	return v, err
}
func (s *Store) FireSchedules(at time.Time) error {
	return s.fireDueSchedules(context.Background(), at)
}
func (s *Store) fireDueSchedules(ctx context.Context, at time.Time) error {
	due, err := listJSON[Schedule](s.DB, "SELECT data FROM schedules WHERE enabled=1 AND next_at<=? ORDER BY next_at", at.UnixMilli())
	if err != nil {
		return err
	}
	var failures error
	for _, sc := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err = s.fireSchedule(ctx, sc.ID, at, ""); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrConflict) {
			failures = errors.Join(failures, err)
		}
	}
	return failures
}

type ScheduleReceipt struct {
	Operation string `json:"operation"`
	At        int64  `json:"at"`
	Outcome   string `json:"outcome"`
	RunID     string `json:"runId,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

func (s *Store) RunSchedule(id, key string) (ScheduleReceipt, error) {
	if len(key) < 8 || len(key) > 100 {
		return ScheduleReceipt{}, errors.New("需要有效的操作编号")
	}
	return s.fireSchedule(context.Background(), id, time.Now(), "manual:"+key)
}

func (s *Store) fireSchedule(ctx context.Context, id string, at time.Time, operation string) (receipt ScheduleReceipt, err error) {
	observed, err := s.Schedule(id)
	if err != nil {
		return receipt, err
	}
	var inputs []ContextSource
	var scanErr error
	if observed.Condition == "changed" || observed.Output != "" {
		// No project/global mutex is held during filesystem I/O.
		if operation != "" || observed.Enabled && observed.NextAt <= at.UnixMilli() {
			inputs, scanErr = s.scheduleSourcesContext(ctx, Project{ID: observed.ProjectID}, observed)
		}
	}
	if ctx.Err() != nil {
		return receipt, ctx.Err()
	}
	s.expertMu.RLock()
	defer s.expertMu.RUnlock()
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	schedule, err := s.Schedule(id)
	if err != nil {
		return receipt, err
	}
	if !reflect.DeepEqual(observed, schedule) {
		return receipt, ErrConflict
	}
	manual := operation != ""
	original := schedule.OriginalNext
	if original == 0 {
		original = schedule.NextAt
	}
	plannedAt := schedule.NextAt
	if !manual {
		if !schedule.Enabled || schedule.NextAt > at.UnixMilli() {
			return receipt, nil
		}
		operation = fmt.Sprintf("cron:%d", original)
	}
	receipt.Operation = operation
	receipt.At = at.UnixMilli()
	err = s.DB.QueryRow("SELECT checked_at,outcome,run_id,detail FROM schedule_checks WHERE schedule_id=? AND operation=?", id, operation).Scan(&receipt.At, &receipt.Outcome, &receipt.RunID, &receipt.Detail)
	if err == nil {
		if !manual {
			if err = schedule.advance(at.UnixMilli()); err != nil {
				return receipt, err
			}
			if schedule.NextAt == 0 {
				schedule.Enabled = false
			}
			_, err = s.DB.Exec("UPDATE schedules SET next_at=?,enabled=?,data=? WHERE id=?", schedule.NextAt, schedule.Enabled, encode(schedule), id)
			return receipt, err
		}
		return receipt, nil
	}
	if err != sql.ErrNoRows {
		return receipt, err
	}
	p, err := s.Project(schedule.ProjectID)
	if err != nil {
		return receipt, err
	}
	if p.Archived {
		if !manual {
			schedule.Enabled = false
			_, e := s.DB.Exec("UPDATE schedules SET enabled=0,data=? WHERE id=?", encode(schedule), id)
			return receipt, e
		}
		return receipt, errors.New("项目已归档")
	}
	if schedule.ExpertRequired && manual {
		receipt.Outcome = "failed"
		receipt.Detail = "请编辑任务以指定有效专家"
		if _, err = s.DB.Exec("INSERT INTO schedule_checks VALUES(?,?,?,?,?,?)", id, operation, receipt.At, receipt.Outcome, "", receipt.Detail); err != nil {
			return receipt, err
		}
		return receipt, errors.New(receipt.Detail)
	}
	if !manual {
		err = schedule.advance(at.UnixMilli())
		if schedule.NextAt == 0 {
			schedule.Enabled = false
		}
		if err != nil {
			return receipt, err
		}
	}
	var fingerprint string
	guarded := schedule.Condition == "changed" || schedule.Output != ""
	if guarded || manual {
		err = s.DB.QueryRow("SELECT id FROM runs WHERE json_extract(data,'$.scheduleId')=? AND state IN ('queued','running','waiting','stopping') ORDER BY created_at DESC LIMIT 1", id).Scan(&receipt.RunID)
		if err == nil {
			if strings.HasPrefix(operation, "manual:retry-") {
				return receipt, ErrLocked
			}
			receipt.Outcome = "active"
			receipt.Detail = "已有执行"
		} else if err != sql.ErrNoRows {
			return receipt, err
		}
	}
	if guarded && receipt.Outcome == "" {
		err = scanErr
		if err != nil {
			receipt.Outcome = "failed"
			receipt.Detail = "检查资料失败：" + err.Error()
			schedule.Enabled = false
			schedule.PausedReason = receipt.Detail
		} else {
			fingerprint = scheduleFingerprint(inputs)
			var last string
			err = s.DB.QueryRow("SELECT fingerprint FROM schedule_inputs WHERE schedule_id=?", id).Scan(&last)
			if err != nil && err != sql.ErrNoRows {
				return receipt, err
			}
			if schedule.Condition == "changed" && (last == fingerprint || last == "" && len(inputs) == 0) {
				receipt.Outcome = "skipped"
				receipt.Detail = "资料无变化"
			}
		}
	}
	var target Session
	if schedule.SessionID != "" && receipt.Outcome == "" {
		target, err = s.Session(schedule.SessionID)
		if err != nil || target.ProjectID != p.ID || target.Lifecycle != "" || target.Draft {
			receipt.Outcome = "failed"
			receipt.Detail = "绑定对话已失效，请重新选择"
			schedule.Enabled = false
			schedule.PausedReason = receipt.Detail
		}
	}
	caps, capErr := s.selectCapabilities(p.ID, schedule.Capabilities)
	if capErr != nil && receipt.Outcome == "" {
		receipt.Outcome = "failed"
		receipt.Detail = capErr.Error()
		schedule.Enabled = false
		schedule.PausedReason = receipt.Detail
	}
	versions := map[string]string{}
	if receipt.Outcome == "" {
		for _, id := range p.ExpertIDs {
			ex, e := s.Expert(id)
			if e != nil {
				receipt.Outcome = "failed"
				receipt.Detail = "专家不可用：" + id
				schedule.Enabled = false
				schedule.PausedReason = receipt.Detail
				break
			}
			versions[id] = ex.Digest
		}
	}
	err = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if receipt.Outcome == "skipped" && schedule.Output == "project_context" {
			if e := restoreUnchangedBrief(tx, p.ID, fingerprint); e != nil {
				return e
			}
		}
		if receipt.Outcome == "" {
			receipt.Outcome = "queued"
			ss := Session{ID: newID(), ProjectID: p.ID, Title: schedule.Name + " · " + at.In(time.FixedZone("CST", 8*3600)).Format("01-02 15:04"), Kind: "automation", ExpertID: schedule.ExpertID, CreatedAt: now(), UpdatedAt: now()}
			ss.HarnessID = "pw-" + ss.ID
			if target.ID != "" {
				ss = target
			}
			m := Message{ID: newID(), Incoming: Incoming{Source: "automation", EventID: schedule.ID + ":" + operation, SessionID: ss.ID, Text: schedule.Instruction, ExpertID: schedule.ExpertID}, State: "queued", SessionID: ss.ID, CreatedAt: now()}
			r := Run{ID: newID(), ProjectID: p.ID, SessionID: ss.ID, MessageID: m.ID, State: "queued", Source: "automation", Text: schedule.Instruction, ExpertID: schedule.ExpertID, ScheduleID: schedule.ID, CreatedAt: now()}
			r.ExpertVersions = versions
			r.ConversationOutput = schedule.ConversationOutput
			r.Capabilities = caps
			if !manual {
				r.ScheduledFor = plannedAt
			}
			if schedule.Condition == "changed" || schedule.Output != "" {
				cfg := schedule
				r.ScheduleConfig = &cfg
				r.InputFingerprint = fingerprint
			}
			m.RunID = r.ID
			receipt.RunID = r.ID
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
				r.ExpertName = ex.Name
			}
			if target.ID == "" {
				if _, e := tx.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?)", ss.ID, p.ID, ss.HarnessID, ss.Kind, ss.UpdatedAt, encode(ss)); e != nil {
					return e
				}
			} else {
				var valid int
				if e := tx.QueryRow("SELECT count(*) FROM sessions WHERE id=? AND project_id=? AND coalesce(json_extract(data,'$.recycledAt'),0)=0 AND coalesce(json_extract(data,'$.lifecycle'),'')=''", ss.ID, p.ID).Scan(&valid); e != nil || valid != 1 {
					return ErrConflict
				}
			}
			if _, e := tx.Exec("INSERT INTO inbox VALUES(?,?,?,?,?,?,?,?,?)", m.ID, "automation", "", m.Incoming.EventID, entryKey(m.Incoming), m.State, ss.ID, m.CreatedAt, encode(m)); e != nil {
				return e
			}
			if _, e := tx.Exec("INSERT INTO runs VALUES(?,?,?,?,?,?,?,?)", r.ID, p.ID, ss.ID, m.ID, r.State, 10, r.CreatedAt, encode(r)); e != nil {
				return e
			}

			if !manual {
				if _, e := tx.Exec("INSERT INTO schedule_occurrences VALUES(?,?,?)", id, strings.TrimPrefix(operation, "cron:"), m.ID); e != nil {
					return e
				}
			}
		}
		if _, e := tx.Exec("INSERT INTO schedule_checks VALUES(?,?,?,?,?,?)", id, operation, receipt.At, receipt.Outcome, receipt.RunID, receipt.Detail); e != nil {
			return e
		}
		_, e := tx.Exec("UPDATE schedules SET enabled=?,next_at=?,data=? WHERE id=?", schedule.Enabled, schedule.NextAt, encode(schedule), id)
		return e
	})
	return receipt, err
}

func (s *Store) Schedule(id string) (Schedule, error) {
	return getJSON[Schedule](s.DB, "SELECT data FROM schedules WHERE id=?", id)
}

func (s *Store) toggleSchedule(id string, enabled bool) (Schedule, []string, error) {
	s.expertMu.RLock()
	defer s.expertMu.RUnlock()
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	sc, e := s.Schedule(id)
	if e != nil {
		return sc, nil, e
	}
	if enabled {
		p, e := s.Project(sc.ProjectID)
		if e != nil {
			return sc, nil, e
		}
		if p.Archived {
			return sc, nil, errors.New("项目已归档")
		}
		if sc.ExpertRequired {
			return sc, nil, errors.New("请先选择专家")
		}
		e = sc.advance(now())
		if e == nil && sc.NextAt == 0 {
			return sc, nil, errors.New("没有未来的执行时间")
		}
		if e != nil {
			return sc, nil, e
		}
	}
	sc.Version++
	sc.Enabled = enabled
	if !sc.ExpertRequired {
		sc.PausedReason = ""
	}
	var active []string
	if !enabled {
		active, e = s.activeScheduleRuns(id)
		if e != nil {
			return sc, nil, e
		}
	}
	_, e = s.DB.Exec("UPDATE schedules SET enabled=?,next_at=?,data=? WHERE id=?", enabled, sc.NextAt, encode(sc), id)
	return sc, active, e
}

func (s *Store) activeScheduleRuns(id string) ([]string, error) {
	var active []string
	rows, e := s.DB.Query("SELECT id FROM runs WHERE json_extract(data,'$.scheduleId')=? AND state IN ('queued','running','waiting')", id)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		active = append(active, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	return active, nil
}
func (s *Store) stopScheduleRuns(ids []string) error {
	s.scheduleMu.Lock()
	stop := s.stopScheduleRun
	s.scheduleMu.Unlock()
	for _, id := range ids {
		if stop == nil {
			return errors.New("执行器未连接，请检查运行状态")
		}
		if e := stop(id); e != nil {
			r, err := s.Run(id)
			if err != nil || !contains([]string{"completed", "failed", "cancelled", "interrupted"}, r.State) {
				return e
			}
		}
	}
	return nil
}
