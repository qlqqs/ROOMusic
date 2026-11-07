package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
)

type ArtistCandidateReason struct {
	Kind        string  `json:"kind"`
	TrackID     *string `json:"track_id,omitempty"`
	ReleaseID   string  `json:"release_id"`
	Role        *string `json:"role,omitempty"`
	LeftName    string  `json:"left_name"`
	RightName   string  `json:"right_name"`
	BracketSide *string `json:"bracket_side,omitempty"`
	AliasPart   *string `json:"alias_part,omitempty"`
}
type ArtistCandidate struct {
	Left          catalog.ArtistSummary   `json:"left"`
	Right         catalog.ArtistSummary   `json:"right"`
	KeyPairs      []ArtistKeyPair         `json:"key_pairs"`
	Reasons       []ArtistCandidateReason `json:"reasons"`
	EvidenceCount int64                   `json:"evidence_count"`
}

// 同一在库曲目上以同一身份共同署名的两位艺人，不在待处理或已忽略列表里提示。
const artistCandidateCoCreditExclusionSQL = ` AND NOT EXISTS (
 SELECT 1 FROM artist_effective_credits a
 JOIN artist_effective_credits b ON b.track_id=a.track_id AND b.role=a.role
 JOIN tracks t ON t.id=a.track_id AND t.source_status='present'
 WHERE a.artist_id=r.left_artist_id AND b.artist_id=r.right_artist_id)`

const artistCandidatesGroupedSQL = artistCandidateResolvedSQL + `, visible AS MATERIALIZED (
 SELECT r.*,LEAST(left_artist_id,right_artist_id) AS first_artist,GREATEST(left_artist_id,right_artist_id) AS second_artist
 FROM resolved r WHERE left_artist_id<>right_artist_id AND NOT EXISTS (
 SELECT 1 FROM artist_candidate_ignores i WHERE i.left_key_id=r.left_key_id AND i.right_key_id=r.right_key_id)` + artistCandidateCoCreditExclusionSQL + `
), pairs AS (
 SELECT first_artist,second_artist,left_key_id,right_key_id,count(*) AS evidence_count FROM visible
 GROUP BY first_artist,second_artist,left_key_id,right_key_id
), ranked AS (
 SELECT *, (row_number() OVER (PARTITION BY first_artist,second_artist ORDER BY left_key_id,right_key_id)-1)/100 AS chunk FROM pairs
), grouped AS (
 SELECT first_artist,second_artist,chunk,sum(evidence_count)::bigint AS evidence_count,
 jsonb_agg(jsonb_build_object('left_key_id',left_key_id,'right_key_id',right_key_id) ORDER BY left_key_id,right_key_id) AS key_pairs
 FROM ranked GROUP BY first_artist,second_artist,chunk
)`

// 已忽略分支沿用相同的解析与分块规则，忽略时间来自受治理操作的提交记录。
const ignoredArtistCandidatesGroupedSQL = artistCandidateResolvedSQL + `, visible AS MATERIALIZED (
 SELECT r.*,LEAST(left_artist_id,right_artist_id) AS first_artist,GREATEST(left_artist_id,right_artist_id) AS second_artist,
 (SELECT o.created_at FROM artist_candidate_ignores i JOIN operations o ON o.id=i.operation_id
 WHERE i.left_key_id=r.left_key_id AND i.right_key_id=r.right_key_id) AS ignored_at
 FROM resolved r WHERE left_artist_id<>right_artist_id AND EXISTS (
 SELECT 1 FROM artist_candidate_ignores i WHERE i.left_key_id=r.left_key_id AND i.right_key_id=r.right_key_id)` + artistCandidateCoCreditExclusionSQL + `
), pairs AS (
 SELECT first_artist,second_artist,left_key_id,right_key_id,count(*) AS evidence_count,max(ignored_at) AS ignored_at FROM visible
 GROUP BY first_artist,second_artist,left_key_id,right_key_id
), ranked AS (
 SELECT *, (row_number() OVER (PARTITION BY first_artist,second_artist ORDER BY left_key_id,right_key_id)-1)/100 AS chunk FROM pairs
), grouped AS (
 SELECT first_artist,second_artist,chunk,sum(evidence_count)::bigint AS evidence_count,max(ignored_at) AS ignored_at,
 jsonb_agg(jsonb_build_object('left_key_id',left_key_id,'right_key_id',right_key_id) ORDER BY left_key_id,right_key_id) AS key_pairs
 FROM ranked GROUP BY first_artist,second_artist,chunk
)`

