package api

import (
	"net/http"

	"workbench-native/internal/core"
)

func (a *Server) vaultRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /vault-create", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if decode(w, r, &in) {
			v, err := a.Store.CreateVault(r.Context(), principal(r.Context()), a.VaultRoot, in.ID, in.Name)
			respond(w, v, err)
		}
	})
	mux.HandleFunc("POST /vault-save", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ResourceID string             `json:"resourceId"`
			Version    string             `json:"version"`
			Changes    []core.VaultChange `json:"changes"`
		}
		if decode(w, r, &in) {
			v, err := a.Store.SaveVault(r.Context(), principal(r.Context()), a.VaultRoot, in.ResourceID, in.Version, in.Changes)
			respond(w, v, err)
		}
	})
}
