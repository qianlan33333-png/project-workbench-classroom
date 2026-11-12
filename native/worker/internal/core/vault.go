package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

const VaultRelation = "obsidian-vault"
const vaultSection = "text"

type VaultChange struct {
	Path   string  `json:"path"`
	Text   *string `json:"text"`
	Create bool    `json:"create,omitempty"`
}

type VaultOrigin struct {
	ResourceID string `json:"resourceId"`
	Version    string `json:"version"`
}

type VaultCopy struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Origin  VaultOrigin   `json:"origin"`
	Changes []VaultChange `json:"changes"`
}

type VaultCopyPathsError struct{ Paths []string }

func (e *VaultCopyPathsError) Error() string { return "部分笔记无法复制，请调整选择。" }

type vaultPathConflict struct{ first, second string }

func (e *vaultPathConflict) Error() string {
	return fmt.Sprintf("%q 与 %q 的路径冲突", e.first, e.second)
}
func (e *vaultPathConflict) Unwrap() error { return ErrInvalid }

type vaultInitial struct {
	origin    *VaultOrigin
	notes     map[string]string
	operation string
}

func validVaultID(id string) bool {
	if !strings.HasPrefix(id, "vault-") {
		return false
	}
	raw := strings.TrimPrefix(id, "vault-")
	parsed, err := uuid.Parse(raw)
	return err == nil && parsed.String() == raw
}

func validVaultPath(name string) bool {
	return strings.EqualFold(path.Ext(name), ".md") && validVaultEntryPath(name)
}

func validVaultEntryPath(name string) bool {
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) ||
		name != path.Clean(name) ||
		strings.ContainsAny(name, `\:*?"<>|[]#^`) || strings.ContainsFunc(name, unicode.IsControl) {
		return false
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || len(segment) > 255 || strings.HasPrefix(segment, ".") ||
			strings.TrimSpace(segment) != segment || strings.HasSuffix(segment, ".") {
			return false
		}
	}
	return true
}

// The last layout survives an intervening write made by a rollback version.
// Older snapshots on either side of a rollback still expose their own notes.
func vaultFoldersAt(ctx context.Context, q resourceQuery, snapshot Snapshot) ([]string, error) {
	if snapshot.VaultFolders != nil {
		return *snapshot.VaultFolders, nil
	}
	var body []byte
	err := q.QueryRowContext(ctx, `SELECT JSON_EXTRACT(body,'$.vaultFolders') FROM wb_snapshots
		WHERE resource_id=? AND state='published' AND published_at<? AND JSON_CONTAINS_PATH(body,'one','$.vaultFolders')
		ORDER BY published_at DESC LIMIT 1`, snapshot.ResourceID, snapshot.PublishedAt).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var folders []string
	err = json.Unmarshal(body, &folders)
	return folders, err
}

