package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
)

// AcquireArtistWriteGate 只获取共用事务 gate；不创建、提交事务或读取本人数据。
func AcquireArtistWriteGate(ctx context.Context, tx *sql.Tx) error { return writegate.Lock(ctx, tx) }

func (s *Store) requireArtistsReady(ctx context.Context) error {
	ready, err := artistgraph.Ready(ctx, s.database)
	if err != nil {
		return fmt.Errorf("%w: artist readiness: %w", ErrQuery, err)
	}
	if !ready {
		return ErrArtistCatalogPreparing
	}
	return nil
}

// ResolveArtists 保留原锚点，最多解析 1,000 个不同有效 UUID；未知身份无键。
// 即使输入为空也检查发布状态，不能把整理中的收藏伪装为空。调用者可传入既有事务。
func (s *Store) ResolveArtists(ctx context.Context, input []string) (map[string]string, error) {
	if err := s.requireArtistsReady(ctx); err != nil {
		return nil, err
	}
	return s.resolveArtists(ctx, input)
}

func (s *Store) resolveArtists(ctx context.Context, input []string) (map[string]string, error) {
	ids, err := personalReadIDs(input, 1000)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.database.QueryContext(ctx, `WITH RECURSIVE routes AS (
 SELECT a.id AS anchor,a.id,ARRAY[a.id] AS visited,false AS cycle FROM artists a WHERE a.id=ANY($1::uuid[])
 UNION ALL
 SELECT r.anchor,o.redirect_artist_id,r.visited||o.redirect_artist_id,o.redirect_artist_id=ANY(r.visited)
 FROM routes r JOIN artist_overrides o ON o.artist_id=r.id
 WHERE o.redirect_artist_id IS NOT NULL AND NOT r.cycle
 ) SELECT r.anchor::text,r.id::text,r.cycle FROM routes r
 LEFT JOIN artist_overrides o ON o.artist_id=r.id WHERE o.redirect_artist_id IS NULL OR r.cycle`, ids)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve artists: %w", ErrQuery, err)
	}
	defer rows.Close()
	for rows.Next() {
		var anchor, canonical string
		var cycle bool
		if err := rows.Scan(&anchor, &canonical, &cycle); err != nil {
			return nil, err
		}
		if cycle {
			return nil, fmt.Errorf("%w: cyclic artist redirect", ErrQuery)
		}
		result[anchor] = canonical
	}
	return result, rows.Err()
}

// artistParticipationCTE 只展开请求艺人的署名；同轨跨名字/角色分别按查询用途去重。
const artistParticipationCTE = `artist_participation AS MATERIALIZED (
 SELECT DISTINCT e.artist_id,e.role,t.id AS track_id,m.release_id,r.group_id,t.source_status='present' AS present
 FROM artist_effective_credits e JOIN tracks t ON t.id=e.track_id
 JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
 WHERE e.artist_id=ANY($1::uuid[])
 UNION
 SELECT DISTINCT e.artist_id,e.role,t.id,m.release_id,r.group_id,t.source_status='present'
 FROM artist_effective_credits e JOIN media m ON m.release_id=e.release_id
 JOIN tracks t ON t.medium_id=m.id JOIN releases r ON r.id=m.release_id
 WHERE e.artist_id=ANY($1::uuid[])
)`

const artistRoleOrder = `CASE role WHEN 'release_artist' THEN 0 WHEN 'album_artist' THEN 1 WHEN 'track_artist' THEN 2 WHEN 'composer' THEN 3 WHEN 'conductor' THEN 4 WHEN 'performer' THEN 5 WHEN 'producer' THEN 6 ELSE 7 END`

// LoadArtistSummaries 返回按 canonical ID 索引的摘要，包含不可用身份；不开放私有表。
func (s *Store) LoadArtistSummaries(ctx context.Context, input []string) (map[string]ArtistSummary, error) {
	resolved, err := s.ResolveArtists(ctx, input)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(resolved))
	seen := map[string]bool{}
	for _, id := range resolved {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return s.loadArtistSummaries(ctx, ids)
}

func (s *Store) loadArtistSummaries(ctx context.Context, ids []string) (map[string]ArtistSummary, error) {
	result := make(map[string]ArtistSummary, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.database.QueryContext(ctx, artistSummarySQL, ids)
	if err != nil {
		return nil, fmt.Errorf("%w: artist summaries: %w", ErrQuery, err)
	}
	defer rows.Close()
	for rows.Next() {
		var item ArtistSummary
		var roles []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.InitialGroup, &item.AlbumCount, &item.TrackCount, &roles); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(roles, &item.Roles); err != nil {
			return nil, err
		}
		if item.Name == "" || len(item.Name) > 4096 || !validArtistInitial(item.InitialGroup) {
			return nil, fmt.Errorf("%w: invalid artist profile", ErrQuery)
		}
		if item.Roles == nil {
			item.Roles = []string{}
		}
		item.Available = item.TrackCount > 0
		result[item.ID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: artist summary rows: %w", ErrQuery, err)
	}
	if len(result) != len(ids) {
		return nil, fmt.Errorf("%w: missing artist profile", ErrQuery)
	}
	return result, nil
}

func validArtistInitial(value string) bool {
	return value == "kana" || value == "#" || (len(value) == 1 && value[0] >= 'A' && value[0] <= 'Z')
}
