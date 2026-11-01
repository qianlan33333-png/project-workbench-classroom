package workbench

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type upload struct{ ID, Name, ConnectionID, ActorID string }

func (s *Store) StageUpload(connection, actor, name string, b []byte) (string, error) {
	c, e := s.Connection(connection)
	if e != nil || !c.Enabled || c.OwnerID != actor {
		return "", errors.New("附件来源没有绑定本人")
	}
	if len(b) > maxFile {
		return "", errors.New("附件超过 128MB")
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if e = validPath(name); e != nil {
		return "", e
	}
	u := upload{newID(), name, connection, actor}
	dir := filepath.Join(s.Root, "uploads", u.ID)
	if e = os.Mkdir(dir, 0700); e != nil {
		return "", e
	}
	if e = os.WriteFile(filepath.Join(dir, "content"), b, 0600); e != nil {
		return "", e
	}
	if e = os.WriteFile(filepath.Join(dir, "meta.json"), []byte(encode(u)), 0600); e != nil {
		return "", e
	}
	return "upload:" + u.ID, nil
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
			r.Attachments = append(r.Attachments, reference)
			continue
		}
		id := strings.TrimPrefix(reference, "upload:")
		if e = validPath(id); e != nil || len(id) != 36 || strings.Contains(id, "/") {
			return errors.New("附件标识无效")
		}
		dir := filepath.Join(s.Root, "uploads", id)
		raw, e := os.ReadFile(filepath.Join(dir, "meta.json"))
		if e != nil {
			return e
		}
		u, e := decodeJSON[upload](string(raw))
		if e != nil {
			return e
		}
		if u.ConnectionID != m.Incoming.ConnectionID || u.ActorID != m.Incoming.ActorID {
			return errors.New("附件来源与消息不匹配")
		}
		b, e := os.ReadFile(filepath.Join(dir, "content"))
		if e != nil {
			return e
		}
		path := "附件/" + id[:8] + "-" + u.Name
		if _, e = root.Stat(path); e == nil {
			return errors.New("附件目标已存在，请人工核对")
		}
		if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e = atomicRoot(root, path, b); e != nil {
			return e
		}
		r.Attachments = append(r.Attachments, path)
	}
	return s.saveRun(*r)
}
