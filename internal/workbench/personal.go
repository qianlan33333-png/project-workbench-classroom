package workbench

import (
	"context"
	"database/sql"

	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"strconv"
	"strings"
	"time"
)

func (s *Store) PersonalSpace() (Project, error) {
	p, e := s.ensureProject(personalProjectID, "个人助手", "", nil)
	if e == nil && p.Archived {
		p.Archived = false
		_, e = s.DB.Exec("UPDATE projects SET archived=0,data=? WHERE id=?", encode(p), p.ID)
	}
	return p, e
}
func (s *Store) Memories() ([]PersonalMemory, error) {
	ms, e := listJSON[PersonalMemory](s.DB, "SELECT data FROM personal_memories WHERE coalesce(json_extract(data,'$.forgotten'),0)=0 ORDER BY json_extract(data,'$.updatedAt')")
	out := []PersonalMemory{}
	for _, m := range ms {
		if s.memoryExcluded("m:"+m.ID) || m.Source != "" && s.memoryExcluded("r:"+m.Source) {
			continue
		}
		if _, e := os.Stat(filepath.Join(s.Root, "forgotten-memories", m.ID)); e == nil {
			continue
		}
		if m.ExpiresAt > 0 && m.ExpiresAt <= now() {
			continue
		}
		if m.Source != "" {
			r, er := s.Run(m.Source)
			if er != nil {
				continue
			}
			if _, er = s.Session(r.SessionID); er != nil {
				continue
			}
			if s.memoryUserText(r) == "" || m.Quote != "" && !strings.Contains(s.memoryUserText(r), m.Quote) {
				continue
			}
		}
		out = append(out, m)
	}
	return out, e
}
func (s *Store) SaveMemory(m PersonalMemory) (PersonalMemory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(m.Text) == "" || len(m.Text) > 16000 {
		return m, errors.New("请填写简洁的个人理解")
	}
	if !contains([]string{"fact", "goal", "temporary", "feedback", "principle"}, m.Kind) {
		return m, errors.New("个人理解类型无效")
	}
	if m.Kind == "temporary" && m.ExpiresAt == 0 {
		m.ExpiresAt = now() + 7*24*60*60*1000
	}
	if m.Kind == "temporary" && (m.ExpiresAt <= now()) {
		return m, errors.New("临时情况需要未来的失效时间")
	}
	var previous *PersonalMemory
	if m.ID != "" {
		old, e := getJSON[PersonalMemory](s.DB, "SELECT data FROM personal_memories WHERE id=?", m.ID)
		if e != nil {
			return m, e
		}
		if old.Version != m.Version || old.Forgotten {
			return m, ErrConflict
		}
		m.Version++
		if old.Protected && m.Source != "" && !m.Correction {
			return m, errors.New("人工纠正只能由本人在界面修改；可记录新的明确纠正，不覆盖旧依据")
		}

		previous = &old
	} else {
		m.ID = newID()
		m.Version = 1
	}
	m.UpdatedAt = now()
	if m.Correction {
		m.Protected = true
	}
	if m.Source == "" {
		m.Confirmed = true
		m.Protected = true
		m.Quote = m.Text
	} else {
		m.Confirmed = true
	}
	// Repeated delivery/tool retries of the same explicit expression are idempotent.
	if m.Source != "" {
		existing, _ := listJSON[PersonalMemory](s.DB, "SELECT data FROM personal_memories WHERE json_extract(data,'$.source')=?", m.Source)
		for _, x := range existing {
			if !x.Forgotten && x.Text == m.Text && x.Quote == m.Quote {
				return x, nil
			}
		}
	}
	refs, e := s.resolveMemoryReplacements(m.Replaces)
	if len(m.Replaces) > 0 {
		if len(m.BaseRefs) == 0 {
			return m, ErrConflict
		}
		for id, version := range m.BaseRefs {
			matched := false
			for _, r := range refs {
				if r.ID == id && r.Version == version {
					matched = true
				}
			}
			if !matched {
				return m, ErrConflict
			}
		}
	}
	if e != nil {
		return m, e
	}
	e = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if previous != nil {
			if previous.Source != "" && previous.Source != m.Source {
				if _, e := tx.Exec("INSERT OR IGNORE INTO personal_memory_exclusions VALUES(?)", "r:"+previous.Source); e != nil {
					return e
				}
			}
			if _, e := tx.Exec("INSERT OR IGNORE INTO personal_memory_revisions VALUES(?,?,?)", previous.ID, previous.Version, encode(previous)); e != nil {
				return e
			}
		}
		for _, ref := range refs {
			if ref.ID == "m:"+m.ID {
				continue
			}
			if _, e := tx.Exec("INSERT OR IGNORE INTO personal_memory_exclusions VALUES(?)", ref.ID); e != nil {
				return e
			}
		}
		_, e := tx.Exec("INSERT INTO personal_memories VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", m.ID, encode(m))
		return e
	})
	return m, e
}
func (s *Store) ForgetMemory(id string, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.CheckUnlocked(personalProjectID); e != nil {
		return e
	}
	m, e := getJSON[PersonalMemory](s.DB, "SELECT data FROM personal_memories WHERE id=?", id)
	if e != nil {
		return e
	}
	if m.Version != version {
		return ErrConflict
	}
	return s.forgetMemoryReferences(m.ID, []MemoryReference{memoryRecordReference(m)})
}
func (s *Store) resetPersonalContext() error {
	p, e := s.PersonalSpace()
	if e != nil {
		return e
	}
	if e = os.RemoveAll(filepath.Join(s.Root, "runtime", "projects", personalProjectID, "sessions")); e != nil {
		return e
	}
	if _, e = s.DB.Exec("DELETE FROM runtime_sessions WHERE session_id=?", p.DefaultSessionID); e != nil {
		return e
	}
	if _, e = s.DB.Exec("DELETE FROM harness_events WHERE session_id=?", p.DefaultSessionID); e != nil {
		return e
	}
	_, e = s.DB.Exec("DELETE FROM daily_contents")
	if e != nil {
		return e
	}
	ds, _ := s.Decisions()
	for _, d := range ds {
		if d.State == "pending" {
			d.State = "stale"
			_, _ = s.DB.Exec("UPDATE personal_decisions SET data=? WHERE id=?", encode(d), d.ID)
		}
	}
	_ = s.SetSetting("personal_reset_notice", time.Now().Format(time.RFC3339))
	return s.Emit(p.DefaultSessionID, "", "personal.context_reset", map[string]string{"reason": "已清理原生会话上下文；后续仅使用有效个人理解和新输入，不从旧聊天恢复已遗忘资料"})
}
func (s *Store) Decisions() ([]PersonalDecision, error) {
	return listJSON[PersonalDecision](s.DB, "SELECT data FROM personal_decisions ORDER BY json_extract(data,'$.createdAt') DESC LIMIT 100")
}
func (s *Store) ProposeDecision(in RuntimeInput, d PersonalDecision) (PersonalDecision, error) {
	if d.Question == "" || len(d.Options) < 2 || len(d.Options) > 3 {
		return d, errors.New("决策需要问题和2–3个选项")
	}
	if old, e := getJSON[PersonalDecision](s.DB, "SELECT data FROM personal_decisions WHERE operation=?", in.Run.ID); e == nil {
		if old.ContextVersion != s.contextVersion() {
			return old, ErrConflict
		}
		return old, nil
	}
	n := 0
	for i := range d.Options {
		o := &d.Options[i]
		o.ID = string(rune('A' + i))
		if o.Recommended {
			n++
		}
		if o.Label == "" || o.Tradeoff == "" {
			return d, errors.New("选项需要内容与取舍")
		}
		if o.Task != nil {
			return d, errors.New("今天只能查询项目；请在项目中创建任务")
		}
	}
	if n != 1 || d.Reason == "" {
		return d, errors.New("需要一个推荐及理由")
	}
	d.ID = newID()
	d.SessionID = in.Session.ID
	d.SourceRun = in.Run.ID
	d.Version = 1
	d.State = "pending"
	d.CreatedAt = now()
	d.ContextVersion = s.contextVersion()
	_, e := s.DB.Exec("INSERT INTO personal_decisions VALUES(?,?,?)", d.ID, in.Run.ID, encode(d))
	if e == nil {
		e = s.Emit(in.Session.ID, in.Run.ID, "personal.decision", d)
	}
	return d, e
}
func (s *Store) AnswerDecision(id string, version int64, answer, other string) (PersonalDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, e := getJSON[PersonalDecision](s.DB, "SELECT data FROM personal_decisions WHERE id=?", id)
	if e != nil {
		return d, e
	}
	if d.State == "answered" || d.State == "deferred" {
		return d, nil
	}
	if d.Version != version || d.State != "pending" {
		return d, ErrConflict
	}
	if d.ContextVersion != s.contextVersion() {
		d.State = "stale"
		_, _ = s.DB.Exec("UPDATE personal_decisions SET data=? WHERE id=?", encode(d), id)
		return d, errors.New("依据已经变化，请更新建议后再选择")
	}
	var selected *DecisionOption
	for i := range d.Options {
		if d.Options[i].ID == answer {
			selected = &d.Options[i]
		}
	}
	if selected == nil && answer != "other" && answer != "defer" {
		return d, errors.New("请选择有效选项")
	}
	if answer == "other" && strings.TrimSpace(other) == "" {
		return d, errors.New("请补充其他想法")
	}
	d.Answer = answer
	d.Other = other
	d.State = "answered"
	if answer == "defer" {
		d.State = "deferred"
	}
	d.Version++
	if selected != nil && selected.Task != nil {
		return d, errors.New("此旧选项包含项目写操作，请进入项目处理")
	}
	_, e = s.DB.Exec("UPDATE personal_decisions SET data=? WHERE id=?", encode(d), id)
	if e == nil {
		_ = s.Emit(d.SessionID, "", "personal.decision_answered", d)
	}
	return d, e
}
func (s *Store) dispatchDecisions() {
	ds, _ := s.Decisions()
	for _, d := range ds {
		if d.State != "answered" || d.Dispatched {
			continue
		}
		text := "[已提交的个人决策，仅本次决定，禁止据此推断长期偏好]\n" + encode(d)
		if _, e := s.Submit(Incoming{Source: "web", EventID: "decision-answer:" + d.ID, SessionID: d.SessionID, Text: text}); e == nil {
			_, _ = s.DB.Exec("UPDATE personal_decisions SET data=json_set(data,'$.dispatched',json('true')) WHERE id=?", d.ID)
		}
	}
}
func (s *Store) personalTool(in RuntimeInput, tool string, args json.RawMessage) (any, error) {
	switch tool {
	case "project_contexts":
		return s.ContextCatalog()
	case "personal_memories":
		var q MemoryEvidenceQuery
		if e := json.Unmarshal(args, &q); e != nil {
			return nil, e
		}
		return s.MemoryEvidence(q)
	case "propose_decision":
		var d PersonalDecision
		if e := json.Unmarshal(args, &d); e != nil {
			return nil, e
		}
		return s.ProposeDecision(in, d)
	case "find_project_sources", "read_project_source":
		var q sourceQuery
		if e := json.Unmarshal(args, &q); e != nil {
			return nil, e
		}
		return s.personalSource(q, tool == "read_project_source")
	case "read_message_attachment":
		var a struct {
			Index  int
			Offset int
		}
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, e
		}
		if a.Index < 0 || a.Index >= len(in.Run.Attachments) || a.Offset < 0 {
			return nil, ErrNotFound
		}
		path := in.Run.Attachments[a.Index]
		txt, e := s.readPersonalText(personalProjectID, path)
		if e != nil {
			return nil, e
		}
		excerpt := sourceExcerpt(txt, a.Offset)
		excerpt["source"] = path
		return excerpt, nil
	case "remember_explicit":
		var a struct {
			Memory PersonalMemory `json:"memory"`
			Quote  string         `json:"quote"`
			Intent string         `json:"intent"`
		}
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, e
		}
		userText := s.memoryUserText(in.Run)
		if len([]rune(a.Quote)) < 3 || !strings.Contains(userText, a.Quote) {
			return nil, errors.New("只能从本轮本人明确表达中学习；选项选择不能变为长期偏好")
		}
		a.Memory.ID = strings.TrimPrefix(a.Memory.ID, "m:")
		a.Memory.Correction = a.Intent == "correction"
		a.Memory.Source = in.Run.ID
		a.Memory.Quote = a.Quote
		a.Memory.Replaces = nil
		return s.SaveMemory(a.Memory)
	}
	return nil, errors.New("今天只能查询项目资料；写操作请进入对应项目")

}
func (w *Worker) personalPrompt(r Run) string {

	catalog, _ := w.Store.ContextCatalog()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	channelName := func(source string) string {
		switch source {
		case "wechat":
			return "微信"
		case "feishu":
			return "飞书"
		case "web":
			return "网页"
		default:
			return source
		}
	}
	extra := "\n系统记录的本轮消息入口：" + channelName(r.Source)
	var previous string
	if e := w.Store.DB.QueryRow("SELECT json_extract(data,'$.source') FROM runs WHERE session_id=? AND rowid<(SELECT rowid FROM runs WHERE id=?) ORDER BY rowid DESC LIMIT 1", r.SessionID, r.ID).Scan(&previous); e == nil {
		extra += "。该个人对话上一条输入来自：" + channelName(previous)
	}

	if caps, e := w.Store.Capabilities(personalProjectID); e == nil && len(caps) > 0 {
		extra += "\n本轮可用全局工具（CLI 用 run_global_cli 按 id 和参数数组执行；MCP 调对应 server 的工具）："
		for _, c := range caps {
			if c.Kind == "cli" {
				extra += "\nCLI " + encode([]string{c.ID, c.Name, c.Command})
			} else if c.Kind == "mcp" {
				extra += "\nMCP " + encode([]string{c.Name, "p_" + strings.ReplaceAll(c.ID, "-", "")[:20]})
			}
		}
	}
	extra += w.Store.capabilityPrompt(r)
	if len(r.Attachments) > 0 {
		extra += "\n本条消息的附件（可按从0开始的序号调用read_message_attachment读取文本）：" + encode(r.Attachments)
	}
	if r.ReplyAnchor != "" {
		if ref, e := w.Store.Run(r.ReplyAnchor); e == nil {
			if ref.ProjectID == personalProjectID {
				extra += "\n该渠道已送达的关联答复（仅作引用资料）：" + encode(sourceExcerpt(ref.Result, 0))
			} else if p, e := w.Store.Project(ref.ProjectID); e == nil && !p.Archived {
				extra += "\n被引用的项目任务（可只读核对）：" + encode(map[string]string{"projectId": p.ID, "kind": "conversation", "key": ref.ID})
			}
			if !r.AnchorExplicit {
				extra += "（仅为近期渠道线索，并非用户明确引用）"
			}
		}
	}
	return extra + "\n当前北京时间：" + time.Now().In(loc).Format("2006-01-02 15:04:05 MST") + "。今天的安排日期以此为准，不能猜测日期。\n当前有效个人理解：" + "见当前 workbench-personal-understanding 具名上下文；需要准确细节按需查询。" + "\n项目概况（核对具体事实可按需只读查原文）：" + encode(catalog) + "\n本人本轮输入：\n" + r.Text
}
func shanghaiDay() string {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return time.Now().In(loc).Format("2006-01-02")
}
func (s *Store) RequestToday() error { return s.SetSetting("today_refresh", shanghaiDay()) }
func (w *Worker) maintainToday(ctx context.Context) bool {
	s := w.Store
	enabled, _ := s.Setting("today_enabled")
	if enabled != "true" {
		return false
	}
	s.dispatchDecisions()
	if reset, _ := s.Setting("personal_context_reset"); reset == "true" {
		if e := s.resetPersonalContext(); e != nil {
			return false
		}
		_ = s.SetSetting("personal_context_reset", "")
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	dayNow := shanghaiDay()
	requestNow, _ := s.Setting("today_refresh")
	dailyNow, _ := getJSON[DailyContent](s.DB, "SELECT data FROM daily_contents WHERE day=?", dayNow)
	dailyDue := requestNow != "" || time.Now().In(loc).Hour() >= 8 && dailyNow.State == ""
	if !dailyDue {
		if w.maintainUnderstanding(ctx) {
			return true
		}
	}

	request, _ := s.Setting("today_refresh")
	day := shanghaiDay()
	daily, _ := getJSON[DailyContent](s.DB, "SELECT data FROM daily_contents WHERE day=?", day)
	if request == "" && (daily.State != "" || time.Now().In(loc).Hour() < 8) {
		return false
	}
	// Failed attempts back off; an explicit refresh can retry immediately.
	if request == "" && daily.State == "failed" {
		return false
	}
	previous, _ := getJSON[DailyContent](s.DB, "SELECT data FROM daily_contents ORDER BY day DESC LIMIT 1")
	// Keep non-empty prior suggestions even when yesterday was intentionally silent.
	suggestions, _ := listJSON[DailyContent](s.DB, "SELECT data FROM daily_contents WHERE coalesce(json_extract(data,'$.text'),'') != '' ORDER BY day DESC LIMIT 14")
	catalog, _ := s.ContextCatalog()
	memoryContext := s.MemoryContext()
	version := s.contextVersion()
	rules, rulesErr := s.PersonalRules()
	if rulesErr != nil {
		return false
	}
	r, e := w.compactCall(ctx, "daily", "本次执行每日建议生成。仅基于下面明确个人理解和项目两块上下文，只输出JSON {\"text\":\"一项中文Markdown建议或空字符串\",\"decision\":null}。text给至多一项值得今天考虑的建议，说明具体依据。无需强行安排小行动。必要时decision可为 {\"question\":\"问题\",\"reason\":\"推荐理由\",\"options\":[{\"label\":\"选项\",\"tradeoff\":\"取舍\",\"recommended\":true},{\"label\":\"另一选项\",\"tradeoff\":\"取舍\",\"recommended\":false}]}，恰有一个推荐，选项仅记录个人决定，无项目写操作。建议可以是学习、观察、取舍、减少投入，不只推任务。不要强行提问，不创建任务、不假称了解未覆盖资料。没有值得提出的新内容时输出空text和null decision。\n当前北京时间："+time.Now().In(loc).Format("2006-01-02 15:04:05 MST")+"\n个人理解："+memoryContext+"\n项目："+encode(catalog)+"\n此前建议记录（未回复不代表拒绝，不凭时间过去重复旧话题，无实质新证据保持安静）："+encode(suggestions), &rules)
	if errors.Is(e, errMaintenanceYield) {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	currentRules, ruleErr := s.PersonalRules()
	if ruleErr != nil {
		e = ruleErr
	} else if currentRules.Version != rules.Version {
		return true
	}
	newDecisionID := ""
	var generated struct {
		Text     string            `json:"text"`
		Decision *PersonalDecision `json:"decision"`
	}
	if e == nil {
		e = json.Unmarshal([]byte(jsonPayload(r.Text)), &generated)
	}
	if e == nil && version != s.contextVersion() {
		e = errors.New("生成期间依据已变化，请更新建议")
	}
	if e == nil && generated.Decision != nil {
		p, er := s.PersonalSpace()
		if er != nil {
			e = er
		} else {
			ss, _ := s.Session(p.DefaultSessionID)
			d, er := s.ProposeDecision(RuntimeInput{Project: p, Session: ss, Run: Run{ID: "daily:" + day + ":" + strconv.FormatInt(daily.Version+1, 10)}}, *generated.Decision)
			e = er
			if er == nil {
				newDecisionID = d.ID
			}
		}
	}
	if e != nil && daily.Day == "" {
		daily.Text = previous.Text
	}
	daily.Day = day
	daily.Version++
	daily.ContextVersion = version
	daily.UpdatedAt = now()
	if e != nil {
		daily.State = "failed"
		daily.Error = safeRuntimeError(e.Error())
	} else {
		daily.PersonalRulesVersion = rules.Version
		daily.State = "ready"
		daily.Error = ""
		daily.DecisionID = newDecisionID
		daily.Text = strings.TrimSpace(generated.Text)
		if daily.Text == "SILENT" {
			daily.Text = ""
		}
	}
	_, _ = s.DB.Exec("INSERT INTO daily_contents VALUES(?,?) ON CONFLICT(day) DO UPDATE SET data=excluded.data", day, encode(daily))
	_ = s.SetSetting("today_refresh", "")
	_ = s.Emit("", "", "today.updated", daily)
	return true
}

// Restore tooling preserves this content-free ledger alongside new tables.
func (s *Store) ApplyForgottenMemories() error {
	entries, e := os.ReadDir(filepath.Join(s.Root, "forgotten-memories"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var ids []string
	for _, entry := range entries {
		if !entry.IsDir() {
			ids = append(ids, entry.Name())
			raw, er := os.ReadFile(filepath.Join(s.Root, "forgotten-memories", entry.Name()))
			if er != nil {
				return er
			}
			var excluded []string
			if json.Unmarshal(raw, &excluded) == nil {
				for _, id := range excluded {
					if _, er = s.DB.Exec("INSERT OR IGNORE INTO personal_memory_exclusions VALUES(?)", id); er != nil {
						return er
					}
				}
			}
			_, e = s.DB.Exec("UPDATE personal_memories SET data=json_set(data,'$.forgotten',json('true'),'$.text','','$.source','','$.quote','') WHERE id=?", entry.Name())
			if e != nil {
				return e
			}
		}
	}
	generation := digest([]byte(strings.Join(ids, "\n")))
	old, _ := s.Setting("memory_forget_generation")
	if old != generation {
		if e = s.resetPersonalContext(); e != nil {
			return e
		}
		return s.SetSetting("memory_forget_generation", generation)
	}
	return nil
}
