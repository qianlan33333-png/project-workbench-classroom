package api

import (
	"errors"
	"net/http"

	"workbench-native/internal/core"
)

// One complete vault, including path names and worst-case JSON string escapes.
// Only the authenticated host can send this payload.
const vaultBatchLimit = 240 * 1024 * 1024

type vaultSaveRequest struct {
	ResourceID string             `json:"resourceId"`
	Version    string             `json:"version"`
	Changes    []core.VaultChange `json:"changes"`
}

func (a *Server) vaultRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /vault-media-file", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ref := core.Ref{ResourceID: q.Get("resourceId"), Version: q.Get("version"), Section: q.Get("section")}
		data, mime, err := a.Store.ReadVaultMedia(r.Context(), principal(r.Context()), ref, q.Get("asset"))
		if err != nil {
			respond(w, nil, err)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /vault-folders", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		folders, err := a.Store.VaultFolders(r.Context(), principal(r.Context()), q.Get("resourceId"), q.Get("version"))
		respond(w, folders, err)
	})
	mux.HandleFunc("POST /vault-structure", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		if !p.IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		var in struct {
			vaultSaveRequest
			Folders *[]string `json:"folders"`
		}
		if decodeLimited(w, r, &in, vaultBatchLimit) {
			v, err := a.Store.SaveVaultStructure(r.Context(), p, a.VaultRoot, in.ResourceID, in.Version, in.Changes, in.Folders)
			respond(w, v, err)
		}
	})
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
		var in vaultSaveRequest
		if decode(w, r, &in) {
			v, err := a.Store.SaveVault(r.Context(), principal(r.Context()), a.VaultRoot, in.ResourceID, in.Version, in.Changes)
			respond(w, v, err)
		}
	})
	mux.HandleFunc("POST /vault-rename", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		if !p.IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		var in vaultSaveRequest
		if decodeLimited(w, r, &in, vaultBatchLimit) {
			v, err := a.Store.SaveVaultRename(r.Context(), p, a.VaultRoot, in.ResourceID, in.Version, in.Changes)
			respond(w, v, err)
		}
	})
	mux.HandleFunc("POST /vault-import", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r.Context())
		if !p.IsOwner() {
			respond(w, nil, core.ErrDenied)
			return
		}
		var in core.VaultCopy
		if !decodeLimited(w, r, &in, vaultBatchLimit) {
			return
		}
		v, err := a.Store.CopyVault(r.Context(), p, a.VaultRoot, in)
		var invalid *core.VaultCopyPathsError
		if errors.As(err, &invalid) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnprocessableEntity)
			write(w, map[string]any{"error": invalid.Error(), "invalidPaths": invalid.Paths})
			return
		}
		respond(w, v, err)
	})
}
