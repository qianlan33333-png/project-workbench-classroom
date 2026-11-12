package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	ErrDenied   = errors.New("resource access denied")
	ErrConflict = errors.New("version conflict")
	ErrInvalid  = errors.New("invalid input")
	identifier  = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)
)

// Principal is supplied by the authenticated Host, never by a model argument.
type Principal struct {
	Kind      string `json:"kind"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId"`
}

func (p Principal) Validate() error {
	if !identifier.MatchString(p.SessionID) {
		return ErrDenied
	}
	if p.Kind != "owner" && p.Kind != "personal" && p.Kind != "project" {
		return ErrDenied
	}
	if p.Kind == "project" && !identifier.MatchString(p.ProjectID) {
		return ErrDenied
	}
	return nil
}
func (p Principal) IsOwner() bool { return p.Kind == "owner" }
func ValidID(s string) bool       { return identifier.MatchString(s) }

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
}
type Section struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Fields []string          `json:"fields"`
	Select map[string]string `json:"select,omitempty"`
}
type Connection struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// SecretEnv names a server environment variable; credentials never enter tool results.
	SecretEnv  string `json:"secretEnv,omitempty"`
	Location   string `json:"location,omitempty"`
	SourceTool string `json:"sourceTool,omitempty"`
}
type Resource struct {
	Kind               string         `json:"kind,omitempty"`
	Description        string         `json:"description,omitempty"`
	NativeEditable     bool           `json:"nativeEditable,omitempty"`
	VaultOrigin        *VaultOrigin   `json:"vaultOrigin,omitempty"`
	JSONSource         *JSONSource    `json:"jsonSource,omitempty"`
	MinRecords         int            `json:"minRecords,omitempty"`
	MinRetainedPercent int            `json:"minRetainedPercent,omitempty"`
	Checks             []RecordCheck  `json:"checks,omitempty"`
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	OwnerProject       string         `json:"ownerProject,omitempty"`
	MaintainerProject  string         `json:"maintainerProject,omitempty"`
	ConnectionID       string         `json:"connectionId,omitempty"`
	Relation           string         `json:"relation,omitempty"`
	KeyField           string         `json:"keyField,omitempty"`
	Sections           []Section      `json:"sections"`
	CurrentVersion     string         `json:"currentVersion,omitempty"`
	Revision           int64          `json:"revision"`
	RecordSchema       map[string]any `json:"recordSchema,omitempty"`
	TrimFields         []string       `json:"trimFields,omitempty"`
	WatermarkField     string         `json:"watermarkField,omitempty"`
}
type RecordCheck struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Section  string            `json:"section"`
	Schema   map[string]any    `json:"schema"`
	Bindings map[string]string `json:"bindings,omitempty"`
}

func (r Resource) Validate() error {
	if (r.Kind != "" && r.Kind != "database" && r.Kind != "corpus") || len([]rune(r.Description)) > 500 {
		return ErrInvalid
	}
	if r.MinRecords < 0 || r.MinRetainedPercent < 0 || r.MinRetainedPercent > 100 {
		return ErrInvalid
	}
	if r.JSONSource != nil {
		if e := r.JSONSource.validate(); e != nil {
			return e
		}
	}
	if !ValidID(r.ID) || r.Name == "" || len(r.Sections) == 0 || len(r.Sections) > 32 {
		return ErrInvalid
	}
	if r.OwnerProject != "" && !ValidID(r.OwnerProject) {
		return ErrInvalid
	}
	if r.MaintainerProject != "" && !ValidID(r.MaintainerProject) {
		return ErrInvalid
	}
	if r.WatermarkField != "" && !ValidID(r.WatermarkField) {
		return ErrInvalid
	}
	for _, f := range r.TrimFields {
		if !ValidID(f) {
			return ErrInvalid
		}
	}
	if _, e := (Method{OutputSchema: r.RecordSchema}).ResolveOutput(); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, s := range r.Sections {
		if !ValidID(s.ID) || seen[s.ID] || len(s.Fields) == 0 || len(s.Fields) > 128 {
			return ErrInvalid
		}
		seen[s.ID] = true
		for _, f := range s.Fields {
			if !ValidID(f) && !(r.NativeEditable && validLibraryColumn(f)) {
				return ErrInvalid
			}
		}
		if e := validateSelection(s); e != nil {
			return e
		}
	}
	checks := map[string]bool{}
	for _, check := range r.Checks {
		if !ValidID(check.ID) || checks[check.ID] || check.Name == "" || !seen[check.Section] || len(check.Schema) == 0 {
			return ErrInvalid
		}
		checks[check.ID] = true
		if e := validateSelection(Section{Select: check.Bindings}); e != nil {
			return e
		}
		if _, e := (Method{OutputSchema: check.Schema}).ResolveOutput(); e != nil {
			return e
		}
	}
	return nil
}

type Ref struct {
	ResourceID string `json:"resourceId"`
	Version    string `json:"version"`
	Section    string `json:"section"`
}
type RecordRef struct {
	Ref Ref    `json:"ref"`
	ID  string `json:"id"`
}