func (s *Store) VaultFolders(ctx context.Context, p Principal, id, version string) ([]string, error) {
	if !p.IsOwner() {
		return nil, ErrDenied
	}
	if !validVaultID(id) || !ValidID(version) {
		return nil, ErrInvalid
	}
	r, err := s.Resource(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.Relation != VaultRelation || r.ConnectionID != "" {
		return nil, ErrDenied
	}
	snapshot, err := s.Snapshot(ctx, id, version)
	if err != nil {
		return nil, err
	}
	if snapshot.State != "published" {
		return nil, ErrConflict
	}
	return vaultFoldersAt(ctx, s.DB, snapshot)
}

func (s *Store) CreateVault(ctx context.Context, p Principal, root, id, name string) (Resource, error) {
	return s.createVault(ctx, p, root, id, name, "", vaultInitial{})
}

func (s *Store) CreateVaultLibrary(ctx context.Context, p Principal, root, commandID string, in LibraryInput) (ResourceDiscovery, error) {
	if in.Kind != "corpus" || len(in.Columns) != 0 {
		return ResourceDiscovery{}, ErrInvalid
	}
	r, err := s.createVault(ctx, p, root, libraryID("vault", p.SessionID+"/"+commandID), in.Name, in.Description, vaultInitial{})
	if err != nil {
		return ResourceDiscovery{}, err
	}
	return ResourceDiscovery{ID: r.ID, Name: r.Name, Kind: r.Kind, Description: r.Description, Access: "manage", Version: r.CurrentVersion, Editable: true, Sections: r.Sections}, nil
}

func (s *Store) CopyVault(ctx context.Context, p Principal, root string, input VaultCopy) (Resource, error) {
	if !p.IsOwner() {
		return Resource{}, ErrDenied
	}
	if !ValidID(input.Origin.ResourceID) || !ValidID(input.Origin.Version) || input.ID == input.Origin.ResourceID ||
		len(input.Changes) == 0 || len(input.Changes) > 10000 {
		return Resource{}, ErrInvalid
	}
	source, err := s.Resource(ctx, input.Origin.ResourceID)
	if err != nil {
		return Resource{}, err
	}
	if source.Relation == VaultRelation {
		return Resource{}, ErrDenied
	}
	snapshot, err := s.Snapshot(ctx, input.Origin.ResourceID, input.Origin.Version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Resource{}, err
	}
	if snapshot.State != "published" {
		return Resource{}, ErrConflict
	}
	var invalid []string
	for _, change := range input.Changes {
		if !validVaultPath(change.Path) || change.Text == nil || !utf8.ValidString(*change.Text) || len(*change.Text) > 240*1024 {
			invalid = append(invalid, change.Path)
		}
	}
	if len(invalid) > 0 {
		return Resource{}, &VaultCopyPathsError{Paths: invalid}
	}
	notes, err := applyVaultChanges(nil, input.Changes)
	var collision *vaultPathConflict
	if errors.As(err, &collision) {
		return Resource{}, &VaultCopyPathsError{Paths: []string{collision.first, collision.second}}
	}
	if err != nil {
		return Resource{}, err
	}
	input.Name = strings.TrimSpace(input.Name)
	operation := Digest(struct {
		Name   string
		Origin VaultOrigin
		Notes  map[string]string
	}{input.Name, input.Origin, notes})
	return s.createVault(ctx, p, root, input.ID, input.Name, "", vaultInitial{origin: &input.Origin, notes: notes, operation: operation})
}

func (s *Store) createVault(ctx context.Context, p Principal, root, id, name, description string, initial vaultInitial) (Resource, error) {
	if !p.IsOwner() {
		if err := s.CheckLibraryPersonal(ctx, p); err != nil {
			return Resource{}, err
		}
	}
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if !filepath.IsAbs(root) || !validVaultID(id) || name == "" ||
		utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return Resource{}, ErrInvalid
	}
	version := Digest(id)
	existingVault := func(existing Resource) (Resource, error) {
		if existing.Relation != VaultRelation {
			return Resource{}, ErrConflict
		}
		if initial.operation == "" {
			if existing.VaultOrigin != nil || existing.Name != name || existing.Description != description {
				return Resource{}, ErrConflict
			}
		} else {
			snapshot, err := s.Snapshot(ctx, id, version)
			if err != nil || snapshot.VaultOperation != initial.operation {
				return Resource{}, ErrConflict
			}
		}
		return existing, nil
	}
	if existing, err := s.Resource(ctx, id); err == nil {
		return existingVault(existing)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Resource{}, err
	}
	r := Resource{ID: id, Name: name, Description: description, Kind: "corpus", NativeEditable: true, Relation: VaultRelation, VaultOrigin: initial.origin, Sections: []Section{
		{ID: vaultSection, Name: "笔记", Fields: []string{"name", "text", "sha256"}},
	}}
	if err := r.Validate(); err != nil {
		return Resource{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Resource{}, err
	}
	defer tx.Rollback()
	if err = guardWrite(ctx, tx); err != nil {
		return Resource{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO wb_resources(id,body,current_version) VALUES(?,?,?)`,
		r.ID, jsonText(r), version); err != nil {
		_ = tx.Rollback()
		if existing, readErr := s.Resource(ctx, id); readErr == nil {
			return existingVault(existing)
		}
		return Resource{}, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return Resource{}, err
	}
	parent := filepath.Join(root, id)
	if err = makeVaultDirectory(parent); err != nil {
		return Resource{}, err
	}
	target := filepath.Join(parent, version)
	keepFiles := false
	if _, err = os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		defer func() {
			if !keepFiles {
				_ = os.RemoveAll(target)
				_ = os.Remove(parent)
			}
		}()
	} else if err != nil {
		return Resource{}, err
	}
	records, fingerprint, err := writeVaultVersion(ctx, parent, version, initial.notes)
	if err != nil {
		return Resource{}, err
	}
	snapshot := Snapshot{ResourceID: id, Version: version, Sections: r.Sections, SourceWatermark: fingerprint, VaultOperation: initial.operation}
	if err = insertVaultSnapshot(ctx, tx, snapshot, records); err != nil {
		return Resource{}, err
	}
	keepFiles = true
	if err = tx.Commit(); err != nil {
		return Resource{}, err
	}
	return s.Resource(ctx, id)
}

func (s *Store) SaveVault(ctx context.Context, p Principal, root, id, previous string, changes []VaultChange) (Snapshot, error) {
	if len(changes) > 200 {
		return Snapshot{}, ErrInvalid
	}
	return s.saveVaultChanges(ctx, p, root, id, previous, changes, nil)
}

// SaveVaultRename applies the old path, new path, and affected Markdown in one version.
func (s *Store) SaveVaultRename(ctx context.Context, p Principal, root, id, previous string, changes []VaultChange) (Snapshot, error) {
	if !p.IsOwner() {
		return Snapshot{}, ErrDenied
	}
	if len(changes) < 2 || len(changes) > 10001 {
		return Snapshot{}, ErrInvalid
	}
	return s.saveVaultChanges(ctx, p, root, id, previous, changes, nil)
}

func (s *Store) SaveVaultStructure(ctx context.Context, p Principal, root, id, previous string, changes []VaultChange, folders *[]string) (Snapshot, error) {
	if !p.IsOwner() {
		return Snapshot{}, ErrDenied
	}
	if folders == nil || len(*folders) > 20000 || len(changes) > 20000 {
		return Snapshot{}, ErrInvalid
	}
	return s.saveVaultChanges(ctx, p, root, id, previous, changes, folders)
}

func (s *Store) saveVaultChanges(ctx context.Context, p Principal, root, id, previous string, changes []VaultChange, folders *[]string) (Snapshot, error) {
	if len(previous) != 64 || !ValidID(previous) || (len(changes) == 0 && folders == nil) {
		return Snapshot{}, ErrInvalid
	}
	version := Digest(struct {
		Previous string
		Changes  []VaultChange
		Folders  *[]string `json:",omitempty"`
	}{previous, changes, folders})
	return s.saveVault(ctx, p, root, id, previous, version, "", folders, func(_ []Record) ([]VaultChange, string, error) {
		return changes, "", nil
	})
}

// saveVault serializes changes and permission updates on the resource row.
func (s *Store) saveVault(ctx context.Context, p Principal, root, id, previous, version, operation string, layout *[]string, build func([]Record) ([]VaultChange, string, error)) (Snapshot, error) {
	if !filepath.IsAbs(root) || !validVaultID(id) {
		return Snapshot{}, ErrInvalid
	}
	if err := p.Validate(); err != nil {
		return Snapshot{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	if err = guardWrite(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	r, err := scanResource(tx.QueryRowContext(ctx, `SELECT body,current_version,revision FROM wb_resources WHERE id=? FOR UPDATE`, id))
	if err != nil {
		return Snapshot{}, err
	}
	if r.Relation != VaultRelation || r.ConnectionID != "" {
		return Snapshot{}, ErrDenied
	}
	if err = s.resourceWriteAllowed(ctx, tx, p, r); err != nil {
		return Snapshot{}, err
	}
	if previous == "" {
		previous = r.CurrentVersion
	}
	load := func(version string) (Snapshot, error) { return readSnapshot(ctx, tx, id, version) }
	priorAttempt, err := load(version)
	switch {
	case err == nil && priorAttempt.State == "published":
		if priorAttempt.VaultOperation != operation {
			return Snapshot{}, ErrConflict
		}
		return priorAttempt, nil
	case err == nil && (priorAttempt.State != "staging" || priorAttempt.Previous != previous):
		return Snapshot{}, ErrConflict
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return Snapshot{}, err
	}
	if r.CurrentVersion != previous {
		return Snapshot{}, ErrConflict
	}

	parent := filepath.Join(root, id)
	if err = checkVaultDirectory(parent); err != nil {
		return Snapshot{}, err
	}
	records, fingerprint, err := readVaultDirectory(ctx, filepath.Join(parent, previous))
	if err != nil {
		return Snapshot{}, err
	}
	published, err := load(previous)
	if err != nil {
		return Snapshot{}, err
	}
	if published.State != "published" || published.Count != len(records) || (published.SourceWatermark != "" && published.SourceWatermark != fingerprint) {
		return Snapshot{}, fmt.Errorf("%w: Markdown directory differs from the published snapshot", ErrConflict)
	}
	changes, itemID, err := build(records)
	if err != nil {
		return Snapshot{}, err
	}
	notes, err := applyVaultChanges(records, changes)
	if err != nil {
		return Snapshot{}, err
	}
	var paths []string
	if layout != nil {
		paths = *layout
	} else {
		paths, err = vaultFoldersAt(ctx, tx, published)
		if err != nil {
			return Snapshot{}, err
		}
		// Retain the parent when the last note moves out or is deleted.
		for _, record := range records {
			if parent := path.Dir(record.Fields["name"].(string)); parent != "." {
				paths = append(paths, parent)
			}
		}
	}
	folders, err := vaultLayout(notes, paths)
	if err != nil {
		return Snapshot{}, err
	}
	target := filepath.Join(parent, version)
	_, statErr := os.Lstat(target)
	keepFiles := false
	if errors.Is(statErr, os.ErrNotExist) {
		defer func() {
			if !keepFiles {
				_ = os.RemoveAll(target)
			}
		}()
	} else if statErr != nil {
		return Snapshot{}, statErr
	}
	records, fingerprint, err = writeVaultVersion(ctx, parent, version, notes)
	if err != nil {
		return Snapshot{}, err
	}
	if priorAttempt.State == "staging" {
		if priorAttempt.SourceWatermark != fingerprint {
			return Snapshot{}, ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM wb_records WHERE resource_id=? AND version=?`, id, version); err != nil {
			return Snapshot{}, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM wb_snapshots WHERE resource_id=? AND version=?`, id, version); err != nil {
			return Snapshot{}, err
		}
	}
	snapshot := Snapshot{ResourceID: id, Version: version, Previous: previous,
		Sections: r.Sections, SourceWatermark: fingerprint, VaultOperation: operation, VaultItemID: itemID, VaultFolders: &folders}
	if err = insertVaultSnapshot(ctx, tx, snapshot, records); err != nil {
		return Snapshot{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE wb_resources SET current_version=?,revision=revision+1 WHERE id=?`, version, id); err != nil {
		return Snapshot{}, err
	}
	snapshot, err = load(version)
	if err != nil {
		return Snapshot{}, err
	}
	// A lost commit acknowledgement must leave the exact files available for retry.
	keepFiles = true
	if err = tx.Commit(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func insertVaultSnapshot(ctx context.Context, tx *sql.Tx, snapshot Snapshot, records []Record) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO wb_snapshots
		(resource_id,version,previous_version,state,body,record_count,published_at)
		VALUES(?,?,?,'published',?,?,UTC_TIMESTAMP(6))`, snapshot.ResourceID, snapshot.Version, snapshot.Previous, jsonText(snapshot), len(records)); err != nil {
		return err
	}
	// Bound each insert even when Markdown expands substantially as JSON.
	const batchSize = 16
	for offset := 0; offset < len(records); offset += batchSize {
		if err := insertSnapshotRecords(ctx, tx, snapshot.ResourceID, snapshot.Version, records[offset:min(offset+batchSize, len(records))]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ChangeVaultItem(ctx context.Context, p Principal, root string, in LibraryChange) (LibraryReceipt, error) {
	out := LibraryReceipt{ResourceID: in.ResourceID, Action: in.Action, ItemID: in.ItemID}
	if !ValidID(in.OperationKey) {
		return out, ErrInvalid
	}
	version := Digest([]string{p.SessionID, in.ResourceID, in.OperationKey})
	v, err := s.saveVault(ctx, p, root, in.ResourceID, "", version, Digest(in), nil, func(records []Record) ([]VaultChange, string, error) {
		return vaultItemChanges(records, in)
	})
	if err != nil {
		return out, err
	}
	out.ItemID, out.Version = v.VaultItemID, v.Version
	return out, nil
}

func vaultItemChanges(records []Record, in LibraryChange) ([]VaultChange, string, error) {
	var oldPath, body string
	switch in.Action {
	case "create":
		if in.ItemID != "" || len(in.Fields) != 2 {
			return nil, "", ErrInvalid
		}
	case "update", "delete":
		for _, record := range records {
			if record.ID == in.ItemID {
				oldPath, body = record.Fields["name"].(string), record.Fields["text"].(string)
				break
			}
		}
		if oldPath == "" {
			return nil, "", ErrConflict
		}
		if in.Action == "delete" {
			if len(in.Fields) != 0 {
				return nil, "", ErrInvalid
			}
			return []VaultChange{{Path: oldPath}}, in.ItemID, nil
		}
		if len(in.Fields) == 0 {
			return nil, "", ErrInvalid
		}
	default:
		return nil, "", ErrInvalid
	}
	name := oldPath
	for key, value := range in.Fields {
		text, ok := value.(string)
		if !ok {
			return nil, "", ErrInvalid
		}
		switch key {
		case "title":
			name = strings.TrimSpace(text)
			if name != "" && !strings.EqualFold(path.Ext(name), ".md") {
				name += ".md"
			}
		case "markdown":
			body = text
		default:
			return nil, "", ErrInvalid
		}
	}
	if !validVaultPath(name) {
		return nil, "", ErrInvalid
	}
	changes := []VaultChange{{Path: name, Text: &body, Create: name != oldPath}}
	if oldPath != "" && oldPath != name {
		changes = append(changes, VaultChange{Path: oldPath})
	}
	return changes, fmt.Sprintf("%x", sha256.Sum256([]byte(name))), nil
}

func applyVaultChanges(records []Record, changes []VaultChange) (map[string]string, error) {
	notes := make(map[string]string, len(records))
	for _, record := range records {
		notes[record.Fields["name"].(string)] = record.Fields["text"].(string)
	}
	touched := make(map[string]bool, len(changes))
	for _, change := range changes {
		if !validVaultPath(change.Path) {
			return nil, ErrInvalid
		}
		if touched[change.Path] {
			return nil, &vaultPathConflict{first: change.Path, second: change.Path}
		}
		touched[change.Path] = true
		if _, exists := notes[change.Path]; change.Create && exists {
			return nil, ErrConflict
		}
		if change.Text == nil {
			if _, exists := notes[change.Path]; !exists {
				return nil, ErrConflict
			}
			delete(notes, change.Path)
		} else {
			if !utf8.ValidString(*change.Text) || len(*change.Text) > 240*1024 {
				return nil, ErrInvalid
			}
			notes[change.Path] = *change.Text
		}
	}
	var bytes int
	for _, body := range notes {
		bytes += len(body)
	}
	if len(notes) > 10000 || bytes > 32*1024*1024 {
		return nil, ErrInvalid
	}
	if _, err := vaultLayout(notes, nil); err != nil {
		return nil, err
	}
	return notes, nil
}

func vaultLayout(notes map[string]string, folders []string) ([]string, error) {
	type entry struct {
		name, origin string
		folder       bool
	}
	seen := make(map[string]entry, len(notes)+len(folders))
	directories := make([]string, 0, len(folders))
	add := func(name string, folder bool) error {
		for component := name; component != "."; component = path.Dir(component) {
			key := norm.NFC.String(strings.ToLower(component))
			if other, exists := seen[key]; exists {
				if other.name != component || other.folder != folder {
					return &vaultPathConflict{first: other.origin, second: name}
				}
				return nil // Its ancestors were already registered.
			}
			seen[key] = entry{name: component, origin: name, folder: folder}
			if folder {
				directories = append(directories, component)
			}
			folder = true
		}
		return nil
	}
	for name := range notes {
		if err := add(name, false); err != nil {
			return nil, err
		}
	}
	for _, folder := range folders {
		if !validVaultEntryPath(folder) {
			return nil, ErrInvalid
		}
		if err := add(folder, true); err != nil {
			return nil, err
		}
	}
	sort.Strings(directories)
	return directories, nil
}

func makeVaultDirectory(name string) error {
	err := os.Mkdir(name, 0700)
	if errors.Is(err, os.ErrExist) {
		return checkVaultDirectory(name)
	}
	return err
}

func checkVaultDirectory(name string) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrDenied
	}
	return nil
}

func readVaultDirectory(ctx context.Context, name string) ([]Record, string, error) {
	if err := checkVaultDirectory(name); err != nil {
		return nil, "", err
	}
	return directoryRecords(ctx, name, ".")
}

func writeVaultVersion(ctx context.Context, parent, version string, notes map[string]string) ([]Record, string, error) {
	staging, err := os.MkdirTemp(parent, ".staging-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(staging)
	for name, body := range notes {
		if err = ctx.Err(); err != nil {
			return nil, "", err
		}
		file := filepath.Join(staging, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			return nil, "", err
		}
		if err = os.WriteFile(file, []byte(body), 0600); err != nil {
			return nil, "", err
		}
	}
	records, fingerprint, err := directoryRecords(ctx, staging, ".")
	if err != nil {
		return nil, "", err
	}
	for offset := 0; offset < len(records); offset += 200 {
		if err = ValidateRecords(records[offset:min(offset+200, len(records))]); err != nil {
			return nil, "", err
		}
	}
	target := filepath.Join(parent, version)
	if err = os.Rename(staging, target); err != nil {
		if _, existing, readErr := readVaultDirectory(ctx, target); readErr != nil || existing != fingerprint {
			return nil, "", err
		}
	}
	return records, fingerprint, nil
}
