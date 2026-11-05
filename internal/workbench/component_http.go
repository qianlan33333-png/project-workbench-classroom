package workbench

import (
	"errors"
	"github.com/gin-gonic/gin"
)

func (a *API) mountComponents(g *gin.RouterGroup) {
	a.mountProjectHistory(g)
	s := a.Store
	g.GET("/sessions/:id/component-reviews", func(c *gin.Context) { v, e := s.ComponentReviews(c.Param("id")); reply(c, v, e) })
	g.POST("/component-reviews/:id/approve", func(c *gin.Context) {
		bind(c, func(q struct {
			Digest       string `json:"digest"`
			OperationKey string `json:"operationKey"`
		}) (any, error) {
			if len(q.OperationKey) < 8 {
				return nil, errors.New("需要稳定的本人确认编号")
			}
			return s.ApproveComponentReview(c.Request.Context(), c.Param("id"), q.Digest, "web:"+q.OperationKey)
		})
	})
	g.POST("/component-reviews/:id/cancel", func(c *gin.Context) { v, e := s.CancelComponentReview(c.Param("id")); reply(c, v, e) })
	g.GET("/sessions/:id/component-config", func(c *gin.Context) { v, e := s.ComponentConfig(c.Param("id")); reply(c, v, e) })
	g.PUT("/sessions/:id/component-config", func(c *gin.Context) {
		bind(c, func(v ComponentRunConfig) (any, error) { v.SessionID = c.Param("id"); return s.SaveComponentConfig(v) })
	})
	g.POST("/sessions/:id/component-config/run", func(c *gin.Context) {
		bind(c, func(v struct {
			OperationKey string `json:"operationKey"`
		}) (any, error) {
			return s.RunComponentConfig(c.Request.Context(), c.Param("id"), v.OperationKey)
		})
	})
	g.GET("/projects/:id/components", func(c *gin.Context) { v, e := s.Components(c.Param("id")); reply(c, v, e) })
	g.GET("/sessions/:id/component-calls", func(c *gin.Context) { v, e := s.ComponentCalls(c.Param("id")); reply(c, v, e) })
	g.POST("/sessions/:id/component-calls", func(c *gin.Context) {
		bind(c, func(q ComponentRequest) (any, error) { return s.SubmitComponent(c.Request.Context(), c.Param("id"), q) })
	})
	g.POST("/component-calls/:id/confirm", func(c *gin.Context) { v, e := s.ConfirmComponent(c.Request.Context(), c.Param("id")); reply(c, v, e) })
	g.POST("/component-calls/:id/refresh", func(c *gin.Context) {
		v, e := s.RefreshComponent(c.Request.Context(), c.Param("id"), false)
		reply(c, v, e)
	})
	g.POST("/component-calls/:id/cancel", func(c *gin.Context) {
		v, e := s.RefreshComponent(c.Request.Context(), c.Param("id"), true)
		reply(c, v, e)
	})
}
