// Package writegate 提供目录事实与投影共用的短事务写锁。
package writegate

import (
	"context"
	"database/sql"
)

// Execer 只要求调用方已持有事务的执行能力，不在本包开启事务。
type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Lock 必须在发行、曲目等业务行锁之前取得；操作记录锁仍按既有顺序先取。
// CATP 与扫描、迁移、operations 和维护选主分别使用不同键空间。
func Lock(ctx context.Context, tx Execer) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::integer, $2::integer)`, 0x43415450, 1)
	return err
}
