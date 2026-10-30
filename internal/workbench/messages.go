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
	return getJSON[Message](s.DB, "SELECT data FROM inbox WHERE id=?", id)
}
func (s *Store) Inbox() ([]Message, error) {
	return listJSON[Message](s.DB, "SELECT data FROM inbox WHERE state IN ('unrouted','clarify','error') ORDER BY created_at")
}
func (s *Store) Runs(session string) ([]Run, error) {
	return listJSON[Run](s.DB, "SELECT data FROM runs WHERE (?='' OR session_id=?) ORDER BY created_at DESC LIMIT 100", session, session)
}
func (s *Store) Run(id string) (Run, error) {
	return getJSON[Run](s.DB, "SELECT data FROM runs WHERE id=?", id)
}
func (s *Store) saveRun(r Run) error {
	_, err := s.DB.Exec("UPDATE runs SET state=?,data=? WHERE id=?", r.State, encode(r), r.ID)
	if err != nil {
		return err
	}
	return s.Emit(r.SessionID, r.ID, "run.updated", r)
}
func (s *Store) saveMessage(m Message) error {
	_, err := s.DB.Exec("UPDATE inbox SET state=?,session_id=?,data=? WHERE id=?", m.State, m.SessionID, encode(m), m.ID)
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
	if in.Source == "wechat" || in.Source == "feishu" {
		c, err := s.Connection(in.ConnectionID)
		if err != nil || !c.Enabled || c.Kind != in.Source || c.OwnerID != in.ActorID || in.ConversationID == "" {
			return Message{}, errors.New("只接受已绑定本人的私聊消息")
		}
		in.SessionID = ""
		in.ExpertID = ""
	}
	m := Message{ID: newID(), Incoming: in, State: "unrouted", CreatedAt: now()}
	_, err := s.DB.Exec("INSERT INTO inbox VALUES(?,?,?,?,?,'unrouted','',?,?)", m.ID, in.Source, in.ConnectionID, in.EventID, entryKey(in), m.CreatedAt, encode(m))
	if err != nil {
		old, e := getJSON[Message](s.DB, "SELECT data FROM inbox WHERE source=? AND connection_id=? AND event_id=?", in.Source, in.ConnectionID, in.EventID)
		if e == nil {
			if encode(old.Incoming) != encode(in) {
				return old, errors.New("同一事件标识对应了不同内容")
			}
			return old, nil
		}
		return m, err
	}
	if err = s.Emit("", "", "message.received", map[string]string{"id": m.ID}); err != nil {
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
	sess, err := s.Session(sessionID)
	if err != nil {
		return m, err
	}
	p, err := s.Project(sess.ProjectID)
	if err != nil {
		return m, err
	}
	if p.Archived {
		return m, errors.New("项目已归档，不能继续执行")
	}
	expert := m.Incoming.ExpertID
	if expert == "" {
		expert = sess.ExpertID
	}
	if expert == "" {
		expert = p.DefaultExpertID
	}
	version := ""
	if expert != "" {
		if !contains(p.ExpertIDs, expert) {
			return m, errors.New("专家尚未加入项目")
		}
		e, err := s.Expert(expert)
		if err != nil {
			return m, err
		}
		version = e.Digest
	}
	run := Run{ID: newID(), ProjectID: p.ID, SessionID: sess.ID, MessageID: m.ID, State: "queued", Source: m.Incoming.Source, Text: m.Incoming.Text, ExpertID: expert, ExpertDigest: version, ReplyAnchor: anchor, AnchorExplicit: explicit, CreatedAt: now()}
	m.State = "queued"
	m.SessionID = sess.ID
	m.RunID = run.ID
	m.Question = ""
	m.Candidates = nil
	priority := 0
	if run.Source == "automation" {
		priority = 10
	}
	err = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO runs VALUES(?,?,?,?,?,?,?,?)", run.ID, p.ID, sess.ID, m.ID, run.State, priority, run.CreatedAt, encode(run)); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE inbox SET state=?,session_id=?,data=? WHERE id=?", m.State, m.SessionID, encode(m), m.ID); err != nil {
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
	if err = s.Emit(sess.ID, run.ID, "message.user", map[string]any{"text": run.Text, "source": run.Source, "messageId": m.ID, "attachments": m.Incoming.Attachments}); err != nil {
		return m, err
	}
	return m, s.Emit(sess.ID, run.ID, "run.updated", run)
}
func (s *Store) Clarify(m Message, question string, candidates []Candidate) (Message, error) {
	m.State = "clarify"
	m.Question = question
	m.Candidates = candidates
	if err := s.saveMessage(m); err != nil {
		return m, err
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
	if m.State != "clarify" && m.State != "unrouted" && m.State != "error" {
		return m, errors.New("这条消息已处理")
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
			active = append(active, p)
		}
	}
	text := strings.TrimSpace(in.Text)
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
		if len(matches) > 0 && (len(matches) != 1 || matches[0].ID != referred.ProjectID) {
			return s.Clarify(m, "引用的对话与消息中的项目不同，请选择这次要进入的项目。", projectCandidates(active))
		}
		return s.queue(m, referred.ID, anchorRun, explicit)
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
