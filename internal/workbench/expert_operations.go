package workbench

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type ExpertFileChange struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	To      string `json:"to,omitempty"`
	Content string `json:"content,omitempty"`
	Upload  string `json:"upload,omitempty"`
}
type ExpertChanges struct {
	BaseDigest string             `json:"baseDigest"`
	Metadata   map[string]string  `json:"metadata,omitempty"`
	Files      []ExpertFileChange `json:"files,omitempty"`
}
type ExpertUsage struct {
	Projects  []Project  `json:"projects"`
	Sessions  []Session  `json:"sessions"`
	Schedules []Schedule `json:"schedules"`
}

func (s *Store) expertRoot(id, version string) (string, error) {
	if err := validPath(id); err != nil || strings.Contains(id, "/") {
		return "", ErrNotFound
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(version) {
		return "", errors.New("专家版本无效")
	}
	return filepath.Join(s.Root, "experts", id, version), nil
}
func (s *Store) ExpertVersion(id, version string) (Expert, error) {
	root, err := s.expertRoot(id, version)
	if err != nil {
		return Expert{}, err
	}
	b, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return Expert{}, err
	}
	e, err := expertManifest(b)
	e.ID = id
	e.Digest = version
	if info, er := os.Stat(root); er == nil {
		e.UpdatedAt = info.ModTime().UnixMilli()
	}
	return e, err
}
func (s *Store) ExpertVersions(id string) ([]Expert, error) {
	if _, err := s.Expert(id); err != nil {
		return nil, err
	}
	dirs, err := os.ReadDir(filepath.Join(s.Root, "experts", id))
	if err != nil {
		return nil, err
	}
	out := []Expert{}
	for _, d := range dirs {
		if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			e, er := s.ExpertVersion(id, d.Name())
			if er != nil {
				return nil, er
			}
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}
func (s *Store) ExpertUsage(id string) (ExpertUsage, error) {
	out := ExpertUsage{Projects: []Project{}, Sessions: []Session{}, Schedules: []Schedule{}}
	ps, err := s.Projects()
	if err != nil {
		return out, err
	}
	for _, p := range ps {
		if contains(p.ExpertIDs, id) || p.DefaultExpertID == id {
			out.Projects = append(out.Projects, p)
		}
	}
	out.Sessions, err = listJSON[Session](s.DB, "SELECT data FROM sessions WHERE json_extract(data,'$.expertId')=?", id)
	if err != nil {
		return out, err
	}
	out.Schedules, err = listJSON[Schedule](s.DB, "SELECT data FROM schedules WHERE json_extract(data,'$.expertId')=?", id)
	return out, err
}
func directoryFiles(root string) ([]FileInfo, error) {
	out := []FileInfo{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("能力包不能包含符号链接")
		}
		rel, _ := filepath.Rel(root, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, FileInfo{Path: filepath.ToSlash(rel), Directory: d.IsDir(), Size: info.Size(), ModifiedAt: info.ModTime().UnixMilli()})
		return nil
	})
	return out, err
}
func (s *Store) ExpertFiles(id, version string) ([]FileInfo, error) {
	root, e := s.expertRoot(id, version)
	if e != nil {
		return nil, e
	}
	return directoryFiles(root)
}
func (s *Store) ReadExpertFile(id, version, path string) ([]byte, error) {
	root, e := s.expertRoot(id, version)
	if e != nil {
		return nil, e
	}
	r, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return readRoot(r, path)
}
func copyExpertFiles(source, dest string) error {
	files, err := directoryFiles(source)
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range files {
		if f.Directory {
			if err = r.MkdirAll(f.Path, 0700); err != nil {
				return err
			}
			continue
		}
		b, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(f.Path)))
		if err != nil {
			return err
		}
		if err = atomicRoot(r, f.Path, b); err != nil {
			return err
		}
	}
	return nil
}
func expertMetadata(b []byte, fields map[string]string) ([]byte, error) {
	normalized := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return nil, errors.New("缺少 SKILL.md 元数据")
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return nil, errors.New("元数据未闭合")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(normalized[4:4+end]), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("元数据必须是对象")
	}
	mapping := doc.Content[0]
	for key, value := range fields {
		target := key
		if key == "name" {
			target = "display-name"
		}
		if target != "display-name" && target != "description" && target != "skill-name" {
			return nil, errors.New("不支持的专家字段：" + key)
		}
		if target == "skill-name" {
			target = "name"
		}
		found := false
		for i := 0; i < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == target {
				mapping.Content[i+1].SetString(value)
				found = true
				break
			}
		}
		if !found {
			mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: target}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
		}
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	enc.Close()
	out.WriteString("---")
	out.WriteString(normalized[4+end+4:])
	return out.Bytes(), nil
}
func (s *Store) ChangeExpert(id string, changes ExpertChanges, uploads map[string][]byte) (Expert, error) {
	s.expertMu.Lock()
	defer s.expertMu.Unlock()
	old, err := s.Expert(id)
	if err != nil {
		return old, err
	}
	if changes.BaseDigest != old.Digest {
		return old, ErrConflict
	}
	if len(changes.Metadata) == 0 && len(changes.Files) == 0 {
		return old, errors.New("没有需要保存的修改")
	}
	if _, ok := changes.Metadata["skill-name"]; ok {
		return old, errors.New("内部 Skill 标识不能修改")
	}
	root, err := os.MkdirTemp(filepath.Join(s.Root, "uploads"), "expert-edit-")
	if err != nil {
		return old, err
	}
	defer os.RemoveAll(root)
	source, _ := s.expertRoot(id, old.Digest)
	if err = copyExpertFiles(source, root); err != nil {
		return old, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return old, err
	}
	defer r.Close()
	for _, change := range changes.Files {
		if err = validPath(change.Path); err != nil {
			return old, err
		}
		if change.Path == "SKILL.md" && (change.Op == "delete" || change.Op == "rename") {
			return old, errors.New("不能删除或重命名专家入口 SKILL.md")
		}
		if change.Path == "SKILL.md" && len(changes.Metadata) > 0 {
			return old, errors.New("请分别保存基础信息和 SKILL.md 原文")
		}
		switch change.Op {
		case "write":
			b := []byte(change.Content)
			if change.Upload != "" {
				var ok bool
				b, ok = uploads[change.Upload]
				if !ok {
					return old, errors.New("上传文件缺失")
				}
			}
			err = atomicRoot(r, change.Path, b)
		case "mkdir":
			err = r.MkdirAll(change.Path, 0700)
		case "delete":
			err = r.Remove(change.Path)
		case "rename":
			if err = validPath(change.To); err == nil {
				if change.To == "SKILL.md" {
					return old, errors.New("不能替换专家入口")
				}
				if _, e := r.Stat(change.To); e == nil {
					return old, ErrConflict
				} else if !errors.Is(e, os.ErrNotExist) {
					return old, e
				}
				err = r.Rename(change.Path, change.To)
			}
		default:
			err = errors.New("文件操作无效")
		}
		if err != nil {
			return old, fmt.Errorf("%s: %w", change.Path, err)
		}
	}
	if len(changes.Metadata) > 0 {
		b, er := readRoot(r, "SKILL.md")
		if er != nil {
			return old, er
		}
		b, er = expertMetadata(b, changes.Metadata)
		if er != nil {
			return old, er
		}
		if er = atomicRoot(r, "SKILL.md", b); er != nil {
			return old, er
		}
	}
	return s.installExpertDirLocked(root, id, changes.BaseDigest)
}
func (s *Store) CopyExpert(id string) (Expert, error) {
	s.expertMu.Lock()
	defer s.expertMu.Unlock()
	old, err := s.Expert(id)
	if err != nil {
		return old, err
	}
	root, err := os.MkdirTemp(filepath.Join(s.Root, "uploads"), "expert-copy-")
	if err != nil {
		return old, err
	}
	defer os.RemoveAll(root)
	source, _ := s.expertRoot(id, old.Digest)
	if err = copyExpertFiles(source, root); err != nil {
		return old, err
	}
	b, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return old, err
	}
	b, err = expertMetadata(b, map[string]string{"name": old.Name + " 副本", "skill-name": "expert-" + newID()[:8]})
	if err != nil {
		return old, err
	}
	if err = os.WriteFile(filepath.Join(root, "SKILL.md"), b, 0600); err != nil {
		return old, err
	}
	return s.installExpertDirLocked(root, "")
}
func (s *Store) ExportExpert(id, version string, w io.Writer) error {
	root, err := s.expertRoot(id, version)
	if err != nil {
		return err
	}
	files, err := directoryFiles(root)
	if err != nil {
		return err
	}
	z := zip.NewWriter(w)
	for _, f := range files {
		name := f.Path
		if f.Directory {
			name += "/"
		}
		out, e := z.Create(name)
		if e != nil {
			z.Close()
			return e
		}
		if !f.Directory {
			b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
			if e != nil {
				z.Close()
				return e
			}
			if _, e = out.Write(b); e != nil {
				z.Close()
				return e
			}
		}
	}
	return z.Close()
}
func (s *Store) DeleteExpert(id, base string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expertMu.Lock()
	defer s.expertMu.Unlock()
	old, err := s.Expert(id)
	if err != nil {
		return err
	}
	if old.Digest != base {
		return ErrConflict
	}
	usage, err := s.ExpertUsage(id)
	if err != nil {
		return err
	}
	err = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		for _, p := range usage.Projects {
			ids := []string{}
			for _, x := range p.ExpertIDs {
				if x != id {
					ids = append(ids, x)
				}
			}
			p.ExpertIDs = ids
			if p.DefaultExpertID == id {
				p.DefaultExpertID = ""
			}
			if _, e := tx.Exec("UPDATE projects SET data=? WHERE id=?", encode(p), p.ID); e != nil {
				return e
			}
		}
		for _, ss := range usage.Sessions {
			ss.ExpertID = "general"
			if _, e := tx.Exec("UPDATE sessions SET data=? WHERE id=?", encode(ss), ss.ID); e != nil {
				return e
			}
		}
		for _, sc := range usage.Schedules {
			sc.Enabled = false
			sc.ExpertID = ""
			sc.PausedReason = "原专家「" + old.Name + "」已删除，请重新选择专家后启用"
			if _, e := tx.Exec("UPDATE schedules SET enabled=0,data=? WHERE id=?", encode(sc), sc.ID); e != nil {
				return e
			}
		}
		_, e := tx.Exec("DELETE FROM experts WHERE id=?", id)
		return e
	})
	if err != nil {
		return err
	}
	return s.Emit("", "", "expert.deleted", map[string]string{"id": id})
}
