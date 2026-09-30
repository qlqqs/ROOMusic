// Package httpapi 是身份能力的 REST 传输层：初始化管理员、登录登出、当前用户与成员管理。
// 权限判断由 identity.Guard 完成；这里的 handler 只解码请求、调用守卫和映射响应。
// 成员写入的 SQL 暂留在 handler 中（B0 技术债，随成员管理审计一并抽出）。
package httpapi

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// Handlers 汇集身份相关的 HTTP handler。
type Handlers struct {
	database *sql.DB
	guard    *identity.Guard
}

// New 返回身份 handler。
func New(database *sql.DB, guard *identity.Guard) *Handlers {
	return &Handlers{database: database, guard: guard}
}

// Register 在 mux 上注册身份相关路由。
func (handlers *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/setup/status", handlers.setupStatus)
	mux.HandleFunc("POST /api/v1/setup/admin", handlers.setupAdmin)
	mux.HandleFunc("POST /api/v1/auth/login", handlers.login)
	mux.HandleFunc("POST /api/v1/auth/logout", handlers.logout)
	mux.HandleFunc("GET /api/v1/auth/me", handlers.me)
	mux.HandleFunc("GET /api/v1/users", handlers.listUsers)
	mux.HandleFunc("POST /api/v1/users", handlers.createUser)
	mux.HandleFunc("PATCH /api/v1/users/{id}", handlers.updateUser)
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
	if !httpserver.DecodeJSON(responseWriter, request, &input) || len(input.Password) < 12 || strings.TrimSpace(input.Username) == "" {
		httpserver.WriteError(responseWriter, request, 400, "invalid_input", "用户名不能为空，密码至少需要 12 个字符")
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
	if err = tx.QueryRowContext(request.Context(), "SELECT EXISTS (SELECT 1 FROM setup_state)").Scan(&setupExists); err != nil || setupExists {
		httpserver.WriteError(responseWriter, request, 409, "setup_closed", "管理员初始化已完成")
		return
	}
	userID := uuid.New()
	if _, err = tx.ExecContext(request.Context(), "INSERT INTO users (id, username, password_hash, role) VALUES ($1::uuid,$2,$3,'admin')", userID, input.Username, passwordHash); err != nil {
		httpserver.WriteError(responseWriter, request, 409, "setup_closed", "管理员初始化已完成")
		return
	}
	if _, err = tx.ExecContext(request.Context(), "INSERT INTO setup_state (completed_at) VALUES (NOW())"); err != nil {
		httpserver.WriteError(responseWriter, request, 409, "setup_closed", "管理员初始化已完成")
		return
	}
	if err = tx.Commit(); err != nil {
		httpserver.WriteError(responseWriter, request, 409, "setup_closed", "管理员初始化已完成")
		return
	}
	httpserver.WriteJSON(responseWriter, 201, map[string]string{"username": input.Username, "role": "admin"})
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
	var userID, passwordHash, role string
	err := handlers.database.QueryRowContext(request.Context(), "SELECT id::text,password_hash,role FROM users WHERE username=$1 AND disabled_at IS NULL", input.Username).Scan(&userID, &passwordHash, &role)
	if err != nil || !identity.VerifyPassword(passwordHash, input.Password) {
		httpserver.WriteError(responseWriter, request, 401, "invalid_credentials", "用户名或密码错误")
		return
	}
	token := identity.GenerateSessionToken()
	_, err = handlers.database.ExecContext(request.Context(), "INSERT INTO sessions (token_hash,user_id,expires_at) VALUES ($1,$2::uuid,NOW()+INTERVAL '24 hours')", identity.HashSessionToken(token), userID)
	if err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	handlers.guard.SetSessionCookie(responseWriter, token)
	httpserver.WriteJSON(responseWriter, 200, map[string]string{"username": input.Username, "role": role})
}

func (handlers *Handlers) logout(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	if cookie, err := request.Cookie(identity.SessionCookieName); err == nil {
		_, _ = handlers.database.ExecContext(request.Context(), "UPDATE sessions SET revoked_at=NOW() WHERE token_hash=$1", identity.HashSessionToken(cookie.Value))
	}
	handlers.guard.ClearSessionCookie(responseWriter)
	httpserver.WriteJSON(responseWriter, 200, map[string]bool{"logged_out": true})
}

func (handlers *Handlers) me(responseWriter http.ResponseWriter, request *http.Request) {
	user, err := handlers.guard.Authenticate(request)
	if err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	httpserver.WriteJSON(responseWriter, 200, map[string]string{"username": user.Username, "role": user.Role})
}

func (handlers *Handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(w, r); !ok {
		return
	}
	rows, err := handlers.database.QueryContext(r.Context(), "SELECT id::text,username,role,disabled_at,created_at FROM users ORDER BY username")
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, username, role string
		var disabled sql.NullTime
		var created time.Time
		if rows.Scan(&id, &username, &role, &disabled, &created) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "username": username, "role": role, "disabled": disabled.Valid, "created_at": created})
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
	if !httpserver.DecodeJSON(w, r, &in) || strings.TrimSpace(in.Username) == "" || len(in.Password) < 12 {
		httpserver.WriteError(w, r, 400, "invalid_input", "用户名不能为空，密码至少需要 12 个字符")
		return
	}
	hash, err := identity.HashPassword(in.Password)
	if err != nil {
		httpserver.WriteError(w, r, 500, "internal_error", "无法创建用户")
		return
	}
	id := uuid.New()
	_, err = handlers.database.ExecContext(r.Context(), "INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,$3,'user')", id, in.Username, hash)
	if err != nil {
		httpserver.WriteError(w, r, 409, "username_taken", "用户名已存在")
		return
	}
	httpserver.WriteJSON(w, 201, map[string]string{"id": id, "username": in.Username, "role": "user"})
}

