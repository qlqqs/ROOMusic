// Package httpapi 是身份能力的 REST 传输层：初始化管理员、登录登出、当前用户与成员管理。
// 权限判断由 identity.Guard 完成；这里的 handler 只解码请求、调用守卫和映射响应。
// 成员列表、状态与会话管理在 identity/users.go；改密码与重置在 identity/passwords.go。
package httpapi

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// invalidAccountMessage 是用户名或密码不合规时的提示；密码只允许可打印 ASCII，长度范围取自 identity 的规则。
var invalidAccountMessage = fmt.Sprintf("用户名不能为空，密码需要 %d 到 %d 位英文字母、数字或符号", identity.MinPasswordLength, identity.MaxPasswordLength)

// isUniqueViolation 报告 err 是否为 PostgreSQL 唯一约束冲突（SQLSTATE 23505）。
// 只有它才表示“已存在”；其他数据库错误必须如实报告为不可用，不能伪装成冲突。
func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

// userIDFromPath 校验路由中的用户 ID；格式不对时写出 400 invalid_id，不访问数据库。
func userIDFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !uuid.Valid(id) {
		httpserver.WriteError(w, r, 400, "invalid_id", "用户标识无效")
		return "", false
	}
	return id, true
}

// Handlers 汇集身份相关的 HTTP handler。
type Handlers struct {
	sessionRevoked   func(string)
	passwordObserver identity.PasswordObserver
	database         *sql.DB
	guard            *identity.Guard
	throttle         *identity.LoginThrottle
	logger           *slog.Logger
}

