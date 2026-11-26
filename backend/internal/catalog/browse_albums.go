package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

type BrowsePage struct{ Limit, Offset int }

type BrowseAlbumParams struct {
	BrowsePage
	Sort             BrowseAlbumSort
	Genre            string
	FromYear, ToYear *int
	Seed             int64
}

type BrowseAlbumSort string

const (
	BrowseAlbumSortTitle  BrowseAlbumSort = "title"
	BrowseAlbumSortNewest BrowseAlbumSort = "added_at_desc"
	BrowseAlbumSortRandom BrowseAlbumSort = "random"
	BrowseAlbumSortYear   BrowseAlbumSort = "year"
	BrowseAlbumSortGenre  BrowseAlbumSort = "genre"
	BrowseAlbumSortArtist BrowseAlbumSort = "artist"
)

type BrowseAlbum struct {
	ID             string
	AddedAt        time.Time
	Representative ReleaseSummary
	Genres         []string
	Duration       float64
	MusicBrainzID  string
}

// 默认版本的筛选先于年份、流派和分页，避免其它版本的标签改变一张专辑的归属。
const browseReleases = `representatives AS (
 SELECT DISTINCT ON (releases.group_id) releases.id,releases.group_id,releases.title,
 COALESCE(NULLIF(releases.album_artist,''),releases.artist) AS artist,releases.genres,NULLIF(releases.year,0) AS year,
 min(releases.created_at) OVER (PARTITION BY releases.group_id) AS added_at
 FROM ` + releasesql.ReleaseSource + ` WHERE ` + releasesql.ReleaseVisiblePredicate + `
 ORDER BY releases.group_id,` + releasesql.ReleaseGroupMemberOrder + `)`

func browseRead[T any](ctx context.Context, s *Store, read func(*Store) (T, error)) (T, error) {
	if db, ok := s.database.(ReadTransactionBeginner); ok {
		return readWithLocalJITOff(ctx, db, func(tx *sql.Tx) (T, error) { return read(NewStore(tx)) })
	}
	return read(s)
}

func (s *Store) ListBrowseAlbums(ctx context.Context, p BrowseAlbumParams) ([]BrowseAlbum, error) {
	return browseRead(ctx, s, func(snapshot *Store) ([]BrowseAlbum, error) {
		ids, err := snapshot.browseAlbumIDs(ctx, p)
		if err != nil {
			return nil, err
		}
		return snapshot.browseAlbums(ctx, ids)
	})
}

func (s *Store) browseAlbumIDs(ctx context.Context, p BrowseAlbumParams) ([]string, error) {
	order := `catalog_list_sort_key(r.title) COLLATE "C",r.group_id`
	filter, joins := "", ""
	args := []any{p.Limit, p.Offset}
	switch p.Sort {
	case "", BrowseAlbumSortTitle:
	case BrowseAlbumSortNewest:
		order = "r.added_at DESC,r.group_id DESC"
	case BrowseAlbumSortRandom:
		order = "hashtextextended(r.group_id::text,$3),r.group_id"
		args = append(args, p.Seed)
	case BrowseAlbumSortGenre:
		filter = " WHERE $3=ANY(r.genres)"
		args = append(args, p.Genre)
	case BrowseAlbumSortYear:
		if p.FromYear == nil || p.ToYear == nil {
			return nil, fmt.Errorf("missing album year range")
		}
		from, to := *p.FromYear, *p.ToYear
		if from > to {
			order = "r.year DESC," + order
		} else {
			order = "r.year," + order
		}
		filter = " WHERE r.year BETWEEN $3 AND $4"
		args = append(args, min(from, to), max(from, to))
	case BrowseAlbumSortArtist:
		joins = ` LEFT JOIN LATERAL (SELECT p.list_sort_key FROM artist_effective_credits c
 JOIN artist_profiles p ON p.artist_id=c.artist_id WHERE c.release_id=r.id AND c.role IN ('album_artist','release_artist')
 AND ` + releasesql.ArtistRefsReady + ` ORDER BY (c.role='album_artist') DESC,c.position,c.artist_id LIMIT 1) a ON true`
		order = `COALESCE(a.list_sort_key,catalog_list_sort_key(r.artist)) COLLATE "C",` + order
	default:
		return nil, fmt.Errorf("unsupported album sort")
	}
	return s.artistIDs(ctx, "WITH "+browseReleases+" SELECT r.group_id::text FROM representatives r"+joins+filter+" ORDER BY "+order+" LIMIT $1 OFFSET $2", args...)
}

