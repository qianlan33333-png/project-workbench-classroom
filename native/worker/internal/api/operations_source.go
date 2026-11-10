package api

import (
	"context"
	"net/http"
	"time"
	"workbench-native/internal/core"
)

func (a *Server) operationsSource(w http.ResponseWriter, r *http.Request) {
	p := principal(r.Context())
	cfg, err := a.readOperations(r.Context(), p.ProjectID)
	if err != nil || p != operationsPrincipal(p.ProjectID) || !cfg.ReadSource {
		respond(w, nil, core.ErrDenied)
		return
	}
	q := r.URL.Query()
	switch q.Get("view") {
	case "catalog":
		a.operationsDirectory(w, r, p, cfg, "catalog", map[string]string{})
	case "members":
		a.operationsDirectory(w, r, p, cfg, "members", map[string]string{
			"package": q.Get("package"), "week": q.Get("week"), "after": q.Get("after"), "revision": q.Get("revision"),
		})
	default:
		respond(w, nil, core.ErrInvalid)
	}
}

func (a *Server) operationsDirectory(w http.ResponseWriter, r *http.Request, p core.Principal, cfg operationsConfig, view string, params map[string]string) {
	ctx, cancel := context.WithTimeout(r.Context(), 84*time.Second)
	defer cancel()
	data, err := a.Store.ReadOperationsDirectory(ctx, p, cfg.Input.ResourceID, view, params)
	if err != nil {
		http.Error(w, "上下文中心暂不可读，请重试。", http.StatusBadGateway)
		return
	}
	respond(w, data, nil)
}
