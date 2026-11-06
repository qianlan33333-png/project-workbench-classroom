package workbench

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

type taskCreation struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Project   string `json:"project"`
	Title     string `json:"title"`
	State     string `json:"state"`
	URL       string `json:"url"`
}

func ownerTaskTurn(in RuntimeInput) bool {
	r := in.Run
	return in.Project.ID == personalProjectID && !in.Routing &&
		r.Agent == nil && r.Gateway == nil && r.ComponentConfig == nil && r.FollowupID == "" && r.ScheduleID == "" &&
		(r.Source == "web" || r.Source == "wechat" || r.Source == "feishu") &&
		!strings.HasPrefix(r.EventID, "decision-answer:") &&
		!strings.HasPrefix(r.Text, "继续讨论这项每日建议（不是创建任务授权）：")
}

func (s *Store) createPersonalTask(in RuntimeInput, args json.RawMessage) (taskCreation, error) {
	denied := errors.New("只能在本人当前轮明确要求时创建任务")
	if !ownerTaskTurn(in) {
		return taskCreation{}, denied
	}
	run, err := s.Run(in.Run.ID)
	if err != nil || run.State != "running" || run.SessionID != in.Session.ID || run.ProjectID != personalProjectID {
		return taskCreation{}, denied
	}
	in.Run = run
	if !ownerTaskTurn(in) {
		return taskCreation{}, denied
	}
	m, err := s.Message(run.MessageID)
	if err != nil || m.RunID != run.ID || m.SessionID != run.SessionID ||
		m.Agent != nil || m.Gateway != nil || m.ComponentConfig != nil || m.Incoming.Source != run.Source {
		return taskCreation{}, denied
	}
	if run.Source == "web" {
		if m.Incoming.ActorID != "owner" || m.Incoming.ConnectionID != "web" {
			return taskCreation{}, denied
		}
	} else {
		connection, e := s.Connection(m.Incoming.ConnectionID)
		if e != nil || !connection.Enabled || connection.Kind != run.Source || connection.OwnerID == "" || connection.OwnerID != m.Incoming.ActorID || m.Incoming.ConversationID == "" {
			return taskCreation{}, denied
		}
	}
	var request struct {
		ProjectID     string `json:"projectId"`
		Title         string `json:"title"`
		Description   string `json:"description"`
		ScheduledDate string `json:"scheduledDate"`
		DueDate       string `json:"dueDate"`
		Quote         string `json:"quote"`
	}
	if err = json.Unmarshal(args, &request); err != nil {
		return taskCreation{}, err
	}
	quote := strings.TrimSpace(request.Quote)
	if len([]rune(quote)) < 2 || !strings.Contains(m.Incoming.Text, quote) {
		return taskCreation{}, denied
	}
	p, err := s.Project(strings.TrimSpace(request.ProjectID))
	if err != nil || p.Archived {
		return taskCreation{}, errors.New("请选择有效项目，个人任务请使用 personal-assistant")
	}
	t := ProjectTask{
		ProjectID: p.ID, Title: strings.TrimSpace(request.Title), Description: strings.TrimSpace(request.Description),
		ScheduledDate: request.ScheduledDate, DueDate: request.DueDate, State: "pending",
	}
	t.Operation = "today-task:" + digest([]byte(run.ID+"\n"+encode(t)))
	t, err = s.saveTask(t, run.ID)
	if err != nil {
		return taskCreation{}, err
	}
	name := p.Name
	if p.ID == personalProjectID {
		name = "个人"
	}
	target := url.Values{"project": {p.ID}, "session": {p.DefaultSessionID}, "task": {t.ID}}
	if p.ID == personalProjectID {
		target = url.Values{"page": {"tasks"}, "scope": {p.ID}, "task": {t.ID}}
	}
	return taskCreation{ID: t.ID, ProjectID: p.ID, Project: name, Title: t.Title, State: t.State, URL: "/#" + target.Encode()}, nil
}
