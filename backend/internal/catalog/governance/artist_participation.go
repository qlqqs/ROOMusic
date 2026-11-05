package governance

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 治理影响范围包括 missing：发行级署名参与该发行全部曲目，直接署名与发行展开按曲目 ID 去重。
// 两个分支都先限制到预算加一，再合并并再次限量，避免将超预算全量身份加载进应用内存。
// $1 为艺人集合，$2/$3 分别为可选的名字键和发行筛选；空集合表示不加该维度筛选。
const artistGovernanceParticipationSQL = `WITH selected_credits AS MATERIALIZED (
 SELECT e.release_id,e.track_id
 FROM artist_effective_credits e JOIN artist_names n ON n.id=e.name_id
 LEFT JOIN tracks t ON t.id=e.track_id LEFT JOIN media m ON m.id=t.medium_id
 WHERE e.artist_id=ANY($1::text[]::uuid[])
 AND (COALESCE(cardinality($2::text[]),0)=0 OR n.key_id=ANY($2::text[]::uuid[]))
 AND (COALESCE(cardinality($3::text[]),0)=0 OR COALESCE(e.release_id,m.release_id)=ANY($3::text[]::uuid[]))
), bounded_participants AS (
 (SELECT DISTINCT track_id FROM selected_credits WHERE track_id IS NOT NULL LIMIT $4)
 UNION
 (SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id
 WHERE m.release_id IN (SELECT release_id FROM selected_credits WHERE release_id IS NOT NULL) LIMIT $4)
 LIMIT $4
)`

func countArtistParticipants(ctx context.Context, q operations.Queryer, artistIDs, keyIDs, releaseIDs []string) (int, error) {
	var count int
	err := q.QueryRowContext(ctx, artistGovernanceParticipationSQL+` SELECT count(*) FROM bounded_participants`, artistIDs, keyIDs, releaseIDs, maxArtistTracks+1).Scan(&count)
	if err != nil {
		return 0, err
	}
	if count > maxArtistTracks {
		return 0, artistScopeTooLarge()
	}
	return count, nil
}

type artistScopeParticipant struct{ TrackID, ReleaseID, Status string }

func loadArtistScopeParticipants(ctx context.Context, q operations.Queryer, artistID string, keyIDs, releaseIDs []string) ([]artistScopeParticipant, error) {
	rows, err := q.QueryContext(ctx, artistGovernanceParticipationSQL+` SELECT t.id::text,m.release_id::text,t.source_status
 FROM bounded_participants p JOIN tracks t ON t.id=p.track_id JOIN media m ON m.id=t.medium_id ORDER BY t.id`, []string{artistID}, keyIDs, releaseIDs, maxArtistTracks+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	participants := []artistScopeParticipant{}
	for rows.Next() {
		var p artistScopeParticipant
		if err := rows.Scan(&p.TrackID, &p.ReleaseID, &p.Status); err != nil {
			return nil, err
		}
		participants = append(participants, p)
		if len(participants) > maxArtistTracks {
			return nil, artistScopeTooLarge()
		}
	}
	return participants, rows.Err()
}
