package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Plain knowledge files use ordinary filesystem APIs. Binary document parsing
// remains an external connector capability, not a second Office/PDF engine.
func directoryRecords(ctx context.Context, location, relation string) ([]Record, string, error) {
	root, e := filepath.EvalSymlinks(location)
	if e != nil {
		return nil, "", e
	}
	if relation == "" {
		relation = "."
	}
	if filepath.IsAbs(relation) {
		return nil, "", ErrDenied
	}
	path, e := filepath.EvalSymlinks(filepath.Join(root, relation))
	if e != nil {
		return nil, "", e
	}
	relative, e := filepath.Rel(root, path)
	if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, "", ErrDenied
	}
	records := []Record{}
	confined, e := os.OpenRoot(path)
	if e != nil {
		return nil, "", e
	}
	defer confined.Close()
	hash := sha256.New()
	total := 0
	e = filepath.WalkDir(path, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrDenied
		}
		if entry.IsDir() {
			if name != path && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" && ext != ".txt" {
			return nil
		}
		stat, e := entry.Info()
		if e != nil {
			return e
		}
		if !stat.Mode().IsRegular() || stat.Size() > 256*1024 {
			return ErrInvalid
		}
		total += int(stat.Size())
		if total > 32*1024*1024 || len(records) >= 10000 {
			return ErrInvalid
		}
		rel, e := filepath.Rel(path, name)
		if e != nil {
			return e
		}
		file, e := confined.Open(rel)
		if e != nil {
			return e
		}
		raw, e := io.ReadAll(io.LimitReader(file, 256*1024+1))
		file.Close()
		if e != nil {
			return e
		}
		if len(raw) > 256*1024 || !utf8.Valid(raw) {
			return ErrInvalid
		}
		rel = filepath.ToSlash(rel)
		digest := sha256.Sum256(raw)
		hash.Write([]byte(rel))
		hash.Write([]byte{0})
		hash.Write(digest[:])
		key := sha256.Sum256([]byte(rel))
		records = append(records, Record{ID: hex.EncodeToString(key[:]), Fields: map[string]any{"name": rel, "text": string(raw), "sha256": hex.EncodeToString(digest[:])}})
		return nil
	})
	return records, hex.EncodeToString(hash.Sum(nil)), e
}
