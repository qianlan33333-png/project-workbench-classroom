package core

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"syscall"
	"unicode"
)

const vaultMediaLimit = 24 * 1024 * 1024

var vaultMediaTypes = map[string]string{
	".pdf": "application/pdf", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp", ".avif": "image/avif",
}

// ReadVaultMedia serves referenced attachments from the currently published
// directory. Older text snapshots do not acquire new files from a live folder.
func (s *Store) ReadVaultMedia(ctx context.Context, p Principal, ref Ref, name string) ([]byte, string, error) {
	mime := vaultMediaTypes[strings.ToLower(path.Ext(name))]
	if mime == "" || len(name) > 1024 || !fs.ValidPath(name) || strings.ContainsAny(name, `<>:"\|?*`) || strings.ContainsFunc(name, unicode.IsControl) {
		return nil, "", ErrInvalid
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return nil, "", ErrDenied
		}
	}
	if err := Authorize(ctx, s, p, ref); err != nil {
		return nil, "", err
	}
	r, err := s.Resource(ctx, ref.ResourceID)
	if err != nil {
		return nil, "", err
	}
	if r.CurrentVersion != ref.Version || r.ConnectionID == "" {
		return nil, "", sql.ErrNoRows
	}
	connection, err := s.sourceConnection(ctx, r)
	if err != nil {
		return nil, "", err
	}
	if connection.Kind != "directory" {
		return nil, "", ErrDenied
	}
	data, err := readDirectoryMedia(connection.Location, r.Relation, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", sql.ErrNoRows
	}
	if err != nil {
		return nil, "", err
	}
	valid := http.DetectContentType(data) == mime
	if mime == "image/avif" {
		valid = len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) &&
			(bytes.Equal(data[8:12], []byte("avif")) || bytes.Equal(data[8:12], []byte("avis")))
	}
	if !valid {
		return nil, "", ErrInvalid
	}
	if err := Authorize(ctx, s, p, ref); err != nil {
		return nil, "", err
	}
	current, err := s.Resource(ctx, ref.ResourceID)
	if err != nil {
		return nil, "", err
	}
	if current.CurrentVersion != ref.Version || current.Relation != r.Relation || current.ConnectionID != r.ConnectionID {
		return nil, "", ErrConflict
	}
	bound, err := s.sourceConnection(ctx, current)
	if err != nil {
		return nil, "", err
	}
	if bound.Kind != connection.Kind || bound.Location != connection.Location {
		return nil, "", ErrConflict
	}
	return data, mime, nil
}

func readDirectoryMedia(location, relation, name string) ([]byte, error) {
	root, err := os.OpenRoot(location)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if relation != "" && relation != "." {
		if !fs.ValidPath(relation) {
			return nil, ErrDenied
		}
		sub, err := root.OpenRoot(relation)
		if err != nil {
			return nil, err
		}
		defer sub.Close()
		root = sub
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		entry, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if entry.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !entry.IsDir()) {
			return nil, ErrDenied
		}
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > vaultMediaLimit {
		return nil, ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, vaultMediaLimit+1))
	if len(data) > vaultMediaLimit {
		return nil, ErrInvalid
	}
	return data, err
}
