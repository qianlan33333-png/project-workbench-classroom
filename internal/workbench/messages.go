package workbench

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

func entryKey(in Incoming) string {
	return digest([]byte(in.Source + "\x00" + in.ConnectionID + "\x00" + in.ActorID + "\x00" + in.ConversationID))
}
func (s *Store) Message(id string) (Message, error) {
	m, e := getJSON[Message](s.DB, "SELECT data FROM inbox WHERE id=?", id)
	if e == nil && (m.SessionID != "" || m.Incoming.SessionID != "") {
		id := m.SessionID
		if id == "" {
			id = m.Incoming.SessionID
		}
		if _, err := s.Session(id); err != nil {
			return Message{}, err
		}
	}
	return m, e
}
func (s *Store) Inbox() ([]Message, error) {
	return listJSON[Message](s.DB, "SELECT data FROM inbox WHERE state IN ('unrouted','clarify','error') ORDER BY created_at")
}
func (s *Store) Runs(session string) ([]Run, error) {
	return s.RunPage(session, "")
}
func (s *Store) RunPage(session, before string) ([]Run, error) {
	var rows []Run
	var err error
	if before != "" {
		rows, err = listJSON[Run](s.DB, "SELECT data FROM runs WHERE session_id IN (SELECT id FROM sessions WHERE coalesce(json_extract(data,'$.recycledAt'),0)=0) AND coalesce(json_extract(data,'$.kind'),'')<>'archive' AND (?='' OR session_id=?) AND rowid<(SELECT rowid FROM runs WHERE id=?) ORDER BY rowid DESC LIMIT 100", session, session, before)
	} else {
		rows, err = listJSON[Run](s.DB, "SELECT data FROM runs WHERE session_id IN (SELECT id FROM sessions WHERE coalesce(json_extract(data,'$.recycledAt'),0)=0) AND coalesce(json_extract(data,'$.kind'),'')<>'archive' AND (?='' OR session_id=?) ORDER BY state IN ('running','waiting','stopping') DESC,rowid DESC LIMIT 100", session, session)
	}
	for i := range rows {
		s.runExpertName(&rows[i])
	}
	return rows, err
}
func (s *Store) runExpertName(r *Run) {
	if r.ExpertName == "" && r.ExpertID != "" && r.ExpertDigest != "" {
		if ex, e := s.ExpertVersion(r.ExpertID, r.ExpertDigest); e == nil {
			r.ExpertName = ex.Name
		}
	}
}
func (s *Store) Run(id string) (Run, error) {
	r, e := getJSON[Run](s.DB, "SELECT data FROM runs WHERE id=?", id)
	if e == nil {
		if _, err := s.Session(r.SessionID); err != nil {
			return Run{}, err
		}
	}
	s.runExpertName(&r)
	return r, e
}
func (s *Store) saveRun(r Run) error {
	_, err := s.DB.Exec("UPDATE runs SET state=?,data=? WHERE id=?", r.State, encode(r), r.ID)
	if err != nil {
		return err
	}
	if r.ScheduleID != "" {
		if _, err = s.DB.Exec("UPDATE schedule_checks SET outcome=?,detail=? WHERE schedule_id=? AND run_id=?", r.State, r.Error, r.ScheduleID, r.ID); err != nil {
			return err
		}
	}
	return s.Emit(r.SessionID, r.ID, "run.updated", r)
}
func (s *Store) saveMessage(m Message) error {
	_, err := s.DB.Exec("UPDATE inbox SET state=?,session_id=?,data=? WHERE id=? AND state NOT IN ('deleted','cancelled')", m.State, m.SessionID, encode(m), m.ID)
	return err
}

