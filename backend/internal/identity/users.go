package identity

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// UserManagementError 提供成员管理的稳定错误码与安全文案；Err 仅供内部诊断。
type UserManagementError struct {
	Code    string
	Message string
	Err     error
}

func (e *UserManagementError) Error() string { return e.Message }
func (e *UserManagementError) Unwrap() error { return e.Err }

// UserSummary 不包含密码哈希或会话信息。
type UserSummary struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
}

// ListUsers 返回按用户名排序的成员列表；任一行读取失败都不返回部分列表。
func ListUsers(ctx context.Context, db *sql.DB) ([]UserSummary, error) {
	rows, err := db.QueryContext(ctx, "SELECT id::text,username,role,disabled_at,created_at FROM users ORDER BY username")
	if err != nil {
		return nil, &UserManagementError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	defer rows.Close()
	items := []UserSummary{}
	for rows.Next() {
		var id, username, role string
		var disabled sql.NullTime
		var created time.Time
		if err := rows.Scan(&id, &username, &role, &disabled, &created); err != nil {
			return nil, &UserManagementError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
		}
		items = append(items, UserSummary{ID: id, Username: username, Role: role, Disabled: disabled.Valid, CreatedAt: created})
	}
	if err := rows.Err(); err != nil {
		return nil, &UserManagementError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	return items, nil
}

// SetUserDisabled 在同一事务内保护最后一个管理员、更新状态并按需撤销会话。
// 调用入口负责管理员鉴权及用户 ID 格式校验。
func SetUserDisabled(ctx context.Context, db *sql.DB, id string, disabled bool, revoked ...func(string)) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return &UserManagementError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	defer tx.Rollback()
	// 先锁启用管理员集合，使并发的最后管理员检查使用同一加锁顺序。
	adminRows, queryErr := tx.QueryContext(ctx, "SELECT id FROM users WHERE role='admin' AND disabled_at IS NULL FOR UPDATE")
	if queryErr != nil {
		return &UserManagementError{Code: "database_unavailable", Message: "服务暂不可用", Err: queryErr}
	}
	admins := 0
	for adminRows.Next() {
		admins++
	}
	rowsErr := adminRows.Err()
	adminRows.Close()
	if rowsErr != nil {
		return &UserManagementError{Code: "database_unavailable", Message: "服务暂不可用", Err: rowsErr}
	}
	var role string
	var currentlyDisabled bool
	err = tx.QueryRowContext(ctx, "SELECT role, disabled_at IS NOT NULL FROM users WHERE id=$1::uuid FOR UPDATE", id).Scan(&role, &currentlyDisabled)
	if err == sql.ErrNoRows {
		return &UserManagementError{Code: "not_found", Message: "用户不存在"}
	}
	if err != nil {
		return &UserManagementError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	if disabled && !currentlyDisabled && role == "admin" {
		if admins <= 1 {
			return &UserManagementError{Code: "last_admin", Message: "不能禁用最后一个管理员"}
		}
	}
	if disabled {
		var result sql.Result
		result, err = tx.ExecContext(ctx, "UPDATE users SET disabled_at=NOW() WHERE id=$1::uuid", id)
		if err == nil {
			if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
				err = affectedErr
			} else if affected != 1 {
				err = sql.ErrNoRows
			}
		}
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, "UPDATE users SET disabled_at=NULL WHERE id=$1::uuid", id)
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
			return &UserManagementError{Code: "not_found", Message: "用户不存在"}
		}
		return &UserManagementError{Code: "database_unavailable", Message: "无法更新用户", Err: err}
	}
	if disabled {
		if _, err = tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid AND revoked_at IS NULL", id); err != nil {
			return &UserManagementError{Code: "database_unavailable", Message: "无法撤销会话", Err: err}
		}
	}
	if err = tx.Commit(); err != nil {
		return &UserManagementError{Code: "database_unavailable", Message: "无法更新用户", Err: err}
	}
	if disabled {
		notifyRevoked(id, revoked)
	}
	return nil
}

// HasActiveSession 判断账号是否仍启用且至少保有一条有效会话。
func HasActiveSession(ctx context.Context, db *sql.DB, id string) (bool, error) {
	var active bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE u.id=$1 AND u.disabled_at IS NULL AND s.revoked_at IS NULL AND s.expires_at>NOW()
	)`, id).Scan(&active)
	return active, err
}

// RevokeUserSessions 检查用户存在后撤销其全部未撤销会话。
// 调用入口负责管理员鉴权及用户 ID 格式校验。
func RevokeUserSessions(ctx context.Context, db *sql.DB, id string, revoked ...func(string)) error {
	// 没有删除用户的接口，存在性检查与撤销之间用户不会消失，所以不需要把两步放进同一个事务。
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE id=$1::uuid)", id).Scan(&exists); err != nil {
		return &UserManagementError{Code: "database_unavailable", Message: "无法撤销会话", Err: err}
	}
	if !exists {
		return &UserManagementError{Code: "not_found", Message: "用户不存在"}
	}
	_, err := db.ExecContext(ctx, "UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid AND revoked_at IS NULL", id)
	if err != nil {
		return &UserManagementError{Code: "database_unavailable", Message: "无法撤销会话", Err: err}
	}
	notifyRevoked(id, revoked)
	return nil
}

// 回调只在身份变更成功提交后通知应用，不依赖管家包。
func notifyRevoked(id string, callbacks []func(string)) {
	for _, callback := range callbacks {
		if callback != nil {
			callback(strings.ToLower(id))
		}
	}
}
