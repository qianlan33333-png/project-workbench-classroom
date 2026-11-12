package core

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
)

const GeneralExpert = "general"
const OperationsExpertRelation = "operations-expert-method"

var ErrProjectExpertRequired = errors.New("target project requires an expert default")

var expertName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func ExpertName(ref string) string {
	_, name, _ := strings.Cut(ref, ":")
	if _, tail, ok := strings.Cut(name, ":"); ok {
		return tail
	}
	return name
}

func ValidExpertRef(project, ref string) bool {
	if len(project) > 64 || !ValidID(project) {
		return false
	}
	if ref == GeneralExpert {
		return true
	}
	for _, prefix := range []string{"shared:", "project:" + project + ":"} {
		if name, ok := strings.CutPrefix(ref, prefix); ok {
			return len(name) <= 64 && expertName.MatchString(name)
		}
	}
	return false
}

func (s *Store) SessionExpert(ctx context.Context, p Principal) (string, error) {
	var ref string
	err := s.DB.QueryRowContext(ctx, `SELECT expert_ref FROM wb_sessions WHERE id=? AND kind=? AND project_id=?`, p.SessionID, p.Kind, p.ProjectID).Scan(&ref)
	return ref, err
}

func (s *Store) BindSessionExpert(ctx context.Context, p Principal, ref string) (string, error) {
	if p.Kind != "project" || strings.HasPrefix(p.SessionID, "object-") || !ValidExpertRef(p.ProjectID, ref) {
		return "", ErrDenied
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE wb_sessions SET expert_ref=? WHERE id=? AND kind='project' AND project_id=? AND expert_ref=''
		AND NOT EXISTS (SELECT 1 FROM wb_message_routes WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=?)`, ref, p.SessionID, p.ProjectID, p.SessionID)
	if err != nil {
		return "", err
	}
	actual, err := s.SessionExpert(ctx, p)
	if err == nil && actual != ref {
		err = ErrConflict
	}
	return actual, err
}

type ExpertDefault struct {
	Ref        string            `json:"expertId"`
	LegacyRefs map[string]string `json:"legacyRefs,omitempty"`
}

func projectExpertKey(project string) string { return "expert-default-" + project }

func readExpertDefault(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, project string) (ExpertDefault, error) {
	v, err := scanJSON[ExpertDefault](db.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=?`, projectExpertKey(project)))
	if IsMissing(err) {
		return ExpertDefault{}, nil
	}
	return v, err
}

func (s *Store) ProjectExpertDefault(ctx context.Context, project string) (ExpertDefault, error) {
	if !ValidID(project) || len(project) > 64 {
		return ExpertDefault{}, ErrInvalid
	}
	return readExpertDefault(ctx, s.DB, project)
}

// Use the same settings lock as new customer routes. Capture legacy sessions
// before the first change of the default, without changing reply revisions.
func (s *Store) SetProjectExpertDefault(ctx context.Context, p Principal, project, ref, expected string, legacy map[string]string) (ExpertDefault, error) {
	if !p.IsOwner() || !ValidExpertRef(project, ref) {
		return ExpertDefault{}, ErrDenied
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ExpertDefault{}, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wb_projects WHERE id=?`, project).Scan(&exists); err != nil {
		return ExpertDefault{}, err
	}
	if exists == 0 {
		return ExpertDefault{}, ErrInvalid
	}
	reply, err := scanJSON[ReplySettings](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=? FOR UPDATE`, "reply-"+project))
	isReply := err == nil
	if err != nil && !IsMissing(err) {
		return ExpertDefault{}, err
	}
	if isReply && (ref == GeneralExpert || !expertName.MatchString(reply.ExpertSkill)) {
		return ExpertDefault{}, ErrInvalid
	}
	key := projectExpertKey(project)
	if _, err = tx.ExecContext(ctx, `INSERT IGNORE INTO wb_settings(id,body) VALUES(?,?)`, key, jsonText(ExpertDefault{})); err != nil {
		return ExpertDefault{}, err
	}
	old, err := scanJSON[ExpertDefault](tx.QueryRowContext(ctx, `SELECT body FROM wb_settings WHERE id=? FOR UPDATE`, key))
	if err != nil {
		return old, err
	}
	if old.Ref != expected && old.Ref != ref {
		return old, ErrConflict
	}
	if len(legacy) > 64 || (!isReply && len(legacy) != 0) {
		return old, ErrInvalid
	}
	if old.LegacyRefs == nil {
		old.LegacyRefs = make(map[string]string)
	}
	for slug, id := range legacy {
		if !expertName.MatchString(slug) || !ValidExpertRef(project, id) || ExpertName(id) != slug {
			return old, ErrInvalid
		}
		if old.LegacyRefs[slug] == "" {
			old.LegacyRefs[slug] = id
		}
	}
	if isReply && old.Ref == "" {
		_, err = tx.ExecContext(ctx, `UPDATE wb_sessions s JOIN wb_message_routes r
			ON s.id=JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.principal.sessionId'))
			SET s.expert_ref=COALESCE((
				SELECT COALESCE(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(m.body,'$.expertRef')),''),
					CONCAT('legacy:',JSON_UNQUOTE(JSON_EXTRACT(m.body,'$.expertSkill'))))
				FROM wb_messages m WHERE m.route_id=r.id
					AND JSON_UNQUOTE(JSON_EXTRACT(m.body,'$.expertSkill')) <> ''
				ORDER BY m.arrival_at DESC,m.event_id DESC LIMIT 1
			),?)
			WHERE r.project_id=? AND s.project_id=? AND s.kind='project' AND s.expert_ref=''`,
			"legacy:"+reply.ExpertSkill, project, project)
		if err != nil {
			return old, err
		}
	}
	next := ExpertDefault{Ref: ref, LegacyRefs: old.LegacyRefs}
	if _, err = tx.ExecContext(ctx, `UPDATE wb_settings SET body=? WHERE id=?`, jsonText(next), key); err != nil {
		return old, err
	}
	return next, tx.Commit()
}

