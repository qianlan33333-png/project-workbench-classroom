package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

const followupSchema = `
CREATE TABLE IF NOT EXISTS personal_followups(
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 key TEXT NOT NULL, state TEXT NOT NULL, next_at INTEGER NOT NULL DEFAULT 0,
 schedule_id TEXT NOT NULL DEFAULT '', data TEXT NOT NULL, UNIQUE(session_id,key));
CREATE INDEX IF NOT EXISTS followup_schedule ON personal_followups(schedule_id) WHERE schedule_id<>'';
`

type Followup struct {
	ID                   string `json:"id"`
	SessionID            string `json:"sessionId"`
	Key                  string `json:"key"`
	Title                string `json:"title"`
	Goal                 string `json:"goal"`
	About                string `json:"about"`
	State                string `json:"state"`
	Version              int64  `json:"version"`
	LastOperation        string `json:"lastOperation,omitempty"`
	LastResumeVersion    int64  `json:"lastResumeVersion,omitempty"`
	NotifyConnectionID   string `json:"notifyConnectionId,omitempty"`
	NotifyConversationID string `json:"notifyConversationId,omitempty"`
	NotifyActorID        string `json:"notifyActorId,omitempty"`
	NotifySource         string `json:"notifySource,omitempty"`
	SourceRun            string `json:"sourceRun"`
	LastRunID            string `json:"lastRunId"`
	DecisionID           string `json:"decisionId,omitempty"`
	ScheduleID           string `json:"scheduleId,omitempty"`
	NextAt               int64  `json:"nextAt,omitempty"`
	ReviewCount          int    `json:"reviewCount"`
	Evidence             string `json:"evidence,omitempty"`
	CompletionRun        string `json:"completionRun,omitempty"`
	Error                string `json:"error,omitempty"`
	CreatedAt            int64  `json:"createdAt"`
	UpdatedAt            int64  `json:"updatedAt"`
}
type FollowupChange struct {
	ID       string            `json:"id"`
	Version  int64             `json:"version"`
	Key      string            `json:"key"`
	Title    string            `json:"title"`
	Goal     string            `json:"goal"`
	About    string            `json:"about"`
	Action   string            `json:"action"`
	At       int64             `json:"at"`
	Evidence string            `json:"evidence"`
	Decision *PersonalDecision `json:"decision,omitempty"`
}

func followupClosed(f Followup) bool { return f.State == "completed" || f.State == "cancelled" }
func (s *Store) Followup(id string) (Followup, error) {
	return getJSON[Followup](s.DB, `SELECT f.data FROM personal_followups f JOIN sessions ss ON ss.id=f.session_id WHERE f.id=? AND coalesce(ss.data->>'$.recycledAt',0)=0`, id)
}
func (s *Store) Followups() ([]Followup, error) {
	fs, e := listJSON[Followup](s.DB, `SELECT f.data FROM personal_followups f JOIN sessions ss ON ss.id=f.session_id WHERE coalesce(ss.data->>'$.recycledAt',0)=0 ORDER BY f.state IN ('completed','cancelled'),f.rowid DESC LIMIT 200`)
	for i := range fs {
		if fs[i].State == "queued" || fs[i].State == "pending" || fs[i].CompletionRun != "" {
			if r, er := s.Run(fs[i].LastRunID); er == nil && contains([]string{"running", "waiting", "stopping"}, r.State) {
				fs[i].State = "running"
			}
		}
	}
	return fs, e
}
func followupTx(tx *sql.Tx, id string) (Followup, error) {
	var raw string
	e := tx.QueryRow("SELECT data FROM personal_followups WHERE id=?", id).Scan(&raw)
	if e != nil {
		return Followup{}, e
	}
	return decodeJSON[Followup](raw)
}
func putFollowup(tx *sql.Tx, f *Followup) error {
	f.UpdatedAt = now()
	_, e := tx.Exec(`INSERT INTO personal_followups VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,next_at=excluded.next_at,schedule_id=excluded.schedule_id,data=excluded.data`, f.ID, f.SessionID, f.Key, f.State, f.NextAt, f.ScheduleID, encode(f))
	if e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO events(session_id,run_id,type,data,created_at) VALUES(?,?,'followup.updated',?,?)", f.SessionID, f.LastRunID, encode(f), now())
	return e
}
func retireFollowupWait(tx *sql.Tx, f Followup) error {
	if f.DecisionID != "" {
		if _, e := tx.Exec(`UPDATE personal_decisions SET data=json_set(data,'$.state','stale') WHERE id=? AND (data->>'$.state'='pending' OR (data->>'$.state'='answered' AND coalesce(data->>'$.dispatched',0)=0))`, f.DecisionID); e != nil {
			return e
		}
	}
	if f.ScheduleID != "" {
		_, e := tx.Exec(`UPDATE schedules SET enabled=0,data=json_set(data,'$.enabled',json('false')) WHERE id=?`, f.ScheduleID)
		return e
	}
	return nil
}
func normalizePersonalDecision(d PersonalDecision) (PersonalDecision, error) {
	if strings.TrimSpace(d.Question) == "" || len(d.Question) > 4000 || len(d.Reason) > 8000 || len(d.Options) < 2 || len(d.Options) > 3 {
		return d, errors.New("决策需要问题和2–3个选项")
	}
	n := 0
	for i := range d.Options {
		o := &d.Options[i]
		o.ID = string(rune('A' + i))
		if o.Recommended {
			n++
		}
		if strings.TrimSpace(o.Label) == "" || strings.TrimSpace(o.Tradeoff) == "" || len(o.Label) > 4000 || len(o.Tradeoff) > 8000 {
			return d, errors.New("选项需要内容与取舍")
		}
		if o.Task != nil {
			return d, errors.New("今天只能查询项目；请在项目中创建任务")
		}
	}
	if n != 1 || strings.TrimSpace(d.Reason) == "" {
		return d, errors.New("需要一个推荐及理由")
	}
	return d, nil
}

