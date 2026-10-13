package operations

import (
	"context"
	"database/sql"
	"errors"
)

// Kind 是一种操作类型的撤销与展示能力，由拥有对应业务表的包实现，经 app 注册给传输层（operations/httpapi），
// 所以 operations 不需要知道任何业务规则。一个类型对应一个 Kind；撤销记录的状态与它所撤销的操作同类型（见 Record.ChangeType）。
type Kind interface {
	// Type 是它处理的 operation_type（TypeReleaseCorrection、TypeReleaseGroupChange）。
	Type() string
	// Revert 在 RevertExecutor 提供的事务里撤销 original：锁定目标、核对 original 是目标上最近一次修改
	// （目标的人工修改版本号等于 original.ResultRevision，否则返回 revert_conflict）、恢复前态，返回撤销记录的目标、前后版本与前后状态。
	// original 已被锁住并确认不是撤销、没有被撤销过；RevertsID 与响应由 RevertExecutor 填写。
	Revert(ctx context.Context, tx *sql.Tx, operationID string, original Record) (Outcome, error)
	// Describe 批量补充记录在列表里的展示信息（每条记录一项，键是记录 ID）：目标当前的状态、改动摘要，以及目标现在是否仍可撤销它。
	// 传入的记录都属于这个类型（含它们的撤销记录）。
	Describe(ctx context.Context, database Queryer, records []Record) (map[string]Description, error)
}

// ReleaseInfo 是记录所指发行当前的展示信息：有效标题、所在的组，以及它现在是否可见。
type ReleaseInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	GroupID string `json:"group_id"`
	Visible bool   `json:"visible"`
}

// Description 是一条记录在列表里的展示信息，由 Kind.Describe 批量提供。
type Description struct {
	Release    *ReleaseInfo // 目标发行；发行已不存在时为 nil
	Changes    any          // 改动摘要（可序列化成 JSON，结构由类型决定，含 kind 字段）
	Revertible bool         // 目标现在仍可撤销这条记录；是否未被撤销、是否本身是撤销由调用方判断
}

// RevertResult 是撤销成功的响应。目前唯一的目标类型是发行，所以直接给 release_id。
type RevertResult struct {
	OperationID        string `json:"operation_id"`
	RevertsOperationID string `json:"reverts_operation_id"`
	ReleaseID          string `json:"release_id"`
	Revision           int64  `json:"revision"`
}

// RevertExecutor 返回撤销的执行器，交给 Run 执行（Spec.Type 为 TypeRevert）。它先锁住并重新读取原记录，所以对同一条记录的并发撤销
// 串行；不存在返回 404 not_found，本身是撤销返回 409 not_revertible，已被撤销返回 409 already_reverted；之后交给 kind 撤销业务变化。
func RevertExecutor(kind Kind, originalID string) Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
		original, err := LoadForUpdate(ctx, tx, originalID)
		if errors.Is(err, ErrNotFound) {
			return Outcome{}, NotFound("not_found", "操作记录不存在")
		}
		if err != nil {
			return Outcome{}, err
		}
		switch {
		case original.Type == TypeRevert:
			return Outcome{}, Conflict("not_revertible", "撤销不能再被撤销；需要的话请重新修改")
		case original.Type != kind.Type():
			return Outcome{}, Conflict("not_revertible", "这类操作不支持撤销")
		case original.RevertedByID != "":
			return Outcome{}, Conflict("already_reverted", "这条操作已经撤销过了")
		}
		outcome, err := kind.Revert(ctx, tx, operationID, original)
		if err != nil {
			return Outcome{}, err
		}
		outcome.RevertsID = original.ID
		outcome.Response = RevertResult{OperationID: operationID, RevertsOperationID: original.ID, ReleaseID: outcome.TargetID, Revision: outcome.ResultRevision}
		return outcome, nil
	}
}