// LoadBrowseAlbums 依输入顺序批量返回可见专辑；同一快照内补齐时长、流派和外部标识。
func (s *Store) LoadBrowseAlbums(ctx context.Context, ids []string) ([]BrowseAlbum, error) {
	return browseRead(ctx, s, func(snapshot *Store) ([]BrowseAlbum, error) { return snapshot.browseAlbums(ctx, ids) })
}

func (s *Store) browseAlbums(ctx context.Context, ids []string) ([]BrowseAlbum, error) {
	result := make([]BrowseAlbum, 0, len(ids))
	for len(ids) > 0 {
		n := min(len(ids), maxPersonalGroupIDs)
		items, err := s.browseAlbumBatch(ctx, ids[:n])
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		ids = ids[n:]
	}
	return result, nil
}

func (s *Store) browseAlbumBatch(ctx context.Context, ids []string) ([]BrowseAlbum, error) {
	groups, err := s.LoadReleaseGroupSummaries(ctx, ids)
	if err != nil {
		return nil, err
	}
	releases := make([]string, 0, len(groups))
	for _, g := range groups {
		if g.Available {
			releases = append(releases, g.Representative.ID)
		}
	}
	rows, err := s.database.QueryContext(ctx, `SELECT r.group_id::text,to_json(r.genres),
 (SELECT min(releases.created_at) FROM releases WHERE releases.group_id=r.group_id AND `+releasesql.ReleaseVisiblePredicate+`),
 COALESCE((SELECT sum(d.duration_seconds) FROM media m JOIN tracks t ON t.medium_id=m.id
 JOIN track_display_durations d ON d.track_id=t.id WHERE m.release_id=r.id AND t.source_status='present'),0),
 COALESCE(release_link.external_id,group_link.external_id,'')
 FROM release_effective r LEFT JOIN external_links release_link ON release_link.source='musicbrainz'
 AND release_link.object_type='release' AND release_link.object_id=r.id
 LEFT JOIN external_links group_link ON group_link.source='musicbrainz' AND group_link.object_type='release_group' AND group_link.object_id=r.group_id
 WHERE r.id=ANY($1::uuid[])`, releases)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	albums := make(map[string]BrowseAlbum, len(groups))
	for rows.Next() {
		var a BrowseAlbum
		var genres []byte
		if err := rows.Scan(&a.ID, &genres, &a.AddedAt, &a.Duration, &a.MusicBrainzID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(genres, &a.Genres); err != nil {
			return nil, err
		}
		a.Representative = *groups[a.ID].Representative
		albums[a.ID] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]BrowseAlbum, 0, len(ids))
	for _, id := range ids {
		if a, ok := albums[strings.ToLower(id)]; ok {
			result = append(result, a)
		}
	}
	return result, nil
}

type GenreCount struct {
	Name                  string
	AlbumCount, SongCount int64
}

func (s *Store) ListGenres(ctx context.Context) ([]GenreCount, error) {
	return browseRead(ctx, s, func(snapshot *Store) ([]GenreCount, error) {
		rows, err := snapshot.database.QueryContext(ctx, "WITH "+browseReleases+`
 SELECT g.name,count(DISTINCT r.group_id),count(t.id)
 FROM representatives r CROSS JOIN LATERAL (SELECT DISTINCT unnest(r.genres) AS name) g
 JOIN media m ON m.release_id=r.id JOIN track_effective t ON t.medium_id=m.id AND t.source_status='present'
 GROUP BY g.name ORDER BY lower(g.name) COLLATE "C",g.name COLLATE "C"`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := []GenreCount{}
		for rows.Next() {
			var g GenreCount
			if err := rows.Scan(&g.Name, &g.AlbumCount, &g.SongCount); err != nil {
				return nil, err
			}
			result = append(result, g)
		}
		return result, rows.Err()
	})
}