// ListArtistCandidates 对同一艺人的超过一百对名字分块分页；忽略不会吞掉未展示的名字对。
func ListArtistCandidates(ctx context.Context, db *sql.DB, page, size int) (catalog.ArtistPage[ArtistCandidate], error) {
	return listArtistCandidates(ctx, db, page, size, false)
}

// ListIgnoredArtistCandidates 按最近忽略时间列出仍然有效的候选名字对。
func ListIgnoredArtistCandidates(ctx context.Context, db *sql.DB, page, size int) (catalog.ArtistPage[ArtistCandidate], error) {
	return listArtistCandidates(ctx, db, page, size, true)
}

func listArtistCandidates(ctx context.Context, db *sql.DB, page, size int, ignored bool) (catalog.ArtistPage[ArtistCandidate], error) {
	result := catalog.ArtistPage[ArtistCandidate]{Items: []ArtistCandidate{}, Pagination: catalog.ArtistPagination{Page: page, PageSize: size}}
	if page < 1 || size < 1 || size > 100 {
		return result, invalidArtistScope()
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	ready, err := artistgraph.Ready(ctx, tx)
	if err != nil {
		return result, err
	}
	if !ready {
		return result, ErrArtistPreviewPreparing
	}
	groupedSQL := artistCandidatesGroupedSQL
	orderSQL := "g.evidence_count DESC,g.first_artist,g.second_artist,g.chunk"
	if ignored {
		groupedSQL = ignoredArtistCandidatesGroupedSQL
		orderSQL = "g.ignored_at DESC," + orderSQL
	}
	if err := tx.QueryRowContext(ctx, groupedSQL+` SELECT count(*) FROM grouped`).Scan(&result.Pagination.Total); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, groupedSQL+` SELECT g.first_artist::text,g.second_artist::text,g.evidence_count,g.key_pairs,
 COALESCE((SELECT jsonb_agg(reason ORDER BY id) FROM (
 SELECT v.id,jsonb_strip_nulls(jsonb_build_object('kind',v.kind,'track_id',v.track_id,'release_id',v.release_id,'role',v.role,
 'left_name',CASE WHEN v.left_artist_id=v.first_artist THEN v.left_name ELSE v.right_name END,
 'right_name',CASE WHEN v.left_artist_id=v.first_artist THEN v.right_name ELSE v.left_name END,
 'bracket_side',CASE WHEN v.kind='bracket_alias' THEN
 CASE WHEN (v.bracket_key_id=v.left_key_id)=(v.left_artist_id=v.first_artist) THEN 'left' ELSE 'right' END END,
 'alias_part',v.alias_part)) AS reason
 FROM visible v JOIN ranked r ON r.first_artist=v.first_artist AND r.second_artist=v.second_artist
 AND r.left_key_id=v.left_key_id AND r.right_key_id=v.right_key_id
 WHERE r.first_artist=g.first_artist AND r.second_artist=g.second_artist AND r.chunk=g.chunk ORDER BY v.id LIMIT 3) samples),'[]'::jsonb)
 FROM grouped g ORDER BY `+orderSQL+` LIMIT $1 OFFSET $2`, size, (page-1)*size)
	if err != nil {
		return result, err
	}
	ids := []string{}
	for rows.Next() {
		var c ArtistCandidate
		var keys, reasons []byte
		if err := rows.Scan(&c.Left.ID, &c.Right.ID, &c.EvidenceCount, &keys, &reasons); err != nil {
			rows.Close()
			return result, err
		}
		if err := json.Unmarshal(keys, &c.KeyPairs); err != nil {
			rows.Close()
			return result, err
		}
		if err := json.Unmarshal(reasons, &c.Reasons); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, c.Left.ID, c.Right.ID)
		result.Items = append(result.Items, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	summaries, err := catalog.NewStore(tx).LoadArtistSummaries(ctx, ids)
	if err != nil {
		return result, err
	}
	for i := range result.Items {
		c := &result.Items[i]
		left, lok := summaries[c.Left.ID]
		right, rok := summaries[c.Right.ID]
		if !lok || !rok {
			return result, fmt.Errorf("candidate missing artist summary")
		}
		c.Left, c.Right = left, right
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}
