package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
)

const LivePolicyVersion = "business-live"

type ResourceAccess struct {
	ProjectID string   `json:"projectId"`
	Sections  []string `json:"sections"`
	Mode      string   `json:"mode"`
}

type ResourcePermissions struct {
	ResourceID string           `json:"resourceId"`
	Grants     []ResourceAccess `json:"grants"`
	Revision   string           `json:"revision"`
}

type ResourceDiscovery struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Description string    `json:"description,omitempty"`
	Access      string    `json:"access"`
	Version     string    `json:"currentVersion,omitempty"`
	Sections    []Section `json:"sections,omitempty"`
	Editable    bool      `json:"editable,omitempty"`
}

type resourceQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func resourceAccessKey(id string) string { return "resource-managers-" + Digest(id) }

func resourceManagers(ctx context.Context, q resourceQuery, id string) (map[string]bool, error) {
	v, err := scanJSON[map[string]bool](q.QueryRowContext(ctx, "SELECT body FROM wb_settings WHERE id=?", resourceAccessKey(id)))
	if IsMissing(err) {
		return map[string]bool{}, nil
	}
	return v, err
}

func writeResourceManagers(ctx context.Context, tx *sql.Tx, id string, v map[string]bool) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO wb_settings(id,body) VALUES(?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", resourceAccessKey(id), jsonText(v))
	return err
}

func seedResourceGrants(ctx context.Context, tx *sql.Tx, r Resource) error {
	managers := map[string]bool{}
	for _, project := range []string{r.OwnerProject, r.MaintainerProject} {
		if project == "" {
			continue
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM wb_projects WHERE id=?)", project).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO wb_grants(resource_id,project_id,section_id) VALUES(?,?,'*')", r.ID, project); err != nil {
			return err
		}
		if r.Relation != BusinessPolicyRelation && r.Relation != OperationsExpertRelation {
			managers[project] = true
		}
	}
	if len(managers) == 0 {
		return nil
	}
	return writeResourceManagers(ctx, tx, r.ID, managers)
}

