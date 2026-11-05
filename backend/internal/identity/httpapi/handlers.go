// Package httpapi 是身份能力的 REST 传输层：初始化管理员、登录登出、当前用户与成员管理。
// 权限判断由 identity.Guard 完成；这里的 handler 只解码请求、调用守卫和映射响应。
// 已有的成员写入 SQL 暂留在 handler 中（B0 技术债）；改密码与重置的 SQL 在 identity/passwords.go。
package httpapi

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	httpserver.WriteJSON(responseWriter, 200, map[string]string{"id": userID, "username": input.Username, "role": role})
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
	httpserver.WriteJSON(responseWriter, 200, map[string]string{"id": user.ID, "username": user.Username, "role": user.Role})
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
			httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
			return
		}
		items = append(items, map[string]any{"id": id, "username": username, "role": role, "disabled": disabled.Valid, "created_at": created})
	}
	if rows.Err() != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
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
	_, err = handlers.database.ExecContext(r.Context(), "INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,$3,'user')", id, in.Username, hash)
	if err != nil {
		if isUniqueViolation(err) {
			httpserver.WriteError(w, r, 409, "username_taken", "用户名已存在")
			return
		}
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
	id, ok := userIDFromPath(w, r)
	if !ok {
		return
	}
	// 没有删除用户的接口，存在性检查与撤销之间用户不会消失，所以不需要把两步放进同一个事务。
	var exists bool
	if err := handlers.database.QueryRowContext(r.Context(), "SELECT EXISTS (SELECT 1 FROM users WHERE id=$1::uuid)", id).Scan(&exists); err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "无法撤销会话")
		return
	}
	if !exists {
		httpserver.WriteError(w, r, 404, "not_found", "用户不存在")
		return
	}
	_, err := handlers.database.ExecContext(r.Context(), "UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid AND revoked_at IS NULL", id)
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "无法撤销会话")
		return
	}
	httpserver.WriteJSON(w, 200, map[string]bool{"revoked": true})
}
