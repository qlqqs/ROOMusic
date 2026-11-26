package catalog

import "context"

// BrowseArtist 在目录摘要上附加用于客户端排序和外链的字段。
type BrowseArtist struct {
	ArtistSummary
	SortName, MusicBrainzID string
}

func (s *Store) BrowseArtists(ctx context.Context) ([]BrowseArtist, error) {
	return artistSnapshot(ctx, s, func(snapshot *Store) ([]BrowseArtist, error) {
		return snapshot.browseArtistPage(ctx, "album_artist", nil)
	})
}

func (s *Store) browseArtistPage(ctx context.Context, role string, page *BrowsePage) ([]BrowseArtist, error) {
	query := selectedArtistsCTE() + ` SELECT id::text FROM selected ORDER BY CASE initial_group WHEN 'kana' THEN 'ZZ' WHEN '#' THEN 'ZZZ' ELSE initial_group END COLLATE "C",sort_key COLLATE "C",id`
	args := []any{"", "", role}
	if page != nil {
		query += " LIMIT $4 OFFSET $5"
		args = append(args, page.Limit, page.Offset)
	}
	ids, err := s.artistIDs(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	summaries, err := s.loadArtistSummaries(ctx, ids)
	if err != nil {
		return nil, err
	}
	ordered := make([]ArtistSummary, 0, len(ids))
	for _, id := range ids {
		ordered = append(ordered, summaries[id])
	}
	return s.browseArtistFields(ctx, ordered)
}

func (s *Store) browseArtistFields(ctx context.Context, items []ArtistSummary) ([]BrowseArtist, error) {
	result := make([]BrowseArtist, len(items))
	if len(items) == 0 {
		return result, nil
	}
	ids := make([]string, len(items))
	index := make(map[string]int, len(items))
	for i, item := range items {
		result[i].ArtistSummary = item
		ids[i] = item.ID
		index[item.ID] = i
	}
	rows, err := s.database.QueryContext(ctx, `WITH `+ArtistMediaFamilyCTE+`
 SELECT p.artist_id::text,p.sort_key,COALESCE((SELECT l.external_id FROM artist_media_family f JOIN external_links l
 ON l.object_id=f.artist_id AND l.object_type='artist' AND l.source='musicbrainz'
 WHERE f.canonical_id=p.artist_id ORDER BY `+ArtistMediaOrder+` LIMIT 1),'')
 FROM artist_profiles p WHERE p.artist_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, mbid string
		if err := rows.Scan(&id, &name, &mbid); err != nil {
			return nil, err
		}
		a := &result[index[id]]
		a.SortName = name
		a.MusicBrainzID = mbid
	}
	return result, rows.Err()
}

type BrowseArtistDetail struct {
	Artist BrowseArtist
	Albums []BrowseAlbum
}

// BrowseArtistAlbums 把艺人参与的可见版本归组，并按代表版本展示。
func (s *Store) BrowseArtistAlbums(ctx context.Context, id string) (BrowseArtistDetail, error) {
	return artistSnapshot(ctx, s, func(snapshot *Store) (BrowseArtistDetail, error) {
		canonical, err := snapshot.canonicalArtist(ctx, id)
		if err != nil {
			return BrowseArtistDetail{}, err
		}
		summaries, err := snapshot.loadArtistSummaries(ctx, []string{canonical})
		if err != nil {
			return BrowseArtistDetail{}, err
		}
		if !summaries[canonical].Available {
			return BrowseArtistDetail{}, ErrArtistNotFound
		}
		artists, err := snapshot.browseArtistFields(ctx, []ArtistSummary{summaries[canonical]})
		if err != nil {
			return BrowseArtistDetail{}, err
		}
		ids, err := snapshot.artistIDs(ctx, "WITH "+artistParticipationCTE+", "+browseReleases+`
 SELECT r.group_id::text FROM representatives r WHERE r.group_id IN (SELECT group_id FROM artist_participation WHERE present)
 ORDER BY catalog_list_sort_key(r.title) COLLATE "C",r.group_id`, []string{canonical})
		if err != nil {
			return BrowseArtistDetail{}, err
		}
		albums, err := snapshot.browseAlbums(ctx, ids)
		return BrowseArtistDetail{Artist: artists[0], Albums: albums}, err
	})
}
