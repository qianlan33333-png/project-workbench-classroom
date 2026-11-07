package core

import (
	"context"
	"sort"
)

func (s *Store) RememberRead(ctx context.Context, p Principal, refs []Ref) error {
	if p.IsOwner() {
		return nil
	}
	if e := p.Validate(); e != nil {
		return e
	}
	for _, ref := range refs {
		if _, e := s.DB.ExecContext(ctx, `INSERT IGNORE INTO wb_session_sources VALUES(?,?,?,?)`, p.SessionID, ref.ResourceID, ref.Version, ref.Section); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) SessionSources(ctx context.Context, p Principal) ([]Ref, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT resource_id,version,section_id FROM wb_session_sources WHERE session_id=?`, p.SessionID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Ref{}
	for rows.Next() {
		var ref Ref
		if e = rows.Scan(&ref.ResourceID, &ref.Version, &ref.Section); e != nil {
			return nil, e
		}
		out = append(out, ref)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return UnionRefs(out), nil
}
func UnionRefs(groups ...[]Ref) []Ref {
	m := map[Ref]bool{}
	for _, group := range groups {
		for _, ref := range group {
			m[ref] = true
		}
	}
	out := []Ref{}
	for ref := range m {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ResourceID+"/"+out[i].Version+"/"+out[i].Section < out[j].ResourceID+"/"+out[j].Version+"/"+out[j].Section
	})
	return out
}
func (s *Store) CheckSession(ctx context.Context, p Principal) error {
	refs, e := s.SessionSources(ctx, p)
	if e != nil {
		return e
	}
	return AuthorizeAll(ctx, s, p, refs)
}

// Reading a result or summary has the same provenance as reading its sources.
// In particular, personal-to-project delegation cannot shed this provenance.
func (s *Store) RememberRunRead(ctx context.Context, p Principal, r Run) error {
	actual, e := s.ActualRunSources(ctx, r.ID)
	if e != nil {
		return e
	}
	refs := UnionRefs(r.Sources, r.Lineage, actual)
	if e = AuthorizeAll(ctx, s, p, refs); e != nil {
		return e
	}
	return s.RememberRead(ctx, p, refs)
}
