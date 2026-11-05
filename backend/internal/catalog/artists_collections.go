package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
)

// ListArtistAlbums 默认仅列专辑艺人参与；可选参数不改变旧调用及默认响应。
func (s *Store) ListArtistAlbums(ctx context.Context, id string, page, size int, options ...ArtistAlbumOptions) (ArtistPage[ArtistAlbum], error) {
	var option ArtistAlbumOptions
	if len(options) > 1 {
		return ArtistPage[ArtistAlbum]{}, fmt.Errorf("%w: multiple artist album options", ErrQuery)
	}
	if len(options) == 1 {
		option = options[0]
	}
	if option.Participation != "" && option.Participation != "any" {
		return ArtistPage[ArtistAlbum]{}, fmt.Errorf("%w: invalid artist album participation", ErrQuery)
	}
	return artistSnapshot(ctx, s, func(snapshot *Store) (ArtistPage[ArtistAlbum], error) {
		canonical, err := snapshot.canonicalArtist(ctx, id)
		if err != nil {
			return ArtistPage[ArtistAlbum]{}, err
		}
		result := ArtistPage[ArtistAlbum]{ArtistID: canonical, Items: []ArtistAlbum{}, Pagination: ArtistPagination{Page: page, PageSize: size}}
		prefix, rolesSQL := artistAlbumsSQL(option.Participation)
		args := []any{[]string{canonical}, searchindex.EscapeLike(option.Query)}
		if err := snapshot.database.QueryRowContext(ctx, prefix+` SELECT count(*) FROM selected`, args...).Scan(&result.Pagination.Total); err != nil {
			return result, err
		}
		rows, err := snapshot.database.QueryContext(ctx, prefix+`
 SELECT c.group_id::text,c.credited_release_id::text,`+rolesSQL+` FROM selected c
 LEFT JOIN catalog_projection_subject_state s ON s.subject_kind='release' AND s.subject_id=c.id
 ORDER BY COALESCE(s.search_title,lower(c.title)) COLLATE "C",c.group_id LIMIT $3 OFFSET $4`, append(args, size, (page-1)*size)...)
		if err != nil {
			return result, err
		}
		ids := []string{}
		items := []ArtistAlbum{}
		for rows.Next() {
			var group string
			var item ArtistAlbum
			var roles []byte
			if err := rows.Scan(&group, &item.CreditedReleaseID, &roles); err != nil {
				rows.Close()
				return result, err
			}
			if err := json.Unmarshal(roles, &item.Roles); err != nil {
				rows.Close()
				return result, err
			}
			ids = append(ids, group)
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, err
		}
		summaries, err := snapshot.loadSearchGroups(ctx, ids)
		if err != nil {
			return result, err
		}
		for i, id := range ids {
			group, ok := summaries[id]
			if !ok {
				return result, fmt.Errorf("%w: artist album unavailable", ErrQuery)
			}
			items[i].Group = group
		}
		result.Items = items
		return result, nil
	})
}

func artistAlbumsSQL(participation string) (string, string) {
	prefix := `WITH `
	credits := `SELECT DISTINCT e.release_id FROM artist_effective_credits e WHERE e.artist_id=ANY($1::uuid[]) AND e.role='album_artist'`
	roles := `'null'::jsonb`
	if participation == "any" {
		// 与安全摘要同一参与口径：发行级展开全部轨，轨级只取该轨；missing 不制造可见参与。
		prefix += artistParticipationCTE + `, album_participation AS MATERIALIZED (
 SELECT DISTINCT group_id,release_id,role FROM artist_participation WHERE present), `
		credits = `SELECT DISTINCT release_id FROM album_participation`
		roles = `(SELECT jsonb_agg(role ORDER BY ` + artistRoleOrder + `) FROM (
 SELECT DISTINCT role FROM album_participation WHERE group_id=c.group_id) roles)`
	}
	prefix += `credited AS MATERIALIZED (
 SELECT DISTINCT ON (releases.group_id) releases.group_id,releases.id AS credited_release_id
 FROM ` + releasesql.ReleaseSource + ` WHERE releases.id IN (` + credits + `) AND ` + releasesql.ReleaseVisiblePredicate + `
 ORDER BY releases.group_id,` + releasesql.ReleaseGroupMemberOrder + `), representatives AS (
 SELECT DISTINCT ON (releases.group_id) releases.group_id,releases.id,releases.title
 FROM ` + releasesql.ReleaseSource + ` WHERE releases.group_id IN (SELECT group_id FROM credited) AND ` + releasesql.ReleaseVisiblePredicate + `
 ORDER BY releases.group_id,` + releasesql.ReleaseGroupMemberOrder + `), selected AS (
 SELECT c.group_id,c.credited_release_id,r.id,r.title FROM credited c JOIN representatives r ON r.group_id=c.group_id
 WHERE $2='' OR EXISTS (SELECT 1 FROM ` + releasesql.ReleaseSource + `
 WHERE releases.group_id=c.group_id AND ` + releasesql.ReleaseVisiblePredicate + ` AND releases.title ILIKE '%'||$2||'%' ESCAPE '~'))`
	return prefix, roles
}

