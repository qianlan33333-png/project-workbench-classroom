package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const replyActivityBudget = 60

// The reference remains stable across versions and contains no message data.
var replyActivitySource = Ref{"reply-inbound-ledger", "live-activity", "personal-only"}

func (s *Store) ensureReplyActivitySource(ctx context.Context) error {
	ref := replyActivitySource
	sections := []Section{{ID: ref.Section, Name: "本人来源", Fields: []string{"source"}}}
	resource := Resource{ID: ref.ResourceID, Name: "自动化应答 · 本人来源", Sections: sections}
	snapshot := Snapshot{ResourceID: ref.ResourceID, Version: ref.Version, Sections: sections, State: "published"}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO wb_resources(id,current_version,body) VALUES(?,?,?)
		ON DUPLICATE KEY UPDATE id=id`, ref.ResourceID, ref.Version, jsonText(resource)); err != nil {
		return err
	}
	stored, err := scanJSON[Resource](tx.QueryRowContext(ctx,
		`SELECT body FROM wb_resources WHERE id=? AND owner_project='' AND current_version=?
		AND NOT EXISTS (SELECT 1 FROM wb_grants WHERE resource_id=wb_resources.id)`, ref.ResourceID, ref.Version))
	if err != nil || Digest(stored) != Digest(resource) {
		if err == nil || IsMissing(err) {
			return fmt.Errorf("%w: reserved reply activity resource differs", ErrConflict)
		}
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO wb_snapshots(resource_id,version,previous_version,state,body,published_at)
		VALUES(?,?,'','published',?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE version=version`,
		ref.ResourceID, ref.Version, jsonText(snapshot)); err != nil {
		return err
	}
	storedSnapshot, err := scanJSON[Snapshot](tx.QueryRowContext(ctx,
		`SELECT body FROM wb_snapshots WHERE resource_id=? AND version=? AND state='published' AND record_count=0`,
		ref.ResourceID, ref.Version))
	if err != nil || Digest(storedSnapshot) != Digest(snapshot) {
		if err == nil || IsMissing(err) {
			return fmt.Errorf("%w: reserved reply activity snapshot differs", ErrConflict)
		}
		return err
	}
	return tx.Commit()
}

type ReplyActivity struct {
	Source            string              `json:"source"`
	Project           string              `json:"project"`
	From              time.Time           `json:"from"`
	To                time.Time           `json:"to"`
	Inbound           int                 `json:"inbound"`
	Conversations     int                 `json:"conversations"`
	WithText          int                 `json:"withText"`
	TextConversations int                 `json:"textConversations"`
	ByState           map[string]int      `json:"byState"`
	Coverage          string              `json:"coverage"`
	Texts             []ReplyActivityText `json:"texts"`
}

type ReplyActivityText struct {
	Conversation int    `json:"conversation"`
	Text         string `json:"text"`
}

// ReadReplyActivity reads the already-routed inbound ledger at a consistent
// snapshot. Conversation ordinals are local to this response.
func (s *Store) ReadReplyActivity(ctx context.Context, p Principal, project string, from, to time.Time) (ReplyActivity, error) {
	out := ReplyActivity{
		Source: "workbench_auto_reply_inbound", From: from.UTC(), To: to.UTC(),
		ByState: map[string]int{}, Texts: []ReplyActivityText{},
	}
	if p.Kind != "personal" || p.Validate() != nil {
		return out, ErrDenied
	}
	if project != "" && !ValidID(project) || !to.After(from) || to.Sub(from) > 31*24*time.Hour {
		return out, fmt.Errorf("%w: supply a valid project and a time range of at most 31 days", ErrInvalid)
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	const configuredProjects = `SELECT p.id,p.body->>'$.name' FROM wb_projects p
		JOIN wb_settings s ON s.id=CONCAT('reply-',p.id)
		WHERE COALESCE(s.body->>'$.connectionId','')<>''`
	projects, err := tx.QueryContext(ctx, configuredProjects+` AND (?='' OR p.id=?) ORDER BY p.id LIMIT 2`, project, project)
	if err != nil {
		return out, err
	}
	var id string
	if projects.Next() {
		err = projects.Scan(&id, &out.Project)
	}
	multiple := projects.Next()
	if err == nil {
		err = projects.Err()
	}
	projects.Close()
	if err != nil {
		return out, err
	}
	if id == "" || multiple {
		return out, fmt.Errorf("%w: no unique automatic reply project; use project_list and supply projectId", ErrInvalid)
	}

	// Use the route index with a broad second-level bound, then apply the exact
	// half-open interval at microsecond precision. Go's JSON timestamps can
	// have no fractional suffix or up to nine fractional digits.
	const scope = ` FROM wb_message_routes r JOIN wb_messages m ON m.route_id=r.id
		WHERE r.project_id=? AND m.body->>'$.principal.projectId'=r.project_id
		AND COALESCE(m.body->>'$.kind','')<>'human'
		AND m.arrival_at>=? AND m.arrival_at<?
		AND CAST(LEFT(REPLACE(REPLACE(m.arrival_at,'T',' '),'Z',''),26) AS DATETIME(6))>=?
		AND CAST(LEFT(REPLACE(REPLACE(m.arrival_at,'T',' '),'Z',''),26) AS DATETIME(6))<?`
	const withText = `REGEXP_LIKE(COALESCE(m.body->>'$.text',''),'[^[:space:]]')`
	args := []any{id, out.From.Format("2006-01-02T15:04:05"),
		out.To.Truncate(time.Second).Add(time.Second).Format("2006-01-02T15:04:05"), out.From, out.To}
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(DISTINCT m.route_id),
		COUNT(IF(`+withText+`,1,NULL)), COUNT(DISTINCT IF(`+withText+`,m.route_id,NULL))`+scope, args...).
		Scan(&out.Inbound, &out.Conversations, &out.WithText, &out.TextConversations)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT COALESCE(m.message_state,'unknown'),COUNT(*)`+scope+` GROUP BY 1`, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var state string
		var count int
		if err = rows.Scan(&state, &count); err != nil {
			break
		}
		out.ByState[state] = count
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}

	switch {
	case out.Inbound == 0:
		out.Coverage = "no_inbound"
	case out.WithText == 0:
		out.Coverage = "no_text"
	case out.TextConversations < 2:
		out.Coverage = "insufficient_conversations"
	case out.WithText > replyActivityBudget:
		out.Coverage = "window_too_large"
	default:
		out.Coverage = "complete"
		if err = readReplyActivityTexts(ctx, tx, &out, scope+` AND `+withText, args); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	if err = s.RememberRead(ctx, p, []Ref{replyActivitySource}); err != nil {
		return out, err
	}
	return out, nil
}

func readReplyActivityTexts(ctx context.Context, tx *sql.Tx, out *ReplyActivity, scope string, args []any) error {
	rows, err := tx.QueryContext(ctx, `SELECT m.route_id,LEFT(m.body->>'$.text',301)`+scope+
		` ORDER BY m.arrival_at,m.connection_id,m.event_id LIMIT 60`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	ordinals := map[string]int{}
	for rows.Next() {
		var route, text string
		if err = rows.Scan(&route, &text); err != nil {
			return err
		}
		if ordinals[route] == 0 {
			ordinals[route] = len(ordinals) + 1
		}
		runes := []rune(text)
		if len(runes) > 300 {
			text = string(runes[:300])
			out.Coverage = "text_truncated"
		}
		out.Texts = append(out.Texts, ReplyActivityText{ordinals[route], strings.TrimSpace(text)})
	}
	return rows.Err()
}
