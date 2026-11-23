package curation

import (
	"context"
	"database/sql"
	"regexp"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type ModelRun struct {
	ConversationID, Model  string
	ActorID, PendingItemID string
	Batch                  func(context.Context) (string, error)
}
type modelRunKey struct{}

func WithModelRun(ctx context.Context, run *ModelRun) context.Context {
	return context.WithValue(ctx, modelRunKey{}, run)
}
func ModelRunFrom(ctx context.Context) (*ModelRun, bool) {
	run, ok := ctx.Value(modelRunKey{}).(*ModelRun)
	return run, ok && run != nil
}

var invalidProvider = regexp.MustCompile("[^a-z0-9._-]")

func NormalizeProvider(model string) string {
	p := invalidProvider.ReplaceAllString(strings.ToLower(model), "-")
	if p == "" {
		return "model"
	}
	return p[:min(64, len(p))]
}

// 旧的程序调用只传 MusicBrainz 写入者。模型写入必须明确给出外部源。
func musicBrainzSource(external string, source Source) (string, error) {
	if external == "musicbrainz" || (external == "" && source.Kind == "source" && source.Provider == "musicbrainz") {
		return "musicbrainz", nil
	}
	return "", operations.Invalid("invalid_input", "外部源必须是 MusicBrainz")
}

func (w *Writer) writerActor(ctx context.Context, batch, confirmed, conversation string) (string, error) {
	if conversation != "" && !uuid.Valid(conversation) {
		return "", operations.Invalid("invalid_input", "会话编号无效")
	}
	if confirmed != "" {
		if batch != "" || !uuid.Valid(confirmed) {
			return "", operations.Invalid("invalid_input", "人工采纳的操作者或批次无效")
		}
		return strings.ToLower(confirmed), nil
	}
	if run, ok := ModelRunFrom(ctx); ok && run.ActorID != "" {
		return run.ActorID, nil
	}
	var actor *string
	if err := w.db.QueryRowContext(ctx, "SELECT enabled_by::text FROM curation_settings WHERE id=1").Scan(&actor); err != nil {
		return "", err
	}
	if actor == nil {
		return "", operations.Conflict("curation_disabled", "自动整理开关已关闭")
	}
	return *actor, nil
}

func checkWrite(ctx context.Context, tx *sql.Tx, spec operations.Spec, confirmed, releaseID string, expected, current int64) error {
	if expected != 0 && expected != current {
		return operations.Conflict("revision_conflict", "版本已变更")
	}
	if confirmed == "" {
		if err := checkExternalWriter(ctx, tx, spec); err != nil {
			return err
		}
		if run, ok := ModelRunFrom(ctx); ok && run.PendingItemID != "" {
			var claimed bool
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_pending_items WHERE id=$1 AND target_id=$2 AND conversation_id=$3 AND status='in_progress' FOR SHARE)`, run.PendingItemID, releaseID, run.ConversationID).Scan(&claimed)
			if err != nil {
				return err
			}
			if !claimed {
				return operations.Conflict("out_of_batch", "待定项不属于本次运行")
			}
		}
		return nil
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM curation_settings WHERE id=1 FOR SHARE").Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return operations.Conflict("curation_disabled", "自动整理开关已关闭")
	}
	admin, err := identity.IsEnabledAdmin(ctx, tx, confirmed)
	if err != nil {
		return err
	}
	if !admin {
		return operations.Conflict("curation_disabled", "采纳者须是启用的管理员")
	}
	return nil
}
