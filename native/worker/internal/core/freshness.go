package core

import "context"

// Freshness is transitive: a current derived version may still use superseded
// source versions. Running jobs retain those versions; new reads expose stale.
func (s *Store) IsStale(ctx context.Context, ref Ref) (bool, error) {
	pending := []Ref{ref}
	seen := map[Ref]bool{}
	for len(pending) > 0 {
		next := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[next] {
			continue
		}
		seen[next] = true
		if len(seen) > MaxSourceRefs {
			return false, ErrInvalid
		}
		r, e := s.Resource(ctx, next.ResourceID)
		if e != nil {
			return false, e
		}
		if r.CurrentVersion != next.Version {
			return true, nil
		}
		snapshot, e := s.Snapshot(ctx, next.ResourceID, next.Version)
		if e != nil {
			return false, e
		}
		pending = append(pending, snapshot.Dependencies...)
	}
	return false, nil
}
