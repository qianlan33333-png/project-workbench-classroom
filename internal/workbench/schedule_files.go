package workbench

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"syscall"
)

// Advance only versions actually written by this run. An independent edit after
// its final snapshot will differ from the saved hash and trigger the next run.
func includeOwnRevisions(tx *sql.Tx, r Run, src []ContextSource) ([]ContextSource, error) {
	byID := make(map[string]ContextSource, len(src))
	for _, v := range src {
		byID[v.ID] = v
	}
	rows, e := tx.Query("SELECT path,after_hash,created_at FROM revisions WHERE project_id=? AND run_id=? ORDER BY created_at,rowid", r.ProjectID, r.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var name, hash string
		var at int64
		if e = rows.Scan(&name, &hash, &at); e != nil {
			return nil, e
		}
		id := "file:" + name
		if !includesInput(r.ScheduleConfig.SourceScope, id) || strings.HasPrefix(name, "定时任务结果/") {
			continue
		}
		if hash == "" {
			delete(byID, id)
		} else {
			byID[id] = ContextSource{ID: id, Hash: hash, At: at, Status: "read"}
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	out := make([]ContextSource, 0, len(byID))
	for _, v := range byID {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

type cancellableInput struct {
	ctx    context.Context
	reader io.Reader
}

func (r cancellableInput) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(b)
}

func scheduleFiles(ctx context.Context, root *os.Root, scope string) ([]ContextSource, error) {
	roots := []string{"."}
	if strings.TrimSpace(scope) != "" {
		roots = nil
		for _, line := range strings.Split(scope, "\n") {
			name := strings.TrimSuffix(strings.TrimSpace(line), "/")
			if name != "" && name != "@任务" && name != "@对话" && name != "@人工说明" {
				roots = append(roots, name)
			}
		}
	}
	seen := map[string]bool{}
	var out []ContextSource
	for _, start := range roots {
		err := fs.WalkDir(root.FS(), start, func(name string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) && name == start {
					return nil
				}
				return err
			}
			if name == "定时任务结果" || strings.HasPrefix(name, "定时任务结果/") || strings.HasPrefix(path.Base(name), ".pw-write-") || seen[name] {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			seen[name] = true
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			h := sha256.New()
			if _, err = io.Copy(h, cancellableInput{ctx, f}); err != nil {
				return err
			}
			out = append(out, ContextSource{ID: "file:" + name, At: info.ModTime().UnixMilli(), Hash: hex.EncodeToString(h.Sum(nil)), Status: "read"})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
