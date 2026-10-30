package workbench

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/scrypt"
)

func secretToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func PasswordHash(password string) (string, error) {
	if len(password) < 10 {
		return "", errors.New("密码至少需要 10 个字符")
	}
	salt := secretToken()[:32]
	key, err := scrypt.Key([]byte(password), []byte(salt), 16384, 8, 1, 32)
	if err != nil {
		return "", err
	}
	return salt + ":" + hex.EncodeToString(key), nil
}
func verifyPassword(hash, password string) bool {
	parts := strings.Split(hash, ":")
	if len(parts) != 2 {
		return false
	}
	expected, err := hex.DecodeString(parts[1])
	if err != nil || len(expected) != 32 {
		return false
	}
	key, err := scrypt.Key([]byte(password), []byte(parts[0]), 16384, 8, 1, 32)
	return err == nil && subtle.ConstantTimeCompare(key, expected) == 1
}
func (a *API) authenticated(c *gin.Context) bool {
	token, err := c.Cookie("pw_session")
	if err != nil {
		return false
	}
	var expiry int64
	err = a.Store.DB.QueryRow("SELECT expires FROM logins WHERE hash=?", digest([]byte(token))).Scan(&expiry)
	return err == nil && expiry > now()
}
func (a *API) authMiddleware(c *gin.Context) {
	if !a.authenticated(c) {
		c.AbortWithStatusJSON(401, gin.H{"error": "请登录工作台"})
		return
	}
	c.Next()
}
func (a *API) sameOrigin(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "same-origin")
	if c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS" {
		origin := c.GetHeader("Origin")
		if origin != "" {
			u, e := url.Parse(origin)
			allowed := c.Request.Host
			if public, e := url.Parse(a.Config.PublicURL); e == nil && public.Host != "" {
				allowed = public.Host
			}
			if e != nil || u.Host != allowed {
				c.AbortWithStatusJSON(403, gin.H{"error": "请求来源不匹配"})
				return
			}
		}
	}
	c.Next()
}
func (a *API) channelMiddleware(c *gin.Context) {
	token := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if a.Config.ChannelToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(a.Config.ChannelToken)) != 1 {
		c.AbortWithStatusJSON(401, gin.H{"error": "渠道身份无效"})
		return
	}
	c.Next()
}
func (a *API) login(c *gin.Context) {
	var body struct {
		Password string `json:"password"`
	}
	if e := c.ShouldBindJSON(&body); e != nil {
		reply(c, nil, e)
		return
	}
	// One account, with a small persistent backoff instead of an extra rate-limit service.
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	if time.Now().Before(a.loginAfter) {
		c.JSON(429, gin.H{"error": "请稍后重试"})
		return
	}
	hash, e := a.Store.Setting("password_hash")
	if e != nil {
		reply(c, nil, e)
		return
	}
	if hash == "" {
		hash = a.Config.PasswordHash
	}
	if !verifyPassword(hash, body.Password) {
		a.loginAfter = time.Now().Add(time.Second * 2)
		c.JSON(401, gin.H{"error": "密码不正确"})
		return
	}
	token := secretToken()
	_, e = a.Store.DB.Exec("INSERT INTO logins VALUES(?,?)", digest([]byte(token)), time.Now().Add(30*24*time.Hour).UnixMilli())
	if e != nil {
		reply(c, nil, e)
		return
	}
	a.cookie(c, token, 30*24*3600)
	c.JSON(200, gin.H{"ok": true})
}
func (a *API) cookie(c *gin.Context, token string, maxAge int) {
	path := a.Config.BasePath
	if path == "" {
		path = "/"
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: "pw_session", Value: token, Path: path, MaxAge: maxAge, HttpOnly: true, Secure: strings.HasPrefix(a.Config.PublicURL, "https://"), SameSite: http.SameSiteLaxMode})
}
