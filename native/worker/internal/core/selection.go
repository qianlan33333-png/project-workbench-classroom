package core

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"
)

// Selection is part of the durable Run. The resource version already freezes
// its ordering and membership; retrying never chooses a new live population.
type Selection struct {
	RecordIDs []string `json:"recordIds,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	After     string   `json:"after,omitempty"`
}

func BoundedSelection(input *Selection) (*Selection, error) {
	v := Selection{Limit: 2000}
	if input == nil {
		return &v, nil
	}
	v = *input
	if v.RecordIDs != nil && len(v.RecordIDs) == 0 {
		return nil, ErrInvalid
	}
	if !utf8.ValidString(v.After) || utf8.RuneCountInString(v.After) > 191 {
		return nil, ErrInvalid
	}
	if len(v.RecordIDs) > 0 {
		if v.Limit != 0 || v.After != "" || len(v.RecordIDs) > 2000 {
			return nil, ErrInvalid
		}
		v.RecordIDs = append([]string(nil), v.RecordIDs...)
		sort.Strings(v.RecordIDs)
		for i, id := range v.RecordIDs {
			if id == "" || !utf8.ValidString(id) || utf8.RuneCountInString(id) > 191 || (i > 0 && id == v.RecordIDs[i-1]) {
				return nil, ErrInvalid
			}
		}
		return &v, nil
	}
	if v.Limit == 0 {
		v.Limit = 2000
	}
	if v.Limit < 1 || v.Limit > 2000 {
		return nil, ErrInvalid
	}
	return &v, nil
}

type SelectionInfo struct {
	SourceCount   int    `json:"sourceCount"`
	SelectedCount int    `json:"selectedCount"`
	NextAfter     string `json:"nextAfter,omitempty"`
}

func (s *Store) SelectionInfo(ctx context.Context, p Principal, ref Ref, selection *Selection) (SelectionInfo, error) {
	var info SelectionInfo
	if selection == nil {
		return info, ErrInvalid
	}
	if e := Authorize(ctx, s, p, ref); e != nil {
		return info, e
	}
	snap, e := s.Snapshot(ctx, ref.ResourceID, ref.Version)
	if e != nil {
		return info, e
	}
	info.SourceCount = snap.Count
	if len(selection.RecordIDs) > 0 {
		// Verify existence without returning record bodies into the main context.
		for offset := 0; offset < len(selection.RecordIDs); offset += 200 {
			ids := selection.RecordIDs[offset:min(offset+200, len(selection.RecordIDs))]
			args := []any{ref.ResourceID, ref.Version}
			wanted := map[string]bool{}
			for _, id := range ids {
				args = append(args, id)
				wanted[id] = true
			}
			rows, err := s.DB.QueryContext(ctx, `SELECT item_id FROM wb_records WHERE resource_id=? AND version=? AND item_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
			if err != nil {
				return info, err
			}
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					rows.Close()
					return info, err
				}
				delete(wanted, id)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return info, err
			}
			if len(wanted) > 0 {
				return info, ErrInvalid
			}
		}
		info.SelectedCount = len(selection.RecordIDs)
		return info, nil
	}
	rows, e := s.DB.QueryContext(ctx, `SELECT item_id FROM wb_records WHERE resource_id=? AND version=? AND item_id>? ORDER BY item_id LIMIT ?`, ref.ResourceID, ref.Version, selection.After, selection.Limit+1)
	if e != nil {
		return info, e
	}
	defer rows.Close()
	last := ""
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return info, e
		}
		if info.SelectedCount == selection.Limit {
			info.NextAfter = last
			break
		}
		last = id
		info.SelectedCount++
	}
	return info, rows.Err()
}
