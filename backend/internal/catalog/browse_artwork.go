package catalog

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// AlbumArtwork 只读代表版本的封面键，供图片列表请求和条件请求使用。
func (s *Store) AlbumArtwork(ctx context.Context, id string) (string, error) {
	var key *string
	err := s.database.QueryRowContext(ctx, `SELECT COALESCE(release_artwork.storage_key,release_external_artwork.storage_key)
 FROM `+releasesql.ReleaseSource+releasesql.ReleaseSummaryArtworkJoin+`
 WHERE releases.group_id=$1::uuid AND `+releasesql.ReleaseVisiblePredicate+
		" ORDER BY "+releasesql.ReleaseGroupMemberOrder+" LIMIT 1", id).Scan(&key)
	if err != nil {
		return "", err
	}
	if key == nil {
		return "", nil
	}
	return *key, nil
}

// ArtistArtwork 优先返回艺人图，否则只找一张有图的可见专辑，不装载整份唱片目录。
func (s *Store) ArtistArtwork(ctx context.Context, id string) (string, error) {
	return artistSnapshot(ctx, s, func(snapshot *Store) (string, error) {
		canonical, err := snapshot.canonicalArtist(ctx, id)
		if err != nil {
			return "", err
		}
		summaries, err := snapshot.loadArtistSummaries(ctx, []string{canonical})
		if err != nil {
			return "", err
		}
		a := summaries[canonical]
		if !a.Available {
			return "", ErrArtistNotFound
		}
		if a.Image != nil {
			return *a.Image, nil
		}
		var key string
		err = snapshot.database.QueryRowContext(ctx, "WITH "+artistParticipationCTE+", "+browseReleases+`
 SELECT COALESCE(release_artwork.storage_key,release_external_artwork.storage_key)
 FROM representatives releases `+releasesql.ReleaseSummaryArtworkJoin+`
 WHERE releases.group_id IN (SELECT group_id FROM artist_participation WHERE present)
 AND COALESCE(release_artwork.storage_key,release_external_artwork.storage_key) IS NOT NULL
 ORDER BY lower(releases.title) COLLATE "C",releases.group_id LIMIT 1`, []string{canonical}).Scan(&key)
		return key, err
	})
}
