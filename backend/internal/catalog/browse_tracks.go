package catalog

import (
	"context"
	"encoding/json"
	"math"
	"strings"
)

type BrowseTrack struct {
	TrackSummary
	Position, Disc                   int
	DiscTitle, Suffix, MusicBrainzID string
	Size                             *int64
	Genres                           []string
	TrackGain, TrackPeak             *float64
}

type RandomTrackParams struct {
	Limit            int
	Seed             int64
	Genre            string
	FromYear, ToYear *int
}

func (s *Store) RandomTracks(ctx context.Context, p RandomTrackParams) ([]BrowseTrack, error) {
	return browseRead(ctx, s, func(snapshot *Store) ([]BrowseTrack, error) {
		ids, err := snapshot.artistIDs(ctx, "WITH "+browseReleases+`
 SELECT t.id::text FROM representatives r JOIN media m ON m.release_id=r.id
 JOIN track_effective t ON t.medium_id=m.id AND t.source_status='present'
 WHERE ($1='' OR $1=ANY(r.genres)) AND ($2::int IS NULL OR r.year >= $2)
 AND ($3::int IS NULL OR r.year <= $3) ORDER BY hashtextextended(t.id::text,$4),t.id LIMIT $5`, p.Genre, p.FromYear, p.ToYear, p.Seed, p.Limit)
		if err != nil {
			return nil, err
		}
		return snapshot.browseTracks(ctx, ids)
	})
}

// LoadBrowseTracks 供曲目、歌单和播放入口复用，按输入顺序保留在场曲目。
func (s *Store) LoadBrowseTracks(ctx context.Context, ids []string) ([]BrowseTrack, error) {
	return browseRead(ctx, s, func(snapshot *Store) ([]BrowseTrack, error) { return snapshot.browseTracks(ctx, ids) })
}

func (s *Store) browseTracks(ctx context.Context, ids []string) ([]BrowseTrack, error) {
	result := make([]BrowseTrack, 0, len(ids))
	for len(ids) > 0 {
		n := min(len(ids), maxSummaryTrackIDs)
		batch, err := s.browseTrackBatch(ctx, ids[:n])
		if err != nil {
			return nil, err
		}
		result = append(result, batch...)
		ids = ids[n:]
	}
	return result, nil
}

func (s *Store) browseTrackBatch(ctx context.Context, ids []string) ([]BrowseTrack, error) {
	summaries, err := s.LoadTrackSummaries(ctx, ids)
	if err != nil {
		return nil, err
	}
	knownIDs := make([]string, 0, len(summaries))
	for id := range summaries {
		knownIDs = append(knownIDs, id)
	}
	rows, err := s.database.QueryContext(ctx, `SELECT t.id::text,t.position,m.position,CASE WHEN m.title='Medium' THEN '' ELSE m.title END,
 lower(COALESCE(substring(COALESCE(NULLIF(t.cue_parent_relative_path,''),t.relative_path) from '\.([[:alnum:]]+)$'),'')),
 t.source_size,to_json(r.genres),COALESCE(l.external_id,''),a.integrated_lufs,a.true_peak_dbtp
 FROM tracks t JOIN media m ON m.id=t.medium_id JOIN release_effective r ON r.id=m.release_id
 LEFT JOIN external_links l ON l.source='musicbrainz' AND l.object_type='track' AND l.object_id=t.id
 LEFT JOIN track_analyses a ON a.track_id=t.id AND a.status='done'
 AND (a.content_fingerprint,a.source_size,a.source_modified_at,a.cue_index_frames,a.cue_end_frames)
 IS NOT DISTINCT FROM (t.content_fingerprint,t.source_size,t.source_modified_at,t.cue_index_frames,t.cue_end_frames)
 WHERE t.id=ANY($1::uuid[]) AND t.source_status='present'`, knownIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tracks := make(map[string]BrowseTrack, len(ids))
	for rows.Next() {
		var id string
		var t BrowseTrack
		var genres []byte
		var lufs, peak *float64
		if err := rows.Scan(&id, &t.Position, &t.Disc, &t.DiscTitle, &t.Suffix, &t.Size, &genres, &t.MusicBrainzID, &lufs, &peak); err != nil {
			return nil, err
		}
		t.TrackSummary = summaries[id]
		if err := json.Unmarshal(genres, &t.Genres); err != nil {
			return nil, err
		}
		if lufs != nil && peak != nil {
			t.TrackGain = trackGainDB(*lufs, *peak)
			linear := math.Pow(10, *peak/20)
			t.TrackPeak = &linear
		}
		tracks[id] = t
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]BrowseTrack, 0, len(ids))
	for _, id := range ids {
		if t, ok := tracks[strings.ToLower(id)]; ok {
			result = append(result, t)
		}
	}
	return result, nil
}

func (s *Store) BrowseAlbumTracks(ctx context.Context, groupID string) (BrowseAlbum, []BrowseTrack, error) {
	type detail struct {
		album  BrowseAlbum
		tracks []BrowseTrack
	}
	d, err := browseRead(ctx, s, func(snapshot *Store) (detail, error) {
		albums, err := snapshot.browseAlbums(ctx, []string{groupID})
		if err != nil {
			return detail{}, err
		}
		if len(albums) == 0 {
			return detail{}, ErrReleaseGroupNotFound
		}
		ids, err := snapshot.artistIDs(ctx, `SELECT t.id::text FROM media m JOIN tracks t ON t.medium_id=m.id
 WHERE m.release_id=$1::uuid AND t.source_status='present' ORDER BY m.position,m.id,t.position,t.id`, albums[0].Representative.ID)
		if err != nil {
			return detail{}, err
		}
		tracks, err := snapshot.browseTracks(ctx, ids)
		return detail{albums[0], tracks}, err
	})
	return d.album, d.tracks, err
}