// All transitions are serialized with run admission/cancellation. The schedule
// mutex is acquired first, matching existing schedule -> Store.mu lock order.
func (s *Store) SaveFollowup(in RuntimeInput, q FollowupChange) (Followup, error) {
	if in.Project.ID != personalProjectID || in.Session.ProjectID != personalProjectID || in.Run.ID == "" || in.Routing {
		return Followup{}, errors.New("仅今天个人助手可管理持续跟进")
	}
	if q.Action == "" {
		q.Action = "save"
	}
	if !contains([]string{"save", "ask", "review", "complete"}, q.Action) {
		return Followup{}, errors.New("无法识别跟进操作")
	}
	if len(q.About) > 16000 || len(q.Evidence) > 16000 {
		return Followup{}, errors.New("跟进内容过长")
	}
	var decision PersonalDecision
	var e error
	if q.Action == "ask" {
		if q.Decision == nil {
			return Followup{}, errors.New("需要决策内容")
		}
		decision, e = normalizePersonalDecision(*q.Decision)
		if e != nil {
			return Followup{}, e
		}
	}
	if q.Action == "complete" && strings.TrimSpace(q.Evidence) == "" {
		return Followup{}, errors.New("请先保存完成条件已经核实的依据")
	}
	contextVersion := s.contextVersion()
	operation := digest([]byte(in.Run.ID + encode(q)))
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	var f Followup
	e = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var raw string
		if er := tx.QueryRow("SELECT data FROM runs WHERE id=? AND project_id=? AND session_id=? AND state IN ('queued','running')", in.Run.ID, personalProjectID, in.Session.ID).Scan(&raw); er != nil {
			return errors.New("本轮已结束或不属于个人会话")
		}
		actual, er := decodeJSON[Run](raw)
		if er != nil {
			return er
		}
		if q.ID == "" {
			if actual.Source == "automation" || actual.FollowupID != "" {
				return errors.New("自动轮不能创建其他跟进事项")
			}
			if strings.TrimSpace(q.Key) == "" || len(q.Key) > 120 || strings.TrimSpace(q.Title) == "" || len([]rune(q.Title)) > 120 || strings.TrimSpace(q.Goal) == "" || len(q.Goal) > 8000 {
				return errors.New("请填写固定标识、简短标题和完成条件")
			}
			er = tx.QueryRow("SELECT data FROM personal_followups WHERE session_id=? AND key=?", in.Session.ID, q.Key).Scan(&raw)
			if er == nil {
				f, er = decodeJSON[Followup](raw)
				return er
			}
			if er != sql.ErrNoRows {
				return er
			}
			f = Followup{ID: newID(), SessionID: in.Session.ID, Key: q.Key, Title: q.Title, Goal: q.Goal, SourceRun: actual.ID, CreatedAt: now()}
			m, er := s.Message(actual.MessageID)
			if er != nil {
				return er
			}
			if isChatChannel(m.Incoming.Source) {
				f.NotifyConnectionID = m.Incoming.ConnectionID
				f.NotifyConversationID = m.Incoming.ConversationID
				f.NotifyActorID = m.Incoming.ActorID
				f.NotifySource = m.Incoming.Source
			}
		} else {
			f, er = followupTx(tx, q.ID)
			if er != nil {
				return er
			}
			if f.SessionID == in.Session.ID && f.LastOperation == operation {
				return nil
			}
			if f.SessionID != in.Session.ID || f.Version != q.Version || followupClosed(f) {
				return ErrConflict
			}
			if actual.FollowupID != "" && (actual.FollowupID != f.ID || f.LastRunID != actual.ID) {
				return errors.New("只能继续本轮所属的跟进事项")
			}
			if actual.Source == "automation" && actual.FollowupID == "" {
				return errors.New("普通自动任务不能修改持续跟进")
			}
			if f.LastRunID != actual.ID {
				var state string
				er = tx.QueryRow("SELECT state FROM runs WHERE id=?", f.LastRunID).Scan(&state)
				if er != nil && er != sql.ErrNoRows {
					return er
				}
				if contains([]string{"queued", "running", "waiting", "stopping"}, state) {
					return ErrLocked
				}
			}
		}
		if q.Action == "review" && (q.At < now()+60000 || q.At > now()+30*24*int64(time.Hour/time.Millisecond)) {
			return errors.New("复查应在1分钟至30天内")
		}
		if q.Action == "review" && f.ReviewCount >= 3 {
			return errors.New("已达到3次自动复查上限，请本人核对后处理")
		}
		if er = retireFollowupWait(tx, f); er != nil {
			return er
		}
		f.Version++
		f.LastOperation = operation
		f.State = "pending"
		f.LastRunID = actual.ID
		f.Error = ""
		f.DecisionID = ""
		f.ScheduleID = ""
		f.NextAt = 0
		f.CompletionRun = ""
		if q.About != "" {
			f.About = q.About
		}
		switch q.Action {
		case "ask":
			decision.ID = newID()
			decision.SessionID = f.SessionID
			decision.SourceRun = actual.ID
			decision.FollowupID = f.ID
			decision.Version = 1
			decision.State = "pending"
			decision.CreatedAt = now()
			decision.ContextVersion = contextVersion
			if _, er = tx.Exec("INSERT INTO personal_decisions VALUES(?,?,?)", decision.ID, "followup:"+f.ID+":"+stringVersion(f.Version), encode(decision)); er != nil {
				return er
			}
			f.State = "waiting_decision"
			f.DecisionID = decision.ID
		case "review":
			sc := Schedule{ID: newID(), FollowupID: f.ID, ProjectID: personalProjectID, SessionID: f.SessionID, Name: "复查：" + f.Title, Instruction: "只读复查当前持续事项；先核实最新资料，只继续剩余步骤。", OnceAt: q.At, NextAt: q.At, Enabled: true, ConversationOutput: true, Timezone: "Asia/Shanghai", Version: 1, CreatedAt: now()}
			if _, er = tx.Exec("INSERT INTO schedules VALUES(?,?,?,?,?)", sc.ID, sc.ProjectID, true, sc.NextAt, encode(sc)); er != nil {
				return er
			}
			f.State = "scheduled"
			f.ScheduleID = sc.ID
			f.NextAt = q.At
		case "complete":
			f.State = "needs_review"
			f.Evidence = q.Evidence
			f.CompletionRun = actual.ID
		}
		return putFollowup(tx, &f)
	})
	if e == nil {
		select {
		case s.notify <- struct{}{}:
		default:
		}
	}
	return f, e
}

