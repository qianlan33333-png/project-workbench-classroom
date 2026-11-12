package main

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"workbench-native/internal/activation"
)

const (
	adminPath    = "/activation-codes/"
	redeemPath   = "/workbench-activation/redeem"
	codeHeader   = "X-Workbench-Issued-Code"
	expiryHeader = "X-Workbench-Issued-Expiry"
)

var issueRequestID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type issuer struct {
	origin        *url.URL
	signingKey    ed25519.PrivateKey
	adminPassword []byte
	ledger        activation.Ledger
	adminPage     http.Handler
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	origin, err := url.Parse(os.Getenv("WORKBENCH_ACTIVATION_ISSUER"))
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.String() != "https://"+origin.Host {
		return errors.New("WORKBENCH_ACTIVATION_ISSUER must be a bare HTTPS origin")
	}
	key, err := readSigningKey(os.Getenv("WORKBENCH_ACTIVATION_SIGNING_KEY_FILE"))
	if err != nil {
		return err
	}
	password, err := hex.DecodeString(os.Getenv("WORKBENCH_ACTIVATION_ADMIN_PASSWORD_SHA256"))
	if err != nil || len(password) != sha256.Size {
		return errors.New("WORKBENCH_ACTIVATION_ADMIN_PASSWORD_SHA256 must be a SHA-256 digest in hex")
	}
	socket := os.Getenv("WORKBENCH_ACTIVATION_SQLPAGE_SOCKET")
	if !filepath.IsAbs(socket) {
		return errors.New("WORKBENCH_ACTIVATION_SQLPAGE_SOCKET must be an absolute path")
	}
	dsn, err := mysql.ParseDSN(os.Getenv("WORKBENCH_ACTIVATION_DB_DSN"))
	if err != nil || dsn.DBName == "" {
		return errors.New("WORKBENCH_ACTIVATION_DB_DSN must select the activation database")
	}
	dsn.ParseTime = true
	dsn.Loc = time.UTC
	db, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		return errors.New("could not configure activation database")
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return errors.New("activation database connection unavailable")
	}

	client := &issuer{
		origin: origin, signingKey: key, adminPassword: password,
		ledger: activation.Ledger{DB: db}, adminPage: sqlPageProxy(socket),
	}
	server := &http.Server{
		Addr: "127.0.0.1:7387", Handler: client.routes(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second,
	}
	return server.ListenAndServe()
}

func readSigningKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read activation signing key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("activation signing key must be PKCS#8 PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid activation signing key")
	}
	edKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("activation signing key must be Ed25519")
	}
	return edKey, nil
}

func sqlPageProxy(socket string) http.Handler {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	return &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(&url.URL{Scheme: "http", Host: "sqlpage"})
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socket)
			},
			ResponseHeaderTimeout: 8 * time.Second,
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Del("Cache-Control")
			response.Header.Del("Set-Cookie")
			return nil
		},
	}
}

func (s *issuer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(redeemPath, s.redeem)
	mux.HandleFunc(adminPath, s.admin)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !strings.EqualFold(r.Host, s.origin.Host) {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *issuer) admin(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	hash := sha256.Sum256([]byte(password))
	if !ok || username != "admin" || subtle.ConstantTimeCompare(hash[:], s.adminPassword) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="Activation codes", charset="UTF-8"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	r.Header.Del("Authorization")
	r.Header.Del("Cookie")
	r.Header.Del(codeHeader)
	r.Header.Del(expiryHeader)
	w.Header().Set("X-Frame-Options", "DENY")
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		s.adminPage.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != adminPath && r.URL.Path != adminPath+"issue" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Origin") != s.origin.String() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if r.URL.Path == adminPath {
		s.adminPage.ServeHTTP(w, r)
		return
	}
	if err := r.ParseForm(); err != nil || !issueRequestID.MatchString(r.PostForm.Get("request")) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	// A retry of the same form derives the same code without storing plaintext.
	mac := hmac.New(sha256.New, s.signingKey.Seed())
	mac.Write([]byte("workbench-activation-code/v1:"))
	mac.Write([]byte(r.PostForm.Get("request")))
	code := base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:24])
	issued, err := s.ledger.Issue(r.Context(), code)
	if err != nil {
		http.Error(w, "暂时无法生成激活码，请重试。", http.StatusServiceUnavailable)
		return
	}
	r.Header.Set(codeHeader, issued.Code)
	r.Header.Set(expiryHeader, issued.ExpiresAt.Add(8*time.Hour).Format(time.DateTime))
	r.Method, r.Body, r.ContentLength = http.MethodGet, nil, 0
	r.TransferEncoding = nil
	r.Header.Del("Content-Type")
	r.URL.Path, r.URL.RawPath, r.URL.RawQuery = adminPath, "", ""
	s.adminPage.ServeHTTP(w, r)
}

func (s *issuer) redeem(w http.ResponseWriter, r *http.Request) {
	reply := func(status int, body map[string]string) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		reply(http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	var input struct {
		Code         string `json:"code"`
		Installation string `json:"installation"`
	}
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2048))
	if contentType != "application/json" || err != nil || json.Unmarshal(body, &input) != nil ||
		!activation.CodePattern.MatchString(input.Code) || !activation.InstallationPattern.MatchString(input.Installation) {
		reply(http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	issuedAt, err := s.ledger.Redeem(r.Context(), input.Code, input.Installation)
	switch {
	case err == nil:
		reply(http.StatusOK, map[string]string{"grant": s.grant(input.Installation, issuedAt)})
	case errors.Is(err, activation.ErrUnknownCode):
		reply(http.StatusNotFound, map[string]string{"error": "invalid_code"})
	case errors.Is(err, activation.ErrAlreadyClaimed):
		reply(http.StatusConflict, map[string]string{"error": "already_claimed"})
	case errors.Is(err, activation.ErrExpired):
		reply(http.StatusGone, map[string]string{"error": "expired"})
	default:
		reply(http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
	}
}

func (s *issuer) grant(installation string, issuedAt int64) string {
	encode := base64.RawURLEncoding.EncodeToString
	header := encode([]byte(`{"alg":"EdDSA","typ":"workbench-activation+jwt"}`))
	payload, _ := json.Marshal(struct {
		Issuer   string `json:"iss"`
		Audience string `json:"aud"`
		Subject  string `json:"sub"`
		IssuedAt int64  `json:"iat"`
	}{s.origin.String(), "qianlan-workbench", installation, issuedAt})
	message := header + "." + encode(payload)
	return message + "." + encode(ed25519.Sign(s.signingKey, []byte(message)))
}
