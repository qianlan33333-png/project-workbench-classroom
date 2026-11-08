package core

import "context"

// Resolve an owner's declared resource IDs to the original incoming message's
// fixed versions. An idle project uses current published resources. Host-bound
// identity is required; this endpoint is never registered as a model tool.
func (s *Store) ResolveExternalSources(ctx context.Context, p Principal, declared []Ref) ([]Ref, error) {
	if len(declared) == 0 || len(declared) > 32 {
		return nil, ErrInvalid
	}
	out := []Ref{}
	var frozen []Ref
	object := false
	if p.Kind == "project" {
		route, e := s.SessionRoute(ctx, p)
		if e == nil {
			object = true
			if !route.Enabled {
				return nil, ErrDenied
			}
			m, e := scanJSON[StoredMessage](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE route_id=? AND message_state='received' ORDER BY arrival_at,event_id LIMIT 1`, route.ID))
			if IsMissing(e) {
				m, e = scanJSON[StoredMessage](s.DB.QueryRowContext(ctx, `SELECT body FROM wb_messages WHERE route_id=? ORDER BY arrival_at DESC,event_id DESC LIMIT 1`, route.ID))
			}
			if e != nil {
				return nil, e
			}
			frozen = m.Knowledge
		} else if !IsMissing(e) {
			return nil, e
		}
	}
	for _, ref := range declared {
		if object {
			found := false
			for _, f := range frozen {
				if f.ResourceID == ref.ResourceID && f.Section == ref.Section {
					ref = f
					found = true
					break
				}
			}
			if !found {
				return nil, ErrDenied
			}
			if e := s.CheckMessageKnowledge(ctx, p, ref); e != nil {
				return nil, e
			}
		} else {
			r, e := s.Resource(ctx, ref.ResourceID)
			if e != nil {
				return nil, e
			}
			ref.Version = r.CurrentVersion
		}
		if e := Authorize(ctx, s, p, ref); e != nil {
			return nil, e
		}
		out = append(out, ref)
	}
	return out, nil
}