func stringVersion(v int64) string { return strconv.FormatInt(v, 10) }

func (s *Store) enqueueFollowupTx(tx *sql.Tx, f *Followup, text string) (Run, error) {
	var valid int
	if e := tx.QueryRow("SELECT count(*) FROM sessions WHERE id=? AND project_id=? AND coalesce(data->>'$.recycledAt',0)=0 AND coalesce(data->>'$.lifecycle','')=''", f.SessionID, personalProjectID).Scan(&valid); e != nil {
		return Run{}, e
	}
	if valid != 1 {
		return Run{}, errors.New("原个人对话已失效")
	}
	r := Run{ID: newID(), FollowupID: f.ID, ProjectID: personalProjectID, SessionID: f.SessionID, State: "queued", Source: "automation", Text: text, CreatedAt: now(), ConversationOutput: true}
	m := Message{ID: newID(), State: "queued", SessionID: f.SessionID, CreatedAt: now(), RunID: r.ID, Incoming: Incoming{Source: "automation", SessionID: f.SessionID, EventID: "followup:" + f.ID + ":" + stringVersion(f.Version), Text: text}}
	r.MessageID = m.ID
	r.EventID = m.Incoming.EventID
	if _, e := tx.Exec("INSERT INTO inbox VALUES(?,?,?,?,?,?,?,?,?)", m.ID, "automation", "", m.Incoming.EventID, entryKey(m.Incoming), m.State, m.SessionID, m.CreatedAt, encode(m)); e != nil {
		return r, e
	}
	if _, e := tx.Exec("INSERT INTO runs VALUES(?,?,?,?,?,?,?,?)", r.ID, r.ProjectID, r.SessionID, m.ID, r.State, 10, r.CreatedAt, encode(r)); e != nil {
		return r, e
	}
	f.State = "queued"
	f.LastRunID = r.ID
	f.NextAt = 0
	f.CompletionRun = ""
	f.Error = ""
	return r, putFollowup(tx, f)
}

