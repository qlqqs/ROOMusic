// Package identity 拥有登录会话与访问决策：密码校验、不透明会话令牌（库中只存哈希）、当前用户查询，
// 以及所有受保护接口共用的守卫（同源校验、管理员校验、未登录响应）。授权在后端决定，前端路由守卫不是权威。
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"golang.org/x/crypto/bcrypt"
)

// SessionCookieName 是会话 Cookie 的名字。
const SessionCookieName = "roomusic_session"

// HashSessionToken 返回会话令牌的 SHA-256；库中只存哈希，不存令牌本身。
func HashSessionToken(token string) []byte { digest := sha256.Sum256([]byte(token)); return digest[:] }

// GenerateSessionToken 返回 32 字节随机令牌的十六进制文本。
func GenerateSessionToken() string {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(tokenBytes)
}

// User 是当前会话对应的已启用用户。
type User struct{ ID, Username, Role string }

// Guard 校验请求的会话与来源。它只读 sessions、users 表，写响应时使用统一错误信封。
type Guard struct {
	database      *sql.DB
	secureCookies bool
	publicURL     string
}

// NewGuard 返回守卫。secureCookies 决定会话 Cookie 的 Secure 标志与允许的来源协议（https）；
// publicURL 非空时来源主机必须与之一致，否则与请求 Host 一致。
func NewGuard(database *sql.DB, secureCookies bool, publicURL string) *Guard {
	return &Guard{database: database, secureCookies: secureCookies, publicURL: publicURL}
}

// Authenticate 返回请求会话对应的用户；缺少、过期、已撤销的会话或已禁用的用户都返回错误。
// 成功时把用户 ID 记入请求观察，供完成日志使用。
func (guard *Guard) Authenticate(request *http.Request) (User, error) {
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return User{}, errors.New("authentication required")
	}
	var user User
	err = guard.database.QueryRowContext(request.Context(), `SELECT u.id::text,u.username,u.role FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at > NOW() AND u.disabled_at IS NULL`, HashSessionToken(cookie.Value)).Scan(&user.ID, &user.Username, &user.Role)
	if err != nil {
		return User{}, errors.New("authentication required")
	}
	httpserver.SetActor(request, user.ID)
	return user, nil
}

// RequireAdmin 要求管理员会话；失败时已写出 401 或 403，返回 false。
func (guard *Guard) RequireAdmin(w http.ResponseWriter, r *http.Request) (User, bool) {
	u, err := guard.Authenticate(r)
	if err != nil {
		guard.WriteUnauthenticated(w, r)
		return User{}, false
	}
	if u.Role != "admin" {
		httpserver.WriteError(w, r, http.StatusForbidden, "forbidden", "需要管理员权限")
		return User{}, false
	}
	return u, true
}

// WriteUnauthenticated 写出 401 unauthorized。
func (guard *Guard) WriteUnauthenticated(responseWriter http.ResponseWriter, request *http.Request) {
	httpserver.WriteError(responseWriter, request, http.StatusUnauthorized, "unauthorized", "需要管理员登录")
}

// RequireSameOrigin 要求写请求带有可信的 Origin；失败时已写出 403，返回 false。
func (guard *Guard) RequireSameOrigin(responseWriter http.ResponseWriter, request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		httpserver.WriteError(responseWriter, request, http.StatusForbidden, "origin_required", "请求缺少来源校验")
		return false
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.User != nil || originURL.Path != "" || originURL.RawQuery != "" || originURL.Fragment != "" {
		httpserver.WriteError(responseWriter, request, http.StatusForbidden, "origin_forbidden", "请求来源不受信任")
		return false
	}
	allowedScheme := "http"
	if guard.secureCookies {
		allowedScheme = "https"
	}
	if originURL.Scheme != allowedScheme {
		httpserver.WriteError(responseWriter, request, http.StatusForbidden, "origin_forbidden", "请求来源不受信任")
		return false
	}
	if guard.publicURL != "" {
		publicURL, _ := url.Parse(guard.publicURL)
		if originURL.Host != publicURL.Host {
			httpserver.WriteError(responseWriter, request, http.StatusForbidden, "origin_forbidden", "请求来源不受信任")
			return false
		}
		return true
	}
	if originURL.Host != request.Host {
		httpserver.WriteError(responseWriter, request, http.StatusForbidden, "origin_forbidden", "请求来源不受信任")
		return false
	}
	return true
}

// SetSessionCookie 写出 24 小时有效的 HttpOnly 会话 Cookie。
func (guard *Guard) SetSessionCookie(responseWriter http.ResponseWriter, token string) {
	http.SetCookie(responseWriter, &http.Cookie{Name: SessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: guard.secureCookies, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(24 * time.Hour)})
}

// HashPassword 用 bcrypt 生成密码哈希。
func HashPassword(password string) (string, error) {
	digest, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("generate password hash: %w", err)
	}
	return string(digest), nil
}

// VerifyPassword 校验密码；同时兼容 bcrypt 迁移前的旧 SHA-256 哈希。
func VerifyPassword(encodedHash, password string) bool {
	if strings.HasPrefix(encodedHash, "$2a$") || strings.HasPrefix(encodedHash, "$2b$") || strings.HasPrefix(encodedHash, "$2y$") {
		return bcrypt.CompareHashAndPassword([]byte(encodedHash), []byte(password)) == nil
	}
	// Keep validating hashes created before the bcrypt migration. New hashes
	// are always bcrypt; this branch only provides a safe transition path.
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 3 || parts[0] != "sha256" {
		return false
	}
	digest := sha256.Sum256([]byte(parts[1] + password))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(digest[:])), []byte(parts[2])) == 1
}

// ClearSessionCookie 让浏览器删除会话 Cookie。
func (guard *Guard) ClearSessionCookie(responseWriter http.ResponseWriter) {
	http.SetCookie(responseWriter, &http.Cookie{Name: SessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: guard.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
