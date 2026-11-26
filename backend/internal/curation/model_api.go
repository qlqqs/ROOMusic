package curation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type ModelItemCounts struct {
	WaitingModel int64 `json:"waiting_model"`
	InProgress   int64 `json:"in_progress"`
	WaitingHuman int64 `json:"waiting_human"`
	Shelved      int64 `json:"shelved"`
}

type ModelProgress struct {
	Running            int             `json:"running"`
	Concurrency        int             `json:"concurrency"`
	WebDailyTokenLimit *int64          `json:"web_daily_token_limit"`
	WebToday           ModelUsage      `json:"web_today"`
	WebLimitReached    bool            `json:"web_limit_reached"`
	WebRetryAt         *time.Time      `json:"web_retry_at"`
	State              string          `json:"state"`
	UnavailableReason  *string         `json:"unavailable_reason"`
	Model              string          `json:"model"`
	Today              ModelUsage      `json:"today"`
	DailyTokenLimit    *int64          `json:"daily_token_limit"`
	Items              ModelItemCounts `json:"items"`
}

type ModelScheduler interface {
	Wake()
	ModelStatus(context.Context) (ModelProgress, error)
}

type ExceptionResolver interface {
	Accept(ctx context.Context, id, actor, requestID string) (ModelException, error)
	Decline(ctx context.Context, id, actor string) (ModelException, error)
}

func IsSourceUnavailable(err error) bool {
	return errors.Is(err, sources.ErrUnavailable) || errors.Is(err, sources.ErrTryLater) || errors.Is(err, sources.ErrRateLimited)
}

func SourceRecoveryTime(err error) time.Time {
	var pause *sources.QueuePause
	if errors.As(err, &pause) {
		return pause.RetryAt
	}
	return time.Time{}
}

func IsMissingSourceMatch(err error) bool {
	return errors.Is(err, sources.ErrNotFound) || errors.Is(err, sources.ErrInvalidQuery)
}

// CheckModelCorrection 在形成待审提案前使用与写入相同的版本、人工优先及不再提出规则。
func (w *Writer) CheckModelCorrection(ctx context.Context, id string, revision int64, changes map[string]catalog.CorrectionIntent, level catalog.AutoWriterLevel) error {
	return w.checkModelFields(ctx, id, "", false, revision, changes, level)
}

func (w *Writer) CheckModelTrackCorrection(ctx context.Context, releaseID, trackID string, revision int64, changes map[string]catalog.CorrectionIntent, level catalog.AutoWriterLevel) error {
	if !uuid.Valid(trackID) {
		return operations.Invalid("invalid_input", "曲目标识无效")
	}
	return w.checkModelFields(ctx, releaseID, trackID, true, revision, changes, level)
}

func (w *Writer) checkModelFields(ctx context.Context, releaseID, trackID string, track bool, revision int64, changes map[string]catalog.CorrectionIntent, level catalog.AutoWriterLevel) error {
	normalize := governance.NormalizeAutoFields
	target, id, definitions := "release", releaseID, catalog.CorrectableFields
	if track {
		normalize = governance.NormalizeTrackAutoFields
		target, id, definitions = "track", trackID, catalog.TrackCorrectableFields
	}
	fields, err := normalize(changes)
	if err != nil {
		return err
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locked autoFieldTarget
	if track {
		t, err := governance.LockAutoTrack(ctx, tx, id)
		if err != nil {
			return err
		}
		if !strings.EqualFold(t.ReleaseID(), releaseID) {
			return operations.Conflict("item_stale", "曲目所属发行已变更")
		}
		locked = t
	} else {
		locked, err = governance.LockAutoRelease(ctx, tx, id)
	}
	if err != nil {
		return err
	}
	if revision != locked.EditRevision() {
		return operations.Conflict("revision_conflict", "版本已变更")
	}
	manual, err := loadManualFields(ctx, tx, target, id)
	if err != nil {
		return err
	}
	for name := range fields {
		if _, ok := manual[name]; ok {
			return operations.Conflict("manual_override", "字段已有人工修正")
		}
	}
	written, result, err := filterAutoFields(ctx, tx, target, id, definitions, locked, fields, false, level)
	if err != nil {
		return err
	}
	for _, reason := range result.Skipped {
		if reason == "lower_priority" {
			return operations.Conflict("nothing_to_apply", "提案含更高级别已写入的字段")
		}
		if reason == "suppressed" {
			return operations.Conflict("nothing_to_apply", "提案含已标记不再提出的字段值")
		}
	}
	if len(written) == 0 {
		return operations.Conflict("nothing_to_apply", "没有需要写入的自动值")
	}
	return tx.Commit()
}