func (s *Store) ResumeFollowup(id string, version int64) (Followup, error) {
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	var f Followup
	e := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var e error
		f, e = followupTx(tx, id)
		if e != nil {
			return e
		}
		if f.Version != version || followupClosed(f) || !contains([]string{"pending", "needs_review", "scheduled"}, f.State) {
			if version > 0 && f.LastResumeVersion == version {
				return nil
			}
			return ErrConflict
		}
		var active int
		if e = tx.QueryRow("SELECT count(*) FROM runs WHERE id=? AND state IN ('queued','running','waiting','stopping')", f.LastRunID).Scan(&active); e != nil {
			return e
		}
		if active != 0 {
			return ErrLocked
		}
		if e = retireFollowupWait(tx, f); e != nil {
			return e
		}
		f.Version++
		f.LastResumeVersion = version
		_, e = s.enqueueFollowupTx(tx, &f, "本人明确要求继续核对这项持续跟进；只读核实当前事实，不重做已有业务。")
		return e
	})
	return f, e
}

func (s *Store) CancelFollowup(id string, version int64) (Followup, error) {
	s.scheduleMu.Lock()
	s.mu.Lock()
	var f Followup
	var runs []string
	e := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var e error
		f, e = followupTx(tx, id)
		if e != nil {
			return e
		}
		if f.State == "cancelled" {
			return nil
		}
		if f.Version != version || f.State == "completed" {
			return ErrConflict
		}
		if e = retireFollowupWait(tx, f); e != nil {
			return e
		}
		rows, e := tx.Query("SELECT id FROM runs WHERE data->>'$.followupId'=? AND state IN ('queued','running','waiting','stopping')", f.ID)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			runs = append(runs, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		// Queued work is cancelled in the same transaction as revocation.
		if _, e = tx.Exec(`UPDATE runs SET state='cancelled',data=json_set(data,'$.state','cancelled','$.finishedAt',?) WHERE data->>'$.followupId'=? AND state='queued'`, now(), f.ID); e != nil {
			return e
		}
		if _, e = tx.Exec(`UPDATE deliveries SET state='cancelled',data=json_set(data,'$.state','cancelled','$.error','持续跟进已取消，未发送') WHERE state='pending' AND data->>'$.kind'='notification' AND data->>'$.runId' IN (SELECT id FROM runs WHERE data->>'$.followupId'=?)`, f.ID); e != nil {
			return e
		}
		f.Version++
		f.State = "cancelled"
		f.NextAt = 0
		f.CompletionRun = ""
		return putFollowup(tx, &f)
	})
	s.mu.Unlock()
	s.scheduleMu.Unlock()
	if e == nil && len(runs) > 0 {
		var active []string
		for _, id := range runs {
			r, er := s.Run(id)
			if er == nil && contains([]string{"running", "waiting", "stopping"}, r.State) {
				active = append(active, id)
			}
		}
		e = s.stopScheduleRuns(active)
	}
	return f, e
}