func (s *Store) Submit(in Incoming) (Message, error) {
	if in.Source == "" {
		in.Source = "web"
	}
	if in.Source != "web" && in.Source != "wechat" && in.Source != "feishu" && in.Source != "automation" {
		return Message{}, errors.New("消息来源无效")
	}
	if in.EventID == "" || len(in.EventID) > 200 {
		return Message{}, errors.New("消息需要稳定的事件标识")
	}
	if len(strings.TrimSpace(in.Text)) == 0 && len(in.Attachments) == 0 {
		return Message{}, errors.New("消息不能为空")
	}
	if len(in.Text) > 100000 {
		return Message{}, errors.New("消息过长")
	}
	if in.Source != "web" {
		in.Capabilities = nil
	}
	if in.Source == "wechat" || in.Source == "feishu" {
		c, err := s.Connection(in.ConnectionID)
		if err != nil || !c.Enabled || c.Kind != in.Source || c.OwnerID != in.ActorID || in.ConversationID == "" {
			return Message{}, errors.New("只接受已绑定本人的私聊消息")
		}
		in.SessionID = ""
		in.ExpertID = ""
		in.References = nil // Only a parsed, explicit text command may grant channel references.
	}
	if len(in.Capabilities) > 0 {
		// A retry of a persisted input must still resolve to the original run
		// after its selected integration is removed. Validate only new inputs;
		// queueing validates again against the resolved project.
		var existing string
		e := s.DB.QueryRow("SELECT id FROM inbox WHERE source=? AND connection_id=? AND event_id=?", in.Source, in.ConnectionID, in.EventID).Scan(&existing)
		if e != nil && e != sql.ErrNoRows {
			return Message{}, e
		}
		if e == sql.ErrNoRows {
			if in.SessionID == "" {
				return Message{}, errors.New("选择能力需要指定对话")
			}
			ss, e := s.Session(in.SessionID)
			if e != nil {
				return Message{}, e
			}
			if _, e = s.selectCapabilities(ss.ProjectID, in.Capabilities); e != nil {
				return Message{}, e
			}
		}
	}
	m := Message{ID: newID(), Incoming: in, State: "unrouted", CreatedAt: now()}
	{
		var count int
		if e := s.DB.QueryRow("SELECT count(*) FROM legacy_consumed WHERE hash=?", digest([]byte(in.Source+":"+in.EventID))).Scan(&count); e != nil {
			return m, e
		}
		if count > 0 {
			m.State = "handled"
		}
	}
	var activated *Session
	err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if _, e := tx.Exec("INSERT INTO inbox VALUES(?,?,?,?,?,?,'',?,?)", m.ID, in.Source, in.ConnectionID, in.EventID, entryKey(in), m.State, m.CreatedAt, encode(m)); e != nil {
			return e
		}
		if in.SessionID != "" {
			var e error
			activated, e = activateSession(tx, in.SessionID, in.Text)
			return e
		}
		return nil
	})
	if err != nil {
		old, e := getJSON[Message](s.DB, "SELECT data FROM inbox WHERE source=? AND connection_id=? AND event_id=?", in.Source, in.ConnectionID, in.EventID)
		if e == nil {
			if old.State == "deleted" && old.IncomingHash == digest([]byte(encode(in))) {
				return old, nil
			}
			if encode(old.Incoming) != encode(in) {
				return old, errors.New("同一事件标识对应了不同内容")
			}
			target := old.SessionID
			if target == "" {
				target = old.Incoming.SessionID
			}
			if target != "" {
				if _, err := s.Session(target); err != nil {
					return Message{}, err
				}
			}
			return old, nil
		}
		return m, err
	}
	if activated != nil {
		if err = s.Emit(activated.ID, "", "session.updated", activated); err != nil {
			return m, err
		}
	}
	if err = s.Emit("", "", "message.received", map[string]string{"id": m.ID, "eventId": in.EventID, "sessionId": in.SessionID, "source": in.Source}); err != nil {
		return m, err
	}
	return m, nil
}
func (s *Store) Focus(in Incoming) (Session, error) {
	var id string
	if err := s.DB.QueryRow("SELECT session_id FROM entry_focus WHERE entry_key=?", entryKey(in)).Scan(&id); err != nil {
		return Session{}, ErrNotFound
	}
	return s.Session(id)
}
func (s *Store) SetFocus(in Incoming, session string) error {
	_, err := s.DB.Exec("INSERT INTO entry_focus VALUES(?,?,?) ON CONFLICT(entry_key) DO UPDATE SET session_id=excluded.session_id,updated_at=excluded.updated_at", entryKey(in), session, now())
	return err
}

