package workbench

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func (a *API) mountExpertManagement(owner *gin.RouterGroup) {
	s := a.Store
	version := func(c *gin.Context) (string, error) {
		if v := c.Query("digest"); v != "" {
			_, e := s.ExpertVersion(c.Param("id"), v)
			return v, e
		}
		e, err := s.Expert(c.Param("id"))
		return e.Digest, err
	}
	owner.GET("/experts/:id", func(c *gin.Context) {
		e, err := s.Expert(c.Param("id"))
		if err != nil {
			reply(c, nil, err)
			return
		}
		usage, err := s.ExpertUsage(e.ID)
		reply(c, gin.H{"expert": e, "usage": usage}, err)
	})
	owner.GET("/experts/:id/versions", func(c *gin.Context) { v, e := s.ExpertVersions(c.Param("id")); reply(c, v, e) })
	owner.GET("/experts/:id/files", func(c *gin.Context) {
		v, e := version(c)
		if e != nil {
			reply(c, nil, e)
			return
		}
		rows, e := s.ExpertFiles(c.Param("id"), v)
		reply(c, rows, e)
	})
	owner.GET("/experts/:id/file", func(c *gin.Context) {
		v, e := version(c)
		if e != nil {
			reply(c, nil, e)
			return
		}
		b, e := s.ReadExpertFile(c.Param("id"), v, c.Query("path"))
		if e != nil {
			reply(c, nil, e)
			return
		}
		fileResponse(c, b, digest(b))
	})
	owner.POST("/experts/:id/changes", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 66<<20)
		var change ExpertChanges
		uploads := map[string][]byte{}
		if c.ContentType() == "multipart/form-data" {
			form, err := c.MultipartForm()
			if err != nil {
				reply(c, nil, err)
				return
			}
			defer form.RemoveAll()
			if err = json.Unmarshal([]byte(c.PostForm("changes")), &change); err != nil {
				reply(c, nil, errors.New("changes 需要 JSON 修改内容"))
				return
			}
			if change.BaseDigest == "" {
				change.BaseDigest = c.PostForm("baseDigest")
			}
			var total int64
			for field, files := range form.File {
				if len(files) != 1 {
					reply(c, nil, errors.New("上传字段需要唯一文件"))
					return
				}
				f, err := files[0].Open()
				if err != nil {
					reply(c, nil, err)
					return
				}
				b, err := io.ReadAll(io.LimitReader(f, (64<<20)+1))
				f.Close()
				total += int64(len(b))
				if err != nil || total > 64<<20 {
					reply(c, nil, errors.New("专家包超过 64MiB"))
					return
				}
				uploads[field] = b
			}
		} else if e := c.ShouldBindJSON(&change); e != nil {
			reply(c, nil, e)
			return
		}
		e, err := s.ChangeExpert(c.Param("id"), change, uploads)
		reply(c, e, err)
	})
	owner.POST("/experts/:id/copy", func(c *gin.Context) { v, e := s.CopyExpert(c.Param("id")); reply(c, v, e) })
	owner.DELETE("/experts/:id", func(c *gin.Context) {
		bind(c, func(v struct {
			BaseDigest string `json:"baseDigest"`
		}) (any, error) { return nil, s.DeleteExpert(c.Param("id"), v.BaseDigest) })
	})
	owner.GET("/experts/:id/export", func(c *gin.Context) {
		v, e := version(c)
		if e != nil {
			reply(c, nil, e)
			return
		}
		f, e := os.CreateTemp(filepath.Join(s.Root, "uploads"), "export-expert-*.zip")
		if e != nil {
			reply(c, nil, e)
			return
		}
		defer os.Remove(f.Name())
		defer f.Close()
		if e = s.ExportExpert(c.Param("id"), v, f); e != nil {
			reply(c, nil, e)
			return
		}
		c.Header("Content-Disposition", `attachment; filename="expert.zip"`)
		c.File(f.Name())
	})
}
