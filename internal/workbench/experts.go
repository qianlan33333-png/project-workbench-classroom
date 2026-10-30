package workbench

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func expertManifest(b []byte) (Expert, error) {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return Expert{}, errors.New("专家入口需要 SKILL.md 及 YAML 元数据")
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return Expert{}, errors.New("SKILL.md 元数据未闭合")
	}
	var meta struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Tools       []string `yaml:"tools"`
	}
	if err := yaml.Unmarshal([]byte(s[4:4+end]), &meta); err != nil {
		return Expert{}, err
	}
	if strings.TrimSpace(meta.Name) == "" || strings.TrimSpace(meta.Description) == "" {
		return Expert{}, errors.New("专家需要 name 和 description")
	}
	return Expert{Name: meta.Name, Description: meta.Description, Tools: meta.Tools}, nil
}
func (s *Store) installExpertDir(root, id string) (Expert, error) {
	b, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return Expert{}, errors.New("专家目录根部需要 SKILL.md")
	}
	e, err := expertManifest(b)
	if err != nil {
		return e, err
	}
	if id == "" {
		e.ID = newID()
		e.CreatedAt = now()
	} else {
		old, err := s.Expert(id)
		if err != nil {
			return e, err
		}
		e.ID = id
		e.CreatedAt = old.CreatedAt
	}
	files := []string{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, er error) error {
		if er != nil {
			return er
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("专家包不支持符号链接")
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		rel, er := filepath.Rel(root, p)
		if er != nil {
			return er
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return e, err
	}
	sort.Strings(files)
	var manifest bytes.Buffer
	var total int64
	for _, p := range files {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			return e, err
		}
		total += int64(len(b))
		if total > 64<<20 {
			return e, errors.New("专家包超过 64MB")
		}
		manifest.WriteString(filepath.ToSlash(p) + "\x00" + digest(b) + "\n")
	}
	e.Digest = digest(manifest.Bytes())
	dest := filepath.Join(s.Root, "experts", e.ID, e.Digest)
	if err = os.MkdirAll(dest, 0700); err != nil {
		return e, err
	}
	r, err := os.OpenRoot(dest)
	if err != nil {
		return e, err
	}
	defer r.Close()
	for _, p := range files {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			return e, err
		}
		if err = atomicRoot(r, filepath.ToSlash(p), b); err != nil {
			return e, err
		}
	}
	_, err = s.DB.Exec("INSERT INTO experts VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", e.ID, encode(e))
	if err != nil {
		return e, err
	}
	return e, s.Emit("", "", "expert.installed", e)
}
func (s *Store) ImportExpert(reader io.ReaderAt, size int64, id string) (Expert, error) {
	z, err := zip.NewReader(reader, size)
	if err != nil {
		return Expert{}, err
	}
	dir, err := os.MkdirTemp(filepath.Join(s.Root, "uploads"), "expert-")
	if err != nil {
		return Expert{}, err
	}
	defer os.RemoveAll(dir)
	if err = unzipInto(z, dir); err != nil {
		return Expert{}, err
	}
	if _, err = os.Stat(filepath.Join(dir, "SKILL.md")); errors.Is(err, os.ErrNotExist) {
		entries, e := os.ReadDir(dir)
		if e != nil {
			return Expert{}, e
		}
		if len(entries) == 1 && entries[0].IsDir() {
			dir = filepath.Join(dir, entries[0].Name())
		}
	}
	return s.installExpertDir(dir, id)
}
func (s *Store) CreateExpert(name, description, instructions string) (Expert, error) {
	dir, err := os.MkdirTemp(filepath.Join(s.Root, "uploads"), "expert-")
	if err != nil {
		return Expert{}, err
	}
	defer os.RemoveAll(dir)
	meta, err := yaml.Marshal(map[string]string{"name": name, "description": description})
	if err != nil {
		return Expert{}, err
	}
	b := append([]byte("---\n"), meta...)
	b = append(b, []byte("---\n\n"+instructions+"\n")...)
	if err = os.WriteFile(filepath.Join(dir, "SKILL.md"), b, 0600); err != nil {
		return Expert{}, err
	}
	return s.installExpertDir(dir, "")
}
func (s *Store) ImportExpertGit(ctx context.Context, repository, commit, subdir, id string) (Expert, error) {
	u, err := url.Parse(repository)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return Expert{}, errors.New("请使用不含凭证的 HTTPS 仓库地址")
	}
	if !regexp.MustCompile(`^[a-fA-F0-9]{40}$`).MatchString(commit) {
		return Expert{}, errors.New("请提供完整的 40 位固定提交")
	}
	if subdir != "" {
		if err = validPath(subdir); err != nil {
			return Expert{}, err
		}
	}
	dir, err := os.MkdirTemp(filepath.Join(s.Root, "uploads"), "git-expert-")
	if err != nil {
		return Expert{}, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for _, args := range [][]string{{"init", dir}, {"-C", dir, "-c", "protocol.file.allow=never", "fetch", "--depth=1", repository, commit}, {"-C", dir, "checkout", "--detach", "FETCH_HEAD"}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
		if _, err = cmd.CombinedOutput(); err != nil {
			return Expert{}, errors.New("无法取得指定仓库提交，请核对地址和提交是否公开可用")
		}
	}
	return s.installExpertDir(filepath.Join(dir, filepath.FromSlash(subdir)), id)
}
