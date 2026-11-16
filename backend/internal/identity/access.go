package identity

import (
	"context"
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
