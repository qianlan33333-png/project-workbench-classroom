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
