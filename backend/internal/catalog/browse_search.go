package catalog

import (
	"context"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

type BrowseSearchParams struct {
	Query                  string
	Artists, Albums, Songs BrowsePage
}
type BrowseSearchResult struct {
	Artists []BrowseArtist
	Albums  []BrowseAlbum
	Songs   []BrowseTrack
}

// BrowseSearch 的三栏各自按绝对偏移分页，不查询总数；同步空查询也覆盖没有搜索投影的曲库。
func (s *Store) BrowseSearch(ctx context.Context, p BrowseSearchParams) (BrowseSearchResult, error) {
	return searchSnapshot(ctx, s, func(snapshot *Store, state CatalogState, generation int64) (BrowseSearchResult, error) {
		var r BrowseSearchResult
		var err error
		if p.Artists.Limit > 0 && state.ArtistsState == "ready" {
			if p.Query == "" {
				r.Artists, err = snapshot.browseArtistPage(ctx, "", &p.Artists)
			} else {
				var hits []SearchArtistItem
				hits, _, err = snapshot.searchArtistsOffset(ctx, p.Query, 1, p.Artists.Limit, p.Artists.Offset, false)
				if err == nil {
					summaries := make([]ArtistSummary, len(hits))
					for i, h := range hits {
						summaries[i] = h.Artist
					}
					r.Artists, err = snapshot.browseArtistFields(ctx, summaries)
				}
			}
			if err != nil {
				return r, err
			}
		}
		if p.Albums.Limit > 0 {
			var ids []string
			if p.Query == "" {
				ids, err = snapshot.browseAlbumIDs(ctx, BrowseAlbumParams{BrowsePage: p.Albums})
			} else {
				ids, err = snapshot.browseSearchIDs(ctx, p.Query, "albums", p.Albums, state, generation)
			}
			if err != nil {
				return r, err
			}
			r.Albums, err = snapshot.browseAlbums(ctx, ids)
			if err != nil {
				return r, err
			}
		}
		if p.Songs.Limit > 0 {
			var ids []string
			if p.Query == "" {
				ids, err = snapshot.artistIDs(ctx, "WITH "+browseReleases+`
 SELECT t.id::text FROM representatives r JOIN media m ON m.release_id=r.id JOIN track_effective t ON t.medium_id=m.id
 WHERE t.source_status='present' ORDER BY lower(t.title) COLLATE "C",t.id LIMIT $1 OFFSET $2`, p.Songs.Limit, p.Songs.Offset)
			} else {
				ids, err = snapshot.browseSearchIDs(ctx, p.Query, "tracks", p.Songs, state, generation)
			}
			if err != nil {
				return r, err
			}
			r.Songs, err = snapshot.browseTracks(ctx, ids)
		}
		return r, err
	})
}

func (s *Store) browseSearchIDs(ctx context.Context, query, kind string, page BrowsePage, state CatalogState, generation int64) ([]string, error) {
	plan := newSearchPlan(query, kind, state.SearchMode, generation)
	if plan.empty {
		return nil, nil
	}
	plan.artistAliases = state.ArtistsState == "ready"
	plan, err := s.prepareSearchPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	// 同一首曲目可以同时命中名字、别名和署名。先取各来源的最佳等级，再按命中组选择可见的默认版本。
	querySQL := "WITH " + plan.hits + `, subjects AS MATERIALIZED (
 SELECT release_id,track_id,min(match_rank) AS match_rank FROM hits GROUP BY release_id,track_id
 ), matches AS MATERIALIZED (
 SELECT h.release_id,h.track_id,h.match_rank FROM subjects h WHERE h.release_id IS NOT NULL
 UNION ALL
 SELECT m.release_id,t.id,h.match_rank FROM subjects h JOIN tracks t ON t.id=h.track_id
 JOIN media m ON m.id=t.medium_id WHERE t.source_status='present'
 ), matched_groups AS (
 SELECT DISTINCT r.group_id FROM matches h JOIN releases r ON r.id=h.release_id
 ), representatives AS MATERIALIZED (
 SELECT representative.* FROM matched_groups g CROSS JOIN LATERAL (
 SELECT releases.group_id,releases.id,releases.title FROM ` + releasesql.ReleaseSource + `
 WHERE releases.group_id=g.group_id AND ` + searchReleaseVisiblePredicate + `
 ORDER BY ` + releasesql.ReleaseGroupMemberOrder + ` LIMIT 1
 ) representative
 ), representative_hits AS (
 SELECT r.group_id,r.id,r.title,h.track_id,h.match_rank FROM representatives r JOIN matches h ON h.release_id=r.id
 )`
	pageSQL := ` SELECT group_id::text FROM representative_hits
 GROUP BY group_id,title ORDER BY min(match_rank),lower(title) COLLATE "C",group_id`
	if kind == "tracks" {
		pageSQL = `, song_hits AS (
 SELECT track_id,match_rank FROM representative_hits WHERE track_id IS NOT NULL
 UNION ALL
 SELECT t.id,h.match_rank FROM representative_hits h JOIN media m ON m.release_id=h.id
 JOIN tracks t ON t.medium_id=m.id WHERE h.track_id IS NULL AND t.source_status='present'
 ), songs AS (
 SELECT track_id,min(match_rank) AS match_rank FROM song_hits GROUP BY track_id
 ) SELECT s.track_id::text FROM songs s JOIN track_effective t ON t.id=s.track_id
 ORDER BY s.match_rank,lower(t.title) COLLATE "C",s.track_id`
	}
	querySQL += pageSQL + fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(plan.args)+1, len(plan.args)+2)
	return s.artistIDs(ctx, plan.withArtistAliases(querySQL), append(plan.args, page.Limit, page.Offset)...)
}
