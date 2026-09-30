package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

var (
	// errUnplannedObservation 表示候选里的观察没有规划记录：规划尚未完成，或该观察所在的 scope 已被暂缓。
	// 不猜测身份，整个候选回滚。
	errUnplannedObservation = errors.New("staged observation has no identity plan")
	// errPlannedTrackMissing 表示规划指定沿用的曲目已经不存在。
	errPlannedTrackMissing = errors.New("planned track no longer exists")
)

// trackPlan 是暂存行上一条观察的曲目规划：沿用哪条曲目（空串表示新建），以及要保存的指纹与文件状态。
type trackPlan struct {
	TrackID     string
	Fingerprint sql.NullString
	Size        sql.NullInt64
	ModifiedAt  sql.NullTime
}

// loadTrackPlans 读取候选中全部观察的规划，键为规范化相对路径。
func loadTrackPlans(ctx context.Context, tx *sql.Tx, scanID, rootID string, candidate OrganizedCandidate) (map[string]trackPlan, error) {
	paths := make([]string, 0, len(candidate.Mediums))
	for _, tracks := range candidate.Mediums {
		for _, track := range tracks {
			paths = append(paths, NormalizedPath(track.Observation.RelativePath))
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT relative_path, COALESCE(planned_track_id::text,''), content_fingerprint, source_size, source_modified_at
		FROM scan_observations
		WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND relative_path=ANY($3::text[]) AND planned`, scanID, rootID, paths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plans := make(map[string]trackPlan, len(paths))
	for rows.Next() {
		var path string
		var plan trackPlan
		if err := rows.Scan(&path, &plan.TrackID, &plan.Fingerprint, &plan.Size, &plan.ModifiedAt); err != nil {
			return nil, err
		}
		plans[path] = plan
	}
	return plans, rows.Err()
}

// persistTrack 按规划写入一条曲目并返回它的 ID：沿用规划指定的曲目（更新路径、来源身份与全部事实），或新建。
// 写入前先腾出被别的曲目持有的来源身份，因此换内容与互换文件名都与处理顺序无关。
func persistTrack(ctx context.Context, tx *sql.Tx, scanID, rootID, mediumID string, disc int, placed OrganizedTrack, observation SourceObservation, plan trackPlan) (string, error) {
	identity := SourceIdentityForObservation(rootID, observation)
	if err := vacateSourceIdentity(ctx, tx, scanID, rootID, identity, plan.TrackID); err != nil {
		return "", err
	}
	trackArtist := metadata.CanonicalTrackArtist(observation.Artist)
	if plan.TrackID == "" {
		trackID := uuid.New()
		_, err := tx.ExecContext(ctx, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity,duration_seconds,codec,sample_rate,channels,bitrate,bit_depth,cue_sheet_path,cue_parent_relative_path,cue_referenced_file,cue_index_frames,cue_end_frames,cue_isrc,content_fingerprint,source_size,source_modified_at)
			VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7::uuid,$8,'present',NOW(),$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)`,
			trackID, mediumID, placed.Position, observation.Title, trackArtist, disc, rootID, observation.RelativePath, observation.SourceKind, identity, observation.DurationSeconds, observation.Codec, observation.SampleRate, observation.Channels, observation.Bitrate, nullablePositiveInt(observation.BitDepth), nullableString(observation.CueSheetPath), nullableString(observation.CueParentRelativePath), nullableString(observation.CueReferencedFile), nullableCueFrame(observation.CueIndexFrames, observation.CueIndexPresent), nullableCueFrame(observation.CueEndFrames, observation.CueEndPresent), nullableString(observation.CueISRC), plan.Fingerprint, plan.Size, plan.ModifiedAt)
		return trackID, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE tracks SET medium_id=$1::uuid,position=$2,title=$3,artist=$4,disc_number=$5,relative_path=$6,source_status='present',observed_at=NOW(),source_kind=$7,source_identity=$8,duration_seconds=$9,codec=$10,sample_rate=$11,channels=$12,bitrate=$13,bit_depth=$14,cue_sheet_path=$15,cue_parent_relative_path=$16,cue_referenced_file=$17,cue_index_frames=$18,cue_end_frames=$19,cue_isrc=$20,content_fingerprint=$21,source_size=$22,source_modified_at=$23 WHERE id=$24::uuid`,
		mediumID, placed.Position, observation.Title, trackArtist, disc, observation.RelativePath, observation.SourceKind, identity, observation.DurationSeconds, observation.Codec, observation.SampleRate, observation.Channels, observation.Bitrate, nullablePositiveInt(observation.BitDepth), nullableString(observation.CueSheetPath), nullableString(observation.CueParentRelativePath), nullableString(observation.CueReferencedFile), nullableCueFrame(observation.CueIndexFrames, observation.CueIndexPresent), nullableCueFrame(observation.CueEndFrames, observation.CueEndPresent), nullableString(observation.CueISRC), plan.Fingerprint, plan.Size, plan.ModifiedAt, plan.TrackID)
	if err != nil {
		return "", err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return "", err
	} else if changed != 1 {
		return "", errPlannedTrackMissing
	}
	return plan.TrackID, nil
}

// vacateSourceIdentity 腾出被另一条曲目持有的来源身份：内容被替换的原曲目，或互换文件名时尚未改名的一方。
// 被腾出的曲目失去来源身份；它若没有在本轮计划中被别的观察沿用，内容已不在该路径，同时置为 missing。
// 计划由后续候选接手的曲目保持 present，扫描中途取消时，下一次完整遍历仍能把它放进消失集合重新配对。
func vacateSourceIdentity(ctx context.Context, tx *sql.Tx, scanID, rootID, identity, plannedTrackID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE tracks SET source_identity=NULL,
			source_status=CASE WHEN EXISTS (SELECT 1 FROM scan_observations planned
				WHERE planned.scan_run_id=$3::uuid AND planned.root_id=$4::uuid AND planned.planned_track_id=tracks.id)
				THEN source_status ELSE 'missing' END
		WHERE source_identity=$1 AND id IS DISTINCT FROM $2::uuid`, identity, nullableString(plannedTrackID), scanID, rootID)
	return err
}