func (s *Store) dispatchFollowupDecision(d PersonalDecision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		f, e := followupTx(tx, d.FollowupID)
		if e != nil {
			return e
		}
		var raw string
		if e = tx.QueryRow("SELECT data FROM personal_decisions WHERE id=?", d.ID).Scan(&raw); e != nil {
			return e
		}
		d, e = decodeJSON[PersonalDecision](raw)
		if e != nil {
			return e
		}
		if d.Dispatched || d.State != "answered" {
			return nil
		}
		if f.State != "waiting_decision" || f.DecisionID != d.ID {
			return ErrConflict
		}
		f.Version++
		if _, e = s.enqueueFollowupTx(tx, &f, "本人已回答原事项的问题。回答仅适用本次范围，先重新核实事实，再继续剩余步骤。\n回答资料："+encode(d)); e != nil {
			return e
		}
		d.Dispatched = true
		_, e = tx.Exec("UPDATE personal_decisions SET data=? WHERE id=?", encode(d), d.ID)
		return e
	})
}

func (s *Store) saveDecisionOutcome(d PersonalDecision) error {
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if _, e := tx.Exec("UPDATE personal_decisions SET data=? WHERE id=?", encode(d), d.ID); e != nil {
			return e
		}
		if d.FollowupID != "" && (d.State == "deferred" || d.State == "stale") {
			f, e := followupTx(tx, d.FollowupID)
			if e != nil {
				return e
			}
			if f.State == "waiting_decision" && f.DecisionID == d.ID {
				f.State = "needs_review"
				f.Version++
				f.Error = "本次决定已暂缓或依据变化，请核对后继续"
				return putFollowup(tx, &f)
			}
		}
		return nil
	})
}

// Called inside the existing saveRun transaction, after process cleanup. A
// claimed completion is only accepted when that same run actually succeeded.
func finishFollowupsTx(tx *sql.Tx, r Run) error {
	if !contains([]string{"completed", "failed", "cancelled", "interrupted"}, r.State) {
		return nil
	}
	rows, e := tx.Query("SELECT data FROM personal_followups WHERE data->>'$.lastRunId'=? AND state NOT IN ('completed','cancelled')", r.ID)
	if e != nil {
		return e
	}
	var fs []Followup
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		f, er := decodeJSON[Followup](raw)
		if er != nil {
			rows.Close()
			return er
		}
		fs = append(fs, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, f := range fs {
		if r.State != "completed" {
			if e = retireFollowupWait(tx, f); e != nil {
				return e
			}
			f.State = "needs_review"
			f.NextAt = 0
			f.Error = "本轮未成功完成，请核实已有结果后继续"
			f.CompletionRun = ""
		} else if f.CompletionRun == r.ID {
			f.State = "completed"
			f.CompletionRun = ""
		} else if f.State == "queued" || f.State == "pending" {
			f.State = "needs_review"
			f.Error = "本轮已结束，尚未记录完成依据或下一步"
		}
		f.Version++
		if e = putFollowup(tx, &f); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) followupRunLive(r Run) bool {
	if r.FollowupID == "" {
		return true
	}
	f, e := s.Followup(r.FollowupID)
	if e != nil || followupClosed(f) || f.LastRunID != r.ID {
		return false
	}
	actual, e := s.Run(r.ID)
	return e == nil && contains([]string{"queued", "running"}, actual.State)
}
func allowedFollowupTool(tool string) bool {
	return contains([]string{"project_contexts", "personal_memories", "find_project_sources", "read_project_source", "query_calendar", "list_followups", "manage_followup"}, tool)
}
func (s *Store) followupTool(in RuntimeInput, tool string, args json.RawMessage) (any, error) {
	if tool == "list_followups" {
		return s.Followups()
	}
	var q FollowupChange
	if e := json.Unmarshal(args, &q); e != nil {
		return nil, e
	}
	return s.SaveFollowup(in, q)
}
