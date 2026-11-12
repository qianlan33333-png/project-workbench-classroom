package api

import (
	"errors"
	"net/http"

	"workbench-native/internal/activation"
	"workbench-native/internal/core"
)

func (a *Server) activationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /activation/codes", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ledger := activation.Ledger{DB: a.Store.DB}
		issued, err := ledger.Generate(r.Context())
		if err != nil {
			http.Error(w, "activation unavailable", http.StatusServiceUnavailable)
			return
		}
		write(w, issued)
	})

	mux.HandleFunc("POST /activation/redeem", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var in struct {
			Code         string `json:"code"`
			Installation string `json:"installation"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !activation.CodePattern.MatchString(in.Code) || !activation.InstallationPattern.MatchString(in.Installation) {
			respond(w, nil, core.ErrInvalid)
			return
		}
		ledger := activation.Ledger{DB: a.Store.DB}
		issuedAt, err := ledger.Redeem(r.Context(), in.Code, in.Installation)
		switch {
		case err == nil:
			write(w, map[string]any{"issuedAt": issuedAt})
		case errors.Is(err, activation.ErrUnknownCode):
			http.Error(w, err.Error(), http.StatusNotFound)
		case errors.Is(err, activation.ErrAlreadyClaimed):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, activation.ErrExpired):
			http.Error(w, err.Error(), http.StatusGone)
		default:
			http.Error(w, "activation unavailable", http.StatusServiceUnavailable)
		}
	})
}
