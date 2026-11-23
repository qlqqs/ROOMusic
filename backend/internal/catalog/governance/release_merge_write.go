package governance

import (
	"context"
	"database/sql"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/personal/albumfavorites"
)

type ReleaseMergeRequest struct {
	TargetID, SourceID, Token string
}

func (r ReleaseMergeRequest) Fingerprint() string {
	return operations.Fingerprint(map[string]string{"type": operations.TypeReleaseMerge, "target": strings.ToLower(r.TargetID), "source": strings.ToLower(r.SourceID), "token": r.Token})
}

type mergeRecordedSource struct {
	MergeReleaseRef
	Revision int64 `json:"edit_revision"`
}

type mergeRecordedMedium struct {
	ID       string `json:"medium_id"`
	Position int    `json:"position"`
}

type mergeRecordState struct {
	Source    mergeRecordedSource                  `json:"source"`
	Target    MergeReleaseRef                      `json:"target"`
	Media     []mergeRecordedMedium                `json:"media"`
	Favorites []albumfavorites.MergedAlbumFavorite `json:"favorites,omitempty"`
}

func lockMergeReleases(ctx context.Context, tx *sql.Tx, target, source string) error {
	if err := ingest.LockProjectionWrites(ctx, tx); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `SELECT id FROM releases WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`, target, source)
	return err
}

func refreshMergeReleases(ctx context.Context, tx *sql.Tx, target, source string) error {
	if err := artistgraph.Refresh(ctx, tx, []string{target, source}); err != nil {
		return err
	}
	if err := searchindex.RefreshRelease(ctx, tx, target); err != nil {
		return err
	}
	return searchindex.RefreshRelease(ctx, tx, source)
}

func releaseTargets(ids ...string) []operations.Target {
	targets := make([]operations.Target, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, operations.Target{Type: operations.TargetRelease, ID: id})
	}
	return targets
}

// ReleaseMergeExecutor 在发行与收藏状态锁内复算预览，原碟身份和收藏前态随操作一起提交。
func ReleaseMergeExecutor(request ReleaseMergeRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, opID string) (operations.Outcome, error) {
		var empty operations.Outcome
		target, source := strings.ToLower(request.TargetID), strings.ToLower(request.SourceID)
		if err := validMergeIDs(target, source); err != nil {
			return empty, err
		}
		if err := lockMergeReleases(ctx, tx, target, source); err != nil {
			return empty, err
		}
		s, err := loadMergeSnapshot(ctx, tx, target, source)
		if err != nil {
			return empty, err
		}
		plan := buildMergePlan(s)
		if request.Token != plan.Token {
			return empty, operations.Conflict("merge_changed", "预览后发行内容已变化，请重新预览")
		}

		before := mergeRecordState{Source: mergeRecordedSource{s.source, s.sourceRevision}, Target: s.target, Media: []mergeRecordedMedium{}}
		after := mergeRecordState{Source: mergeRecordedSource{s.source, s.sourceRevision + 1}, Target: s.target, Media: []mergeRecordedMedium{}}
		for i, m := range s.media {
			if m.FromRelease == source {
				before.Media = append(before.Media, mergeRecordedMedium{m.MediumID, m.position})
				after.Media = append(after.Media, mergeRecordedMedium{m.MediumID, plan.Media[i].PositionAfter})
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_merges(source_release_id,target_release_id,operation_id) VALUES($1,$2,$3)`, source, target, opID); err != nil {
			return empty, err
		}
		// 改挂的同时腾到目标的最小碟号以下，整次 UPDATE 都不会撞唯一约束。
		if _, err := tx.ExecContext(ctx, `WITH moving AS (
			SELECT id, (SELECT least(min(position),0) FROM media WHERE release_id=$1)-row_number() OVER (ORDER BY id) AS temporary
			FROM media WHERE release_id=$2
		) UPDATE media m SET origin_release_id=m.release_id,origin_position=m.position,
			release_id=$1,position=moving.temporary FROM moving WHERE m.id=moving.id`, target, source); err != nil {
			return empty, err
		}
		if err := ingest.LayoutMergedMedia(ctx, tx, target); err != nil {
			return empty, err
		}
		if !s.sourceGroupVisible {
			before.Favorites, err = albumfavorites.MoveMergedAlbumFavorites(ctx, tx, s.source.GroupID, s.target.GroupID)
			if err != nil {
				return empty, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution='merged',resolved_at=clock_timestamp(),updated_at=clock_timestamp()
			WHERE kind='release_merge' AND status<>'resolved' AND (target_id IN ($1,$2) OR EXISTS (
				SELECT 1 FROM jsonb_array_elements(candidates) c WHERE c->>'release_id' IN ($1::uuid::text,$2::uuid::text)))`, target, source); err != nil {
			return empty, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision=edit_revision+1 WHERE id=$1`, source); err != nil {
			return empty, err
		}
		if err := refreshMergeReleases(ctx, tx, target, source); err != nil {
			return empty, err
		}
		return operations.Outcome{TargetType: operations.TargetRelease, TargetID: target, Targets: releaseTargets(target, source),
			ExpectedRevision: s.sourceRevision, ResultRevision: s.sourceRevision + 1,
			Before: before, After: after, Response: map[string]any{"operation": map[string]string{"id": opID}, "release": s.target}}, nil
	}
}