const artistTrackOrder = `COALESCE(s.search_title,lower(r.title)) COLLATE "C",r.id,m.position,t.position,t.id`
const artistTrackOrderingJoins = ` JOIN tracks t ON t.id=c.track_id JOIN media m ON m.id=t.medium_id JOIN release_effective r ON r.id=m.release_id
 LEFT JOIN catalog_projection_subject_state s ON s.subject_kind='release' AND s.subject_id=r.id `

func (s *Store) ListArtistTracks(ctx context.Context, id, role string, page, size int) (ArtistPage[TrackSummary], error) {
	return artistSnapshot(ctx, s, func(snapshot *Store) (ArtistPage[TrackSummary], error) {
		canonical, err := snapshot.canonicalArtist(ctx, id)
		if err != nil {
			return ArtistPage[TrackSummary]{}, err
		}
		result := ArtistPage[TrackSummary]{ArtistID: canonical, Items: []TrackSummary{}, Pagination: ArtistPagination{Page: page, PageSize: size}}
		prefix := `WITH candidates AS MATERIALIZED (SELECT DISTINCT track_id FROM artist_effective_credits WHERE artist_id=$1::uuid AND role=$2 AND track_id IS NOT NULL)`
		if err := snapshot.database.QueryRowContext(ctx, prefix+` SELECT count(*) FROM candidates`, canonical, role).Scan(&result.Pagination.Total); err != nil {
			return result, err
		}
		ids, err := snapshot.artistIDs(ctx, prefix+` SELECT c.track_id::text FROM candidates c `+artistTrackOrderingJoins+` ORDER BY `+artistTrackOrder+` LIMIT $3 OFFSET $4`, canonical, role, size, (page-1)*size)
		if err != nil {
			return result, err
		}
		result.Items, err = snapshot.completeArtistTracks(ctx, ids, false)
		return result, err
	})
}

const artistQueueCandidates = `WITH candidates AS MATERIALIZED (
 SELECT e.track_id FROM artist_effective_credits e WHERE e.artist_id=$1::uuid AND e.track_id IS NOT NULL
 UNION SELECT t.id FROM artist_effective_credits e JOIN media m ON m.release_id=e.release_id JOIN tracks t ON t.medium_id=m.id WHERE e.artist_id=$1::uuid AND e.release_id IS NOT NULL
)`

func artistQueueSQL(order string) string {
	if order == "random" {
		return artistQueueCandidates + ` SELECT c.track_id::text FROM candidates c JOIN tracks t ON t.id=c.track_id
 WHERE t.source_status='present' ORDER BY md5($2::text||':'||t.id::text),t.id LIMIT 10000`
	}
	return artistQueueCandidates + ` SELECT c.track_id::text FROM candidates c ` + artistTrackOrderingJoins + ` WHERE t.source_status='present' ORDER BY ` + artistTrackOrder + ` LIMIT 10000`
}

func (s *Store) LoadArtistQueue(ctx context.Context, id, order string, seed *string) (ArtistQueue, error) {
	return artistSnapshot(ctx, s, func(snapshot *Store) (ArtistQueue, error) {
		canonical, err := snapshot.canonicalArtist(ctx, id)
		if err != nil {
			return ArtistQueue{}, err
		}
		result := ArtistQueue{ArtistID: canonical, Order: order, Seed: seed, Items: []TrackSummary{}, Limit: 10000}
		if err := snapshot.database.QueryRowContext(ctx, artistQueueCandidates+` SELECT count(*) FILTER(WHERE t.source_status='present'),count(*) FILTER(WHERE t.source_status<>'present') FROM candidates c JOIN tracks t ON t.id=c.track_id`, canonical).Scan(&result.Total, &result.MissingTotal); err != nil {
			return result, err
		}
		args := []any{canonical}
		if order == "random" {
			if seed == nil {
				return result, fmt.Errorf("missing queue seed")
			}
			args = append(args, *seed)
		}
		ids, err := snapshot.artistIDs(ctx, artistQueueSQL(order), args...)
		if err != nil {
			return result, err
		}
		result.Items, err = snapshot.completeArtistTracks(ctx, ids, true)
		if err != nil {
			return result, err
		}
		result.Returned = len(result.Items)
		result.Truncated = result.Total > int64(result.Returned)
		return result, nil
	})
}

// 每批先关闭 rows 再补下一批；整个队列沿调用方同一个 RR 快照，任何缺项整体失败。
func (s *Store) completeArtistTracks(ctx context.Context, ids []string, presentOnly bool) ([]TrackSummary, error) {
	result := make([]TrackSummary, 0, len(ids))
	for start := 0; start < len(ids); start += 1000 {
		batch := ids[start:min(start+1000, len(ids))]
		summaries, err := s.LoadTrackSummaries(ctx, batch)
		if err != nil {
			return nil, err
		}
		for _, id := range batch {
			item, ok := summaries[id]
			if !ok || (presentOnly && !item.Present) {
				return nil, fmt.Errorf("%w: artist track summary unavailable", ErrQuery)
			}
			result = append(result, item)
		}
	}
	return result, nil
}
