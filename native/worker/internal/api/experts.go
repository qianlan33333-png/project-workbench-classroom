package api

import (
	"net/http"
	"workbench-native/internal/core"
)

func (a *Server) expertRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /experts/usage", func(w http.ResponseWriter, r *http.Request) {
		values, err := a.Store.ExpertUsage(r.Context(), principal(r.Context()))
		respond(w, values, err)
	})
	mux.HandleFunc("GET /session/expert", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		if id := r.URL.Query().Get("runId"); id != "" {
			message, err := a.Store.IncomingForRun(r.Context(), id)
			if err != nil || p.Kind != "project" || message.Principal != p {
				respond(w, nil, core.ErrDenied)
				return
			}
			ref := message.ExpertRef
			if ref == "" && message.ExpertSkill != "" {
				ref = "legacy:" + message.ExpertSkill
			}
			ref, err = a.Store.ResolveLegacyExpertRef(r.Context(), p.ProjectID, ref)
			respond(w, core.ExpertDefault{Ref: ref}, err)
			return
		}
		ref, err := a.Store.SessionExpert(r.Context(), p)
		if err == nil {
			ref, err = a.Store.ResolveLegacyExpertRef(r.Context(), p.ProjectID, ref)
		}
		respond(w, core.ExpertDefault{Ref: ref}, err)
	})
	mux.HandleFunc("POST /session/expert", func(w http.ResponseWriter, r *http.Request) {
		var in core.ExpertDefault
		if decode(w, r, &in) {
			ref, err := a.Store.BindSessionExpert(r.Context(), principal(r.Context()), in.Ref)
			respond(w, core.ExpertDefault{Ref: ref}, err)
		}
	})
	mux.HandleFunc("GET /expert-default", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		out, err := a.Store.ProjectExpertDefault(r.Context(), r.URL.Query().Get("projectId"))
		respond(w, out, err)
	})
	mux.HandleFunc("GET /expert-default/legacy", func(w http.ResponseWriter, r *http.Request) {
		names, err := a.Store.LegacyExpertNames(r.Context(), principal(r.Context()), r.URL.Query().Get("projectId"))
		respond(w, names, err)
	})
	mux.HandleFunc("POST /expert-default", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ProjectID  string            `json:"projectId"`
			ExpertID   string            `json:"expertId"`
			Expected   string            `json:"expected"`
			LegacyRefs map[string]string `json:"legacyRefs,omitempty"`
		}
		if decode(w, r, &in) {
			out, err := a.Store.SetProjectExpertDefault(r.Context(), principal(r.Context()), in.ProjectID, in.ExpertID, in.Expected, in.LegacyRefs)
			respond(w, out, err)
		}
	})
}