// New 返回身份 handler。
func New(database *sql.DB, guard *identity.Guard, throttle *identity.LoginThrottle, logger *slog.Logger) *Handlers {
	if throttle == nil {
		throttle = identity.NewLoginThrottle(time.Now)
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Handlers{database: database, guard: guard, throttle: throttle, logger: logger}
}

// Register 在 mux 上注册身份相关路由。
func (handlers *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/setup/status", handlers.setupStatus)
	mux.HandleFunc("POST /api/v1/setup/admin", handlers.setupAdmin)
	mux.HandleFunc("POST /api/v1/auth/login", handlers.login)
	mux.HandleFunc("POST /api/v1/auth/logout", handlers.logout)
	mux.HandleFunc("GET /api/v1/auth/me", handlers.me)
	mux.HandleFunc("POST /api/v1/auth/password", handlers.changePassword)
	mux.HandleFunc("GET /api/v1/users", handlers.listUsers)
	mux.HandleFunc("POST /api/v1/users", handlers.createUser)
	mux.HandleFunc("PATCH /api/v1/users/{id}", handlers.updateUser)
	mux.HandleFunc("POST /api/v1/users/{id}/password", handlers.resetPassword)
	mux.HandleFunc("POST /api/v1/users/{id}/sessions/revoke", handlers.revokeUserSessions)
}

func (handlers *Handlers) setupStatus(responseWriter http.ResponseWriter, request *http.Request) {
	var completed bool
	err := handlers.database.QueryRowContext(request.Context(), "SELECT EXISTS (SELECT 1 FROM setup_state)").Scan(&completed)
	if err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	httpserver.WriteJSON(responseWriter, 200, map[string]bool{"setup_required": !completed})
}

func (handlers *Handlers) setupAdmin(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !httpserver.DecodeJSON(responseWriter, request, &input) {
		return
	}
	if strings.TrimSpace(input.Username) == "" || !identity.ValidPassword(input.Password) {
		httpserver.WriteError(responseWriter, request, 400, "invalid_input", invalidAccountMessage)
		return
	}
	passwordHash, err := identity.HashPassword(input.Password)
	if err != nil {
		httpserver.WriteError(responseWriter, request, 500, "internal_error", "无法创建管理员")
		return
	}
	tx, err := handlers.database.BeginTx(request.Context(), nil)
	if err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer tx.Rollback()
	var setupExists bool
	if err = tx.QueryRowContext(request.Context(), "SELECT EXISTS (SELECT 1 FROM setup_state)").Scan(&setupExists); err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	if setupExists {
		httpserver.WriteError(responseWriter, request, 409, "setup_closed", "管理员初始化已完成")
		return
	}
	userID := uuid.New()
	if _, err = tx.ExecContext(request.Context(), "INSERT INTO users (id, username, password_hash, role) VALUES ($1::uuid,$2,$3,'admin')", userID, input.Username, passwordHash); err != nil {
		writeSetupWriteError(responseWriter, request, err)
		return
	}
	if _, err = tx.ExecContext(request.Context(), "INSERT INTO setup_state (completed_at) VALUES (NOW())"); err != nil {
		writeSetupWriteError(responseWriter, request, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeSetupWriteError(responseWriter, request, err)
		return
	}
	httpserver.WriteJSON(responseWriter, 201, map[string]string{"id": userID, "username": input.Username, "role": "admin"})
}

// writeSetupWriteError 映射初始化事务里的写入失败：只有唯一约束冲突（另一次初始化抢先写入了管理员或初始化标记）
// 才是 setup_closed，其他数据库错误如实报告为不可用。
func writeSetupWriteError(responseWriter http.ResponseWriter, request *http.Request, err error) {
	if isUniqueViolation(err) {
		httpserver.WriteError(responseWriter, request, 409, "setup_closed", "管理员初始化已完成")
		return
	}
	httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
}

func (handlers *Handlers) login(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !httpserver.DecodeJSON(responseWriter, request, &input) {
		return
	}
	source := identity.SourceOf(request.RemoteAddr)
	result, attempt, err := identity.Login(request.Context(), handlers.database, handlers.throttle, input.Username, input.Password, source, handlers.passwordObserver)
	var throttled *identity.ThrottledError
	switch {
	case errors.Is(err, identity.ErrInvalidCredentials):
		handlers.logLoginFailure(request, input.Username, source, attempt)
		httpserver.WriteError(responseWriter, request, 401, "invalid_credentials", "用户名或密码错误")
		return
	case errors.As(err, &throttled):
		if throttled.Started {
			handlers.logger.WarnContext(request.Context(), "登录来源开始等待", "event", "identity.login.throttled",
				"module", "identity", "request_id", request.Header.Get("X-Request-ID"), "scope", "source",
				"source", source, "retry_after_seconds", identity.RetryAfterSeconds(throttled.RetryAfter))
		}
		writeThrottledError(responseWriter, request, throttled, "登录失败次数太多，请过几分钟再试")
		return
	case err != nil:
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	handlers.guard.SetSessionCookie(responseWriter, request, result.Token)
	httpserver.WriteJSON(responseWriter, 200, map[string]string{"id": result.User.ID, "username": result.User.Username, "role": result.User.Role})
}

func writeThrottledError(w http.ResponseWriter, r *http.Request, err *identity.ThrottledError, message string) {
	w.Header().Set("Retry-After", strconv.FormatInt(identity.RetryAfterSeconds(err.RetryAfter), 10))
	httpserver.WriteError(w, r, http.StatusTooManyRequests, "too_many_attempts", message)
}

func (handlers *Handlers) logLoginFailure(r *http.Request, username, source string, attempt *identity.Attempt) {
	// 只截取有界前缀，再清理不完整或非法 UTF-8；不把整个不受信任的用户名复制进日志。
	if len(username) > 64 {
		username = username[:64]
	}
	if !utf8.ValidString(username) {
		username = strings.ToValidUTF8(username, "")
	}
	fields := []any{"module", "identity", "request_id", r.Header.Get("X-Request-ID"),
		"source", source, "username", username, "consecutive_failures", attempt.Failures()}
	handlers.logger.InfoContext(r.Context(), "登录凭据核对失败", append(fields, "event", "identity.login.failed")...)
	if wait := attempt.LockedFor(); wait > 0 {
		handlers.logger.WarnContext(r.Context(), "登录账号开始等待", append(fields, "event", "identity.login.throttled",
			"scope", "account", "retry_after_seconds", identity.RetryAfterSeconds(wait))...)
	}
}

func (handlers *Handlers) logout(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	if cookie, err := request.Cookie(identity.SessionCookieName); err == nil {
		var userID string
		err := handlers.database.QueryRowContext(request.Context(), "UPDATE sessions SET revoked_at=NOW() WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at > NOW() RETURNING user_id::text", identity.HashSessionToken(cookie.Value)).Scan(&userID)
		if err == nil && handlers.sessionRevoked != nil {
			handlers.sessionRevoked(userID)
		}
	}
	handlers.guard.ClearSessionCookie(responseWriter, request)
	httpserver.WriteJSON(responseWriter, 200, map[string]bool{"logged_out": true})
}

func (handlers *Handlers) me(responseWriter http.ResponseWriter, request *http.Request) {
	user, err := handlers.guard.Authenticate(request)
	if err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	httpserver.WriteJSON(responseWriter, 200, map[string]string{"id": user.ID, "username": user.Username, "role": user.Role})
}

func (handlers *Handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(w, r); !ok {
		return
	}
	users, err := identity.ListUsers(r.Context(), handlers.database)
	if err != nil {
		writeUserManagementError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(users))
	for _, user := range users {
		items = append(items, map[string]any{"id": user.ID, "username": user.Username, "role": user.Role, "disabled": user.Disabled, "created_at": user.CreatedAt})
	}
	httpserver.WriteJSON(w, 200, map[string]any{"items": items})
}

func (handlers *Handlers) createUser(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	if _, ok := handlers.guard.RequireAdmin(w, r); !ok {
		return
	}
	var in struct{ Username, Password string }
	if !httpserver.DecodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Username) == "" || !identity.ValidPassword(in.Password) {
		httpserver.WriteError(w, r, 400, "invalid_input", invalidAccountMessage)
		return
	}
	hash, err := identity.HashPassword(in.Password)
	if err != nil {
		httpserver.WriteError(w, r, 500, "internal_error", "无法创建用户")
		return
	}
	id := uuid.New()
	tx, err := handlers.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "无法创建用户")
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), "INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,$3,'user')", id, in.Username, hash)
	if err != nil {
		if isUniqueViolation(err) {
			httpserver.WriteError(w, r, 409, "username_taken", "用户名已存在")
			return
		}
		httpserver.WriteError(w, r, 503, "database_unavailable", "无法创建用户")
		return
	}
	if handlers.passwordObserver != nil {
		if err := handlers.passwordObserver.ObservePassword(r.Context(), tx, id, in.Password); err != nil {
			httpserver.WriteError(w, r, 503, "database_unavailable", "无法创建用户")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "无法创建用户")
		return
	}
	httpserver.WriteJSON(w, 201, map[string]string{"id": id, "username": in.Username, "role": "user"})
}

