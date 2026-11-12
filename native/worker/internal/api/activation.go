package api

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"time"

	"workbench-native/internal/core"
)

var installationID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var activationCode = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)

func (a *Server) activationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /activation/codes", func(w http.ResponseWriter, r *http.Request) {
		if !principal(r.Context()).IsOwner() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		raw := make([]byte, 24)
		if _, err := rand.Read(raw); err != nil {
			http.Error(w, "activation unavailable", http.StatusServiceUnavailable)
			return
		}
		code := base64.RawURLEncoding.EncodeToString(raw)
		digest := sha256.Sum256([]byte(code))
		expires := time.Now().UTC().Truncate(time.Second).Add(7 * 24 * time.Hour)
		if _, err := a.Store.DB.ExecContext(r.Context(), `INSERT INTO wb_activation_codes (digest, expires_at) VALUES (?, ?)`, digest[:], expires); err != nil {
			http.Error(w, "activation unavailable", http.StatusServiceUnavailable)
			return
		}
		write(w, map[string]any{"code": code, "expiresAt": expires})
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
		if !activationCode.MatchString(in.Code) || !installationID.MatchString(in.Installation) {
			respond(w, nil, core.ErrInvalid)
			return
		}
		ctx := r.Context()
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			http.Error(w, "activation unavailable", http.StatusServiceUnavailable)
			return
		}
		defer tx.Rollback()

		digest := sha256.Sum256([]byte(in.Code))
		var expires time.Time
		var installation sql.NullString
		var claimed sql.NullTime
		err = tx.QueryRowContext(ctx, `SELECT expires_at, installation, claimed_at FROM wb_activation_codes WHERE digest=? FOR UPDATE`, digest[:]).Scan(&expires, &installation, &claimed)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "invalid code", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "activation unavailable", http.StatusServiceUnavailable)
			return
		}
		if installation.Valid && installation.String != in.Installation {
			http.Error(w, "code already claimed", http.StatusConflict)
			return
		}
		if !installation.Valid {
			now := time.Now().UTC().Truncate(time.Second)
			if !now.Before(expires) {
				http.Error(w, "code expired", http.StatusGone)
				return
			}
			if _, err := tx.ExecContext(ctx, `UPDATE wb_activation_codes SET installation=?, claimed_at=? WHERE digest=?`, in.Installation, now, digest[:]); err != nil {
				http.Error(w, "activation unavailable", http.StatusServiceUnavailable)
				return
			}
			claimed = sql.NullTime{Time: now, Valid: true}
		}
		if !claimed.Valid || tx.Commit() != nil {
			http.Error(w, "activation unavailable", http.StatusServiceUnavailable)
			return
		}
		write(w, map[string]any{"issuedAt": claimed.Time.Unix()})
	})
}
