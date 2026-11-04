package workbench

import (
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *API) mountToday(g *gin.RouterGroup) {
	s := a.Store
	a.mountPlanner(g)
	g.POST("/personal/attachments", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 129<<20)
		header, e := c.FormFile("file")
		if e != nil {
			reply(c, nil, e)
			return
		}
		f, e := header.Open()
		if e != nil {
			reply(c, nil, e)
			return
		}
		defer f.Close()
		b, e := io.ReadAll(io.LimitReader(f, maxFile+1))
		if e != nil {
			reply(c, nil, e)
			return
		}
		path, e := s.StagePersonalUpload(c.PostForm("sessionId"), header.Filename, b)
		name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
		reply(c, gin.H{"path": path, "name": name, "size": len(b)}, e)
	})
	g.GET("/personal/followups", func(c *gin.Context) { v, e := s.Followups(); reply(c, v, e) })
	g.POST("/personal/followups/:id/continue", func(c *gin.Context) {
		bind(c, func(v struct {
			Version int64 `json:"version"`
		}) (any, error) {
			return s.ResumeFollowup(c.Param("id"), v.Version)
		})
	})
	g.POST("/personal/followups/:id/cancel", func(c *gin.Context) {
		bind(c, func(v struct {
			Version int64 `json:"version"`
		}) (any, error) {
			return s.CancelFollowup(c.Param("id"), v.Version)
		})
	})
	g.GET("/personal/understanding", func(c *gin.Context) { v, e := s.Understanding(); reply(c, v, e) })
	g.POST("/personal/understanding/refresh", func(c *gin.Context) { reply(c, gin.H{"state": "pending"}, s.RequestUnderstanding()) })
	g.GET("/personal/understanding/evidence", func(c *gin.Context) {
		offset, _ := strconv.Atoi(c.Query("offset"))
		v, e := s.MemoryEvidence(MemoryEvidenceQuery{Query: c.Query("query"), ID: c.Query("id"), ParagraphID: c.Query("paragraphId"), Offset: offset})
		reply(c, v, e)
	})
	g.GET("/personal/rules", func(c *gin.Context) { v, e := s.PersonalRules(); reply(c, v, e) })
	g.PUT("/personal/rules", func(c *gin.Context) { bind(c, func(v PersonalRules) (any, error) { return s.SavePersonalRules(v) }) })
	g.GET("/projects/:id/context", func(c *gin.Context) { v, e := s.ProjectContext(c.Param("id")); reply(c, v, e) })
	g.POST("/projects/:id/context/refresh", func(c *gin.Context) {
		c.JSON(409, gin.H{"error": "请在该项目的定时任务中执行整理"})
	})
	g.PUT("/projects/:id/context", func(c *gin.Context) {
		bind(c, func(v struct {
			Description string `json:"description"`
			Version     int64  `json:"version"`
		}) (any, error) {
			return s.CorrectContext(c.Param("id"), v.Description, v.Version)
		})
	})
	g.GET("/tasks", func(c *gin.Context) { v, e := s.Tasks(""); reply(c, v, e) })
	g.GET("/projects/:id/tasks", func(c *gin.Context) { v, e := s.Tasks(c.Param("id")); reply(c, v, e) })
	g.POST("/projects/:id/tasks", func(c *gin.Context) {
		bind(c, func(t ProjectTask) (any, error) {
			t.ID = ""
			t.ProjectID = c.Param("id")
			if t.ProjectID == personalProjectID {
				if _, e := s.PersonalSpace(); e != nil {
					return nil, e
				}
			}
			t.Source = ""
			return s.SaveTask(t)
		})
	})
	g.GET("/tasks/:id", func(c *gin.Context) { v, e := s.Task(c.Param("id")); reply(c, v, e) })
	g.PUT("/tasks/:id", func(c *gin.Context) {
		bind(c, func(t ProjectTask) (any, error) { t.ID = c.Param("id"); return s.SaveTask(t) })
	})
	g.DELETE("/tasks/:id", func(c *gin.Context) {
		v, _ := strconv.ParseInt(c.Query("version"), 10, 64)
		reply(c, nil, s.DeleteTask(c.Param("id"), v))
	})
	g.POST("/tasks/:id/prepare", func(c *gin.Context) {
		bind(c, func(v struct {
			Another bool `json:"another"`
			Refill  bool `json:"refill"`
		}) (any, error) {
			return s.PrepareTask(c.Param("id"), v.Another, v.Refill)
		})
	})
	g.PUT("/tasks/:id/draft", func(c *gin.Context) {
		bind(c, func(v struct {
			Text    string `json:"text"`
			Version int64  `json:"version"`
		}) (any, error) {
			return nil, s.SaveTaskDraft(c.Param("id"), v.Text, v.Version)
		})
	})
	g.GET("/today", func(c *gin.Context) {
		p, e := s.PersonalSpace()
		if e != nil {
			reply(c, nil, e)
			return
		}
		_ = s.SetSetting("today_enabled", "true")
		rs, e := s.RunPage(p.DefaultSessionID, "")
		if e != nil {
			reply(c, nil, e)
			return
		}
		ds, e := s.Decisions()
		if e != nil {
			reply(c, nil, e)
			return
		}
		daily, _ := getJSON[DailyContent](s.DB, "SELECT data FROM daily_contents ORDER BY day DESC LIMIT 1")
		contexts, e := s.ContextCatalog()
		notes, err := s.todayChannelNotes()
		if err != nil {
			reply(c, nil, err)
			return
		}
		notice, _ := s.Setting("personal_reset_notice")
		followups, err := s.Followups()
		if err != nil {
			reply(c, nil, err)
			return
		}
		reply(c, gin.H{"followups": followups, "channelNotes": notes, "contextResetAt": notice, "sessionId": p.DefaultSessionID, "runs": rs, "decisions": ds, "daily": daily, "contexts": contexts}, e)
	})
	g.POST("/today/refresh", func(c *gin.Context) {
		e := s.RequestToday()
		reply(c, gin.H{"state": "pending"}, e)
	})
	g.POST("/today/continue", func(c *gin.Context) {
		bind(c, func(v struct {
			Day     string `json:"day"`
			Version int64  `json:"version"`
			EventID string `json:"eventId"`
		}) (any, error) {
			d, e := getJSON[DailyContent](s.DB, "SELECT data FROM daily_contents WHERE day=?", v.Day)
			if e != nil {
				return nil, e
			}
			if d.Version != v.Version {
				return nil, ErrConflict
			}
			p, e := s.PersonalSpace()
			if e != nil {
				return nil, e
			}
			return s.Submit(Incoming{Source: "web", SessionID: p.DefaultSessionID, EventID: v.EventID, Text: "继续讨论这项每日建议（不是创建任务授权）：\n" + d.Text})
		})
	})
	g.GET("/personal/memories", func(c *gin.Context) { v, e := s.Memories(); reply(c, v, e) })
	g.POST("/personal/memories", func(c *gin.Context) {
		bind(c, func(m PersonalMemory) (any, error) { m.Source = ""; return s.SaveMemory(m) })
	})
	g.DELETE("/personal/memories/:id", func(c *gin.Context) {
		v, e := strconv.ParseInt(c.Query("version"), 10, 64)
		if e != nil {
			reply(c, nil, errors.New("需要 version"))
			return
		}
		if strings.HasPrefix(c.Param("id"), "p:") {
			reply(c, nil, s.ForgetUnderstanding(c.Param("id"), v))
		} else {
			reply(c, nil, s.ForgetMemory(c.Param("id"), v))
		}
	})
	g.POST("/personal/decisions/:id/answer", func(c *gin.Context) {
		bind(c, func(v struct {
			Version int64  `json:"version"`
			Answer  string `json:"answer"`
			Other   string `json:"other"`
		}) (any, error) {
			d, e := s.AnswerDecision(c.Param("id"), v.Version, v.Answer, v.Other)
			if e == nil {
				s.dispatchDecisions()
			}
			return d, e
		})
	})
}
