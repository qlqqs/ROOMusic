package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const maxSummaryTrackIDs = 1000

// TrackAudioFacts 是上次扫描留下的安全音频事实，不包含路径或文件身份。
type TrackAudioFacts struct {
	SourceKind string  `json:"source_kind"`
	Codec      *string `json:"codec"`
	BitDepth   *int    `json:"bit_depth"`
	SampleRate *int    `json:"sample_rate"`
	Channels   *int    `json:"channels"`
	Bitrate    *int    `json:"bitrate"`
}

// TrackSummary 保留缺失曲目的元数据，发行与组始终是读取时的当前位置。
type TrackSummary struct {
	TrackID         string          `json:"track_id"`
	Title           string          `json:"title"`
	Artist          string          `json:"artist"`
	ArtistRefs      []ArtistRef     `json:"artist_refs"`
	GroupID         string          `json:"group_id"`
	Present         bool            `json:"present"`
	DurationSeconds *float64        `json:"duration_seconds"`
	Release         ReleaseSummary  `json:"release"`
	Source          TrackAudioFacts `json:"source"`
}

// 曲目、去重发行及批量计数保持同一语句快照，每发行只计算一次摘要；不扩大目录列表或详情的可见性。
var loadTrackSummariesSQL = `WITH selected_tracks AS MATERIALIZED (
 SELECT t.id, t.title, t.artist, t.source_status, dd.duration_seconds,
 t.source_kind, NULLIF(BTRIM(t.codec), '') AS codec, t.bit_depth, t.sample_rate, t.channels, t.bitrate, m.release_id
 FROM tracks t JOIN media m ON m.id=t.medium_id
 LEFT JOIN track_display_durations dd ON dd.track_id=t.id
 WHERE t.id = ANY($1::text[]::uuid[])
), summary_release_ids AS MATERIALIZED (
 SELECT DISTINCT release_id AS id FROM selected_tracks
), ` + releasesql.SelectedTrackArtistRefsCTE + `, ` + releasesql.BatchSummaryCountsCTE + `, ` + releasesql.BatchSummaryArtistRefsCTE + `, summaries (id,title,artist,album_artist,year,source_type,media_type,medium_count,track_count,attention_count,artwork_key,artwork_mime,artwork_width,artwork_height,artist_refs,album_artist_refs,group_id) AS MATERIALIZED (
 SELECT ` + releasesql.BatchSummaryProjection + `, releases.group_id::text
 FROM ` + releasesql.ReleaseSource + ` JOIN summary_release_ids ON summary_release_ids.id=releases.id` + releasesql.BatchSummaryJoins + `
)
SELECT t.id::text,t.title,t.artist,t.source_status='present',t.duration_seconds,
 t.source_kind,t.codec,t.bit_depth,t.sample_rate,t.channels,t.bitrate,
 s.id,s.title,s.artist,s.album_artist,s.year,s.source_type,s.media_type,s.medium_count,s.track_count,s.attention_count,
 s.artwork_key,s.artwork_mime,s.artwork_width,s.artwork_height,s.artist_refs,s.album_artist_refs,s.group_id,COALESCE(track_artist_refs.artist_refs,'[]'::jsonb)
FROM selected_tracks t JOIN summaries s ON s.id=t.release_id::text
 LEFT JOIN track_artist_refs ON track_artist_refs.track_id=t.id`

// LoadTrackSummaries 批量读取含 missing 的曲目摘要；无效 UUID 跳过、小写去重，不存在者无键。
// 空输入不查库，不同有效 ID 最多 1,000 个；缺失曲目仍保留标题、艺人、发行及扫描事实。
func (s *Store) LoadTrackSummaries(ctx context.Context, trackIDs []string) (map[string]TrackSummary, error) {
	result := map[string]TrackSummary{}
	ids := make([]string, 0, min(len(trackIDs), maxSummaryTrackIDs))
	seen := map[string]bool{}
	for _, id := range trackIDs {
		if !uuid.Valid(id) {
			continue
		}
		id = strings.ToLower(id)
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		if len(ids) > maxSummaryTrackIDs {
			return nil, fmt.Errorf("load track summaries: more than %d tracks", maxSummaryTrackIDs)
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.database.QueryContext(ctx, loadTrackSummariesSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("load track summaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item TrackSummary
		var columns ReleaseSummaryColumns
		var refs []byte
		var duration sql.NullFloat64
		var codec sql.NullString
		var depth, rate, channels, bitrate sql.NullInt64
		targets := []any{&item.TrackID, &item.Title, &item.Artist, &item.Present, &duration,
			&item.Source.SourceKind, &codec, &depth, &rate, &channels, &bitrate}
		targets = append(targets, columns.ScanTargets()...)
		targets = append(targets, &item.GroupID, &refs)
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("load track summaries: %w", err)
		}
		item.ArtistRefs, err = decodeArtistRefs(refs)
		if err != nil {
			return nil, fmt.Errorf("load track summaries: %w", err)
		}
		item.Release, err = columns.Summary()
		if err != nil {
			return nil, fmt.Errorf("load track summaries: %w", err)
		}
		if strings.TrimSpace(item.Source.SourceKind) == "" {
			return nil, fmt.Errorf("load track summaries: empty source kind")
		}
		if duration.Valid && duration.Float64 > 0 && !math.IsNaN(duration.Float64) && !math.IsInf(duration.Float64, 0) {
			item.DurationSeconds = &duration.Float64
		}
		if codec.Valid {
			codec.String = strings.TrimSpace(codec.String)
			codec.Valid = codec.String != ""
		}
		item.Source.Codec = releasesql.StringPointerFromNull(codec)
		item.Source.BitDepth = positiveAudioInt(depth)
		item.Source.SampleRate = positiveAudioInt(rate)
		item.Source.Channels = positiveAudioInt(channels)
		item.Source.Bitrate = positiveAudioInt(bitrate)
		result[item.TrackID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load track summaries: %w", err)
	}
	return result, nil
}

func positiveAudioInt(value sql.NullInt64) *int {
	if !value.Valid || value.Int64 <= 0 {
		return nil
	}
	return releasesql.IntPointerFromNull(value)
}
