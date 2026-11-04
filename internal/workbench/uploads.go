package workbench

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type upload struct {
	ID, Name, ConnectionID, ActorID string
	SessionID                       string `json:"sessionId,omitempty"`
}

func (s *Store) StageUpload(connection, actor, name string, b []byte) (string, error) {
	c, e := s.Connection(connection)
	if e != nil || !c.Enabled || c.OwnerID != actor {
		return "", errors.New("附件来源没有绑定本人")
	}
	return s.stageUpload(upload{ConnectionID: connection, ActorID: actor}, name, b)
}

func (s *Store) StagePersonalUpload(session, name string, b []byte) (string, error) {
	ss, e := s.Session(session)
	if e != nil || ss.ProjectID != personalProjectID || ss.Lifecycle != "" || ss.RecycledAt != 0 {
		return "", errors.New("请选择有效的个人对话")
	}
	return s.stageUpload(upload{ConnectionID: "web", ActorID: "owner", SessionID: session}, name, b)
}

func (s *Store) stageUpload(u upload, name string, b []byte) (string, error) {
	if len(b) > maxFile {
		return "", errors.New("附件超过 128MB")
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if e := validPath(name); e != nil {
		return "", e
	}
	if len(name) > 220 {
		return "", errors.New("文件名过长，请缩短后重试")
	}
	u.ID, u.Name = newID(), name
	dir := filepath.Join(s.Root, "uploads", u.ID)
	if e := os.Mkdir(dir, 0700); e != nil {
		return "", e
	}
	if e := os.WriteFile(filepath.Join(dir, "content"), b, 0600); e != nil {
		return "", e
	}
	if e := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(encode(u)), 0600); e != nil {
		return "", e
	}
	return "upload:" + u.ID, nil
}

func (s *Store) uploadMeta(reference string) (upload, error) {
	id := strings.TrimPrefix(reference, "upload:")
	if !strings.HasPrefix(reference, "upload:") || len(id) != 36 || strings.ContainsAny(id, "/\\") || validPath(id) != nil {
		return upload{}, errors.New("附件标识无效")
	}
	root, e := os.OpenRoot(filepath.Join(s.Root, "uploads"))
	if e != nil {
		return upload{}, e
	}
	defer root.Close()
	raw, e := root.ReadFile(id + "/meta.json")
	if e != nil {
		return upload{}, e
	}
	u, e := decodeJSON[upload](string(raw))
	if e == nil && u.ID != id {
		return upload{}, errors.New("附件标识不匹配")
	}
	return u, e
}

func (s *Store) validateMessageAttachments(in Incoming, ss Session) error {
	if ss.ProjectID == personalProjectID && len(in.Attachments) > 8 {
		return errors.New("一条消息最多添加8个附件")
	}
	for _, ref := range in.Attachments {
		if !strings.HasPrefix(ref, "upload:") {
			if ss.ProjectID == personalProjectID {
				return errors.New("请从输入框上传本地附件，项目资料仍按只读方式查询")
			}
			continue
		}
		u, e := s.uploadMeta(ref)
		if e != nil {
			return e
		}
		if u.SessionID != "" && (in.Source != "web" || ss.ProjectID != personalProjectID || ss.ID != u.SessionID) {
			return errors.New("附件不属于当前个人对话")
		}
		if u.ConnectionID != in.ConnectionID || u.ActorID != in.ActorID {
			return errors.New("附件来源与消息不匹配")
		}
		if in.Source == "web" && ss.ProjectID == personalProjectID && u.SessionID == "" {
			return errors.New("请使用本次个人对话上传的附件")
		}
	}
	return nil
}

func (s *Store) MaterializeAttachments(r *Run) error {
	m, e := s.Message(r.MessageID)
	if e != nil {
		return e
	}
	root, e := s.openRoot(r.ProjectID)
	if e != nil {
		return e
	}
	defer root.Close()
	var materialized []string
	for _, reference := range m.Incoming.Attachments {
		if !strings.HasPrefix(reference, "upload:") {
			if isChatChannel(m.Incoming.Source) {
				return errors.New("渠道附件需要已上传的本人附件标识")
			}
			if e = validPath(reference); e != nil {
				return e
			}
			if _, e = root.Stat(reference); e != nil {
				return e
			}
			materialized = append(materialized, reference)
			continue
		}
		id := strings.TrimPrefix(reference, "upload:")
		if e = validPath(id); e != nil || len(id) != 36 || strings.Contains(id, "/") {
			return errors.New("附件标识无效")
		}
		u, e := s.uploadMeta(reference)
		if e != nil {
			return e
		}
		if u.ConnectionID != m.Incoming.ConnectionID || u.ActorID != m.Incoming.ActorID || u.SessionID != "" && (m.Incoming.Source != "web" || r.ProjectID != personalProjectID || r.SessionID != u.SessionID) {
			return errors.New("附件来源与消息不匹配")
		}
		b, e := s.uploadContent(id)
		if e != nil {
			return e
		}
		path := "附件/" + id[:8] + "-" + u.Name
		if existing, er := root.ReadFile(path); er == nil {
			if digest(existing) != digest(b) {
				return errors.New("附件目标内容已变化，请人工核对")
			}
		} else {
			if !errors.Is(er, os.ErrNotExist) {
				return er
			}
			if e = atomicRoot(root, path, b); e != nil {
				return e
			}
		}
		materialized = append(materialized, path)
	}
	r.Attachments = materialized
	return s.saveRun(*r)
}

func (s *Store) uploadContent(id string) ([]byte, error) {
	root, e := os.OpenRoot(filepath.Join(s.Root, "uploads"))
	if e != nil {
		return nil, e
	}
	defer root.Close()
	f, e := root.Open(id + "/content")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Size() > maxFile {
		return nil, errors.New("附件内容无效或超限")
	}
	b, e := io.ReadAll(io.LimitReader(f, maxFile+1))
	if len(b) > maxFile {
		return nil, errors.New("附件内容超限")
	}
	return b, e
}
