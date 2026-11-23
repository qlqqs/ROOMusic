package curation

import (
	"context"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type ModelItemCounts struct {
	WaitingModel int64 `json:"waiting_model"`
	InProgress   int64 `json:"in_progress"`
	WaitingHuman int64 `json:"waiting_human"`
	Shelved      int64 `json:"shelved"`
}

type ModelProgress struct {
	State             string          `json:"state"`
	UnavailableReason *string         `json:"unavailable_reason"`
	Model             string          `json:"model"`
	Today             ModelUsage      `json:"today"`
	DailyTokenLimit   *int64          `json:"daily_token_limit"`
	Items             ModelItemCounts `json:"items"`
}

type ModelStatus interface {
	ModelStatus(context.Context) (ModelProgress, error)
}

type ExceptionResolver interface {
	Accept(ctx context.Context, id, actor, requestID string) (ModelException, error)
	Decline(ctx context.Context, id, actor string) (ModelException, error)
}

func IsSourceUnavailable(err error) bool {
	return errors.Is(err, sources.ErrUnavailable) || errors.Is(err, sources.ErrTryLater) || errors.Is(err, sources.ErrRateLimited)
}

func IsMissingSourceMatch(err error) bool {
	return errors.Is(err, sources.ErrNotFound) || errors.Is(err, sources.ErrInvalidQuery)
}

// CheckModelCorrection 在形成待审提案前使用与写入相同的版本、人工优先及不再提出规则。
func (w *Writer) CheckModelCorrection(ctx context.Context, id string, revision int64, changes map[string]catalog.CorrectionIntent) error {
	fields, err := governance.NormalizeAutoFields(changes)
	if err != nil {
		return err
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	locked, err := governance.LockAutoRelease(ctx, tx, id)
	if err != nil {
		return err
	}
	if revision != locked.EditRevision() {
		return operations.Conflict("revision_conflict", "发行修正版本已变更")
	}
	manual, err := catalog.LoadCorrectionIntents(ctx, tx, id)
	if err != nil {
		return err
	}
	for name := range fields {
		if _, ok := manual[name]; ok {
			return operations.Conflict("manual_override", "字段已有人工修正")
		}
	}
	written, result, err := filterFields(ctx, tx, id, locked, fields, false)
	if err != nil {
		return err
	}
	for _, reason := range result.Skipped {
		if reason == "suppressed" {
			return operations.Conflict("nothing_to_apply", "提案含已标记不再提出的字段值")
		}
	}
	if len(written) == 0 {
		return operations.Conflict("nothing_to_apply", "没有需要写入的自动值")
	}
	return tx.Commit()
}
