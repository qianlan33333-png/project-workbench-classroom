package core

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

const replyPracticePrefix = "reply-practice-"
const PracticeReplyState = "practice_done"

func IsReplyPracticeConnection(id string) bool {
	return strings.HasPrefix(id, replyPracticePrefix)
}

func practiceConnection(project string) string {
	return replyPracticePrefix + uuid.NewSHA1(uuid.NameSpaceURL, []byte(project)).String()
}

// The local binding keeps the ordinary project/session routing contract. It has
// no address or credential and is never a selectable channel account.
func (s *Store) EnableReplyPractice(ctx context.Context, p Principal, project string) (ReplySettings, error) {
	if !p.IsOwner() || !ValidID(project) {
		return ReplySettings{}, ErrDenied
	}
	binding := Transport{ID: practiceConnection(project), Kind: "reply-practice", Name: "课堂练习", Projects: []string{project}, Enabled: true}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ReplySettings{}, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM wb_projects WHERE id=?", project).Scan(&exists); err != nil {
		return ReplySettings{}, err
	}
	if exists != 1 {
		return ReplySettings{}, ErrInvalid
	}
	if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO wb_transports VALUES(?,?)", binding.ID, jsonText(binding)); err != nil {
		return ReplySettings{}, err
	}
	saved, err := scanJSON[Transport](tx.QueryRowContext(ctx, "SELECT body FROM wb_transports WHERE id=? FOR UPDATE", binding.ID))
	if err != nil {
		return ReplySettings{}, err
	}
	if Digest(saved) != Digest(binding) {
		return ReplySettings{}, ErrConflict
	}
	key := "reply-" + project
	cfg, err := scanJSON[ReplySettings](tx.QueryRowContext(ctx, "SELECT body FROM wb_settings WHERE id=? FOR UPDATE", key))
	if IsMissing(err) {
		return ReplySettings{}, ErrDenied
	}
	if err != nil {
		return ReplySettings{}, err
	}
	if cfg.ConnectionID == binding.ID {
		return cfg, nil
	}
	if cfg.ConnectionID != "" || cfg.ProjectID != project {
		return ReplySettings{}, ErrConflict
	}
	off := false
	cfg.ConnectionID, cfg.Enabled, cfg.ReadProfile, cfg.Notify, cfg.Subject = binding.ID, true, &off, false, nil
	cfg.Revision++
	if _, err = tx.ExecContext(ctx, "UPDATE wb_settings SET body=? WHERE id=?", jsonText(cfg), key); err != nil {
		return ReplySettings{}, err
	}
	return cfg, tx.Commit()
}

type ReplyPracticeInput struct {
	ProjectID  string `json:"projectId"`
	ExerciseID string `json:"exerciseId"`
	MessageID  string `json:"messageId"`
	Text       string `json:"text"`
}

func (s *Store) AcceptReplyPractice(ctx context.Context, p Principal, in ReplyPracticeInput) (StoredMessage, error) {
	canonical := func(id string) bool {
		v, err := uuid.Parse(id)
		return err == nil && v != uuid.Nil && v.String() == id
	}
	if !canonical(in.ExerciseID) || !canonical(in.MessageID) || strings.TrimSpace(in.Text) == "" || len(in.Text) > 64000 {
		return StoredMessage{}, ErrInvalid
	}
	cfg, err := s.ReplySettings(ctx, p, in.ProjectID)
	if err != nil {
		return StoredMessage{}, err
	}
	if cfg.ConnectionID != practiceConnection(in.ProjectID) {
		return StoredMessage{}, ErrDenied
	}
	message := IncomingMessage{ConnectionID: cfg.ConnectionID, EventID: in.ExerciseID + "/" + in.MessageID,
		ConversationID: in.ExerciseID, ActorID: in.ExerciseID, DisplayName: "练习客户 · " + in.ExerciseID[:6], Text: in.Text}
	if _, err = s.EnsureReplyRoute(ctx, message); err != nil {
		return StoredMessage{}, err
	}
	return s.AcceptMessage(ctx, message)
}

// FinishUnansweredIncoming keeps a failed exercise turn separate from an
// explicit handoff. A model outage must not pause the simulated conversation.
func (s *Store) FinishUnansweredIncoming(ctx context.Context, m StoredMessage, failure string) error {
	if !IsReplyPracticeConnection(m.ConnectionID) {
		return s.MessageDisposition(ctx, m.Principal, m.ConnectionID, m.EventID, "handoff")
	}
	switch failure {
	case "MISSING_CREDENTIAL", "AUTH", "INVALID_CREDENTIAL", "QUOTA", "ACCOUNT_QUOTA", "SERVER", "TRANSPORT", "TIMEOUT", "RATE_LIMIT":
	default:
		failure = "no_reply"
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE wb_messages SET body=JSON_SET(body,'$.state','practice_failed','$.failure',?)
		WHERE connection_id=? AND event_id=? AND message_state='received'`, failure, m.ConnectionID, m.EventID)
	return err
}

func (s *Store) InstallReplyPracticeCorpus(ctx context.Context, owner Principal, root, project, text string) (Resource, error) {
	cfg, err := s.ReplySettings(ctx, owner, project)
	if err != nil {
		return Resource{}, err
	}
	if cfg.ConnectionID != practiceConnection(project) || strings.TrimSpace(text) == "" || len(text) > 16384 {
		return Resource{}, ErrDenied
	}
	id := "vault-" + uuid.NewSHA1(uuid.NameSpaceURL, []byte("classroom-example/"+project)).String()
	r, err := s.Resource(ctx, id)
	if IsMissing(err) {
		r, err = s.createVault(ctx, owner, root, id, "课堂示例语料", "", vaultInitial{
			notes: map[string]string{"蓝松方案.md": text},
		})
	}
	if err != nil {
		return r, err
	}
	if r.Relation != VaultRelation {
		return Resource{}, ErrConflict
	}
	perms, err := s.ResourcePermissions(ctx, owner, r.ID)
	if err != nil {
		return r, err
	}
	for _, grant := range perms.Grants {
		if grant.ProjectID == project {
			return r, nil
		}
	}
	err = s.SetGrant(ctx, owner, Grant{ResourceID: r.ID, ProjectID: project, Sections: []string{vaultSection}})
	return r, err
}
