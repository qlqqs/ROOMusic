// Package identity 拥有登录会话与访问决策：密码校验、不透明会话令牌（库中只存哈希）、当前用户查询，
// 以及所有受保护接口共用的守卫（同源校验、管理员校验、未登录响应）。授权在后端决定，前端路由守卫不是权威。
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/weborigin"
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
	database       *sql.DB
	allowedOrigins map[string]bool // 非 nil 即清单模式。
	sameHost       bool            // 主机一致模式。
	logger         *slog.Logger
	secureCookies  bool   // 仅旧模式。
	publicURL      string // 仅旧模式。
}

// NewGuard 返回旧模式守卫。secureCookies 决定会话 Cookie 的 Secure 标志与允许的来源协议（https）；
// publicURL 非空时来源主机必须与之一致，否则与请求 Host 一致。
func NewGuard(database *sql.DB, secureCookies bool, publicURL string) *Guard {
	return &Guard{database: database, secureCookies: secureCookies, publicURL: publicURL}
}

// NewGuardWithAllowedOrigins 返回清单模式守卫；origins 必须已规范化，空清单属于装配错误。
func NewGuardWithAllowedOrigins(database *sql.DB, origins []string) *Guard {
	if len(origins) == 0 {
		panic("identity: allowed origins must not be empty")
	}
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		allowed[origin] = true
	}
	return &Guard{database: database, allowedOrigins: allowed}
}

// NewGuardWithSameHost 返回主机一致模式守卫：Origin 与请求 Host 规范化后一致就放行，协议不限。
func NewGuardWithSameHost(database *sql.DB) *Guard {
	return &Guard{database: database, sameHost: true}
}

// WithLogger 在装配时注入来源拒绝日志的记录器；未注入时使用默认记录器。
func (guard *Guard) WithLogger(logger *slog.Logger) *Guard {
	guard.logger = logger
	return guard
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

// WriteUnauthenticated 写出 401 unauthorized。所有需要登录的接口共用这条消息，不提及角色：
// 普通用户的会话失效时也会收到它。
func (guard *Guard) WriteUnauthenticated(responseWriter http.ResponseWriter, request *http.Request) {
	httpserver.WriteError(responseWriter, request, http.StatusUnauthorized, "unauthorized", "需要登录")
}

// RequireSameOrigin 要求写请求带有可信的 Origin；失败时已写出 403，返回 false。
func (guard *Guard) RequireSameOrigin(responseWriter http.ResponseWriter, request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		httpserver.WriteError(responseWriter, request, http.StatusForbidden, "origin_required", "请求缺少来源校验")
		return false
	}
	if guard.allowedOrigins != nil || guard.sameHost {
		if _, trusted := guard.trustedOrigin(request); !trusted {
			guard.logRejectedOrigin(request)
			httpserver.WriteError(responseWriter, request, http.StatusForbidden, "origin_forbidden", "请求来源不受信任")
			return false
		}
		return true
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

// trustedOrigin 返回已规范化且被当前模式接受的来源；仅用于清单模式与主机一致模式。
func (guard *Guard) trustedOrigin(request *http.Request) (string, bool) {
	origin, err := weborigin.ParseRequest(request.Header.Get("Origin"))
	if err != nil {
		return "", false
	}
	if guard.allowedOrigins != nil {
		return origin, guard.allowedOrigins[origin]
	}
	if guard.sameHost {
		scheme, _, _ := strings.Cut(origin, "://")
		host, err := weborigin.FromHost(scheme, request.Host)
		return origin, err == nil && host == origin
	}
	return "", false
}

func (guard *Guard) logRejectedOrigin(request *http.Request) {
	mode := "allow-list"
	if guard.sameHost {
		mode = "same-host"
	}
	bounded := func(value string) string {
		if len(value) > 256 {
			value = value[:256]
		}
		// 与登录日志一致，清理截断后的不完整或非法 UTF-8，避免 JSON 替换字符扩大字段。
		if !utf8.ValidString(value) {
			value = strings.ToValidUTF8(value, "")
		}
		return value
	}
	logger := guard.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(request.Context(), "请求来源不受信任", "event", "identity.origin.rejected",
		"module", "identity", "request_id", request.Header.Get("X-Request-ID"),
		"mode", bounded(mode), "origin", bounded(request.Header.Get("Origin")), "host", bounded(request.Host))
}

// SetSessionCookie 写出 24 小时有效的 HttpOnly 会话 Cookie。
func (guard *Guard) SetSessionCookie(responseWriter http.ResponseWriter, request *http.Request, token string) {
	http.SetCookie(responseWriter, &http.Cookie{Name: SessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: guard.cookieSecure(request), SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(24 * time.Hour)})
}

// cookieSecure 与来源校验共用解析规则；写 Cookie 的调用方必须先通过 RequireSameOrigin。
func (guard *Guard) cookieSecure(request *http.Request) bool {
	if guard.allowedOrigins == nil && !guard.sameHost {
		return guard.secureCookies
	}
	origin, trusted := guard.trustedOrigin(request)
	return trusted && strings.HasPrefix(origin, "https://")
}

// 新密码只允许可打印 ASCII（含空格），使字符数与字节数一致。
// bcrypt 的输入上限是 72 字节，必须在创建入口拒绝超限输入，避免返回 500。
const (
	MinPasswordLength = 12
	MaxPasswordLength = 72
)

// ValidPassword 报告密码是否为 12–72 位可打印 ASCII（U+0020–U+007E）。
func ValidPassword(password string) bool {
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return false
	}
	for i := 0; i < len(password); i++ {
		if password[i] < 0x20 || password[i] > 0x7e {
			return false
		}
	}
	return true
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
func (guard *Guard) ClearSessionCookie(responseWriter http.ResponseWriter, request *http.Request) {
	http.SetCookie(responseWriter, &http.Cookie{Name: SessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: guard.cookieSecure(request), SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

// Usernames 批量返回这些用户的用户名，键是小写 UUID；不存在的用户没有键。操作记录页用它显示“谁”。
func (guard *Guard) Usernames(ctx context.Context, ids []string) (map[string]string, error) {
	names := map[string]string{}
	if len(ids) == 0 {
		return names, nil
	}
	rows, err := guard.database.QueryContext(ctx, `SELECT id::text, username FROM users WHERE id = ANY($1::text[]::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, username string
		if err := rows.Scan(&id, &username); err != nil {
			return nil, err
		}
		names[id] = username
	}
	return names, rows.Err()
}
