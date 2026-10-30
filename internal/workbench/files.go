package workbench

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxFile = 128 << 20

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validPath(p string) error {
	if !fs.ValidPath(p) || p == "." || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") {
		return errors.New("请使用项目内的相对路径")
	}
	return nil
}
func (s *Store) openRoot(project string) (*os.Root, error) {
	dir, err := s.ProjectRoot(project)
	if err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
}
func readRoot(r *os.Root, p string) ([]byte, error) {
	if err := validPath(p); err != nil {
		return nil, err
	}
	f, err := r.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFile {
		return nil, errors.New("文件类型或大小不支持")
	}
	return io.ReadAll(io.LimitReader(f, maxFile+1))
}
func atomicRoot(r *os.Root, p string, b []byte) error {
	if err := validPath(p); err != nil {
		return err
	}
	if len(b) > maxFile {
		return errors.New("文件超过 128MB")
	}
	dir := path.Dir(p)
	if err := r.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp := path.Join(dir, ".pw-write-"+newID())
	f, err := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return r.Rename(tmp, p)
}
func (s *Store) ReadFile(project, p string) ([]byte, string, error) {
	r, err := s.openRoot(project)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	b, err := readRoot(r, p)
	if err != nil {
		return nil, "", err
	}
	return b, digest(b), nil
}
func (s *Store) blob(b []byte) (string, error) {
	h := digest(b)
	p := filepath.Join(s.Root, "blobs", h[:2], h)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return h, nil
	}
	if err != nil {
		return "", err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		os.Remove(p)
		return "", err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	return h, closeErr
}
func (s *Store) Blob(h string) ([]byte, error) {
	if len(h) != 64 {
		return nil, errors.New("版本标识无效")
	}
	if _, err := hex.DecodeString(h); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(s.Root, "blobs", h[:2], h))
}

