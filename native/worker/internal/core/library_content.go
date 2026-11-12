package core

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

type LibraryInput struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Description string   `json:"description,omitempty"`
	Columns     []string `json:"columns,omitempty"`
}

type LibraryChange struct {
	ResourceID   string         `json:"resourceId"`
	OperationKey string         `json:"operationKey"`
	Action       string         `json:"action"`
	ItemID       string         `json:"itemId,omitempty"`
	Fields       map[string]any `json:"fields,omitempty"`
}

type LibraryReceipt struct {
	ResourceID string `json:"resourceId"`
	ItemID     string `json:"itemId"`
	Action     string `json:"action"`
	Version    string `json:"version,omitempty"`
}

func validLibraryColumn(s string) bool {
	if strings.TrimSpace(s) != s || s == "" || utf8.RuneCountInString(s) > 60 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func libraryID(prefix, scope string) string {
	return prefix + "-" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(scope)).String()
}

func (s *Store) CreateLibrary(ctx context.Context, p Principal, commandID string, in LibraryInput) (ResourceDiscovery, error) {
	var out ResourceDiscovery
	if err := s.CheckLibraryPersonal(ctx, p); err != nil {
		return out, err
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 100 || (in.Kind != "database" && in.Kind != "corpus") || len(in.Columns) > 40 {
		return out, ErrInvalid
	}
	columns := in.Columns
	if in.Kind == "corpus" {
		if len(columns) > 0 {
			return out, ErrInvalid
		}
		columns = []string{"title", "markdown"}
	} else if len(columns) == 0 {
		columns = []string{"名称", "内容"}
	}
	seen := map[string]bool{}
	for _, c := range columns {
		if !validLibraryColumn(c) || seen[c] {
			return out, ErrInvalid
		}
		seen[c] = true
	}
	id := libraryID("library", p.SessionID+"/"+commandID)
	version := libraryID("initial", id)
	r := Resource{ID: id, Kind: in.Kind, Name: in.Name, Description: in.Description, NativeEditable: true, Sections: []Section{{ID: "main", Name: "内容", Fields: columns}}}
	if err := r.Validate(); err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "INSERT IGNORE INTO wb_resources(id,owner_project,current_version,body) VALUES(?,'',?,?)", id, version, jsonText(r))
	if err != nil {
		return out, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return out, err
	}
	if n == 0 {
		existing, err := scanJSON[Resource](tx.QueryRowContext(ctx, "SELECT body FROM wb_resources WHERE id=? FOR UPDATE", id))
		if err != nil {
			return out, err
		}
		if Digest(existing) != Digest(r) {
			return out, ErrConflict
		}
	} else {
		snap := Snapshot{ResourceID: id, Version: version, Sections: r.Sections, State: "published", Dependencies: []Ref{}}
		if err = insertLibrarySnapshot(ctx, tx, snap); err != nil {
			return out, err
		}
	}
	out = ResourceDiscovery{ID: id, Name: r.Name, Description: r.Description, Kind: r.Kind, Access: "manage", Version: version, Editable: true, Sections: []Section{{ID: "main", Name: "内容"}}}
	return out, tx.Commit()
}

func insertLibrarySnapshot(ctx context.Context, tx *sql.Tx, v Snapshot) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO wb_snapshots(resource_id,version,previous_version,state,record_count,published_at,body) VALUES(?,?,?,'published',?,UTC_TIMESTAMP(6),?)", v.ResourceID, v.Version, v.Previous, v.Count, jsonText(v))
	return err
}

func (s *Store) DescribeLibrary(ctx context.Context, p Principal, id, description string) (ResourceDiscovery, error) {
	if !p.IsOwner() {
		if err := s.CheckLibraryPersonal(ctx, p); err != nil {
			return ResourceDiscovery{}, err
		}
	}
	return s.updateLibraryMetadata(ctx, p, id, nil, description)
}

func (s *Store) UpdateLibraryMetadata(ctx context.Context, p Principal, id, name, description string) (ResourceDiscovery, error) {
	if !p.IsOwner() {
		return ResourceDiscovery{}, ErrDenied
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ResourceDiscovery{}, ErrInvalid
	}
	return s.updateLibraryMetadata(ctx, p, id, &name, description)
}

