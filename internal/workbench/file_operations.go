package workbench

import (
	"errors"
	"fmt"
	"os"
	"path"
)

const maxEditableFile = 2 << 20

func (s *Store) CreateDirectory(project, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.CheckUnlocked(project); err != nil {
		return err
	}
	if err := validPath(name); err != nil {
		return err
	}
	r, err := s.openRoot(project)
	if err != nil {
		return err
	}
	defer r.Close()
	if err = r.MkdirAll(name, 0700); err != nil {
		return err
	}
	return s.Emit("", "", "directory.created", map[string]string{"projectId": project, "path": name})
}

// A rename-on-conflict is selected under the same lock as the eventual write.
func (s *Store) UploadFile(project, name string, data []byte, base, policy string) (Revision, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.CheckUnlocked(project); err != nil {
		return Revision{}, false, err
	}
	if err := validPath(name); err != nil {
		return Revision{}, false, err
	}
	if policy != "" && policy != "skip" && policy != "rename" && policy != "replace" {
		return Revision{}, false, errors.New("上传冲突策略无效")
	}
	r, err := s.openRoot(project)
	if err != nil {
		return Revision{}, false, err
	}
	defer r.Close()
	_, exists := r.Stat(name)
	if exists != nil && !errors.Is(exists, os.ErrNotExist) {
		return Revision{}, false, exists
	}
	if exists == nil {
		if policy == "skip" {
			return Revision{Path: name}, true, nil
		}
		if policy == "rename" {
			original := name
			ext := path.Ext(name)
			for n := 2; ; n++ {
				name = fmt.Sprintf("%s (%d)%s", original[:len(original)-len(ext)], n, ext)
				_, e := r.Stat(name)
				if errors.Is(e, os.ErrNotExist) {
					break
				}
				if e != nil {
					return Revision{}, false, e
				}
			}
			base = ""
		}
	}
	v, err := s.saveFile(project, name, data, base, false, "")
	return v, false, err
}
