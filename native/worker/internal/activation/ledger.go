package activation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"regexp"
	"time"

	"github.com/go-sql-driver/mysql"
)

var (
	CodePattern         = regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)
	InstallationPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	ErrUnknownCode      = errors.New("invalid code")
	ErrAlreadyClaimed   = errors.New("code already claimed")
	ErrExpired          = errors.New("code expired")
)

type Ledger struct{ DB *sql.DB }

type Issued struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (ledger Ledger) Generate(ctx context.Context) (Issued, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return Issued{}, err
	}
	return ledger.Issue(ctx, base64.RawURLEncoding.EncodeToString(raw))
}

// The caller supplies a securely generated code. Retrying the same issuance
// returns its original deadline, without incrementing the number issued.
func (ledger Ledger) Issue(ctx context.Context, code string) (Issued, error) {
	digest := sha256.Sum256([]byte(code))
	expires := time.Now().UTC().Truncate(time.Second).Add(7 * 24 * time.Hour)
	if _, err := ledger.DB.ExecContext(ctx, "INSERT INTO wb_activation_codes (digest, expires_at) VALUES (?, ?)", digest[:], expires); err != nil {
		var databaseError *mysql.MySQLError
		if !errors.As(err, &databaseError) || databaseError.Number != 1062 {
			return Issued{}, err
		}
		if err := ledger.DB.QueryRowContext(ctx, "SELECT expires_at FROM wb_activation_codes WHERE digest=?", digest[:]).Scan(&expires); err != nil {
			return Issued{}, err
		}
	}
	return Issued{Code: code, ExpiresAt: expires}, nil
}

func (ledger Ledger) Redeem(ctx context.Context, code, instance string) (int64, error) {
	tx, err := ledger.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	digest := sha256.Sum256([]byte(code))
	var expires time.Time
	var installation sql.NullString
	var claimed sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT expires_at, installation, claimed_at FROM wb_activation_codes WHERE digest=? FOR UPDATE`, digest[:]).Scan(&expires, &installation, &claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrUnknownCode
	}
	if err != nil {
		return 0, err
	}
	if installation.Valid && installation.String != instance {
		return 0, ErrAlreadyClaimed
	}
	if !installation.Valid {
		now := time.Now().UTC().Truncate(time.Second)
		if !now.Before(expires) {
			return 0, ErrExpired
		}
		if _, err := tx.ExecContext(ctx, `UPDATE wb_activation_codes SET installation=?, claimed_at=? WHERE digest=?`, instance, now, digest[:]); err != nil {
			return 0, err
		}
		claimed = sql.NullTime{Time: now, Valid: true}
	}
	if !claimed.Valid {
		return 0, errors.New("missing activation timestamp")
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return claimed.Time.Unix(), nil
}
