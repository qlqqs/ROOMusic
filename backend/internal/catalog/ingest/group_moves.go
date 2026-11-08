package ingest

import "context"

// EmptiedGroupMoves 返回本根扫描腾空的组到接收曲目最多的新发行所在组的映射。
// 本函数只读，必须在本根全部候选写入后调用；组内仍有任何曲目（包括 missing）时不转移。
func EmptiedGroupMoves(ctx context.Context, database queryer, scanID, rootID string) (map[string]string, error) {
	rows, err := database.QueryContext(ctx, `WITH moved AS (
		SELECT o.previous_release_id, m.release_id, COUNT(*) AS track_count
		FROM scan_observations o
		JOIN tracks t ON t.id=o.planned_track_id
		JOIN media m ON m.id=t.medium_id
		WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND NOT o.withheld
		  AND m.release_id<>o.previous_release_id
		GROUP BY o.previous_release_id, m.release_id
	), received AS (
		SELECT old.group_id AS from_group_id, moved.release_id, SUM(moved.track_count) AS track_count
		FROM moved JOIN releases old ON old.id=moved.previous_release_id
		WHERE NOT EXISTS (
			SELECT 1 FROM releases r JOIN media m ON m.release_id=r.id JOIN tracks t ON t.medium_id=m.id
			WHERE r.group_id=old.group_id
		)
		GROUP BY old.group_id, moved.release_id
	)
	SELECT DISTINCT ON (received.from_group_id) received.from_group_id::text, r.group_id::text
	FROM received JOIN releases r ON r.id=received.release_id
	ORDER BY received.from_group_id, received.track_count DESC, r.created_at, r.id`, scanID, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	moves := make(map[string]string)
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, err
		}
		moves[from] = to
	}
	return moves, rows.Err()
}
