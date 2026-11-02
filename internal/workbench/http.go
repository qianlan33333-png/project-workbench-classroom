package workbench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

type API struct {
	toolMu     sync.Mutex
	Store      *Store
	Worker     *Worker
	Config     Config
	Shutdown   <-chan struct{}
	loginMu    sync.Mutex
	loginAfter time.Time
}

func reply(c *gin.Context, v any, err error) {
	if err != nil {
		status := 400
		if errors.Is(err, ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			status = 404
		}
		if errors.Is(err, ErrLocked) {
			status = 423
		}
		if errors.Is(err, ErrConflict) {
			status = 409
		}
		c.JSON(status, gin.H{"error": safeRuntimeError(err.Error())})
		return
	}
	if v == nil {
		v = gin.H{"ok": true}
	}
	c.JSON(200, v)
}
func bind[T any](c *gin.Context, f func(T) (any, error)) {
	var v T
	if e := c.ShouldBindJSON(&v); e != nil {
		reply(c, nil, errors.New("请求格式不正确"))
		return
	}
	out, e := f(v)
	reply(c, out, e)
}
func (a *API) Mount(r *gin.Engine) {
	s := a.Store
	s.toolManager = a.startToolJob
	s.calendarRunner = (&Harness{Store: s, Config: a.Config}).calendarCommand
	r.Use(a.sameOrigin)
	r.MaxMultipartMemory = 8 << 20
	base := strings.TrimRight(a.Config.BasePath, "/")
	g := r.Group(base + "/api/v1")
	g.GET("/health", func(c *gin.Context) {
		e := s.DB.Ping()
		if e != nil {
			c.JSON(503, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(200, gin.H{"status": "ok", "version": "0.1.0", "igo": "0.4.5", "harness": "ce7073d51254c26cd63b22a60835b8f948deb447"})
	})
	g.GET("/auth", func(c *gin.Context) { c.JSON(200, gin.H{"authenticated": a.authenticated(c)}) })
	g.POST("/login", a.login)
	owner := g.Group("")
	owner.Use(a.authMiddleware)
	a.mountGateway(g, owner)
	a.mountToday(owner)
	owner.GET("/browser/*path", a.projectBrowser)
	owner.POST("/logout", func(c *gin.Context) {
		token, _ := c.Cookie("pw_session")
		_, e := s.DB.Exec("DELETE FROM logins WHERE hash=?", digest([]byte(token)))
		a.cookie(c, "", -1)
		reply(c, nil, e)
	})
	owner.GET("/projects", func(c *gin.Context) { v, e := s.Projects(); reply(c, v, e) })
	owner.POST("/projects", func(c *gin.Context) {
		bind(c, func(v Project) (any, error) { return s.CreateProject(v.Name, v.Description, v.Aliases) })
	})
	owner.GET("/projects/:id", func(c *gin.Context) { v, e := s.Project(c.Param("id")); reply(c, v, e) })
	owner.GET("/projects/:id/capabilities", func(c *gin.Context) { v, e := s.Capabilities(c.Param("id")); reply(c, v, e) })
	owner.PUT("/projects/:id", func(c *gin.Context) {
		bind(c, func(v Project) (any, error) { return s.UpdateProject(c.Param("id"), v) })
	})
	owner.PATCH("/projects/:id", func(c *gin.Context) {
		bind(c, func(v map[string]json.RawMessage) (any, error) { return s.PatchProject(c.Param("id"), v) })
	})
	owner.DELETE("/projects/:id", func(c *gin.Context) { reply(c, nil, s.DeleteProject(c.Param("id"))) })
	owner.POST("/projects/:id/directories", func(c *gin.Context) {
		bind(c, func(v struct {
			Path string `json:"path"`
		}) (any, error) {
			return nil, s.CreateDirectory(c.Param("id"), v.Path)
		})
	})
	owner.GET("/projects/:id/files", func(c *gin.Context) { v, e := s.Files(c.Param("id")); reply(c, v, e) })
	owner.GET("/projects/:id/file", a.getFile)
	owner.PUT("/projects/:id/file", func(c *gin.Context) {
		bind(c, func(v struct {
			Path     string `json:"path"`
			Content  string `json:"content"`
			BaseHash string `json:"baseHash"`
		}) (any, error) {
			return s.SaveFile(c.Param("id"), v.Path, []byte(v.Content), v.BaseHash)
		})
	})
	owner.DELETE("/projects/:id/file", func(c *gin.Context) {
		v, e := s.DeleteFile(c.Param("id"), c.Query("path"), c.Query("baseHash"))
		reply(c, v, e)
	})
	owner.POST("/projects/:id/upload", func(c *gin.Context) { a.upload(c, false) })
	owner.POST("/projects/:id/import", func(c *gin.Context) { a.upload(c, true) })
	owner.GET("/projects/:id/export", func(c *gin.Context) {
		if e := s.CheckUnlocked(c.Param("id")); e != nil {
			reply(c, nil, e)
			return
		}
		tmp, e := os.CreateTemp(filepath.Join(s.Root, "uploads"), "export-*.zip")
		if e != nil {
			reply(c, nil, e)
			return
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		if e = s.ExportZip(c.Param("id"), tmp); e != nil {
			reply(c, nil, e)
			return
		}
		c.Header("Content-Disposition", `attachment; filename="project.zip"`)
		c.File(tmp.Name())
	})
	owner.GET("/projects/:id/search", func(c *gin.Context) { v, e := s.Search(c.Param("id"), c.Query("q")); reply(c, v, e) })
	owner.GET("/projects/:id/history-search", func(c *gin.Context) { v, e := s.SessionSearch(c.Param("id"), c.Query("q")); reply(c, v, e) })
	owner.GET("/projects/:id/revisions", func(c *gin.Context) { v, e := s.Revisions(c.Param("id"), c.Query("path")); reply(c, v, e) })
	owner.GET("/versions/:hash", func(c *gin.Context) {
		b, e := s.Blob(c.Param("hash"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		c.Data(200, "text/plain; charset=utf-8", b)
	})
	owner.POST("/revisions/:id/undo", func(c *gin.Context) { v, e := s.UndoRevision(c.Param("id")); reply(c, v, e) })
	owner.GET("/projects/:id/sessions", func(c *gin.Context) { v, e := s.Sessions(c.Param("id")); reply(c, v, e) })
	owner.POST("/projects/:id/sessions", func(c *gin.Context) {
		bind(c, func(v Session) (any, error) { return s.CreateSession(c.Param("id"), v.Title, "topic", v.ExpertID) })
	})
	owner.GET("/sessions/:id", func(c *gin.Context) { v, e := s.Session(c.Param("id")); reply(c, v, e) })
	owner.PUT("/sessions/:id", func(c *gin.Context) {
		bind(c, func(v struct {
			Title    *string `json:"title"`
			ExpertID *string `json:"expertId"`
		}) (any, error) {
			return s.UpdateSession(c.Param("id"), v.Title, v.ExpertID)
		})
	})

	owner.PUT("/sessions/:id/pin", func(c *gin.Context) {
		bind(c, func(v struct {
			Pinned bool `json:"pinned"`
		}) (any, error) {
			return s.PinSession(c.Param("id"), v.Pinned)
		})
	})
	owner.POST("/sessions/:id/archive/stop", func(c *gin.Context) {
		rows, e := listJSON[Run](s.DB, "SELECT data FROM runs WHERE session_id=? AND json_extract(data,'$.kind')='archive' AND state IN ('queued','running','waiting') LIMIT 1", c.Param("id"))
		if e == nil && len(rows) > 0 {
			e = a.Worker.Stop(rows[0].ID)
		}
		reply(c, nil, e)
	})
	owner.POST("/sessions/:id/archive", func(c *gin.Context) { v, e := s.QueueArchive(c.Param("id")); reply(c, v, e) })
	owner.DELETE("/sessions/:id", func(c *gin.Context) { v, e := s.RecycleSession(c.Param("id"), "delete"); reply(c, v, e) })
	owner.GET("/recycle-bin", func(c *gin.Context) { v, e := s.RecycleBin(); reply(c, v, e) })
	owner.POST("/recycle-bin/:id/restore", func(c *gin.Context) { v, e := s.RestoreSession(c.Param("id")); reply(c, v, e) })
	owner.DELETE("/recycle-bin/:id", func(c *gin.Context) { reply(c, nil, s.PurgeSession(c.Param("id"))) })
	owner.GET("/sessions/:id/history", func(c *gin.Context) { v, e := s.History(c.Param("id")); reply(c, v, e) })
	owner.GET("/inbox", func(c *gin.Context) { v, e := s.Inbox(); reply(c, v, e) })
	owner.GET("/messages/by-event/:event", func(c *gin.Context) {
		v, e := getJSON[Message](s.DB, "SELECT data FROM inbox WHERE source='web' AND connection_id='web' AND event_id=?", c.Param("event"))
		if e == nil && v.SessionID != "" {
			if _, err := s.Session(v.SessionID); err != nil {
				reply(c, nil, err)
				return
			}
		}
		reply(c, v, e)
	})
	owner.POST("/messages", func(c *gin.Context) {
		bind(c, func(v Incoming) (any, error) {
			v.Source = "web"
			v.ActorID = "owner"
			v.ConnectionID = "web"
			v.ConversationID = "web"
			return s.Submit(v)
		})
	})
	owner.POST("/messages/:id/resolve", func(c *gin.Context) {
		bind(c, func(v struct {
			SessionID string `json:"sessionId"`
		}) (any, error) {
			return s.ResolveMessage(c.Param("id"), v.SessionID)
		})
	})
	owner.GET("/runs", func(c *gin.Context) { v, e := s.RunPage(c.Query("sessionId"), c.Query("before")); reply(c, v, e) })
	owner.GET("/runs/:id", func(c *gin.Context) { v, e := s.Run(c.Param("id")); reply(c, v, e) })
	owner.POST("/runs/:id/stop", func(c *gin.Context) { reply(c, nil, a.Worker.Stop(c.Param("id"))) })
	owner.GET("/interactions", func(c *gin.Context) { v, e := s.PendingInteractions(); reply(c, v, e) })
	owner.POST("/interactions/:id/answer", func(c *gin.Context) {
		b, e := io.ReadAll(io.LimitReader(c.Request.Body, 128<<10))
		if e == nil {
			e = a.Worker.Answer(c.Param("id"), b)
		}
		reply(c, nil, e)
	})
	owner.GET("/events", a.events)
	a.mountExpertManagement(owner)
	owner.GET("/experts", func(c *gin.Context) { v, e := s.Experts(); reply(c, v, e) })
	owner.POST("/experts", func(c *gin.Context) {
		bind(c, func(v struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			Instructions string `json:"instructions"`
		}) (any, error) {
			return s.CreateExpert(v.Name, v.Description, v.Instructions)
		})
	})
	owner.POST("/experts/import", func(c *gin.Context) { a.importExpert(c) })
	owner.POST("/experts/git", func(c *gin.Context) {
		bind(c, func(v struct {
			URL        string `json:"url"`
			Commit     string `json:"commit"`
			Subdir     string `json:"subdir"`
			ID         string `json:"id"`
			BaseDigest string `json:"baseDigest"`
		}) (any, error) {
			return s.ImportExpertGit(c.Request.Context(), v.URL, v.Commit, v.Subdir, v.ID, v.BaseDigest)
		})
	})
	owner.GET("/experts/:id/skill", func(c *gin.Context) {
		v, e := s.Expert(c.Param("id"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		b, e := os.ReadFile(filepath.Join(s.Root, "experts", v.ID, v.Digest, "SKILL.md"))
		reply(c, gin.H{"content": string(b)}, e)
	})
	owner.GET("/schedules", func(c *gin.Context) { v, e := s.Schedules(c.Query("projectId")); reply(c, v, e) })
	owner.POST("/schedules", func(c *gin.Context) { bind(c, func(v Schedule) (any, error) { return s.SaveSchedule(v) }) })
	owner.PUT("/schedules/:id", func(c *gin.Context) {
		bind(c, func(v Schedule) (any, error) { v.ID = c.Param("id"); return s.SaveSchedule(v) })
	})
	owner.DELETE("/schedules/:id", func(c *gin.Context) {
		s.scheduleMu.Lock()
		defer s.scheduleMu.Unlock()
		res, e := s.DB.Exec("DELETE FROM schedules WHERE id=? AND NOT EXISTS(SELECT 1 FROM runs WHERE json_extract(data,'$.scheduleId')=? AND state IN ('queued','running','waiting','stopping'))", c.Param("id"), c.Param("id"))
		if e == nil {
			n, _ := res.RowsAffected()
			if n == 0 {
				e = errors.New("请先停止执行，再删除任务")
			}
		}
		reply(c, nil, e)
	})
	owner.POST("/schedules/:id/enabled", func(c *gin.Context) {
		bind(c, func(v struct {
			Enabled bool `json:"enabled"`
		}) (any, error) {
			sc, ids, e := s.toggleSchedule(c.Param("id"), v.Enabled)
			if e != nil {
				return nil, e
			}
			return sc, s.stopScheduleRuns(ids)
		})
	})
	owner.POST("/schedules/:id/run", func(c *gin.Context) {
		bind(c, func(v struct {
			Operation string `json:"operation"`
		}) (any, error) {
			return s.RunSchedule(c.Param("id"), v.Operation)
		})
	})
	owner.GET("/schedules/:id/checks", func(c *gin.Context) {
		rows, e := s.DB.Query("SELECT operation,checked_at,outcome,run_id,detail FROM schedule_checks WHERE schedule_id=? ORDER BY checked_at DESC LIMIT 30", c.Param("id"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		defer rows.Close()
		data := []ScheduleReceipt{}
		for rows.Next() {
			var r ScheduleReceipt
			if e = rows.Scan(&r.Operation, &r.At, &r.Outcome, &r.RunID, &r.Detail); e != nil {
				reply(c, nil, e)
				return
			}
			data = append(data, r)
		}
		reply(c, data, rows.Err())
	})
	owner.GET("/schedules/:id/runs", func(c *gin.Context) {
		v, e := listJSON[Run](s.DB, "SELECT data FROM runs WHERE json_extract(data,'$.scheduleId')=? ORDER BY created_at DESC LIMIT 100", c.Param("id"))
		reply(c, v, e)
	})
	owner.GET("/connections", func(c *gin.Context) { v, e := s.Connections(); reply(c, v, e) })
	owner.GET("/notification-targets", func(c *gin.Context) {
		rows, e := s.DB.Query("SELECT DISTINCT i.connection_id,json_extract(i.data,'$.incoming.conversationId'),json_extract(c.data,'$.label') FROM inbox i JOIN connections c ON c.id=i.connection_id WHERE json_extract(c.data,'$.enabled')=1 AND json_extract(i.data,'$.incoming.actorId')=json_extract(c.data,'$.ownerId')")
		if e != nil {
			reply(c, nil, e)
			return
		}
		defer rows.Close()
		out := []map[string]string{}
		for rows.Next() {
			var id, chat, label string
			if e = rows.Scan(&id, &chat, &label); e != nil {
				break
			}
			out = append(out, map[string]string{"connectionId": id, "conversationId": chat, "label": label})
		}
		if e == nil {
			e = rows.Err()
		}
		reply(c, out, e)
	})
	owner.POST("/connections", func(c *gin.Context) {
		bind(c, func(v struct {
			Connection Connection     `json:"connection"`
			Secrets    map[string]any `json:"secrets"`
		}) (any, error) {
			return s.SaveConnection(v.Connection, v.Secrets)
		})
	})
	owner.GET("/deliveries", func(c *gin.Context) { v, e := s.Deliveries(); reply(c, v, e) })
	owner.GET("/settings", func(c *gin.Context) {
		c.JSON(200, gin.H{"provider": a.Config.Provider, "model": a.Config.Model, "runtimeMode": a.Config.RuntimeMode, "browserUrl": a.Config.BrowserURL, "timezone": "Asia/Shanghai", "publicUrl": a.Config.PublicURL, "maxConcurrentRuns": maxProjectRuns + 1, "maxProjectRuns": maxProjectRuns})
	})
	owner.POST("/settings/password", func(c *gin.Context) {
		bind(c, func(v struct {
			Password string `json:"password"`
		}) (any, error) {
			h, e := PasswordHash(v.Password)
			if e != nil {
				return nil, e
			}
			return nil, s.SetSetting("password_hash", h)
		})
	})

	owner.GET("/cli-tools", func(c *gin.Context) { v, e := s.CLITools(); reply(c, v, e) })
	owner.POST("/cli-tools", func(c *gin.Context) { bind(c, func(v CLITool) (any, error) { return s.SaveCLITool(v) }) })
	owner.DELETE("/cli-tools/:id", func(c *gin.Context) { reply(c, nil, s.RemoveCLITool(c.Param("id"))) })
	owner.GET("/tool-jobs", func(c *gin.Context) { v, e := s.ToolJobs(); reply(c, v, e) })
	owner.POST("/cli-tools/:id/:action", func(c *gin.Context) { v, e := a.startToolJob(c.Param("id"), c.Param("action"), nil); reply(c, v, e) })
	owner.GET("/plugins", func(c *gin.Context) { v, e := s.Plugins(); reply(c, v, e) })
	owner.POST("/plugins", func(c *gin.Context) { bind(c, func(v Plugin) (any, error) { return s.SavePlugin(v) }) })
	owner.DELETE("/plugins/:id", func(c *gin.Context) { reply(c, nil, s.DeletePlugin(c.Param("id"))) })
	channel := g.Group("/channel")
	channel.Use(a.channelMiddleware)
	channel.POST("/connections/:id/bind", func(c *gin.Context) {
		bind(c, func(v struct {
			Code  string `json:"code"`
			Actor string `json:"actor"`
		}) (any, error) {
			return s.BindConnection(c.Param("id"), v.Code, v.Actor)
		})
	})
	channel.GET("/connections", func(c *gin.Context) {
		cs, e := s.Connections()
		if e != nil {
			reply(c, nil, e)
			return
		}
		out := []map[string]any{}
		for _, v := range cs {
			if v.Kind != "wechat" && v.Kind != "feishu" {
				continue
			}
			secrets, _ := getJSON[map[string]any](s.DB, "SELECT data FROM connection_secrets WHERE id=?", v.ID)
			out = append(out, map[string]any{"connection": v, "secrets": secrets})
		}
		c.JSON(200, out)
	})
	channel.POST("/upload", func(c *gin.Context) {
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
		path, e := s.StageUpload(c.PostForm("connectionId"), c.PostForm("actorId"), header.Filename, b)
		reply(c, gin.H{"path": path}, e)
	})
	channel.POST("/messages", func(c *gin.Context) {
		bind(c, func(v Incoming) (any, error) {
			if v.Source != "wechat" && v.Source != "feishu" {
				return nil, errors.New("渠道无效")
			}
			return s.Submit(v)
		})
	})
	channel.POST("/connections/:id/heartbeat", func(c *gin.Context) {
		bind(c, func(v struct {
			Status string `json:"status"`
		}) (any, error) {
			return nil, s.Heartbeat(c.Param("id"), v.Status)
		})
	})
	channel.GET("/connections/:id/state", func(c *gin.Context) {
		v, e := s.Setting("channel_state:" + c.Param("id"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		if v == "" {
			v = "{}"
		}
		c.Data(200, "application/json", []byte(v))
	})
	channel.PUT("/connections/:id/state", func(c *gin.Context) {
		bind(c, func(v map[string]any) (any, error) {
			return nil, s.SetSetting("channel_state:"+c.Param("id"), encode(v))
		})
	})
	channel.POST("/connections/:id/claim", func(c *gin.Context) {
		v, e := s.ClaimDelivery(c.Param("id"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		c.JSON(200, gin.H{"delivery": v})
	})
	channel.POST("/deliveries/:id/finish", func(c *gin.Context) {
		bind(c, func(v struct {
			State    string   `json:"state"`
			Receipt  string   `json:"receipt"`
			Receipts []string `json:"receipts"`
			Error    string   `json:"error"`
		}) (any, error) {
			return nil, s.FinishDeliveryReceipts(c.Param("id"), v.State, v.Receipt, v.Receipts, v.Error)
		})
	})
	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != "GET" || !strings.HasPrefix(c.Request.URL.Path, base+"/") || strings.HasPrefix(c.Request.URL.Path, base+"/api/") {
			c.JSON(404, gin.H{"error": "找不到这个接口"})
			return
		}
		rel := strings.TrimPrefix(c.Request.URL.Path, base+"/")
		if rel != "" && validPath(rel) == nil {
			f := filepath.Join(a.Config.WebDir, filepath.FromSlash(rel))
			if st, e := os.Stat(f); e == nil && !st.IsDir() {
				c.File(f)
				return
			}
		}
		c.Header("Cache-Control", "no-cache")
		c.File(filepath.Join(a.Config.WebDir, "index.html"))
	})
}
func (a *API) getFile(c *gin.Context) {
	b, h, e := a.Store.ReadFile(c.Param("id"), c.Query("path"))
	if e != nil {
		reply(c, nil, e)
		return
	}
	fileResponse(c, b, h)
}
func fileResponse(c *gin.Context, b []byte, h string) {
	c.Header("ETag", `"`+h+`"`)
	if c.Query("raw") == "1" {
		typ := mime.TypeByExtension(filepath.Ext(c.Query("path")))
		if typ == "" {
			typ = "application/octet-stream"
		}
		if strings.HasPrefix(typ, "text/html") || strings.Contains(typ, "svg") {
			c.Header("Content-Security-Policy", "sandbox")
		}
		if c.Query("download") == "1" {
			c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(c.Query("path"))}))
		}
		c.Data(200, typ, b)
		return
	}
	binary := !utf8.Valid(b) || bytes.ContainsRune(b, 0)
	content := ""
	tooLarge := len(b) > maxEditableFile
	if !binary && (!tooLarge || c.Query("view") != "1") {
		content = string(b)
	}
	reply(c, gin.H{"path": c.Query("path"), "content": content, "binary": binary, "tooLarge": tooLarge, "editable": !binary && !tooLarge, "hash": h, "size": len(b)}, nil)
}
func (a *API) upload(c *gin.Context, archive bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512<<20)
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
	if archive {
		e = a.Store.ImportZipTo(c.Param("id"), f, header.Size, c.PostForm("targetDir"))
		reply(c, nil, e)
		return
	}
	b, e := io.ReadAll(io.LimitReader(f, maxFile+1))
	if e != nil {
		reply(c, nil, e)
		return
	}
	if len(b) > maxFile {
		reply(c, nil, errors.New("附件超过 128MB"))
		return
	}
	path := c.PostForm("path")
	if path == "" {
		path = "附件/" + header.Filename
	}
	v, skipped, e := a.Store.UploadFile(c.Param("id"), path, b, c.PostForm("baseHash"), c.PostForm("conflict"))
	if skipped && e == nil {
		reply(c, gin.H{"path": v.Path, "skipped": true}, nil)
		return
	}
	reply(c, v, e)
}
func (a *API) importExpert(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<20)
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
	v, e := a.Store.ImportExpert(f, header.Size, c.PostForm("id"), c.PostForm("baseDigest"))
	reply(c, v, e)
}
func (a *API) events(c *gin.Context) {
	after, _ := strconv.ParseInt(c.Query("after"), 10, 64)
	if last, e := strconv.ParseInt(c.GetHeader("Last-Event-ID"), 10, 64); e == nil && last > after {
		after = last
	}
	session := c.Query("sessionId")
	if c.Query("json") == "1" {
		v, e := a.Store.Events(after, session)
		reply(c, v, e)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("X-Accel-Buffering", "no")
	c.Status(200)
	c.Writer.Flush()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.Now()
	for {
		events, e := a.Store.Events(after, session)
		if e != nil {
			return
		}
		for _, event := range events {
			b, _ := json.Marshal(event)
			if _, e = fmt.Fprintf(c.Writer, "id: %d\ndata: %s\n\n", event.Seq, b); e != nil {
				return
			}
			after = event.Seq
		}
		if len(events) > 0 {
			c.Writer.Flush()
			if len(events) == 500 {
				continue
			}
		}
		select {
		case <-a.Shutdown:
			return
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			if time.Since(heartbeat) > 15*time.Second {
				fmt.Fprint(c.Writer, ": keepalive\n\n")
				c.Writer.Flush()
				heartbeat = time.Now()
			}
		}
	}
}
