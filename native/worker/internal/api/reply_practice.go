package api

import (
	"net/http"
	"os"
	"workbench-native/internal/core"
)

func (a *Server) replyPracticeRoutes(mux *http.ServeMux) {
	enabled := os.Getenv("WORKBENCH_REPLY_PRACTICE") == "1"
	owner := func(w http.ResponseWriter, r *http.Request) bool {
		if !principal(r.Context()).IsOwner() {
			respond(w, nil, core.ErrDenied)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /reply-practice", func(w http.ResponseWriter, r *http.Request) {
		if owner(w, r) {
			respond(w, map[string]bool{"available": enabled}, nil)
		}
	})
	mux.HandleFunc("POST /reply-practice", func(w http.ResponseWriter, r *http.Request) {
		if !owner(w, r) {
			return
		}
		if !enabled {
			respond(w, nil, core.ErrDenied)
			return
		}
		var in struct {
			ProjectID string `json:"projectId"`
		}
		if decode(w, r, &in) {
			settings, err := a.Store.EnableReplyPractice(r.Context(), principal(r.Context()), in.ProjectID)
			respond(w, settings, err)
		}
	})
	mux.HandleFunc("POST /reply-practice/message", func(w http.ResponseWriter, r *http.Request) {
		if !owner(w, r) {
			return
		}
		if !enabled {
			respond(w, nil, core.ErrDenied)
			return
		}
		var in core.ReplyPracticeInput
		if !decode(w, r, &in) {
			return
		}
		message, err := a.Store.AcceptReplyPractice(r.Context(), principal(r.Context()), in)
		if err != nil {
			respond(w, nil, err)
			return
		}
		// Keep the original ID when Temporal is temporarily unreachable; the
		// existing incoming sweep can start this persisted input.
		if message.State == "received" {
			if err = a.startIncoming(r.Context(), message); err != nil {
				http.Error(w, "已登记练习消息；可用原请求重试。", http.StatusServiceUnavailable)
				return
			}
		}
		respond(w, message, nil)
	})
}
