package workbench

import (
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	gw "projectworkbench/pkg/wecomgateway"
)

func (a *API) mountGateway(public, owner *gin.RouterGroup) {
	s := a.Store
	public.POST("/gateway/hooks/:id", func(c *gin.Context) {
		_, secret, e := s.gatewayConnection(c.Param("id"))
		if e != nil {
			c.AbortWithStatus(404)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		raw, e := io.ReadAll(c.Request.Body)
		if e != nil {
			c.AbortWithStatus(413)
			return
		}
		event, e := gw.Verify(secret.WebhookSecret, raw, c.GetHeader("X-Gateway-Signature"), c.GetHeader("X-Gateway-Event-Id"), time.Now())
		if e != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid signed webhook"})
			return
		}
		if e = s.ReceiveGateway(c.Param("id"), event, raw); e != nil {
			c.AbortWithStatusJSON(409, gin.H{"error": "event rejected or could not be persisted"})
			return
		}
		c.JSON(200, gin.H{"ok": true, "eventId": event.EventID})
	})
	owner.GET("/gateway/connections", func(c *gin.Context) { v, e := s.GatewayConnections(); reply(c, v, e) })
	owner.POST("/gateway/connections", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		bind(c, func(v GatewayRegistration) (any, error) { return s.SaveGatewayConnection(v) })
	})
	owner.GET("/gateway/bindings", func(c *gin.Context) {
		v, e := listJSON[GatewayBinding](s.DB, "SELECT data FROM gateway_bindings")
		reply(c, v, e)
	})
	owner.POST("/gateway/bindings", func(c *gin.Context) { bind(c, func(v GatewayBinding) (any, error) { return s.SaveGatewayBinding(v) }) })
	owner.GET("/gateway/sessions", func(c *gin.Context) { v, e := s.GatewaySessions(); reply(c, v, e) })
	owner.POST("/gateway/sessions/:id/pause", func(c *gin.Context) { reply(c, nil, s.SetGatewayMode(c.Param("id"), "human", "负责人暂停AI")) })
	owner.POST("/gateway/sessions/:id/resume", func(c *gin.Context) {
		reply(c, nil, s.SetGatewayMode(c.Param("id"), "auto", "负责人明确恢复AI"))
	})
}
