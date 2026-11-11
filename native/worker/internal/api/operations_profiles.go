package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"
	"workbench-native/internal/core"
)

const operationsProfileSection = "main"

type operationsProfileCursor struct {
	Version  string `json:"version"`
	Query    string `json:"query"`
	Position int    `json:"position"`
}

type operationsProfileSummary struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
}

func (a *Server) localOperationsProfiles(w http.ResponseWriter, r *http.Request, p core.Principal, resourceID string) {
	ctx := r.Context()
	resource, err := a.Store.Resource(ctx, resourceID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if resource.OwnerProject != p.ProjectID || resource.CurrentVersion == "" {
		respond(w, nil, core.ErrDenied)
		return
	}
	ref := core.Ref{ResourceID: resourceID, Version: resource.CurrentVersion, Section: operationsProfileSection}
	if err = core.Authorize(ctx, a.Store, p, ref); err != nil {
		respond(w, nil, err)
		return
	}
	q := r.URL.Query()
	if requested := q.Get("snapshot"); requested != "" && requested != ref.Version {
		respond(w, map[string]any{"state": "source_changed"}, nil)
		return
	}
	if id := q.Get("id"); id != "" {
		rows, err := a.Store.ReadIDs(ctx, p, ref, []string{id})
		if err != nil || len(rows) != 1 {
			if err == nil {
				err = sql.ErrNoRows
			}
			respond(w, nil, err)
			return
		}
		groups, ok := rows[0].Fields["groups"].([]any)
		if !ok {
			respond(w, nil, core.ErrInvalid)
			return
		}
		respond(w, map[string]any{"state": "ready", "snapshot": ref.Version, "id": id,
			"nickname": rows[0].Fields["detailNickname"], "groups": groups, "missingSources": rows[0].Fields["missingSources"]}, nil)
		return
	}
	query := strings.ToLower(strings.TrimSpace(q.Get("q")))
	if utf8.RuneCountInString(query) > 120 {
		respond(w, nil, core.ErrInvalid)
		return
	}
	after := -1
	if cursor := q.Get("after"); cursor != "" {
		var page operationsProfileCursor
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if len(cursor) > 1024 || err != nil || json.Unmarshal(raw, &page) != nil || page.Position < 0 || page.Query != query {
			respond(w, nil, core.ErrInvalid)
			return
		}
		if page.Version != ref.Version {
			respond(w, map[string]any{"state": "source_changed"}, nil)
			return
		}
		after = page.Position
	}

	condition := "resource_id=? AND version=?"
	args := []any{ref.ResourceID, ref.Version}
	if query != "" {
		// Match the original archive's normalized substring search on each
		// individual search term. The search terms never enter the response.
		condition += " AND JSON_SEARCH(body,'one',?,'!','$.search[*]') IS NOT NULL"
		literal := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(query)
		args = append(args, "%"+literal+"%")
	}
	total := 0
	if query == "" {
		snapshot, err := a.Store.Snapshot(ctx, ref.ResourceID, ref.Version)
		if err != nil {
			respond(w, nil, err)
			return
		}
		total = snapshot.Count
	} else if err = a.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM wb_records WHERE "+condition, args...).Scan(&total); err != nil {
		respond(w, nil, err)
		return
	}
	const position = "CAST(JSON_UNQUOTE(JSON_EXTRACT(body,'$.position')) AS SIGNED)"
	rows, err := a.Store.DB.QueryContext(ctx, "SELECT item_id,JSON_UNQUOTE(JSON_EXTRACT(body,'$.nickname')),"+position+
		" FROM wb_records WHERE "+condition+" AND "+position+">? ORDER BY "+position+" LIMIT 21", append(args, after)...)
	if err != nil {
		respond(w, nil, err)
		return
	}
	defer rows.Close()
	items := make([]operationsProfileSummary, 0, 20)
	next := ""
	for rows.Next() {
		var item operationsProfileSummary
		var at int
		if err = rows.Scan(&item.ID, &item.Nickname, &at); err != nil {
			respond(w, nil, err)
			return
		}
		if len(items) == 20 {
			data, _ := json.Marshal(operationsProfileCursor{Version: ref.Version, Query: query, Position: after})
			next = base64.RawURLEncoding.EncodeToString(data)
			break
		}
		items = append(items, item)
		after = at
	}
	if err = rows.Err(); err != nil {
		respond(w, nil, err)
		return
	}
	respond(w, map[string]any{"state": "ready", "snapshot": ref.Version, "total": total, "records": items, "next": next}, nil)
}