func (handlers *Handlers) updateUser(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	_, ok := handlers.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	var in struct {
		Disabled *bool `json:"disabled"`
	}
	if !httpserver.DecodeJSON(w, r, &in) || in.Disabled == nil {
		httpserver.WriteError(w, r, 400, "invalid_input", "缺少禁用状态")
		return
	}
	id := r.PathValue("id")
	tx, err := handlers.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer tx.Rollback()
	// Lock administrator rows first so concurrent last-admin checks share one order.
	adminRows, queryErr := tx.QueryContext(r.Context(), "SELECT id FROM users WHERE role='admin' AND disabled_at IS NULL FOR UPDATE")
	if queryErr != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	admins := 0
	for adminRows.Next() {
		admins++
	}
	rowsErr := adminRows.Err()
	adminRows.Close()
	if rowsErr != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	var role string
	var currentlyDisabled bool
	err = tx.QueryRowContext(r.Context(), "SELECT role, disabled_at IS NOT NULL FROM users WHERE id=$1::uuid FOR UPDATE", id).Scan(&role, &currentlyDisabled)
	if err == sql.ErrNoRows {
		httpserver.WriteError(w, r, 404, "not_found", "用户不存在")
		return
	}
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	if *in.Disabled && !currentlyDisabled && role == "admin" {
		if admins <= 1 {
			httpserver.WriteError(w, r, 409, "last_admin", "不能禁用最后一个管理员")
			return
		}
	}
	if *in.Disabled {
		var result sql.Result
		result, err = tx.ExecContext(r.Context(), "UPDATE users SET disabled_at=NOW() WHERE id=$1::uuid", id)
		if err == nil {
			if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
				err = affectedErr
			} else if affected != 1 {
				err = sql.ErrNoRows
			}
		}
	} else {
		var result sql.Result
		result, err = tx.ExecContext(r.Context(), "UPDATE users SET disabled_at=NULL WHERE id=$1::uuid", id)
		if err == nil {
			if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
				err = affectedErr
			} else if affected != 1 {
				err = sql.ErrNoRows
			}
		}
	}
	if err != nil {
		if err == sql.ErrNoRows {
			httpserver.WriteError(w, r, 404, "not_found", "用户不存在")
			return
		}
		httpserver.WriteError(w, r, 503, "database_unavailable", "无法更新用户")
		return
	}
	if *in.Disabled {
		if _, err = tx.ExecContext(r.Context(), "UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid AND revoked_at IS NULL", id); err != nil {
			httpserver.WriteError(w, r, 503, "database_unavailable", "无法撤销会话")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "无法更新用户")
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
	_, err := handlers.database.ExecContext(r.Context(), "UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid AND revoked_at IS NULL", r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "无法撤销会话")
		return
	}
	httpserver.WriteJSON(w, 200, map[string]bool{"revoked": true})
}
