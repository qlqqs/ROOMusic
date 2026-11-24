package identity

import (
	"context"
	"database/sql"
	"net/http"
)

// EnabledUser 返回当前启用用户；不存在或已停用时返回 sql.ErrNoRows，其他错误保留。
func (guard *Guard) EnabledUser(ctx context.Context, id string) (User, error) {
	var user User
	err := guard.database.QueryRowContext(ctx, `SELECT id::text,username,role FROM users
 WHERE id=$1::uuid AND disabled_at IS NULL`, id).Scan(&user.ID, &user.Username, &user.Role)
	return user, err
}

// RequireOriginIfPresent 允许没有 Origin 头的客户端；带头时沿用现有的来源规则。
func (guard *Guard) RequireOriginIfPresent(w http.ResponseWriter, r *http.Request) bool {
	if _, present := r.Header["Origin"]; !present {
		return true
	}
	return guard.RequireSameOrigin(w, r)
}

// IsEnabledAdmin 在当前事务中核对管理员身份，并持有行共享锁直到事务结束。
func IsEnabledAdmin(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id=$1::uuid AND role='admin' AND disabled_at IS NULL FOR SHARE)`, id).Scan(&enabled)
	return enabled, err
}

// EnabledAccountCounts 只返回启用的账号数与其中的管理员数。
func (guard *Guard) EnabledAccountCounts(ctx context.Context) (members, admins int, err error) {
	err = guard.database.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE role='admin')
        FROM users WHERE disabled_at IS NULL`).Scan(&members, &admins)
	return
}
