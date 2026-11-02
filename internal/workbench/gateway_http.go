package workbench

import (
	"database/sql"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	gw "projectworkbench/pkg/wecomgateway"
)

func (a *API) mountGateway(public, owner *gin.RouterGroup) {
	s := a.Store
	public.POST("/gateway/hooks/:id", func(c *gin.Context) {
		if isCRM(c.Request) {
			a.crmHook(c)
			return
		}
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
		v, e := listJSON[GatewayBinding](s.DB, "SELECT data FROM gateway_bindings WHERE json_extract(data,'$.projectId')!=''")
		reply(c, v, e)
	})
	owner.POST("/gateway/bindings", func(c *gin.Context) { bind(c, func(v GatewayBinding) (any, error) { return s.SaveGatewayBinding(v) }) })
	owner.GET("/gateway/sessions", func(c *gin.Context) {
		if project := c.Query("projectId"); project != "" {
			v, e := listJSON[GatewaySession](s.DB, "SELECT gs.data FROM gateway_sessions gs JOIN sessions ss ON ss.id=gs.id WHERE ss.project_id=? ORDER BY gs.rowid DESC", project)
			reply(c, v, e)
			return
		}
		v, e := s.GatewaySessions()
		reply(c, v, e)
	})
	owner.GET("/gateway/sessions/:id", func(c *gin.Context) { v, e := s.gatewaySession(c.Param("id")); reply(c, v, e) })
	owner.POST("/gateway/sessions/:id/pause", func(c *gin.Context) { reply(c, nil, s.SetGatewayMode(c.Param("id"), "human", "负责人暂停AI")) })
	owner.POST("/gateway/sessions/:id/resume", func(c *gin.Context) {
		bind(c, func(q struct {
			Version        int64 `json:"version"`
			BindingVersion int64 `json:"bindingVersion"`
		}) (any, error) {
			var g GatewaySession
			if q.Version < 1 || q.BindingVersion < 1 {
				return nil, crmError("invalid_request")
			}
			e := s.Transaction(c.Request.Context(), func(tx *sql.Tx) error {
				var e error
				g, e = txGatewaySession(tx, c.Param("id"))
				if e != nil {
					return e
				}
				if g.Mode != "human" || g.Version != q.Version || g.BindingVersion != q.BindingVersion {
					return ErrConflict
				}
				return s.changeGatewayModeTx(tx, &g, "auto", "负责人明确恢复AI")
			})
			return g, e
		})
	})
}
