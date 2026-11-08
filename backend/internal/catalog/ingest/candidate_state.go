package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CandidateMetadataSignature 覆盖候选的写入输入，包括署名证据、角色细分、作品标签和碟轨顺序。
// 封面字节与延迟加载状态不属于元数据变化；其来源文件状态由 library 的签名覆盖。
// 内容指纹由身份规划校验，来源大小和修改时间由 library 另行签名（这些字段不进入观察 JSON）。
func CandidateMetadataSignature(candidate OrganizedCandidate) (string, error) {
	// JoinGroupID 仅用于新建发行，不应让首次写入与沿用发行时的签名不同。
	candidate.JoinGroupID = ""
	mediums := make(map[int][]OrganizedTrack, len(candidate.Mediums))
	for disc, tracks := range candidate.Mediums {
		mediums[disc] = append([]OrganizedTrack(nil), tracks...)
		for index := range mediums[disc] {
			observation := &mediums[disc][index].Observation
			observation.Artwork = nil
			observation.ArtworkDeferred = false
		}
	}
	candidate.Mediums = mediums
	// JSON 固定结构字段顺序并排序 map 键；保留切片顺序，因为署名证据和曲目位置有序。
	hash := sha256.New()
	if err := json.NewEncoder(hash).Encode(candidate); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CandidateUnchanged 仅在签名相同且全部已规划、未暂缓成员仍沿用原曲目与发行时返回可跳过。
// 空候选或任一成员不满足条件时，整个候选都不可跳过。
// 应有的持久化事实必须完整；无音频负载导致规划后指纹仍为空的来源允许不存指纹。
func CandidateUnchanged(ctx context.Context, database queryer, scanID, rootID, candidateKey, signature string) (trackIDs []string, ok bool, err error) {
	rows, err := database.QueryContext(ctx, `WITH members AS (
		SELECT o.planned_track_id,
			(o.planned_track_id IS NOT NULL AND o.matched_track_id IS NOT NULL
			 AND o.planned_track_id=o.matched_track_id AND NOT o.content_changed
			 AND o.planned_release_id=s.release_id
			 AND t.source_status='present' AND m.release_id=s.release_id
			 AND t.source_size IS NOT NULL AND t.source_modified_at IS NOT NULL
			 AND (o.content_fingerprint IS NULL OR t.content_fingerprint IS NOT NULL)) IS TRUE AS unchanged
		FROM scan_observations o
		JOIN scan_candidate_states s ON s.root_id=o.root_id AND s.candidate_key=o.candidate_key
		LEFT JOIN tracks t ON t.id=o.planned_track_id
		LEFT JOIN media m ON m.id=t.medium_id
		WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND o.candidate_key=$3
			AND o.planned AND NOT o.withheld AND s.signature=$4
	)
	SELECT planned_track_id::text FROM members
	WHERE NOT EXISTS (SELECT 1 FROM members WHERE NOT unchanged)
	ORDER BY planned_track_id`, scanID, rootID, candidateKey, signature)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var trackID string
		if err := rows.Scan(&trackID); err != nil {
			return nil, false, err
		}
		trackIDs = append(trackIDs, trackID)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return trackIDs, len(trackIDs) > 0, nil
}

// TouchTracks 只更新 present 曲目的观察时间，不改来源大小、修改时间或其他事实。
func TouchTracks(ctx context.Context, database Execer, trackIDs []string) error {
	if len(trackIDs) == 0 {
		return nil
	}
	_, err := database.ExecContext(ctx, `UPDATE tracks SET observed_at=NOW()
		WHERE id=ANY($1::uuid[]) AND source_status='present'`, trackIDs)
	return err
}

// RecordCandidateState 记录已成功保存且完成封面处理的候选；调用方负责满足该前置条件。
func RecordCandidateState(ctx context.Context, database Execer, rootID, candidateKey, releaseID, signature string) error {
	_, err := database.ExecContext(ctx, `INSERT INTO scan_candidate_states(root_id,candidate_key,release_id,signature)
		VALUES($1::uuid,$2,$3::uuid,$4)
		ON CONFLICT(root_id,candidate_key) DO UPDATE SET release_id=EXCLUDED.release_id,
			signature=EXCLUDED.signature,updated_at=NOW()`, rootID, candidateKey, releaseID, signature)
	return err
}

// CleanupCandidateStates 删除本轮暂存中未出现的候选状态。
// 调用方必须保证本根遍历完整、未取消，并在清理暂存之前调用。
func CleanupCandidateStates(ctx context.Context, database Execer, scanID, rootID string) error {
	_, err := database.ExecContext(ctx, `DELETE FROM scan_candidate_states s
		WHERE s.root_id=$2::uuid AND NOT EXISTS (
			SELECT 1 FROM scan_observations o
			WHERE o.scan_run_id=$1::uuid AND o.root_id=s.root_id AND o.candidate_key=s.candidate_key
		)`, scanID, rootID)
	return err
}
