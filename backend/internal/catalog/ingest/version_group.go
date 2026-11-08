package ingest

import (
	"context"
	"database/sql"
	"errors"
)

// PlannedVersionGroup 返回已有沿用规划中候选键最小者的组；全是新版本时返回空串。
// 仅读取扫描规划，不移动已有发行，也不修改组的默认版本与修订号。
func PlannedVersionGroup(ctx context.Context, database queryer, scanID, rootID string, candidates []OrganizedCandidate) (string, error) {
	keys := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		keys = append(keys, CandidateIdentity(rootID, candidate.Anchor))
	}
	if len(keys) == 0 {
		return "", nil
	}
	var groupID string
	err := database.QueryRowContext(ctx, `SELECT r.group_id::text
		FROM scan_observations o JOIN releases r ON r.id=o.planned_release_id
		WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND o.planned
		  AND o.candidate_key=ANY($3::text[])
		ORDER BY o.candidate_key LIMIT 1`, scanID, rootID, keys).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return groupID, err
}

// ReleaseGroupOf 返回已保存发行的当前组，读取失败交由调用方停止该版本批次。
func ReleaseGroupOf(ctx context.Context, database queryer, releaseID string) (string, error) {
	var groupID string
	err := database.QueryRowContext(ctx, "SELECT group_id::text FROM releases WHERE id=$1::uuid", releaseID).Scan(&groupID)
	return groupID, err
}
