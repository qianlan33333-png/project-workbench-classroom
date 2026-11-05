package workbench

import (
	"context"
	"database/sql"
	"errors"
	"github.com/gin-gonic/gin"
)

const projectHistorySchema = `CREATE TABLE IF NOT EXISTS project_history_sources(project_id TEXT NOT NULL REFERENCES projects(id),source_id TEXT NOT NULL REFERENCES projects(id),data TEXT NOT NULL,PRIMARY KEY(project_id,source_id));`

type ProjectHistorySource struct {
	ProjectID string `json:"projectId"`
	SourceID  string `json:"sourceId"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
}

func (s *Store) SetProjectHistorySources(target string, ids []string) ([]ProjectHistorySource, error) {
	if len(ids) > 20 {
		return nil, errors.New("历史来源不能超过20个")
	}
	out := []ProjectHistorySource{}
	e := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		p, e := getJSON[Project](tx, "SELECT data FROM projects WHERE id=?", target)
		if e != nil || p.Archived || target == personalProjectID {
			return errors.New("请选择有效业务项目")
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if id == target || seen[id] || id == personalProjectID {
				return errors.New("历史来源无效或重复")
			}
			seen[id] = true
			source, e := getJSON[Project](tx, "SELECT data FROM projects WHERE id=?", id)
			if e != nil || !source.Archived {
				return errors.New("历史来源必须先完成收拢并归档")
			}
			v := ProjectHistorySource{ProjectID: target, SourceID: id, Name: source.Name, CreatedAt: now()}
			if _, e = tx.Exec("INSERT INTO project_history_sources VALUES(?,?,?) ON CONFLICT(project_id,source_id) DO UPDATE SET data=excluded.data", target, id, encode(v)); e != nil {
				return e
			}
			out = append(out, v)
		}
		// Additive: a later call cannot silently erase the only discoverable link to
		// retained records. Source removal stays an explicit future owner operation.
		return nil
	})
	return out, e
}
func (s *Store) ProjectHistorySources(target string) ([]ProjectHistorySource, error) {
	if _, e := s.Project(target); e != nil {
		return nil, e
	}
	return listJSON[ProjectHistorySource](s.DB, "SELECT data FROM project_history_sources WHERE project_id=? ORDER BY source_id", target)
}
func (s *Store) ProjectHistorySessions(target string) ([]Session, error) {
	return listJSON[Session](s.DB, "SELECT s.data FROM sessions s JOIN project_history_sources h ON h.source_id=s.project_id WHERE h.project_id=? AND coalesce(s.data->>'$.recycledAt',0)=0 ORDER BY s.updated_at DESC LIMIT 200", target)
}
func (s *Store) ProjectHistoryRuns(target, session string) ([]Run, error) {
	var n int
	if e := s.DB.QueryRow("SELECT count(*) FROM sessions s JOIN project_history_sources h ON h.source_id=s.project_id WHERE h.project_id=? AND s.id=? AND coalesce(s.data->>'$.recycledAt',0)=0", target, session).Scan(&n); e != nil || n != 1 {
		return nil, ErrNotFound
	}
	return s.Runs(session)
}
func (s *Store) projectHistoryAllowed(target, source string) bool {
	if target == source {
		return true
	}
	var n int
	e := s.DB.QueryRow("SELECT count(*) FROM project_history_sources WHERE project_id=? AND source_id=?", target, source).Scan(&n)
	return e == nil && n == 1
}
func (a *API) mountProjectHistory(g *gin.RouterGroup) {
	g.POST("/projects/:id/merge-history", func(c *gin.Context) {
		bind(c, func(q ProjectMerge) (any, error) { return a.Store.CommitProjectMerge(c.Param("id"), q) })
	})
	g.GET("/projects/:id/history-sources", func(c *gin.Context) { v, e := a.Store.ProjectHistorySources(c.Param("id")); reply(c, v, e) })
	g.PUT("/projects/:id/history-sources", func(c *gin.Context) {
		bind(c, func(q struct {
			SourceIDs []string `json:"sourceIds"`
		}) (any, error) {
			return a.Store.SetProjectHistorySources(c.Param("id"), q.SourceIDs)
		})
	})
	g.GET("/projects/:id/history-sessions", func(c *gin.Context) { v, e := a.Store.ProjectHistorySessions(c.Param("id")); reply(c, v, e) })
	g.GET("/projects/:id/history-runs", func(c *gin.Context) {
		v, e := a.Store.ProjectHistoryRuns(c.Param("id"), c.Query("sessionId"))
		reply(c, v, e)
	})
}

// CommitProjectMerge only switches ownership after files/configuration copies
// have been independently verified. Old sessions and every historical row stay put.
type ProjectMerge struct {
	SourceIDs     []string `json:"sourceIds"`
	ScheduleMoves []struct {
		FromID string   `json:"fromId"`
		ToID   string   `json:"toId"`
		Source Schedule `json:"source"`
		Target Schedule `json:"target"`
	} `json:"scheduleMoves"`
}

func (s *Store) CommitProjectMerge(target string, q ProjectMerge) ([]ProjectHistorySource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Never wait for scheduleMu while holding mu: scheduled follow-ups use the
	// inverse pair. A busy scheduler makes merge retry without a lock cycle.
	if !s.scheduleMu.TryLock() {
		return nil, ErrLocked
	}
	defer s.scheduleMu.Unlock()
	if len(q.SourceIDs) < 1 || len(q.SourceIDs) > 20 {
		return nil, errors.New("合并来源数量无效")
	}
	out := []ProjectHistorySource{}
	e := s.Transaction(context.Background(), func(tx *sql.Tx) error {
		p, e := getJSON[Project](tx, "SELECT data FROM projects WHERE id=?", target)
		if e != nil || p.Archived || target == personalProjectID {
			return errors.New("合并目标无效")
		}
		sources := map[string]Project{}
		for _, id := range q.SourceIDs {
			if id == target || id == personalProjectID || sources[id].ID != "" {
				return errors.New("合并来源无效")
			}
			source, e := getJSON[Project](tx, "SELECT data FROM projects WHERE id=?", id)
			if e != nil {
				return e
			}
			var active int
			for _, sqlq := range []string{"SELECT count(*) FROM runs WHERE project_id=? AND state IN ('queued','running','waiting','stopping')", "SELECT count(*) FROM component_calls WHERE data->>'$.projectId'=? AND state NOT IN ('succeeded','failed','cancelled')", "SELECT count(*) FROM component_reviews WHERE data->>'$.projectId'=? AND state NOT IN ('completed','failed','cancelled')"} {
				if e = tx.QueryRow(sqlq, id).Scan(&active); e != nil {
					return e
				}
				if active > 0 {
					return errors.New("来源仍有在途任务，请收拢后合并")
				}
			}
			sources[id] = source
		}
		moved := map[string]bool{}
		for _, m := range q.ScheduleMoves {
			from, e := getJSON[Schedule](tx, "SELECT data FROM schedules WHERE id=?", m.FromID)
			if e != nil {
				return e
			}
			to, e := getJSON[Schedule](tx, "SELECT data FROM schedules WHERE id=?", m.ToID)
			if e != nil {
				return e
			}
			if sources[from.ProjectID].ID == "" || to.ProjectID != target || from.ID == to.ID || moved[from.ID] || to.Enabled {
				return errors.New("定时迁移映射无效或目标已启用")
			}
			// Compare complete submitted snapshots; no schedule edit is silently lost.
			if encode(from) != encode(m.Source) || encode(to) != encode(m.Target) {
				return ErrConflict
			}
			// Retain the source's exact timing, exceptions and business definition.
			// SaveSchedule's new-record path normalizes those fields; the merge restores
			// them from the locked source while preserving the new identity and binding.
			targetID, targetSession, created, version := to.ID, to.SessionID, to.CreatedAt, to.Version
			targetComponent := to.Component
			to = from
			to.ID = targetID
			to.ProjectID = target
			to.SessionID = targetSession
			to.CreatedAt = created
			to.Version = version + 1
			if from.Component != nil {
				if targetComponent == nil {
					return errors.New("目标定时缺少组件绑定")
				}
				copyConfig := *from.Component
				copyConfig.SessionID = targetSession
				copyConfig.PluginID = targetComponent.PluginID
				copyConfig.PluginVersion = targetComponent.PluginVersion
				to.Component = &copyConfig
				if copyConfig.Mode == "expert" {
					prompt, er := s.componentExpertPrompt(copyConfig)
					if er != nil {
						return er
					}
					to.Instruction = prompt
				}
			}
			to.Enabled = from.Enabled
			from.Enabled = false
			from.Version++
			if _, e = tx.Exec("UPDATE schedules SET enabled=?,next_at=?,data=? WHERE id=?", to.Enabled, to.NextAt, encode(to), to.ID); e != nil {
				return e
			}
			if _, e = tx.Exec("UPDATE schedules SET enabled=0,data=? WHERE id=?", encode(from), from.ID); e != nil {
				return e
			}
			moved[from.ID] = true
		}
		for id, source := range sources {
			var active int
			if e = tx.QueryRow("SELECT count(*) FROM schedules WHERE project_id=? AND enabled=1", id).Scan(&active); e != nil {
				return e
			}
			if active > 0 {
				return errors.New("来源仍有未转移的启用定时任务")
			}
			source.Archived = true
			if _, e = tx.Exec("UPDATE projects SET archived=1,data=? WHERE id=?", encode(source), id); e != nil {
				return e
			}
			v := ProjectHistorySource{ProjectID: target, SourceID: id, Name: source.Name, CreatedAt: now()}
			if _, e = tx.Exec("INSERT INTO project_history_sources VALUES(?,?,?) ON CONFLICT(project_id,source_id) DO NOTHING", target, id, encode(v)); e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	return out, e
}
