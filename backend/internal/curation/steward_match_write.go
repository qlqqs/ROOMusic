package curation

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

type stewardMatchTransaction struct {
	tx          *sql.Tx
	origin      operations.Origin
	requestID   string
	afterCommit []func()
}

// StewardMatchWrite 将管家一次对应的治理操作和结论一起提交，各步仍有独立的撤销记录。
func (w *Writer) StewardMatchWrite(ctx context.Context, origin operations.Origin, requestID string, apply func(*sql.Tx, *Writer) error) error {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	scoped := *w
	scoped.matchTransaction = &stewardMatchTransaction{tx: tx, origin: origin, requestID: requestID}
	if err := apply(tx, &scoped); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, after := range scoped.matchTransaction.afterCommit {
		after()
	}
	return nil
}

func (w *Writer) runMatchOperation(ctx context.Context, spec operations.Spec, execute operations.Executor) (operations.Result, error) {
	scope := w.matchTransaction
	if scope == nil {
		return operations.Run(ctx, w.db, spec, execute)
	}
	entry := scope.origin
	entry.ProviderKind, entry.Provider, entry.Basis = spec.Origin.ProviderKind, spec.Origin.Provider, spec.Origin.Basis
	spec.Origin, spec.RequestID = &entry, scope.requestID
	result, after, err := operations.RunInTx(ctx, scope.tx, spec, execute)
	if err == nil && after != nil {
		scope.afterCommit = append(scope.afterCommit, after)
	}
	return result, err
}
