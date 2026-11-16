package operations

import (
	"context"
	"database/sql"
	"errors"
)

// LookupResult 只读查询服务端保存的操作者与幂等键，不执行或重试操作。
// 调用方必须使用该项原先持久化的键；不存在时返回 ErrNotFound。
func LookupResult(ctx context.Context, tx *sql.Tx, actorID, key string) (Result, error) {
	var result Result
	err := tx.QueryRowContext(ctx, `SELECT id::text,response FROM operations WHERE actor_id=$1::uuid AND idempotency_key=$2::uuid`, actorID, key).Scan(&result.OperationID, &result.Response)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, ErrNotFound
	}
	if err != nil {
		return Result{}, unavailable("read operation result", err)
	}
	result.Replayed = true
	return result, nil
}