func (s *Store) Files(project string) ([]FileInfo, error) {
	r, err := s.openRoot(project)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := []FileInfo{}
	err = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".pw-write-") {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		out = append(out, FileInfo{Path: p, Size: info.Size(), ModifiedAt: info.ModTime().UnixMilli(), Directory: d.IsDir()})
		if len(out) > 50000 {
			return errors.New("项目文件数超过 50000")
		}
		return nil
	})
	return out, err
}
func (s *Store) saveFile(project, p string, b []byte, base string, remove bool, run string) (Revision, error) {
	r, err := s.openRoot(project)
	if err != nil {
		return Revision{}, err
	}
	defer r.Close()
	old, readErr := readRoot(r, p)
	before := ""
	if readErr == nil {
		before = digest(old)
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Revision{}, readErr
	}
	if before != base {
		return Revision{}, ErrConflict
	}
	if remove && before == "" {
		return Revision{}, ErrNotFound
	}
	if before != "" {
		if _, err = s.blob(old); err != nil {
			return Revision{}, err
		}
	}
	after := ""
	if !remove {
		after, err = s.blob(b)
		if err != nil {
			return Revision{}, err
		}
	}
	rev := Revision{ID: newID(), ProjectID: project, RunID: run, Path: p, Before: before, After: after, CreatedAt: now()}
	// A durable intent permits reconciliation if the process dies between rename and metadata.
	intent := filepath.Join(s.Root, "projects", project, "file-intent.json")
	if err = os.WriteFile(intent, []byte(encode(rev)), 0600); err != nil {
		return rev, err
	}
	if remove {
		err = r.Remove(p)
	} else {
		err = atomicRoot(r, p, b)
	}
	if err != nil {
		os.Remove(intent)
		return rev, err
	}
	if err = s.addRevision(rev); err != nil {
		return rev, err
	}
	if err = os.Remove(intent); err != nil {
		return rev, err
	}
	if err = s.IndexFile(project, p); err != nil {
		return rev, err
	}
	return rev, s.Emit("", run, "file.changed", rev)
}
func (s *Store) SaveFile(project, p string, b []byte, base string) (Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.CheckUnlocked(project); err != nil {
		return Revision{}, err
	}
	return s.saveFile(project, p, b, base, false, "")
}
func (s *Store) DeleteFile(project, p, base string) (Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.CheckUnlocked(project); err != nil {
		return Revision{}, err
	}
	return s.saveFile(project, p, nil, base, true, "")
}
func (s *Store) addRevision(r Revision) error {
	_, err := s.DB.Exec("INSERT OR IGNORE INTO revisions VALUES(?,?,?,?,?,?,?)", r.ID, r.ProjectID, r.RunID, r.Path, r.Before, r.After, r.CreatedAt)
	return err
}
func (s *Store) Revisions(project, p string) ([]Revision, error) {
	rows, err := s.DB.Query("SELECT id,project_id,run_id,path,before_hash,after_hash,created_at FROM revisions WHERE project_id=? AND (?='' OR path=?) ORDER BY created_at DESC LIMIT 200", project, p, p)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		var v Revision
		if err = rows.Scan(&v.ID, &v.ProjectID, &v.RunID, &v.Path, &v.Before, &v.After, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) UndoRevision(id string) (Revision, error) {
	var r Revision
	err := s.DB.QueryRow("SELECT id,project_id,run_id,path,before_hash,after_hash,created_at FROM revisions WHERE id=?", id).Scan(&r.ID, &r.ProjectID, &r.RunID, &r.Path, &r.Before, &r.After, &r.CreatedAt)
	if err != nil {
		return r, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.CheckUnlocked(r.ProjectID); err != nil {
		return r, err
	}
	var b []byte
	if r.Before != "" {
		b, err = s.Blob(r.Before)
		if err != nil {
			return r, err
		}
	}
	return s.saveFile(r.ProjectID, r.Path, b, r.After, r.Before == "", "")
}

func (s *Store) Snapshot(project string) (map[string]string, error) {
	files, err := s.Files(project)
	if err != nil {
		return nil, err
	}
	r, err := s.openRoot(project)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out := map[string]string{}
	for _, f := range files {
		if f.Directory {
			continue
		}
		b, err := readRoot(r, f.Path)
		if err != nil {
			return nil, fmt.Errorf("记录 %s 的版本: %w", f.Path, err)
		}
		h, err := s.blob(b)
		if err != nil {
			return nil, err
		}
		out[f.Path] = h
	}
	return out, nil
}
func (s *Store) FinishSnapshot(run Run) error {
	var raw string
	if err := s.DB.QueryRow("SELECT data FROM baselines WHERE run_id=?", run.ID).Scan(&raw); err != nil {
		// No executor starts before this baseline is durable. A failed baseline
		// therefore has no runtime writes to reconcile.
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	before := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &before); err != nil {
		return err
	}
	after, err := s.Snapshot(run.ProjectID)
	if err != nil {
		return err
	}
	paths := map[string]bool{}
	for p := range before {
		paths[p] = true
	}
	for p := range after {
		paths[p] = true
	}
	changed := 0
	for p := range paths {
		if before[p] == after[p] {
			continue
		}
		changed++
		rev := Revision{ID: run.ID + ":" + p, ProjectID: run.ProjectID, RunID: run.ID, Path: p, Before: before[p], After: after[p], CreatedAt: now()}
		if err = s.addRevision(rev); err != nil {
			return err
		}
		if err = s.IndexFile(run.ProjectID, p); err != nil {
			return err
		}
	}
	return s.Emit(run.SessionID, run.ID, "files.recorded", map[string]any{"changed": changed})
}
func (s *Store) RecoverFileIntents() error {
	ps, err := s.Projects()
	if err != nil {
		return err
	}
	for _, p := range ps {
		file := filepath.Join(s.Root, "projects", p.ID, "file-intent.json")
		b, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var rev Revision
		if err = json.Unmarshal(b, &rev); err != nil {
			return err
		}
		_, h, e := s.ReadFile(p.ID, rev.Path)
		if errors.Is(e, os.ErrNotExist) {
			h = ""
		} else if e != nil {
			return e
		}
		if h == rev.After {
			if err = s.addRevision(rev); err != nil {
				return err
			}
			if err = s.IndexFile(p.ID, rev.Path); err != nil {
				return err
			}
		} else if h != rev.Before {
			return fmt.Errorf("文件 %s 的未完成保存需要人工核对", rev.Path)
		}
		if err = os.Remove(file); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) IndexFile(project, p string) error {
	content := ""
	h := ""
	b, hash, err := s.ReadFile(project, p)
	if err == nil && len(b) <= maxEditableFile && utf8.Valid(b) && !strings.ContainsRune(string(b), 0) {
		content = string(b)
		h = hash
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM documents WHERE project_id=? AND path=?", project, p); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM document_search WHERE project_id=? AND path=?", project, p); err != nil {
			return err
		}
		if h == "" {
			return nil
		}
		if _, err := tx.Exec("INSERT INTO documents VALUES(?,?,?,?)", project, p, h, content); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO document_search(project_id,path,content) VALUES(?,?,?)", project, p, content)
		return err
	})
}
func (s *Store) Search(project, q string) ([]map[string]string, error) {
	if _, err := s.ProjectRoot(project); err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return []map[string]string{}, nil
	}
	rows, err := s.DB.Query("SELECT path,content FROM documents WHERE project_id=? AND (instr(content,?)>0 OR instr(path,?)>0) LIMIT 50", project, q, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var p, c string
		if err = rows.Scan(&p, &c); err != nil {
			return nil, err
		}
		rs := []rune(c)
		i := strings.Index(c, q)
		if i > 0 {
			rs = []rune(c[:i])
			start := max(0, len(rs)-45)
			rs = []rune(c)[start:]
		}
		if len(rs) > 220 {
			rs = rs[:220]
		}
		out = append(out, map[string]string{"path": p, "snippet": string(rs)})
	}
	return out, rows.Err()
}