func (s *Store) queue(m Message, sessionID string, anchor string, explicit bool) (Message, error) {
	s.expertMu.RLock()
	defer s.expertMu.RUnlock()
	if current, e := s.Message(m.ID); e == nil && current.RunID != "" {
		return current, nil
	}
	sess, err := s.Session(sessionID)
	if err != nil {
		return m, err
	}
	if sess.Lifecycle != "" {
		return m, errors.New("对话正在归档")
	}
	p, err := s.Project(sess.ProjectID)
	if err != nil {
		return m, err
	}
	if p.Archived {
		return m, errors.New("项目已归档，不能继续执行")
	}
	if isChatChannel(m.Incoming.Source) && p.ID != personalProjectID {
		return m, errors.New("机器人消息属于今天个人助手")
	}
	if p.ID == personalProjectID && (len(m.Incoming.Capabilities) > 0 || len(m.Incoming.References) > 0 || len(m.Incoming.Attachments) > 0 && !isChatChannel(m.Incoming.Source)) {
		return m, errors.New("个人助手不接收业务文件或执行能力，请进入对应项目")
	}
	expert := m.Incoming.ExpertID
	if expert == "" {
		expert = sess.ExpertID
	}
	if expert == "" {
		expert = p.DefaultExpertID
	}
	if expert == "general" {
		expert = ""
	}
	version := ""
	expertName := ""
	if expert != "" {
		if !contains(p.ExpertIDs, expert) {
			return m, errors.New("专家尚未加入项目")
		}
		e, err := s.Expert(expert)
		if err != nil {
			return m, err
		}
		version = e.Digest
		expertName = e.Name
	}
	run := Run{ExpertName: expertName, ID: newID(), ProjectID: p.ID, SessionID: sess.ID, MessageID: m.ID, EventID: m.Incoming.EventID, State: "queued", Source: m.Incoming.Source, Text: m.Incoming.Text, ExpertID: expert, ExpertDigest: version, ReplyAnchor: anchor, AnchorExplicit: explicit, CreatedAt: now()}
	run.Capabilities, err = s.selectCapabilities(p.ID, m.Incoming.Capabilities)
	if err != nil {
		return m, err
	}
	run.ExpertVersions = map[string]string{}
	for _, id := range p.ExpertIDs {
		ex, e := s.Expert(id)
		if e != nil {
			return m, e
		}
		run.ExpertVersions[id] = ex.Digest
	}
	if m.References == nil {
		m.References = m.Incoming.References
	}
	if len(m.References) > 20 {
		return m, errors.New("一次最多引用 20 份资料")
	}
	for _, ref := range m.References {
		if _, e := s.Project(ref.ProjectID); e != nil {
			return m, e
		}
		if e := validPath(ref.Path); e != nil {
			return m, e
		}
	}
	m.State = "queued"
	m.SessionID = sess.ID
	m.RunID = run.ID
	m.Question = ""
	m.Candidates = nil
	priority := 0
	if run.Source == "automation" {
		priority = 10
	}
	alreadyQueued := false
	var activated *Session
	err = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRow("SELECT data FROM inbox WHERE id=?", m.ID).Scan(&existing)
		if err != nil {
			return err
		}
		old, err := decodeJSON[Message](existing)
		if err != nil {
			return err
		}
		if old.State == "deleted" {
			return ErrNotFound
		}
		if old.RunID != "" {
			m = old
			alreadyQueued = true
			return nil
		}
		var sessionRaw string
		if err := tx.QueryRow("SELECT data FROM sessions WHERE id=?", sess.ID).Scan(&sessionRaw); err != nil {
			return err
		}
		currentSession, e := decodeJSON[Session](sessionRaw)
		if e != nil {
			return e
		}
		if currentSession.RecycledAt > 0 || currentSession.Lifecycle != "" {
			return errors.New("对话正在归档或已在回收站")
		}
		if _, err := tx.Exec("INSERT INTO runs VALUES(?,?,?,?,?,?,?,?)", run.ID, p.ID, sess.ID, m.ID, run.State, priority, run.CreatedAt, encode(run)); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE inbox SET state=?,session_id=?,data=? WHERE id=?", m.State, m.SessionID, encode(m), m.ID); err != nil {
			return err
		}
		if err = startTaskForSession(tx, sess.ID); err != nil {
			return err
		}
		activated, err = activateSession(tx, sess.ID, m.Incoming.Text)
		if err != nil {
			return err
		}
		if m.Incoming.Source != "automation" {
			if _, err := tx.Exec("INSERT INTO entry_focus VALUES(?,?,?) ON CONFLICT(entry_key) DO UPDATE SET session_id=excluded.session_id,updated_at=excluded.updated_at", entryKey(m.Incoming), sess.ID, now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return m, err
	}
	if alreadyQueued {
		return m, nil
	}
	if ts, e := s.Tasks(p.ID); e == nil {
		for _, t := range ts {
			if t.SessionID == sess.ID && t.State == "in_progress" {
				_ = s.Emit("", "", "task.updated", t)
			}
		}
	}

	if activated != nil {
		if err = s.Emit(sess.ID, "", "session.updated", activated); err != nil {
			return m, err
		}
	}
	if err = s.Emit(sess.ID, run.ID, "message.user", map[string]any{"text": run.Text, "source": run.Source, "messageId": m.ID, "attachments": m.Incoming.Attachments}); err != nil {
		return m, err
	}
	return m, s.Emit(sess.ID, run.ID, "run.updated", run)
}
func (s *Store) Clarify(m Message, question string, candidates []Candidate) (Message, error) {
	m.State = "clarify"
	m.Question = question
	m.Candidates = candidates
	res, err := s.DB.Exec("UPDATE inbox SET state='clarify',data=? WHERE id=? AND state IN ('unrouted','clarify','error')", encode(m), m.ID)
	if err != nil {
		return m, err
	}
	if n, e := res.RowsAffected(); e != nil {
		return m, e
	} else if n == 0 {
		return s.Message(m.ID)
	}
	if err := s.Emit("", "", "message.clarify", m); err != nil {
		return m, err
	}
	text := question
	for i, c := range candidates {
		text += fmt.Sprintf("\n%d. %s", i+1, c.Label)
	}
	return m, s.QueueReply(m, "", "", "clarify", text)
}
func (s *Store) ResolveMessage(id, session string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.Message(id)
	if err != nil {
		return m, err
	}
	if session == "" && isChatChannel(m.Incoming.Source) && m.RunID != "" {
		return m, nil
	}
	if m.State != "clarify" && m.State != "unrouted" && m.State != "error" {
		return m, errors.New("这条消息已处理")
	}
	if session == "" && isChatChannel(m.Incoming.Source) {
		return s.routeChannelToToday(m)
	}
	return s.queue(m, session, "", false)
}

