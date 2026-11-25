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

// ModelClaimDigest 绑定发给模型的完整指纹和识别摘要。相同领取行可能被程序重新判定。
func (p PendingItem) ModelClaimDigest() string { return p.InputDigest + "/" + p.IdentityDigest }

type ModelRun struct {
	ClaimDigests           map[string]string
	ConversationID, Model  string
	ActorID, PendingItemID string
	TaskKind               string
	Abandon                func(context.Context, string, string) error
	EvidencePage           func(context.Context, string, string) (string, error)
	ToolInputDigest        string
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

func checkWrite(ctx context.Context, tx *sql.Tx, spec operations.Spec, confirmed, targetID string, expected, current int64) error {
	if expected != 0 && expected != current {
		return operations.Conflict("revision_conflict", "版本已变更")
	}
	if confirmed == "" {
		if err := checkExternalWriter(ctx, tx, spec); err != nil {
			return err
		}
		if run, ok := ModelRunFrom(ctx); ok && run.PendingItemID != "" {
			column := "target_id"
			if spec.Type == operations.TypeArtistLink {
				column = "artist_id"
			}
			return checkClaimedTarget(ctx, tx, run, targetID, column, "")
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

func checkClaimedTarget(ctx context.Context, tx *sql.Tx, run *ModelRun, targetID, column, kind string) error {
	var current PendingItem
	err := tx.QueryRowContext(ctx, `SELECT input_digest,COALESCE(identity_digest,'') FROM curation_pending_items
 WHERE id=$1 AND `+column+`=$2 AND conversation_id=$3 AND status='in_progress' AND ($4='' OR kind=$4) FOR SHARE`,
		run.PendingItemID, targetID, run.ConversationID, kind).Scan(&current.InputDigest, &current.IdentityDigest)
	if err == sql.ErrNoRows {
		return operations.Conflict("out_of_batch", "待定项不属于本次运行")
	}
	if err != nil {
		return err
	}
	if run.ClaimDigests != nil && run.ClaimDigests[run.PendingItemID] != current.ModelClaimDigest() {
		return operations.Conflict("item_stale", "领取时的证据已变化")
	}
	return nil
}
