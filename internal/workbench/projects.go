package workbench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) Projects() ([]Project, error) {
	return listJSON[Project](s.DB, "SELECT data FROM projects WHERE id <> 'personal-assistant' ORDER BY archived,name")
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
	return s.updateProject(id, update)
}
func (s *Store) updateProject(id string, update Project) (Project, error) {
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
	oldExperts := append([]string(nil), p.ExpertIDs...)
	p.Name = strings.TrimSpace(update.Name)
	p.Aliases = update.Aliases
	descriptionChanged := p.Description != update.Description
	p.Description = update.Description
	p.ExpertIDs = update.ExpertIDs
	p.DefaultExpertID = update.DefaultExpertID
	p.PluginIDs = update.PluginIDs
	p.Archived = update.Archived
	s.expertMu.Lock()
	defer s.expertMu.Unlock()
	err = s.Transaction(context.Background(), func(tx *sql.Tx) error {
		if _, e := tx.Exec("UPDATE projects SET name=?,archived=?,data=? WHERE id=?", p.Name, p.Archived, encode(p), p.ID); e != nil {
			return e
		}
		if descriptionChanged {
			var raw string
			c := ProjectContext{ProjectID: p.ID}
			e := tx.QueryRow("SELECT data FROM project_contexts WHERE project_id=?", p.ID).Scan(&raw)
			if e == nil {
				c, e = decodeJSON[ProjectContext](raw)
				if e != nil {
					return e
				}
			} else if e != sql.ErrNoRows {
				return e
			}
			if _, e = tx.Exec("INSERT OR IGNORE INTO context_versions VALUES(?,?,?)", p.ID, c.Version, encode(c)); e != nil {
				return e
			}
			c.Manual = p.Description
			c.ManualAt = now()
			c.Description = p.Description
			c.Version++
			c.UpdatedAt = now()
			c.State = "pending"
			if _, e = tx.Exec("INSERT INTO project_contexts VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET data=excluded.data", p.ID, encode(c)); e != nil {
				return e
			}
		}
		for _, expert := range oldExperts {
			if contains(p.ExpertIDs, expert) {
				continue
			}
			if _, e := tx.Exec("UPDATE sessions SET data=json_set(data,'$.expertId','general') WHERE project_id=? AND json_extract(data,'$.expertId')=?", p.ID, expert); e != nil {
				return e
			}
			if _, e := tx.Exec("UPDATE schedules SET enabled=0,data=json_set(data,'$.enabled',json('false'),'$.expertId','','$.pausedReason','专家已从项目移除，请重新选择专家后启用') WHERE project_id=? AND json_extract(data,'$.expertId')=?", p.ID, expert); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return p, err
	}

	return p, s.Emit("", "", "project.updated", p)
}

func (s *Store) PatchProject(id string, fields map[string]json.RawMessage) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.Project(id)
	if err != nil {
		return p, err
	}
	for key := range fields {
		switch key {
		case "name", "description", "aliases", "expertIds", "defaultExpertId", "pluginIds", "archived":
		default:
			return p, errors.New("不支持修改项目字段：" + key)
		}
	}
	if err = json.Unmarshal([]byte(encode(fields)), &p); err != nil {
		return p, err
	}
	return s.updateProject(id, p)
}
func (s *Store) Sessions(project string) ([]Session, error) {
	return listJSON[Session](s.DB, "SELECT data FROM sessions WHERE project_id=? AND kind<>'automation' AND coalesce(json_extract(data,'$.draft'),0)=0 AND coalesce(json_extract(data,'$.recycledAt'),0)=0 ORDER BY coalesce(json_extract(data,'$.pinnedAt'),0)>0 DESC,coalesce(json_extract(data,'$.pinnedAt'),0) DESC,updated_at DESC", project)
}
func (s *Store) Session(id string) (Session, error) {
	ss, e := s.rawSession(id)
	if e == nil && ss.RecycledAt > 0 {
		return Session{}, errors.New("对话已移入回收站，请先找回")
	}
	return ss, e
}
func (s *Store) CreateSession(project, title, kind, expert string) (Session, error) {
	s.expertMu.RLock()
	defer s.expertMu.RUnlock()
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
	title = strings.TrimSpace(title)
	if len([]rune(title)) > 80 {
		return Session{}, errors.New("对话标题最多 80 个字")
	}
	sess := Session{ID: newID(), ProjectID: project, Title: title, Kind: kind, ExpertID: expert, CreatedAt: now(), UpdatedAt: now()}
	sess.TitleSource = "manual"
	if title == "" && kind == "topic" {
		sess.Draft = true
		sess.TitleSource = "pending"
	}
	sess.HarnessID = "pw-" + sess.ID
	_, err = s.DB.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?)", sess.ID, project, sess.HarnessID, kind, sess.UpdatedAt, encode(sess))
	if err != nil {
		return sess, err
	}
	return sess, s.Emit(sess.ID, "", "session.created", sess)
}

// UpdateSession patches only supplied fields; renaming never resets the expert.
func (s *Store) UpdateSession(id string, title, expert *string) (Session, error) {
	s.expertMu.RLock()
	defer s.expertMu.RUnlock()
	var sess Session
	err := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRow("SELECT data FROM sessions WHERE id=?", id).Scan(&raw); e != nil {
			return e
		}
		var e error
		sess, e = decodeJSON[Session](raw)
		if e != nil {
			return e
		}
		if sess.RecycledAt > 0 || sess.Lifecycle != "" {
			return errors.New("对话正在归档或已在回收站")
		}
		if title != nil {
			value := strings.TrimSpace(*title)
			if value == "" || len([]rune(value)) > 80 {
				return errors.New("对话标题需为 1–80 个字")
			}
			sess.Title, sess.TitleSource = value, "manual"
		}
		if expert != nil {
			var projectRaw string
			if e = tx.QueryRow("SELECT data FROM projects WHERE id=?", sess.ProjectID).Scan(&projectRaw); e != nil {
				return e
			}
			p, e := decodeJSON[Project](projectRaw)
			if e != nil {
				return e
			}
			if *expert != "" && *expert != "general" && !contains(p.ExpertIDs, *expert) {
				return errors.New("此专家尚未加入项目")
			}
			sess.ExpertID = *expert
		}
		sess.UpdatedAt = max(now(), sess.UpdatedAt+1)
		_, e = tx.Exec("UPDATE sessions SET data=?,updated_at=? WHERE id=?", encode(sess), sess.UpdatedAt, id)
		return e
	})
	if err != nil {
		return sess, err
	}
	return sess, s.Emit(sess.ID, "", "session.updated", sess)
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