func (handlers *Handlers) updateUser(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	if _, ok := handlers.guard.RequireAdmin(w, r); !ok {
		return
	}
	id, ok := userIDFromPath(w, r)
	if !ok {
		return
	}
	var in struct {
		Disabled *bool `json:"disabled"`
	}
	if !httpserver.DecodeJSON(w, r, &in) {
		return
	}
	if in.Disabled == nil {
		httpserver.WriteError(w, r, 400, "invalid_input", "缺少禁用状态")
		return
	}
	if err := identity.SetUserDisabled(r.Context(), handlers.database, id, *in.Disabled, handlers.sessionRevoked); err != nil {
		writeUserManagementError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, map[string]bool{"disabled": *in.Disabled})
}

func (handlers *Handlers) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	if _, ok := handlers.guard.RequireAdmin(w, r); !ok {
		return
	}
	id, ok := userIDFromPath(w, r)
	if !ok {
		return
	}
	if err := identity.RevokeUserSessions(r.Context(), handlers.database, id, handlers.sessionRevoked); err != nil {
		writeUserManagementError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, map[string]bool{"revoked": true})
}

func writeUserManagementError(w http.ResponseWriter, r *http.Request, err error) {
	var managementError *identity.UserManagementError
	if !errors.As(err, &managementError) {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	status := http.StatusServiceUnavailable
	switch managementError.Code {
	case "not_found":
		status = http.StatusNotFound
	case "last_admin":
		status = http.StatusConflict
	}
	httpserver.WriteError(w, r, status, managementError.Code, managementError.Message)
}

// WithSessionRevoked 在装配时注入登出、停用与吊销会话后的应用通知。
func (handlers *Handlers) WithSessionRevoked(callback func(string)) *Handlers {
	handlers.sessionRevoked = callback
	return handlers
}

// WithPasswordObserver 注入密码校验与变更事务的观察者。
func (handlers *Handlers) WithPasswordObserver(observer identity.PasswordObserver) *Handlers {
	handlers.passwordObserver = observer
	return handlers
}
