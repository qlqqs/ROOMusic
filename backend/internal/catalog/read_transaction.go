package catalog

import (
	"context"
	"database/sql"
	"fmt"
)

// ReadTransactionBeginner 允许读取用例拥有短事务，不改变调用方连接的会话配置。
type ReadTransactionBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// ReadRecentlyAddedReleaseGroups 是概览最近新增入口；查询及事务局部配置失败均按查询错误返回。
func ReadRecentlyAddedReleaseGroups(ctx context.Context, database ReadTransactionBeginner) ([]ReleaseGroupSummary, error) {
	return readWithLocalJITOff(ctx, database, func(tx *sql.Tx) ([]ReleaseGroupSummary, error) {
		return NewStore(tx).ListRecentlyAddedReleaseGroups(ctx)
	})
}

// ReadReleaseGroupsByAddedAt 在同一个只读快照中读取计数与当前页；其它排序仍使用原入口。
func ReadReleaseGroupsByAddedAt(ctx context.Context, database ReadTransactionBeginner, params ListParams) (ReleaseGroupPage, error) {
	params.Sort = ReleaseGroupSortAddedAtDesc
	return readWithLocalJITOff(ctx, database, func(tx *sql.Tx) (ReleaseGroupPage, error) {
		snapshot := NewStore(tx)
		if params.Search == "" {
			return snapshot.listReleaseGroupsLegacy(ctx, params)
		}
		state, generation, err := snapshot.searchState(ctx)
		if err != nil {
			return ReleaseGroupPage{}, err
		}
		return snapshot.listReleaseGroupsSearch(ctx, params, state, generation)
	})
}

func readWithLocalJITOff[T any](ctx context.Context, database ReadTransactionBeginner, read func(*sql.Tx) (T, error)) (T, error) {
	var zero T
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return zero, fmt.Errorf("%w: begin catalog read: %w", ErrQuery, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SET LOCAL jit = off"); err != nil {
		return zero, fmt.Errorf("%w: configure catalog read: %w", ErrQuery, err)
	}
	result, err := read(tx)
	if err != nil {
		return zero, fmt.Errorf("%w: catalog read: %w", ErrQuery, err)
	}
	if err := tx.Commit(); err != nil {
		return zero, fmt.Errorf("%w: commit catalog read: %w", ErrQuery, err)
	}
	return result, nil
}

// ReadWithLocalJITOff 供 catalog 子包复用同一只读快照与事务局部 JIT 策略。
func ReadWithLocalJITOff[T any](ctx context.Context, database ReadTransactionBeginner, read func(*sql.Tx) (T, error)) (T, error) {
	return readWithLocalJITOff(ctx, database, read)
}