type RouteDecision struct {
	SessionID string `json:"sessionId"`
	ProjectID string `json:"projectId"`
	Question  string `json:"question"`
	Reason    string `json:"reason"`
}
type RouteCatalog struct {
	Projects []Project `json:"projects"`
	Sessions []Session `json:"sessions"`
	Focus    *Session  `json:"focus,omitempty"`
}
type SemanticRouter func(context.Context, Incoming, RouteCatalog) (RouteDecision, error)

func (s *Store) Route(ctx context.Context, id string, semantic SemanticRouter) (Message, error) {
	m, err := s.Message(id)
	if err != nil || m.State != "unrouted" {
		return m, err
	}
	in := m.Incoming
	if isChatChannel(in.Source) {
		return s.routeChannelToToday(m)
	}
	if in.SessionID != "" {
		return s.queue(m, in.SessionID, "", false)
	}
	projects, err := s.Projects()
	if err != nil {
		return m, err
	}
	active := []Project{}
	for _, p := range projects {
		if !p.Archived {
			p.Description = string([]rune(p.Description)[:min(240, len([]rune(p.Description)))])
			active = append(active, p)
		}
	}
	text := strings.TrimSpace(in.Text)
	// A deliberately explicit grammar grants exactly one external file, never a whole project.
	if parts := regexp.MustCompile(`^引用项目「([^」]+)」文件「([^」]+)」到项目「([^」]+)」[：:]([\s\S]+)$`).FindStringSubmatch(text); len(parts) > 0 {
		var sources, targets []Project
		for _, p := range active {
			if p.Name == parts[1] {
				sources = append(sources, p)
			}
			if p.Name == parts[3] {
				targets = append(targets, p)
			}
		}
		if len(sources) != 1 || len(targets) != 1 {
			return s.Clarify(m, "引用的来源或目标项目不唯一，请使用完整项目名。", projectCandidates(active))
		}
		m.References = []FileReference{{ProjectID: sources[0].ID, Path: parts[2]}}
		return s.queue(m, targets[0].DefaultSessionID, "", true)
	}
	if match := regexp.MustCompile(`^(?:新建项目|创建项目|/新建)\s+([^\n]{1,80})$`).FindStringSubmatch(text); len(match) > 0 {
		p, e := s.ensureProject("from-"+m.ID, strings.TrimSpace(match[1]), "", nil)
		if e != nil {
			return m, e
		}
		m.State = "handled"
		m.SessionID = p.DefaultSessionID
		if e = s.saveMessage(m); e != nil {
			return m, e
		}
		if e = s.SetFocus(in, p.DefaultSessionID); e != nil {
			return m, e
		}
		return m, s.QueueReply(m, "", p.DefaultSessionID, "created", "已新建项目「"+p.Name+"」，当前进入项目助手。对话编号 #S"+p.DefaultSessionID[:8])
	}
	if text == "任务状态" || text == "/任务" || text == "当前任务" {
		focus, _ := s.Focus(in)
		runs, e := s.Runs(focus.ID)
		if e != nil {
			return m, e
		}
		var lines []string
		for _, r := range runs {
			if len(lines) >= 8 {
				break
			}
			p, _ := s.Project(r.ProjectID)
			lines = append(lines, fmt.Sprintf("%s · #T%s · %s", p.Name, r.ID[:8], stateLabel(r.State)))
		}
		answer := "目前没有任务记录。"
		if len(lines) > 0 {
			answer = strings.Join(lines, "\n")
		}
		m.State = "handled"
		if e = s.saveMessage(m); e != nil {
			return m, e
		}
		return m, s.QueueReply(m, "", "", "status", answer)
	}
	if text == "项目列表" || text == "有哪些项目" || text == "/项目" {
		var names []string
		for _, p := range active {
			names = append(names, p.Name)
		}
		answer := "还没有项目，请在网页新建或导入项目。"
		if len(names) > 0 {
			answer = "可用项目：\n" + strings.Join(names, "\n")
		}
		m.State = "handled"
		if err = s.saveMessage(m); err != nil {
			return m, err
		}
		return m, s.QueueReply(m, "", "", "catalog", answer)
	}
	// A bare choice applies only to the unique pending question in this entry.
	if regexp.MustCompile(`^[1-9][0-9]?$`).MatchString(text) {
		pending, e := listJSON[Message](s.DB, "SELECT data FROM inbox WHERE entry_key=? AND state='clarify' ORDER BY created_at", entryKey(in))
		if e != nil {
			return m, e
		}
		if len(pending) == 1 {
			var choice int
			fmt.Sscanf(text, "%d", &choice)
			if choice > 0 && choice <= len(pending[0].Candidates) {
				candidate := pending[0].Candidates[choice-1]
				original, e := s.queue(pending[0], candidate.SessionID, "", false)
				if e != nil {
					return m, e
				}
				m.State = "handled"
				m.SessionID = original.SessionID
				m.RunID = original.RunID
				return m, s.saveMessage(m)
			}
		}
		return s.Clarify(m, "请回复具体的项目名或引用要回答的问题。", nil)
	}
	matches := []Project{}
	for _, p := range active {
		found := strings.Contains(text, p.Name)
		for _, a := range p.Aliases {
			if a != "" && strings.Contains(text, a) {
				found = true
			}
		}
		if found {
			matches = append(matches, p)
		}
	}
	anchorRun := ""
	explicit := false
	var referred Session
	if in.ReplyTo != "" {
		ds, e := listJSON[Delivery](s.DB, "SELECT data FROM deliveries WHERE connection_id=? AND state IN ('sent','accepted')", in.ConnectionID)
		if e != nil {
			return m, e
		}
		for _, d := range ds {
			if d.ConversationID == in.ConversationID && (d.Receipt == in.ReplyTo || d.ID == in.ReplyTo || contains(d.Receipts, in.ReplyTo)) {
				referred, e = s.Session(d.SessionID)
				if e == nil {
					anchorRun = d.RunID
					explicit = true
				}
				break
			}
		}
	}
	if match := regexp.MustCompile(`(?i)#(?:T|任务)([a-f0-9-]{8,36})`).FindStringSubmatch(text); len(match) > 0 {
		runs, e := listJSON[Run](s.DB, "SELECT data FROM runs WHERE id LIKE ? LIMIT 2", strings.ToLower(match[1])+"%")
		if e != nil {
			return m, e
		}
		if len(runs) == 1 {
			referred, e = s.Session(runs[0].SessionID)
			if e != nil {
				return m, e
			}
			anchorRun = runs[0].ID
			explicit = true
		}
	}
	if referred.ID != "" {
		if referred.Kind == "automation" {
			p, e := s.Project(referred.ProjectID)
			if e != nil {
				return m, e
			}
			target := p.DefaultSessionID
			if focus, e := s.Focus(in); e == nil && focus.ProjectID == p.ID {
				target = focus.ID
			}
			referred, e = s.Session(target)
			if e != nil {
				return m, e
			}
		}
		if len(matches) > 0 && (len(matches) != 1 || matches[0].ID != referred.ProjectID) {
			return s.Clarify(m, "引用的对话与消息中的项目不同，请选择这次要进入的项目。", projectCandidates(active))
		}
		return s.queue(m, referred.ID, anchorRun, explicit)
	}
	if match := regexp.MustCompile(`(?i)(?:#S|[&#]session=)([a-f0-9-]{8,36})`).FindStringSubmatch(text); len(match) > 0 {
		ss, e := listJSON[Session](s.DB, "SELECT data FROM sessions WHERE id LIKE ? AND kind<>'automation' LIMIT 2", strings.ToLower(match[1])+"%")
		if e != nil {
			return m, e
		}
		if len(ss) != 1 {
			return s.Clarify(m, "对话编号不唯一或已不存在，请从项目内选择对话。", projectCandidates(active))
		}
		return s.queue(m, ss[0].ID, "", true)
	}
	if len(matches) > 1 {
		return s.Clarify(m, "这条消息涉及多个项目，请选择本次工作的项目；跨项目资料需要明确指定引用。", projectCandidates(matches))
	}
	focus, focusErr := s.Focus(in)
	if len(matches) == 1 {
		target := matches[0].DefaultSessionID
		if focusErr == nil && focus.ProjectID == matches[0].ID {
			target = focus.ID
		}
		return s.routeTopic(m, target)
	}
	if text == "回到项目助手" && focusErr == nil {
		p, e := s.Project(focus.ProjectID)
		if e != nil {
			return m, e
		}
		return s.queue(m, p.DefaultSessionID, "", false)
	}
	if focusErr == nil && (isContinuation(text) || strings.HasPrefix(text, "单独开") || strings.HasPrefix(text, "另开")) {
		return s.routeTopic(m, focus.ID)
	}
	if len(active) == 0 {
		return s.Clarify(m, "还没有项目，请先在网页新建项目或导入资料。", nil)
	}
	if semantic != nil {
		catalog := RouteCatalog{Projects: active, Sessions: []Session{}}
		if focusErr == nil {
			catalog.Focus = &focus
		}
		for _, p := range active {
			ss, e := s.Sessions(p.ID)
			if e != nil {
				return m, e
			}
			catalog.Sessions = append(catalog.Sessions, ss...)
		}
		decision, e := semantic(ctx, in, catalog)
		if e == nil && decision.Question == "" {
			target := decision.SessionID
			if target == "" {
				for _, p := range active {
					if p.ID == decision.ProjectID {
						target = p.DefaultSessionID
					}
				}
			}
			if target != "" {
				sess, e := s.Session(target)
				if e == nil {
					for _, p := range active {
						if p.ID == sess.ProjectID {
							return s.routeTopic(m, target)
						}
					}
				}
			}
		}
		if e == nil && decision.Question != "" {
			return s.Clarify(m, decision.Question, projectCandidates(active))
		}
	}
	return s.Clarify(m, "这次工作属于哪个项目？", projectCandidates(active))
}
func projectCandidates(ps []Project) []Candidate {
	out := []Candidate{}
	for _, p := range ps {
		out = append(out, Candidate{ProjectID: p.ID, SessionID: p.DefaultSessionID, Label: p.Name})
	}
	return out
}
func isContinuation(t string) bool {
	for _, prefix := range []string{"继续", "接着", "第三", "第二", "第一", "第四", "第五", "把刚才", "刚才", "再改", "改成", "换成", "这个", "那个", "好的", "可以", "请继续", "展开", "缩短"} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}
func (s *Store) routeTopic(m Message, target string) (Message, error) {
	text := strings.TrimSpace(m.Incoming.Text)
	if strings.HasPrefix(text, "单独开") || strings.HasPrefix(text, "另开") {
		title := strings.TrimSpace(regexp.MustCompile(`^(单独开|另开)(一个|个)?(对话)?`).ReplaceAllString(text, ""))
		title = strings.Trim(title, " ：:，,。\n")
		if title == "" {
			title = "新专题"
		}
		parent, err := s.Session(target)
		if err != nil {
			return m, err
		}
		ss, err := s.CreateSession(parent.ProjectID, title, "topic", parent.ExpertID)
		if err != nil {
			return m, err
		}
		target = ss.ID
	}
	anchor := ""
	ds, err := listJSON[Delivery](s.DB, "SELECT data FROM deliveries WHERE connection_id=? AND state IN ('sent','accepted') ORDER BY created_at DESC LIMIT 100", m.Incoming.ConnectionID)
	if err != nil {
		return m, err
	}
	for _, d := range ds {
		if d.Kind == "answer" && d.SessionID == target && d.ConversationID == m.Incoming.ConversationID {
			anchor = d.RunID
			break
		}
	}
	return s.queue(m, target, anchor, false)
}