func unzipInto(z *zip.Reader, root string) error {
	if len(z.File) > 50000 {
		return errors.New("压缩包文件过多")
	}
	var total uint64
	seen := map[string]bool{}
	for _, f := range z.File {
		p := strings.TrimSuffix(f.Name, "/")
		if err := validPath(p); err != nil {
			return err
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return errors.New("导入不支持符号链接")
		}
		if seen[p] {
			return fmt.Errorf("重复路径: %s", p)
		}
		seen[p] = true
		if f.UncompressedSize64 > maxFile {
			return errors.New("单文件超过 128MB")
		}
		total += f.UncompressedSize64
		if total > 1<<30 {
			return errors.New("解压后超过 1GB")
		}
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range z.File {
		p := strings.TrimSuffix(f.Name, "/")
		if f.FileInfo().IsDir() {
			if err = r.MkdirAll(p, 0700); err != nil {
				return err
			}
			continue
		}
		rd, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(rd, maxFile+1))
		rd.Close()
		if err != nil {
			return err
		}
		if err = atomicRoot(r, p, b); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) ImportZip(project string, reader io.ReaderAt, size int64) error {
	return s.ImportZipTo(project, reader, size, "")
}
func (s *Store) ImportZipTo(project string, reader io.ReaderAt, size int64, target string) error {
	if target != "" {
		if e := validPath(target); e != nil {
			return e
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.CheckUnlocked(project); err != nil {
		return err
	}
	root, err := s.ProjectRoot(project)
	if err != nil {
		return err
	}
	z, err := zip.NewReader(reader, size)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), "import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = unzipInto(z, stage); err != nil {
		return err
	}
	files := []string{}
	dirs := []string{}
	err = filepath.WalkDir(stage, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() && p != stage {
			rel, e := filepath.Rel(stage, p)
			if e != nil {
				return e
			}
			dirs = append(dirs, filepath.ToSlash(rel))
		}
		if !d.IsDir() {
			rel, e := filepath.Rel(stage, p)
			if e != nil {
				return e
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	conflicts := []string{}
	for _, p := range files {
		if _, e := r.Stat(path.Join(target, p)); e == nil {
			conflicts = append(conflicts, path.Join(target, p))
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("%w: 导入不会覆盖以下路径：%s", ErrConflict, strings.Join(conflicts, "、"))
	}
	for _, dir := range dirs {
		if e := r.MkdirAll(path.Join(target, dir), 0700); e != nil {
			return e
		}
	}
	sort.Strings(files)
	for _, p := range files {
		b, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(p)))
		if err != nil {
			return err
		}
		if _, err = s.saveFile(project, path.Join(target, p), b, "", false, ""); err != nil {
			return err
		}
	}
	return s.Emit("", "", "project.imported", map[string]string{"projectId": project})
}
func (s *Store) ExportZip(project string, w io.Writer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.CheckUnlocked(project); err != nil {
		return err
	}
	files, err := s.Files(project)
	if err != nil {
		return err
	}
	r, err := s.openRoot(project)
	if err != nil {
		return err
	}
	defer r.Close()
	z := zip.NewWriter(w)
	for _, f := range files {
		if f.Directory {
			if _, err := z.Create(strings.TrimSuffix(f.Path, "/") + "/"); err != nil {
				z.Close()
				return err
			}
			continue
		}
		b, err := readRoot(r, f.Path)
		if err != nil {
			z.Close()
			return err
		}
		out, err := z.Create(f.Path)
		if err != nil {
			z.Close()
			return err
		}
		if _, err = out.Write(b); err != nil {
			z.Close()
			return err
		}
	}
	return z.Close()
}
