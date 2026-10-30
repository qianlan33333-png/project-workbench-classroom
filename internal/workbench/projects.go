package workbench

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) Projects() ([]Project, error) {
	return listJSON[Project](s.DB, "SELECT data FROM projects ORDER BY archived,name")
}
func (s *Store) Project(id string) (Project, error) {
	return getJSON[Project](s.DB, "SELECT data FROM projects WHERE id=?", id)
}
func (s *Store) ProjectRoot(id string) (string, error) {
	p, err := s.Project(id)
	if err != nil {
		return "", err
	}
	if p.Archived {
		return "", errors.New("项目已归档，请先恢复项目")
	}
	return filepath.Join(s.Root, "projects", p.ID, "files"), nil
}
func (s *Store) CreateProject(name, description string, aliases []string) (Project, error) {
	return s.ensureProject(newID(), name, description, aliases)
}
func (s *Store) ensureProject(id, name, description string, aliases []string) (Project, error) {
	if p, e := s.Project(id); e == nil {
		return p, nil
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return Project{}, errors.New("项目名称需为 1–80 个字")
	}
	p := Project{ID: id, Name: name, Description: description, Aliases: aliases, ExpertIDs: []string{}, PluginIDs: []string{}, CreatedAt: now()}
	sess := Session{ID: newID(), ProjectID: p.ID, Title: "项目助手", Kind: "assistant", CreatedAt: now(), UpdatedAt: now()}
	sess.HarnessID = "pw-" + sess.ID
	p.DefaultSessionID = sess.ID
	root := filepath.Join(s.Root, "projects", p.ID, "files")
	if err := os.MkdirAll(root, 0700); err != nil {
		return p, err
	}
	err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO projects VALUES(?,?,0,?)", p.ID, p.Name, encode(p)); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?)", sess.ID, sess.ProjectID, sess.HarnessID, sess.Kind, sess.UpdatedAt, encode(sess))
		return err
	})
	if err != nil {
		if existing, e := s.Project(id); e == nil {
			return existing, nil
		}
		os.RemoveAll(filepath.Dir(root))
		return p, err
	}
	return p, s.Emit(sess.ID, "", "project.created", p)
}
func (s *Store) UpdateProject(id string, update Project) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.Project(id)
	if err != nil {
		return p, err
	}
	if err = s.CheckUnlocked(id); err != nil {
		return p, err
	}
	if strings.TrimSpace(update.Name) == "" {
		return p, errors.New("项目名称不能为空")
	}
	for _, id := range update.ExpertIDs {
		if _, err = s.Expert(id); err != nil {
			return p, errors.New("专家不存在")
		}
	}
	for _, id := range update.PluginIDs {
		if _, err = s.Plugin(id); err != nil {
			return p, errors.New("插件不存在")
		}
	}
	if update.DefaultExpertID != "" && !contains(update.ExpertIDs, update.DefaultExpertID) {
		return p, errors.New("默认专家必须先加入项目")
	}
	p.Name = strings.TrimSpace(update.Name)
	p.Aliases = update.Aliases
	p.Description = update.Description
	p.ExpertIDs = update.ExpertIDs
	p.DefaultExpertID = update.DefaultExpertID
	p.PluginIDs = update.PluginIDs
	p.Archived = update.Archived
	_, err = s.DB.Exec("UPDATE projects SET name=?,archived=?,data=? WHERE id=?", p.Name, p.Archived, encode(p), p.ID)
	if err != nil {
		return p, err
	}
	return p, s.Emit("", "", "project.updated", p)
}
func (s *Store) Sessions(project string) ([]Session, error) {
	return listJSON[Session](s.DB, "SELECT data FROM sessions WHERE project_id=? AND kind<>'automation' ORDER BY kind='assistant' DESC,updated_at DESC", project)
}
func (s *Store) Session(id string) (Session, error) {
	return getJSON[Session](s.DB, "SELECT data FROM sessions WHERE id=?", id)
}
func (s *Store) CreateSession(project, title, kind, expert string) (Session, error) {
	p, err := s.Project(project)
	if err != nil {
		return Session{}, err
	}
	if p.Archived {
		return Session{}, errors.New("项目已归档")
	}
	if expert != "" && expert != "general" && !contains(p.ExpertIDs, expert) {
		return Session{}, errors.New("此专家尚未加入项目")
	}
	if kind != "topic" && kind != "automation" {
		return Session{}, errors.New("不支持的对话类型")
	}
	if strings.TrimSpace(title) == "" {
		title = "新对话"
	}
	sess := Session{ID: newID(), ProjectID: project, Title: title, Kind: kind, ExpertID: expert, CreatedAt: now(), UpdatedAt: now()}
	sess.HarnessID = "pw-" + sess.ID
	_, err = s.DB.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?)", sess.ID, project, sess.HarnessID, kind, sess.UpdatedAt, encode(sess))
	if err != nil {
		return sess, err
	}
	return sess, s.Emit(sess.ID, "", "session.created", sess)
}
func (s *Store) UpdateSession(id, title, expert string) (Session, error) {
	sess, err := s.Session(id)
	if err != nil {
		return sess, err
	}
	p, err := s.Project(sess.ProjectID)
	if err != nil {
		return sess, err
	}
	if expert != "" && expert != "general" && !contains(p.ExpertIDs, expert) {
		return sess, errors.New("此专家尚未加入项目")
	}
	if strings.TrimSpace(title) != "" {
		sess.Title = title
	}
	sess.ExpertID = expert
	sess.UpdatedAt = now()
	_, err = s.DB.Exec("UPDATE sessions SET data=?,updated_at=? WHERE id=?", encode(sess), sess.UpdatedAt, id)
	return sess, err
}
func contains(a []string, v string) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}
func (s *Store) Expert(id string) (Expert, error) {
	return getJSON[Expert](s.DB, "SELECT data FROM experts WHERE id=?", id)
}
func (s *Store) Experts() ([]Expert, error) {
	return listJSON[Expert](s.DB, "SELECT data FROM experts ORDER BY id")
}