func (s *Store) migrateResourceGrants(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "INSERT IGNORE INTO wb_settings(id,body) VALUES('resource-access-migrated','{}')")
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT body FROM wb_resources WHERE owner_project<>'' OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.maintainerProject'))<>''")
	if err != nil {
		return err
	}
	var resources []Resource
	for rows.Next() {
		r, err := scanJSON[Resource](rows)
		if err != nil {
			rows.Close()
			return err
		}
		resources = append(resources, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range resources {
		if r.Relation == BusinessPolicyRelation && r.Description == "" {
			r.Description = "现行业务口径，包含商品价格、直播回放、问卷和活动等对客信息及有效期。个人订单与权益请查用户背景资料。"
			if _, err = tx.ExecContext(ctx, "UPDATE wb_resources SET body=?,revision=revision+1 WHERE id=?", jsonText(r), r.ID); err != nil {
				return err
			}
		}
		if err = seedResourceGrants(ctx, tx, r); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func loadResourcePermissions(ctx context.Context, q resourceQuery, id string) (ResourcePermissions, error) {
	out := ResourcePermissions{ResourceID: id, Grants: []ResourceAccess{}}
	managers, err := resourceManagers(ctx, q, id)
	if err != nil {
		return out, err
	}
	rows, err := q.QueryContext(ctx, "SELECT project_id,section_id FROM wb_grants WHERE resource_id=? ORDER BY project_id,section_id", id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var project, section string
		if err = rows.Scan(&project, &section); err != nil {
			return out, err
		}
		i := len(out.Grants) - 1
		if i < 0 || out.Grants[i].ProjectID != project {
			mode := "read"
			if managers[project] {
				mode = "manage"
			}
			out.Grants = append(out.Grants, ResourceAccess{ProjectID: project, Mode: mode})
			i++
		}
		out.Grants[i].Sections = append(out.Grants[i].Sections, section)
	}
	out.Revision = Digest(out.Grants)
	return out, rows.Err()
}

func (s *Store) ResourcePermissions(ctx context.Context, p Principal, id string) (ResourcePermissions, error) {
	if !p.IsOwner() {
		return ResourcePermissions{}, ErrDenied
	}
	if _, err := s.Resource(ctx, id); err != nil {
		return ResourcePermissions{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return ResourcePermissions{}, err
	}
	defer tx.Rollback()
	out, err := loadResourcePermissions(ctx, tx, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// The resource row serializes ACL changes with content writes and publishing.
func (s *Store) changeResourcePermissions(ctx context.Context, id string, change func(ResourcePermissions) ([]ResourceAccess, error)) (ResourcePermissions, error) {
	if isPersonalSource(id) {
		return ResourcePermissions{}, ErrDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ResourcePermissions{}, err
	}
	defer tx.Rollback()
	r, err := scanJSON[Resource](tx.QueryRowContext(ctx, "SELECT body FROM wb_resources WHERE id=? FOR UPDATE", id))
	if err != nil {
		return ResourcePermissions{}, err
	}
	old, err := loadResourcePermissions(ctx, tx, id)
	if err != nil {
		return old, err
	}
	grants, err := change(old)
	if err != nil {
		return old, err
	}
	sections := map[string]bool{"*": true}
	for _, section := range r.Sections {
		sections[section.ID] = true
	}
	seen := map[string]bool{}
	managers := map[string]bool{}
	if _, err = tx.ExecContext(ctx, "DELETE FROM wb_grants WHERE resource_id=?", id); err != nil {
		return old, err
	}
	for _, g := range grants {
		if !ValidID(g.ProjectID) || seen[g.ProjectID] || len(g.Sections) == 0 || len(g.Sections) > len(sections) || (g.Mode != "read" && g.Mode != "manage") {
			return old, ErrInvalid
		}
		seen[g.ProjectID] = true
		used := map[string]bool{}
		for _, section := range g.Sections {
			if !sections[section] || used[section] {
				return old, ErrInvalid
			}
			used[section] = true
			if _, err = tx.ExecContext(ctx, "INSERT INTO wb_grants(resource_id,project_id,section_id) VALUES(?,?,?)", id, g.ProjectID, section); err != nil {
				return old, err
			}
		}
		if g.Mode == "manage" {
			managers[g.ProjectID] = true
		}
	}
	if err = writeResourceManagers(ctx, tx, id, managers); err != nil {
		return old, err
	}
	out, err := loadResourcePermissions(ctx, tx, id)
	if err != nil {
		return old, err
	}
	return out, tx.Commit()
}

func (s *Store) PutResourcePermissions(ctx context.Context, p Principal, in ResourcePermissions) (ResourcePermissions, error) {
	if !p.IsOwner() {
		return ResourcePermissions{}, ErrDenied
	}
	return s.changeResourcePermissions(ctx, in.ResourceID, func(old ResourcePermissions) ([]ResourceAccess, error) {
		if in.Revision != old.Revision {
			return nil, ErrConflict
		}
		return in.Grants, nil
	})
}

func (s *Store) SetResourceGrant(ctx context.Context, p Principal, id string, grant ResourceAccess) (ResourcePermissions, error) {
	if !p.IsOwner() || !ValidID(grant.ProjectID) {
		return ResourcePermissions{}, ErrDenied
	}
	return s.changeResourcePermissions(ctx, id, func(old ResourcePermissions) ([]ResourceAccess, error) {
		out := make([]ResourceAccess, 0, len(old.Grants)+1)
		for _, g := range old.Grants {
			if g.ProjectID != grant.ProjectID {
				out = append(out, g)
			} else if grant.Mode == "" {
				grant.Mode = g.Mode
			}
		}
		if len(grant.Sections) > 0 {
			if grant.Mode == "" {
				grant.Mode = "read"
			}
			out = append(out, grant)
		}
		return out, nil
	})
}

func hasResourceSections(ctx context.Context, q resourceQuery, project, id string, sections []Section) (bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT section_id FROM wb_grants WHERE resource_id=? AND project_id=?", id, project)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var section string
		if err = rows.Scan(&section); err != nil {
			return false, err
		}
		have[section] = true
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	if have["*"] {
		return true, nil
	}
	for _, section := range sections {
		if !have[section.ID] {
			return false, nil
		}
	}
	return len(sections) > 0, nil
}

func (s *Store) CheckResourceWrite(ctx context.Context, p Principal, id string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if isPersonalSource(id) {
		return ErrDenied
	}
	r, err := s.Resource(ctx, id)
	if err != nil {
		return err
	}
	return s.resourceWriteAllowed(ctx, s.DB, p, r)
}

func (s *Store) resourceWriteAllowed(ctx context.Context, q resourceQuery, p Principal, r Resource) error {
	if p.IsOwner() {
		return nil
	}
	if p.Kind == "personal" {
		return s.checkLibraryPersonal(ctx, q, p)
	}
	if p.Kind != "project" {
		return ErrDenied
	}
	var customer bool
	err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM wb_message_routes WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.principal.sessionId'))=?)", p.SessionID).Scan(&customer)
	if err != nil {
		return err
	}
	if customer {
		return ErrDenied
	}
	managers, err := resourceManagers(ctx, q, r.ID)
	if err != nil {
		return err
	}
	if !managers[p.ProjectID] {
		return ErrDenied
	}
	ok, err := hasResourceSections(ctx, q, p.ProjectID, r.ID, r.Sections)
	if err != nil {
		return err
	}
	if !ok {
		return ErrDenied
	}
	return nil
}

func (s *Store) checkLibraryPersonal(ctx context.Context, q resourceQuery, p Principal) error {
	if p.Kind != "personal" || p.ProjectID != "" {
		return ErrDenied
	}
	var config struct {
		SessionID string `json:"sessionId"`
	}
	v, err := scanJSON[struct {
		SessionID string `json:"sessionId"`
	}](q.QueryRowContext(ctx, "SELECT body FROM wb_settings WHERE id='personal'"))
	if err != nil && !IsMissing(err) {
		return err
	}
	config = v
	if config.SessionID == "" || config.SessionID != p.SessionID {
		return ErrDenied
	}
	return nil
}

func (s *Store) CheckLibraryPersonal(ctx context.Context, p Principal) error {
	return s.checkLibraryPersonal(ctx, s.DB, p)
}

func (s *Store) snapshotWriteAllowed(ctx context.Context, q resourceQuery, p Principal, r Resource) error {
	if r.Relation == OperationsExpertRelation && !p.IsOwner() {
		return ErrDenied
	}

	if r.Relation == VaultRelation {
		return ErrDenied
	}
	if !p.IsOwner() && (r.NativeEditable || r.Relation == BusinessPolicyRelation || isAttachmentResource(r)) {
		return ErrDenied
	}
	return s.resourceWriteAllowed(ctx, q, p, r)
}
func (s *Store) lockSnapshotWrite(ctx context.Context, tx *sql.Tx, p Principal, id string) (Resource, error) {
	r, err := s.lockResourceWrite(ctx, tx, p, id)
	if err != nil {
		return r, err
	}
	if r.Relation == VaultRelation || (!p.IsOwner() && (r.NativeEditable || r.Relation == BusinessPolicyRelation || isAttachmentResource(r))) {
		return r, ErrDenied
	}
	return r, nil
}

func (s *Store) lockResourceWrite(ctx context.Context, tx *sql.Tx, p Principal, id string) (Resource, error) {
	r, err := scanJSON[Resource](tx.QueryRowContext(ctx, "SELECT body FROM wb_resources WHERE id=? FOR UPDATE", id))
	if err != nil {
		return r, err
	}
	return r, s.resourceWriteAllowed(ctx, tx, p, r)
}

func resourceKind(r Resource, connectionKind string) string {
	if r.Kind != "" {
		return r.Kind
	}
	if connectionKind == "directory" || strings.Contains(r.Name, "语料") {
		return "corpus"
	}
	for _, section := range r.Sections {
		for _, field := range section.Fields {
			if field == "markdown" {
				return "corpus"
			}
		}
	}
	return "database"
}

func (s *Store) ResourceDirectory(ctx context.Context, p Principal) ([]ResourceDiscovery, error) {
	if p.Kind == "project" {
		if _, err := s.SessionRoute(ctx, p); !IsMissing(err) {
			if err == nil {
				err = ErrDenied
			}
			return nil, err
		}
	} else if !p.IsOwner() {
		if err := s.CheckLibraryPersonal(ctx, p); err != nil {
			return nil, err
		}
	}
	readable, err := s.Resources(ctx, p)
	if err != nil {
		return nil, err
	}
	byID := map[string]Resource{}
	for _, r := range readable {
		byID[r.ID] = r
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT r.body,c.body FROM wb_resources r LEFT JOIN wb_connections c ON c.id=JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.connectionId')) ORDER BY r.id")
	if err != nil {
		return nil, err
	}
	type entry struct {
		resource Resource
		source   Connection
	}
	var entries []entry
	for rows.Next() {
		var raw, source []byte
		if err = rows.Scan(&raw, &source); err != nil {
			rows.Close()
			return nil, err
		}
		var v entry
		if err = json.Unmarshal(raw, &v.resource); err != nil {
			rows.Close()
			return nil, err
		}
		if len(source) > 0 {
			if err = json.Unmarshal(source, &v.source); err != nil {
				rows.Close()
				return nil, err
			}
		}
		entries = append(entries, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []ResourceDiscovery{}
	for _, entry := range entries {
		r := entry.resource
		if isPersonalSource(r.ID) || isAttachmentResource(r) {
			continue
		}
		kind := resourceKind(r, entry.source.Kind)
		item := ResourceDiscovery{ID: r.ID, Name: r.Name, Kind: kind, Description: r.Description, Access: "none"}
		if v, ok := byID[r.ID]; ok {
			item.Access, item.Version = "read", v.CurrentVersion
			if r.Relation == BusinessPolicyRelation {
				item.Version = ""
				item.Editable = false
			}
			for _, section := range v.Sections {
				item.Sections = append(item.Sections, Section{ID: section.ID, Name: section.Name, Fields: section.Fields})
			}
			manage := p.Kind == "personal" || p.IsOwner()
			if p.Kind == "project" {
				managers, err := resourceManagers(ctx, s.DB, r.ID)
				if err != nil {
					return nil, err
				}
				manage = managers[p.ProjectID]
			}
			if manage {
				item.Access = "manage"
				item.Editable = (r.NativeEditable || r.Relation == BusinessPolicyRelation) && len(v.Sections) == len(r.Sections)
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func isAttachmentResource(r Resource) bool {
	for _, section := range r.Sections {
		for _, f := range section.Fields {
			if f == "attachmentId" && section.ID == "content" {
				return true
			}
		}
	}
	return false
}

type LibraryDocument struct {
	URI   string `json:"uri"`
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
}
type LibraryDocuments struct {
	External  bool              `json:"external"`
	Documents []LibraryDocument `json:"documents,omitempty"`
	Content   string            `json:"content,omitempty"`
	Version   string            `json:"version,omitempty"`
}

// Hold shared locks on source resources until a derived write commits. ACL
// changes take an exclusive lock on the same row and cannot race publication.
type resourceTransactionGate struct{ tx *sql.Tx }

func (g resourceTransactionGate) Resource(ctx context.Context, id string) (Resource, error) {
	return scanJSON[Resource](g.tx.QueryRowContext(ctx, "SELECT body FROM wb_resources WHERE id=? FOR SHARE", id))
}
func (g resourceTransactionGate) Snapshot(ctx context.Context, id, version string) (Snapshot, error) {
	return readSnapshot(ctx, g.tx, id, version)
}
func (g resourceTransactionGate) Granted(ctx context.Context, id, project, section string) (bool, error) {
	var allowed bool
	err := g.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM wb_grants WHERE resource_id=? AND project_id=? AND section_id IN ('*',?))", id, project, section).Scan(&allowed)
	return allowed, err
}
