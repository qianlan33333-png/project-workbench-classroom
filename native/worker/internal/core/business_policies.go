package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const BusinessPolicyRelation = "wb_business_policies"

var errBusinessPolicyExpired = errors.New("business policy expired before sending")

type BusinessPolicy struct {
	ID         string     `json:"id,omitempty"`
	Topic      string     `json:"topic"`
	Name       string     `json:"name"`
	Aliases    string     `json:"aliases"`
	Answer     string     `json:"answer"`
	Enabled    bool       `json:"enabled"`
	ValidFrom  *time.Time `json:"validFrom"`
	ValidUntil *time.Time `json:"validUntil"`
}

type BusinessFact struct {
	Name   string `json:"name"`
	Answer string `json:"answer"`
}

type BusinessFacts struct {
	Items []BusinessFact `json:"items"`
	More  bool           `json:"more"`
}

func (s *Store) HasBusinessPolicies(project string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var found bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM wb_resources WHERE owner_project=?
		AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.relation'))=? AND JSON_EXTRACT(body,'$.connectionId') IS NULL)`,
		project, BusinessPolicyRelation).Scan(&found)
	return err == nil && found
}

func (s *Store) businessPolicyResource(ctx context.Context, id string) (Resource, error) {
	r, err := s.Resource(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Relation != BusinessPolicyRelation || r.ConnectionID != "" || r.OwnerProject == "" {
		return r, ErrDenied
	}
	return r, nil
}

func (s *Store) ListBusinessPolicies(ctx context.Context, p Principal, id string) ([]BusinessPolicy, error) {
	if !p.IsOwner() {
		return nil, ErrDenied
	}
	if _, err := s.businessPolicyResource(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,topic,name,aliases,answer,enabled,valid_from,valid_until
		FROM wb_business_policies WHERE resource_id=? ORDER BY topic,name,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BusinessPolicy{}
	for rows.Next() {
		var v BusinessPolicy
		if err = rows.Scan(&v.ID, &v.Topic, &v.Name, &v.Aliases, &v.Answer, &v.Enabled, &v.ValidFrom, &v.ValidUntil); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) PutBusinessPolicy(ctx context.Context, p Principal, id string, v BusinessPolicy) (BusinessPolicy, error) {
	if !p.IsOwner() {
		return v, ErrDenied
	}
	if _, err := s.businessPolicyResource(ctx, id); err != nil {
		return v, err
	}
	v.Topic = strings.TrimSpace(v.Topic)
	v.Name = strings.TrimSpace(v.Name)
	v.Aliases = strings.TrimSpace(v.Aliases)
	v.Answer = strings.TrimSpace(v.Answer)
	if !ValidID(v.Topic) || len(v.Topic) > 48 || v.Name == "" || utf8.RuneCountInString(v.Name) > 160 ||
		utf8.RuneCountInString(v.Aliases) > 512 || v.Answer == "" || utf8.RuneCountInString(v.Answer) > 1200 {
		return v, ErrInvalid
	}
	if v.ValidFrom != nil {
		start := v.ValidFrom.UTC().Truncate(time.Microsecond)
		v.ValidFrom = &start
	}
	if v.ValidUntil != nil {
		end := v.ValidUntil.UTC().Truncate(time.Microsecond)
		v.ValidUntil = &end
		if v.ValidFrom != nil && !v.ValidFrom.Before(end) {
			return v, ErrInvalid
		}
	}
	if v.ID == "" {
		v.ID = uuid.NewString()
		_, err := s.DB.ExecContext(ctx, `INSERT INTO wb_business_policies
			(resource_id,id,topic,name,aliases,answer,enabled,valid_from,valid_until) VALUES (?,?,?,?,?,?,?,?,?)`,
			id, v.ID, v.Topic, v.Name, v.Aliases, v.Answer, v.Enabled, v.ValidFrom, v.ValidUntil)
		return v, err
	}
	if _, err := uuid.Parse(v.ID); err != nil {
		return v, ErrInvalid
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE wb_business_policies SET topic=?,name=?,aliases=?,answer=?,enabled=?,valid_from=?,valid_until=?
		WHERE resource_id=? AND id=?`, v.Topic, v.Name, v.Aliases, v.Answer, v.Enabled, v.ValidFrom, v.ValidUntil, id, v.ID)
	if err != nil {
		return v, err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return v, err
		}
		var exists int
		err = s.DB.QueryRowContext(ctx, `SELECT 1 FROM wb_business_policies WHERE resource_id=? AND id=?`, id, v.ID).Scan(&exists)
		return v, err
	}
	return v, nil
}

// An object may read the live public table registered to its own project.
// The policy deadline travels with the inbound message, outside model output.
func (s *Store) LookupBusinessFacts(ctx context.Context, p Principal, connection, event, id, topic, hint string) (BusinessFacts, error) {
	out := BusinessFacts{Items: []BusinessFact{}}
	if p.Kind != "project" || !ValidID(topic) || len(topic) > 48 || utf8.RuneCountInString(hint) > 160 {
		return out, ErrInvalid
	}
	m, err := s.Message(ctx, p, connection, event)
	if err != nil {
		return out, err
	}
	if m.State != "received" {
		return out, ErrDenied
	}
	route, err := s.SessionRoute(ctx, p)
	if err != nil {
		return out, err
	}
	if !route.Enabled || !route.AutoReply || route.ConnectionID != connection || route.ID != m.RouteID {
		return out, ErrDenied
	}
	r, err := s.businessPolicyResource(ctx, id)
	if err != nil {
		return out, err
	}
	if r.OwnerProject != p.ProjectID {
		return out, ErrDenied
	}
	hint = strings.Join(strings.Fields(hint), "")
	rows, err := s.DB.QueryContext(ctx, `SELECT name,answer,valid_until FROM wb_business_policies
		WHERE resource_id=? AND topic=? AND enabled
		AND (valid_from IS NULL OR valid_from<=UTC_TIMESTAMP(6))
		AND (valid_until IS NULL OR valid_until>UTC_TIMESTAMP(6))
		AND (?='' OR LOCATE(?,REPLACE(REPLACE(name,' ',''),'　',''))>0 OR LOCATE(?,REPLACE(REPLACE(aliases,' ',''),'　',''))>0)
		ORDER BY name,id LIMIT 9`, id, topic, hint, hint, hint)
	if err != nil {
		return out, err
	}
	deadline, err := readBusinessFacts(rows, &out)
	if err != nil {
		return BusinessFacts{}, err
	}
	if deadline.Valid {
		// Store the earliest expiry from any fact supplied to this turn.
		// A candidate list also uses those facts and shares the same boundary.
		stamp := messageTime(deadline.Time)
		_, err = s.DB.ExecContext(ctx, `UPDATE wb_messages SET body=JSON_SET(body,'$.businessValidUntil',
			LEAST(COALESCE(JSON_UNQUOTE(JSON_EXTRACT(body,'$.businessValidUntil')),?),?))
			WHERE connection_id=? AND event_id=? AND message_state='received'`, stamp, stamp, connection, event)
		if err != nil {
			return BusinessFacts{}, err
		}
	}
	return out, nil
}

func readBusinessFacts(rows *sql.Rows, out *BusinessFacts) (sql.NullTime, error) {
	defer rows.Close()
	var earliest sql.NullTime
	for rows.Next() {
		if len(out.Items) == 8 {
			out.More = true
			break
		}
		var fact BusinessFact
		var deadline sql.NullTime
		if err := rows.Scan(&fact.Name, &fact.Answer, &deadline); err != nil {
			return earliest, err
		}
		if deadline.Valid && (!earliest.Valid || deadline.Time.Before(earliest.Time)) {
			earliest = deadline
		}
		out.Items = append(out.Items, fact)
	}
	return earliest, rows.Err()
}