func (r Ref) Validate() error {
	if !ValidID(r.ResourceID) || !ValidID(r.Version) || !ValidID(r.Section) {
		return ErrInvalid
	}
	return nil
}

type Snapshot struct {
	ResourceID      string         `json:"resourceId"`
	Version         string         `json:"version"`
	Previous        string         `json:"previous,omitempty"`
	Sections        []Section      `json:"sections"`
	Dependencies    []Ref          `json:"dependencies"`
	State           string         `json:"state"`
	Count           int            `json:"count"`
	SourceWatermark string         `json:"sourceWatermark,omitempty"`
	VaultOperation  string         `json:"vaultOperation,omitempty"`
	VaultItemID     string         `json:"vaultItemId,omitempty"`
	RecordSchema    map[string]any `json:"recordSchema,omitempty"`
	PublishedAt     *time.Time     `json:"publishedAt,omitempty"`
}
type Record struct {
	ID     string         `json:"id"`
	Fields map[string]any `json:"fields"`
}
type Page struct {
	Dependencies []Ref      `json:"dependencies,omitempty"`
	Ref          Ref        `json:"ref"`
	Records      []Record   `json:"records"`
	Next         string     `json:"next,omitempty"`
	Count        int        `json:"count"`
	Stale        bool       `json:"stale"`
	PublishedAt  *time.Time `json:"publishedAt,omitempty"`
}
type Grant struct {
	ResourceID string   `json:"resourceId"`
	ProjectID  string   `json:"projectId"`
	Sections   []string `json:"sections"`
}
type Gate interface {
	Resource(context.Context, string) (Resource, error)
	Snapshot(context.Context, string, string) (Snapshot, error)
	Granted(context.Context, string, string, string) (bool, error)
}

// Authorize applies the same gate to reads, cached results, exports and retries.
// A derived resource cannot launder its dependencies into a new project.
// MaxSourceRefs bounds metadata, not business record count. A fresh Session is
// the explicit way to start work without inheriting an old conversation's data.
const MaxSourceRefs = 10000

func Authorize(ctx context.Context, g Gate, p Principal, ref Ref) error {
	return AuthorizeAll(ctx, g, p, []Ref{ref})
}
func AuthorizeAll(ctx context.Context, g Gate, p Principal, refs []Ref) error {
	if e := p.Validate(); e != nil {
		return e
	}
	seen := map[Ref]bool{}
	resources := map[string]Resource{}
	permissions := map[string]bool{}
	pending := append([]Ref(nil), refs...)
	for len(pending) > 0 {
		ref := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[ref] {
			continue
		}
		if e := ref.Validate(); e != nil {
			return e
		}
		seen[ref] = true
		if len(seen) > MaxSourceRefs {
			return fmt.Errorf("%w: source history exceeds %d references; start a new Session for an independent task", ErrInvalid, MaxSourceRefs)
		}
		if isPersonalSource(ref.ResourceID) && p.Kind == "project" {
			return ErrDenied
		}
		r, ok := resources[ref.ResourceID]
		if !ok {
			var e error
			r, e = g.Resource(ctx, ref.ResourceID)
			if e != nil {
				return e
			}
			resources[ref.ResourceID] = r
		}
		if ref.Version == LivePolicyVersion {
			if r.Relation != BusinessPolicyRelation || len(r.Sections) != 1 || r.Sections[0].ID != ref.Section {
				return ErrDenied
			}
			if p.Kind == "project" {
				ok, err := g.Granted(ctx, ref.ResourceID, p.ProjectID, ref.Section)
				if err != nil {
					return err
				}
				if !ok {
					return ErrDenied
				}
			}
			continue
		}
		snap, e := g.Snapshot(ctx, ref.ResourceID, ref.Version)
		if e != nil {
			return e
		}
		if snap.State != "published" {
			return ErrDenied
		}
		found := false
		for _, section := range snap.Sections {
			if section.ID == ref.Section {
				found = true
				break
			}
		}
		if !found {
			return ErrDenied
		}
		if p.Kind == "project" {
			key := ref.ResourceID + "/" + ref.Section
			allowed, checked := permissions[key]
			if !checked {
				allowed, e = g.Granted(ctx, ref.ResourceID, p.ProjectID, ref.Section)
				if e != nil {
					return e
				}
				permissions[key] = allowed
			}
			if !allowed {
				return ErrDenied
			}
		}
		pending = append(pending, snap.Dependencies...)
	}
	return nil
}
func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func ValidateRecords(records []Record) error {
	if len(records) > 200 {
		return fmt.Errorf("%w: page exceeds 200 records", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, r := range records {
		if r.ID == "" || len(r.ID) > 191 || len(r.Fields) == 0 || seen[r.ID] {
			return ErrInvalid
		}
		seen[r.ID] = true
		b, e := json.Marshal(r.Fields)
		if e != nil || len(b) > 256*1024 {
			return fmt.Errorf("%w: record too large", ErrInvalid)
		}
	}
	return nil
}
