package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const VaultRelation = "obsidian-vault"
const vaultSection = "text"

type VaultChange struct {
	Path string  `json:"path"`
	Text *string `json:"text"`
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
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) ||
		name != path.Clean(name) || !strings.EqualFold(path.Ext(name), ".md") ||
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

func (s *Store) CreateVault(ctx context.Context, p Principal, root, id, name string) (Resource, error) {
	if !p.IsOwner() {
		return Resource{}, ErrDenied
	}
	name = strings.TrimSpace(name)
	if !filepath.IsAbs(root) || !validVaultID(id) || name == "" ||
		utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return Resource{}, ErrInvalid
	}
	if existing, err := s.Resource(ctx, id); err == nil {
		if existing.Relation != VaultRelation || existing.Name != name {
			return Resource{}, ErrConflict
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Resource{}, err
	}
	r := Resource{ID: id, Name: name, Relation: VaultRelation, Sections: []Section{
		{ID: vaultSection, Name: "笔记", Fields: []string{"name", "text", "sha256"}},
	}}
	version := Digest(id)
	if err := os.MkdirAll(filepath.Join(root, id, version), 0700); err != nil {
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
			if existing.Relation != VaultRelation || existing.Name != name {
				return Resource{}, ErrConflict
			}
			return existing, nil
		}
		return Resource{}, err
	}
	snapshot := Snapshot{ResourceID: id, Version: version, Sections: r.Sections}
	if _, err = tx.ExecContext(ctx, `INSERT INTO wb_snapshots
		(resource_id,version,previous_version,state,body,published_at) VALUES(?,?,'','published',?,UTC_TIMESTAMP(6))`,
		id, version, jsonText(snapshot)); err != nil {
		return Resource{}, err
	}
	if err = tx.Commit(); err != nil {
		return Resource{}, err
	}
	return s.Resource(ctx, id)
}

func (s *Store) SaveVault(ctx context.Context, p Principal, root, id, previous string, changes []VaultChange) (Snapshot, error) {
	if !p.IsOwner() {
		return Snapshot{}, ErrDenied
	}
	if !filepath.IsAbs(root) || !validVaultID(id) || len(previous) != 64 || !ValidID(previous) ||
		len(changes) == 0 || len(changes) > 200 {
		return Snapshot{}, ErrInvalid
	}
	r, err := s.Resource(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	if r.Relation != VaultRelation || r.ConnectionID != "" {
		return Snapshot{}, ErrDenied
	}
	version := Digest(struct {
		Previous string
		Changes  []VaultChange
	}{previous, changes})
	if saved, readErr := s.Snapshot(ctx, id, version); readErr == nil && saved.State == "published" {
		return saved, nil
	} else if readErr != nil && !errors.Is(readErr, sql.ErrNoRows) {
		return Snapshot{}, readErr
	}
	if r.CurrentVersion != previous {
		return Snapshot{}, ErrConflict
	}

	parent := filepath.Join(root, id)
	records, fingerprint, err := directoryRecords(ctx, filepath.Join(parent, previous), ".")
	if err != nil {
		return Snapshot{}, err
	}
	published, err := s.Snapshot(ctx, id, previous)
	if err != nil {
		return Snapshot{}, err
	}
	if published.Count != len(records) || (published.SourceWatermark != "" && published.SourceWatermark != fingerprint) {
		return Snapshot{}, fmt.Errorf("%w: Markdown directory differs from the published snapshot", ErrConflict)
	}
	notes, err := applyVaultChanges(records, changes)
	if err != nil {
		return Snapshot{}, err
	}
	records, fingerprint, err = writeVaultVersion(ctx, parent, version, notes)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{ResourceID: id, Version: version, SourceWatermark: fingerprint}
	beginErr := s.BeginSnapshot(ctx, p, snapshot)
	staged, err := s.Snapshot(ctx, id, version)
	if beginErr != nil && (err != nil || staged.SourceWatermark != fingerprint) {
		return Snapshot{}, beginErr
	}
	if err != nil {
		return Snapshot{}, err
	}
	if staged.Previous != previous {
		return Snapshot{}, ErrConflict
	}
	if staged.State == "published" {
		return staged, nil
	}
	for offset := 0; offset < len(records); offset += 200 {
		if err = s.Stage(ctx, p, id, version, records[offset:min(offset+200, len(records))]); err != nil {
			if saved, readErr := s.Snapshot(ctx, id, version); readErr == nil && saved.State == "published" {
				return saved, nil
			}
			return Snapshot{}, err
		}
	}
	return s.Publish(ctx, p, id, version, len(records))
}

func applyVaultChanges(records []Record, changes []VaultChange) (map[string]string, error) {
	notes := make(map[string]string, len(records))
	for _, record := range records {
		notes[record.Fields["name"].(string)] = record.Fields["text"].(string)
	}
	touched := make(map[string]bool, len(changes))
	for _, change := range changes {
		if !validVaultPath(change.Path) || touched[change.Path] {
			return nil, ErrInvalid
		}
		touched[change.Path] = true
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
	seen := make(map[string]string, len(notes))
	var bytes int
	for name, body := range notes {
		bytes += len(body)
		for key := name; key != "."; key = path.Dir(key) {
			folded := strings.ToLower(key)
			if other, exists := seen[folded]; exists && other != key {
				return nil, fmt.Errorf("%w: ambiguous Markdown path", ErrInvalid)
			}
			seen[folded] = key
		}
	}
	if len(notes) > 10000 || bytes > 32*1024*1024 {
		return nil, ErrInvalid
	}
	return notes, nil
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
		if _, existing, readErr := directoryRecords(ctx, target, "."); readErr != nil || existing != fingerprint {
			return nil, "", err
		}
	}
	return records, fingerprint, nil
}