func (s *Store) updateLibraryMetadata(ctx context.Context, p Principal, id string, name *string, description string) (ResourceDiscovery, error) {
	var out ResourceDiscovery
	if isPersonalSource(id) || (name == nil && strings.HasPrefix(id, "attachment-")) {
		return out, ErrDenied
	}
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > 500 {
		return out, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	r, err := s.lockResourceWrite(ctx, tx, p, id)
	if err != nil {
		return out, err
	}
	if name == nil && isAttachmentResource(r) {
		return out, ErrDenied
	}
	saveKind := name != nil && r.Kind == "" && strings.Contains(r.Name, "语料") != strings.Contains(*name, "语料")
	if saveKind {
		// Keep a legacy library in its current tab even if its name no longer
		// matches the old name-based classification.
		var connectionKind string
		if r.ConnectionID != "" {
			err := tx.QueryRowContext(ctx, `SELECT JSON_UNQUOTE(JSON_EXTRACT(body,'$.kind')) FROM wb_connections WHERE id=?`, r.ConnectionID).Scan(&connectionKind)
			if err != nil && !IsMissing(err) {
				return out, err
			}
		}
		r.Kind = resourceKind(r, connectionKind)
	}
	if name != nil {
		r.Name = *name
	}
	statement := `UPDATE wb_resources SET body=JSON_SET(body,'$.name',?,'$.description',?),revision=revision+1 WHERE id=?`
	args := []any{r.Name, description, id}
	if saveKind {
		statement = `UPDATE wb_resources SET body=JSON_SET(body,'$.name',?,'$.description',?,'$.kind',?),revision=revision+1 WHERE id=?`
		args = []any{r.Name, description, r.Kind, id}
	}
	if _, err = tx.ExecContext(ctx, statement, args...); err != nil {
		return out, err
	}
	return ResourceDiscovery{ID: id, Name: r.Name, Kind: r.Kind, Description: description, Access: "manage"}, tx.Commit()
}

func (s *Store) ChangeLibraryItem(ctx context.Context, p Principal, in LibraryChange) (LibraryReceipt, error) {
	out := LibraryReceipt{ResourceID: in.ResourceID, ItemID: in.ItemID, Action: in.Action}
	if !ValidID(in.OperationKey) || !ValidID(in.ResourceID) {
		return out, ErrInvalid
	}
	switch in.Action {
	case "create":
		if in.ItemID != "" || len(in.Fields) == 0 {
			return out, ErrInvalid
		}
		out.ItemID = libraryID("item", p.SessionID+"/"+in.ResourceID+"/"+in.OperationKey)
	case "update":
		if !ValidID(in.ItemID) || len(in.Fields) == 0 {
			return out, ErrInvalid
		}
	case "delete":
		if !ValidID(in.ItemID) || len(in.Fields) > 0 {
			return out, ErrInvalid
		}
	default:
		return out, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	r, err := s.lockResourceWrite(ctx, tx, p, in.ResourceID)
	if err != nil {
		return out, err
	}
	if r.Relation == BusinessPolicyRelation {
		if in.Action == "create" {
			out.ItemID = strings.TrimPrefix(out.ItemID, "item-")
		}
		if in.Action == "delete" {
			out.Action = "disable"
		}
		err = changeBusinessPolicy(ctx, tx, out, in.Fields)
		if err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	if !r.NativeEditable || r.ConnectionID != "" || r.Relation != "" || len(r.Sections) != 1 {
		return out, ErrDenied
	}
	var current string
	if err = tx.QueryRowContext(ctx, "SELECT current_version FROM wb_resources WHERE id=?", r.ID).Scan(&current); err != nil {
		return out, err
	}
	if current == "" {
		return out, ErrConflict
	}
	out.Version = libraryID("edit", p.SessionID+"/"+r.ID+"/"+in.OperationKey)
	watermark := Digest(in)
	old, err := scanJSON[Snapshot](tx.QueryRowContext(ctx, "SELECT body FROM wb_snapshots WHERE resource_id=? AND version=?", r.ID, out.Version))
	if err == nil {
		if old.SourceWatermark != watermark {
			return out, ErrConflict
		}
		return out, tx.Commit()
	}
	if !IsMissing(err) {
		return out, err
	}
	var priorCount int
	if err = tx.QueryRowContext(ctx, "SELECT record_count FROM wb_snapshots WHERE resource_id=? AND version=? AND state='published'", r.ID, current).Scan(&priorCount); err != nil {
		return out, err
	}
	data, err := scanJSON[map[string]any](tx.QueryRowContext(ctx, "SELECT body FROM wb_records WHERE resource_id=? AND version=? AND item_id=?", r.ID, current, out.ItemID))
	if in.Action == "create" {
		if !IsMissing(err) {
			if err == nil {
				err = ErrConflict
			}
			return out, err
		}
		data = map[string]any{}
		priorCount++
	} else if err != nil {
		return out, err
	} else if in.Action == "delete" {
		priorCount--
	}
	if in.Action != "delete" {
		allowed := map[string]bool{}
		for _, field := range r.Sections[0].Fields {
			allowed[field] = true
		}
		for k, v := range in.Fields {
			if !allowed[k] {
				return out, fmt.Errorf("%w: unknown column %q", ErrInvalid, k)
			}
			data[k] = v
		}
		if r.Kind == "corpus" {
			for _, k := range []string{"title", "markdown"} {
				v, ok := data[k].(string)
				if !ok || strings.TrimSpace(v) == "" {
					return out, ErrInvalid
				}
			}
		}
		if err = ValidateRecords([]Record{{ID: out.ItemID, Fields: data}}); err != nil {
			return out, err
		}
	}
	snap := Snapshot{ResourceID: r.ID, Version: out.Version, Previous: current, Sections: r.Sections, State: "published", Count: priorCount, SourceWatermark: watermark, Dependencies: []Ref{}}
	if err = insertLibrarySnapshot(ctx, tx, snap); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO wb_records(resource_id,version,item_id,digest,body) SELECT resource_id,?,item_id,digest,body FROM wb_records WHERE resource_id=? AND version=? AND item_id<>?", out.Version, r.ID, current, out.ItemID); err != nil {
		return out, err
	}
	if in.Action != "delete" {
		if _, err = tx.ExecContext(ctx, "INSERT INTO wb_records(resource_id,version,item_id,digest,body) VALUES(?,?,?,?,?)", r.ID, out.Version, out.ItemID, Digest(data), jsonText(data)); err != nil {
			return out, err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE wb_resources SET current_version=?,revision=revision+1 WHERE id=?", out.Version, r.ID); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

type PolicyQuery struct {
	ResourceID      string `json:"resourceId"`
	Query           string `json:"query,omitempty"`
	After           string `json:"after,omitempty"`
	IncludeInactive bool   `json:"includeInactive,omitempty"`
}
type PolicyPage struct {
	Items []BusinessPolicy `json:"items"`
	Next  string           `json:"next,omitempty"`
}

func (s *Store) LibraryPolicies(ctx context.Context, p Principal, in PolicyQuery) (PolicyPage, error) {
	out := PolicyPage{Items: []BusinessPolicy{}}
	if len(in.Query) > 300 || len(in.After) > 36 {
		return out, ErrInvalid
	}
	if p.Kind == "project" {
		if _, err := s.SessionRoute(ctx, p); !IsMissing(err) {
			return out, ErrDenied
		}
	} else if err := s.CheckLibraryPersonal(ctx, p); err != nil {
		return out, err
	}
	r, err := s.businessPolicyResource(ctx, in.ResourceID)
	if err != nil {
		return out, err
	}
	if len(r.Sections) != 1 {
		return out, ErrDenied
	}
	ref := Ref{r.ID, LivePolicyVersion, r.Sections[0].ID}
	if err = Authorize(ctx, s, p, ref); err != nil {
		return out, err
	}
	manage := false
	if in.IncludeInactive {
		if err = s.CheckResourceWrite(ctx, p, r.ID); err != nil {
			return out, err
		}
		manage = true
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,topic,name,aliases,answer,enabled,valid_from,valid_until FROM wb_business_policies
 WHERE resource_id=? AND id>? AND (?='' OR LOCATE(?,CONCAT_WS(' ',topic,name,aliases))>0)
 AND (? OR (enabled AND (valid_from IS NULL OR valid_from<=UTC_TIMESTAMP(6)) AND (valid_until IS NULL OR valid_until>UTC_TIMESTAMP(6))))
 ORDER BY id LIMIT 9`, r.ID, in.After, in.Query, in.Query, manage)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(out.Items) == 8 {
			out.Next = out.Items[7].ID
			break
		}
		var row BusinessPolicy
		if err = rows.Scan(&row.ID, &row.Topic, &row.Name, &row.Aliases, &row.Answer, &row.Enabled, &row.ValidFrom, &row.ValidUntil); err != nil {
			return PolicyPage{}, err
		}
		out.Items = append(out.Items, row)
	}
	if err = rows.Err(); err != nil {
		return PolicyPage{}, err
	}
	rows.Close()
	if manage {
		if err = s.CheckResourceWrite(ctx, p, r.ID); err != nil {
			return PolicyPage{}, err
		}
	}
	if err = Authorize(ctx, s, p, ref); err != nil {
		return PolicyPage{}, err
	}
	if err = s.RememberRead(ctx, p, []Ref{ref}); err != nil {
		return PolicyPage{}, err
	}
	return out, nil
}

func changeBusinessPolicy(ctx context.Context, tx *sql.Tx, out LibraryReceipt, fields map[string]any) error {
	var v BusinessPolicy
	v.ID = out.ItemID
	if _, err := uuid.Parse(v.ID); err != nil {
		return ErrInvalid
	}
	scan := tx.QueryRowContext(ctx, "SELECT id,topic,name,aliases,answer,enabled,valid_from,valid_until FROM wb_business_policies WHERE resource_id=? AND id=? FOR UPDATE", out.ResourceID, v.ID).Scan(&v.ID, &v.Topic, &v.Name, &v.Aliases, &v.Answer, &v.Enabled, &v.ValidFrom, &v.ValidUntil)
	if out.Action == "create" {
		if scan != nil && !IsMissing(scan) {
			return scan
		}
		v = BusinessPolicy{ID: out.ItemID, Enabled: true}
	} else if scan != nil {
		return scan
	}
	if out.Action == "disable" {
		v.Enabled = false
	}
	for k, value := range fields {
		switch k {
		case "topic", "name", "aliases", "answer":
			x, ok := value.(string)
			if !ok {
				return ErrInvalid
			}
			switch k {
			case "topic":
				v.Topic = x
			case "name":
				v.Name = x
			case "aliases":
				v.Aliases = x
			case "answer":
				v.Answer = x
			}
		case "enabled":
			x, ok := value.(bool)
			if !ok {
				return ErrInvalid
			}
			v.Enabled = x
		case "validFrom", "validUntil":
			var point *time.Time
			if value != nil {
				x, ok := value.(string)
				if !ok {
					return ErrInvalid
				}
				t, err := time.Parse(time.RFC3339Nano, x)
				if err != nil {
					return ErrInvalid
				}
				point = &t
			}
			if k == "validFrom" {
				v.ValidFrom = point
			} else {
				v.ValidUntil = point
			}
		default:
			return ErrInvalid
		}
	}
	if out.Action == "create" && scan == nil {
		var old BusinessPolicy
		if err := tx.QueryRowContext(ctx, "SELECT id,topic,name,aliases,answer,enabled,valid_from,valid_until FROM wb_business_policies WHERE resource_id=? AND id=?", out.ResourceID, v.ID).Scan(&old.ID, &old.Topic, &old.Name, &old.Aliases, &old.Answer, &old.Enabled, &old.ValidFrom, &old.ValidUntil); err != nil {
			return err
		}
		normalized, err := normalizeBusinessPolicy(v)
		if err != nil {
			return err
		}
		if Digest(old) != Digest(normalized) {
			return ErrConflict
		}
		return nil
	}
	_, err := saveBusinessPolicy(ctx, tx, out.ResourceID, v, out.Action == "create")
	return err
}