// Legacy slugs were persisted before source-scoped IDs were available. The
// first default change freezes aliases while the original sources are visible.
func (s *Store) LegacyExpertNames(ctx context.Context, p Principal, project string) ([]string, error) {
	if !p.IsOwner() || !ValidID(project) {
		return nil, ErrDenied
	}
	cfg, err := s.ReplySettings(ctx, p, project)
	if err != nil {
		return nil, err
	}
	if cfg.ExpertSkill == "" {
		return []string{}, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT JSON_UNQUOTE(JSON_EXTRACT(m.body,'$.expertSkill')) AS slug
 FROM wb_messages m WHERE connection_id=? AND JSON_UNQUOTE(JSON_EXTRACT(m.body,'$.principal.projectId'))=?
 AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(m.body,'$.expertRef')),'') IN ('',CONCAT('legacy:',JSON_UNQUOTE(JSON_EXTRACT(m.body,'$.expertSkill'))))
 AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(m.body,'$.expertSkill')),'')<>'' LIMIT 63`, cfg.ConnectionID, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{cfg.ExpertSkill}
	for rows.Next() {
		var slug string
		if err = rows.Scan(&slug); err != nil {
			return nil, err
		}
		if slug != cfg.ExpertSkill {
			out = append(out, slug)
		}
	}
	return out, rows.Err()
}

func (s *Store) ResolveLegacyExpertRef(ctx context.Context, project, ref string) (string, error) {
	slug, ok := strings.CutPrefix(ref, "legacy:")
	if !ok {
		return ref, nil
	}
	cfg, err := s.ProjectExpertDefault(ctx, project)
	if err != nil {
		return "", err
	}
	if resolved := cfg.LegacyRefs[slug]; resolved != "" {
		return resolved, nil
	}
	return ref, nil
}

type ExpertUsage struct {
	ProjectID   string `json:"projectId"`
	ExpertID    string `json:"expertId"`
	Sessions    int    `json:"sessions,omitempty"`
	Customers   int    `json:"customers,omitempty"`
	DefaultKind string `json:"defaultKind,omitempty"`
}

func (s *Store) ExpertUsage(ctx context.Context, p Principal) ([]ExpertUsage, error) {
	if !p.IsOwner() {
		return nil, ErrDenied
	}
	out := []ExpertUsage{}
	rows, err := s.DB.QueryContext(ctx, `SELECT project_id,expert_ref,SUM(LEFT(id,7)<>'object-'),SUM(LEFT(id,7)='object-')
 FROM wb_sessions WHERE kind='project' AND expert_ref<>'' GROUP BY project_id,expert_ref`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item ExpertUsage
		if err = rows.Scan(&item.ProjectID, &item.ExpertID, &item.Sessions, &item.Customers); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT SUBSTRING(d.id,16),JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.expertId')),
  IF(r.id IS NULL,'project','reply') FROM wb_settings d LEFT JOIN wb_settings r ON r.id=CONCAT('reply-',SUBSTRING(d.id,16))
  WHERE d.id LIKE 'expert-default-%' AND JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.expertId')) <> ''
  UNION ALL SELECT SUBSTRING(r.id,7),CONCAT('legacy:',JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.expertSkill'))),'reply'
  FROM wb_settings r LEFT JOIN wb_settings d ON d.id=CONCAT('expert-default-',SUBSTRING(r.id,7))
  WHERE r.id LIKE 'reply-%' AND JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.expertSkill')) <> ''
  AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.expertId')),'')=''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ExpertUsage
		if err = rows.Scan(&item.ProjectID, &item.ExpertID, &item.DefaultKind); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].ExpertID, err = s.ResolveLegacyExpertRef(ctx, out[i].ProjectID, out[i].ExpertID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
