package workbench

import (
	"fmt"
	"regexp"
	"strings"
)

var followupAnswerCommand = regexp.MustCompile(`^回答\s+#D([a-fA-F0-9-]{8,36})\s+([\s\S]+)$`)

func (s *Store) followupReply(r Run, text string) string {
	fs, e := listJSON[Followup](s.DB, "SELECT data FROM personal_followups WHERE data->>'$.lastRunId'=? AND state='waiting_decision'", r.ID)
	if e != nil {
		return text
	}
	for _, f := range fs {
		d, e := getJSON[PersonalDecision](s.DB, "SELECT data FROM personal_decisions WHERE id=?", f.DecisionID)
		if e != nil || d.State != "pending" {
			continue
		}
		text += "\n\n" + f.Title + "：" + d.Question
		for _, o := range d.Options {
			text += "\n" + o.ID + ". " + o.Label + "（" + o.Tradeoff + "）"
		}
		text += "\n建议：" + d.Reason + "\n回复「回答 #D" + d.ID[:8] + " A」选择，也可把 A 换成其他想法或“暂缓”。"
	}
	return text
}

// Only the verified private target that originated the followup receives an
// autonomous update. The existing delivery outbox owns dedupe and unknowns.
func (s *Store) queueFollowupNotification(r Run) error {
	if r.FollowupID == "" {
		return nil
	}
	f, e := s.Followup(r.FollowupID)
	if e != nil {
		return e
	}
	if f.LastRunID != r.ID || f.NotifyConnectionID == "" || !contains([]string{"waiting_decision", "needs_review", "completed"}, f.State) {
		return nil
	}
	c, e := s.Connection(f.NotifyConnectionID)
	if e != nil || !c.Enabled || c.Kind != f.NotifySource || c.OwnerID != f.NotifyActorID || !isChatChannel(c.Kind) {
		return nil
	}
	m := Message{ID: "followup-update:" + f.ID + ":" + stringVersion(f.Version), SessionID: r.SessionID, Incoming: Incoming{Source: c.Kind, ConnectionID: c.ID, ActorID: c.OwnerID, ConversationID: f.NotifyConversationID}}
	text := resultReply(Project{Name: "持续跟进"}, r)
	if f.State == "needs_review" {
		text += "\n\n事项仍待核实，尚未结案。"
	}
	return s.QueueReply(m, r.ID, r.SessionID, "notification", s.followupReply(r, text))
}

func (w *Worker) handleFollowupControl(m Message) (bool, error) {
	if !isChatChannel(m.Incoming.Source) {
		return false, nil
	}
	parts := followupAnswerCommand.FindStringSubmatch(strings.TrimSpace(m.Incoming.Text))
	if len(parts) == 0 {
		return false, nil
	}
	c, e := w.Store.Connection(m.Incoming.ConnectionID)
	if e != nil || !c.Enabled || c.Kind != m.Incoming.Source || c.OwnerID != m.Incoming.ActorID || m.Incoming.ConversationID == "" {
		return true, ErrNotFound
	}
	ds, e := listJSON[PersonalDecision](w.Store.DB, `SELECT d.data FROM personal_decisions d JOIN personal_followups f ON f.id=d.data->>'$.followupId' JOIN sessions ss ON ss.id=f.session_id WHERE d.id LIKE ? AND f.data->>'$.decisionId'=d.id AND coalesce(ss.data->>'$.recycledAt',0)=0 LIMIT 2`, strings.ToLower(parts[1])+"%")
	if e != nil {
		return true, e
	}
	text := "没有找到唯一的有效问题，请在今天核对。"
	if len(ds) == 1 {
		d := ds[0]
		m.SessionID = d.SessionID
		answer := strings.TrimSpace(parts[2])
		other := ""
		if answer == "暂缓" {
			answer = "defer"
		} else if !contains([]string{"A", "B", "C"}, answer) {
			other = answer
			answer = "other"
		}
		d, e = w.Store.AnswerDecision(d.ID, d.Version, answer, other)
		if e != nil {
			text = "这次决定未生效：" + safeRuntimeError(e.Error()) + "。请在今天核对。"
		} else {
			w.Store.dispatchDecisions()
			text = fmt.Sprintf("已记录「%s」的本次回答。", d.Question)
			if d.State == "deferred" {
				text += "已暂缓，不会自动续办。"
			} else {
				text += "将只读核对当前事实后继续。"
			}
		}
	}
	m.State = "handled"
	if e = w.Store.saveMessage(m); e != nil {
		return true, e
	}
	return true, w.Store.QueueReply(m, "", m.SessionID, "decision-answer", text)
}
