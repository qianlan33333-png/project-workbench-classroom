package workbench

import "github.com/gin-gonic/gin"

func (a *API) mountComponents(g *gin.RouterGroup) {
	s := a.Store
	g.GET("/projects/:id/components", func(c *gin.Context) { v, e := s.Components(c.Param("id")); reply(c, v, e) })
	g.GET("/sessions/:id/component-calls", func(c *gin.Context) { v, e := s.ComponentCalls(c.Param("id")); reply(c, v, e) })
	g.POST("/sessions/:id/component-calls", func(c *gin.Context) {
		bind(c, func(q ComponentRequest) (any, error) { return s.SubmitComponent(c.Request.Context(), c.Param("id"), q) })
	})
	g.POST("/component-calls/:id/refresh", func(c *gin.Context) {
		v, e := s.RefreshComponent(c.Request.Context(), c.Param("id"), false)
		reply(c, v, e)
	})
	g.POST("/component-calls/:id/cancel", func(c *gin.Context) {
		v, e := s.RefreshComponent(c.Request.Context(), c.Param("id"), true)
		reply(c, v, e)
	})
}
