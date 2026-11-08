package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// 版本对比（阶段 M4）：一张专辑的各个版本并排比较发行级字段与内容（介质、曲目、时长、音频格式）。只读，所有登录成员可用。
// 成员的顺序与上限和组详情一致（代表版本规则，默认版本在前，最多 maxReleaseGroupMembers 个）；文本字段是有效值（含人工修正），
// 介质、曲目、时长与格式只计当前存在的曲目。这里不做“音质”判断，也不给出排名：格式只是如实列出编码与参数。

// maxComparedFormats 是一个版本里不同音频参数组合（编码、位深、采样率）的上限。真实的资料库里一个版本只有个位数的组合；
// 超过上限的版本让整个对比读取失败，不静默截断（与介质、曲目投影超界时的处理一致）。
const maxComparedFormats = 32

// AudioFormat 是一个版本里使用同一组参数的 present 曲目：未知的参数为 nil。
type AudioFormat struct {
	Codec      *string `json:"codec"`
	BitDepth   *int    `json:"bit_depth"`
	SampleRate *int    `json:"sample_rate"`
	TrackCount int64   `json:"track_count"`
}

// ComparedRelease 是对比表里的一列。DurationSeconds 是总时长：只要有一首曲目的时长未知（零、负数与非有限值都算未知），
// 就是 nil，不拿已知的部分冒充总数（与发行详情页头部的规则一致）；
// Formats 按曲目数降序、再按编码、位深、采样率排序，没有曲目时是空数组；CorrectedFields 与发行详情相同。
type ComparedRelease struct {
	ID              string        `json:"id"`
	Title           string        `json:"title"`
	Artist          string        `json:"artist"`
	AlbumArtist     *string       `json:"album_artist"`
	Year            *int          `json:"year"`
	Edition         *string       `json:"edition"`
	Label           *string       `json:"label"`
	CatalogNumber   *string       `json:"catalog_number"`
	Barcode         *string       `json:"barcode"`
	Genres          []string      `json:"genres"`
	SourceType      *string       `json:"source_type"`
	MediaType       *string       `json:"media_type"`
	MediumCount     int64         `json:"medium_count"`
	TrackCount      int64         `json:"track_count"`
	DurationSeconds *float64      `json:"duration_seconds"`
	Formats         []AudioFormat `json:"formats"`
	CorrectedFields []string      `json:"corrected_fields"`
}

// ReleaseComparison 是版本对比的响应。ReleaseCount 是完整的可见版本数，可以大于 Releases 的长度；
// DefaultReleaseID 与组详情相同：只在默认版本正在生效时有值，此时它就是 Releases 的第一项。
type ReleaseComparison struct {
	GroupID          string            `json:"group_id"`
	DefaultReleaseID *string           `json:"default_release_id"`
	ReleaseCount     int64             `json:"release_count"`
	Releases         []ComparedRelease `json:"releases"`
}

// comparedReleaseQuery 读取成员与字段：一条查询，成员数、默认版本与每个成员的介质、曲目与时长汇总都在里面。
// 汇总只看 present 曲目；总时长只在每一首曲目的时长都是有限的正数时才给出，否则是 NULL。
const comparedReleaseQuery = `SELECT releases.id::text, releases.title, releases.artist,
		NULLIF(BTRIM(releases.album_artist), ''), NULLIF(releases.year, 0),
		NULLIF(BTRIM(releases.edition), ''), NULLIF(BTRIM(releases.label), ''), NULLIF(BTRIM(releases.catalog_number), ''),
		NULLIF(BTRIM(releases.barcode), ''), to_jsonb(releases.genres)::text,
		NULLIF(BTRIM(releases.source_type), ''), NULLIF(BTRIM(releases.media_type), ''),
		present.medium_count, present.track_count, present.duration_seconds,
		COUNT(*) OVER (),
		(SELECT default_group.default_release_id::text FROM release_groups default_group WHERE default_group.id = releases.group_id)
	FROM ` + releasesql.ReleaseSource + `
	CROSS JOIN LATERAL (
		SELECT COUNT(DISTINCT present_medium.id) AS medium_count,
		       COUNT(*) AS track_count,
		       CASE WHEN COUNT(*) FILTER (WHERE dd.duration_seconds > 0 AND dd.duration_seconds < 'Infinity'::float8) = COUNT(*)
		            THEN SUM(dd.duration_seconds)::float8 END AS duration_seconds
		FROM media present_medium
		JOIN tracks present_track ON present_track.medium_id = present_medium.id
		LEFT JOIN track_display_durations dd ON dd.track_id = present_track.id
		WHERE present_medium.release_id = releases.id AND present_track.source_status = 'present'
	) present
	WHERE releases.group_id = $1::uuid AND ` + releasesql.ReleaseVisiblePredicate + `
	ORDER BY ` + releasesql.ReleaseGroupMemberOrder + `
	LIMIT $2`

// LoadReleaseComparison 读取一张专辑各个可见版本的对比数据。组不存在或没有可见成员时返回 ErrReleaseGroupNotFound。
// 查询数量固定（成员与字段、格式汇总、修正字段各一条，后两条按成员 ID 批量），不随版本数增长。
// 查询执行失败时错误包含 ErrQuery。
func (s *Store) LoadReleaseComparison(ctx context.Context, groupID string) (ReleaseComparison, error) {
	rows, err := s.database.QueryContext(ctx, comparedReleaseQuery, groupID, maxReleaseGroupMembers)
	if err != nil {
		return ReleaseComparison{}, fmt.Errorf("%w: compare releases: %w", ErrQuery, err)
	}
	defer rows.Close()
	comparison := ReleaseComparison{GroupID: strings.ToLower(groupID), Releases: []ComparedRelease{}}
	var stored sql.NullString
	for rows.Next() {
		var release ComparedRelease
		var albumArtist, edition, label, catalogNumber, barcode, sourceType, mediaType sql.NullString
		var genresJSON string
		var year sql.NullInt64
		var duration sql.NullFloat64
		if err := rows.Scan(&release.ID, &release.Title, &release.Artist, &albumArtist, &year, &edition, &label, &catalogNumber, &barcode, &genresJSON,
			&sourceType, &mediaType, &release.MediumCount, &release.TrackCount, &duration, &comparison.ReleaseCount, &stored); err != nil {
			return ReleaseComparison{}, err
		}
		release.AlbumArtist, release.Year = releasesql.StringPointerFromNull(albumArtist), releasesql.IntPointerFromNull(year)
		release.Edition, release.Label, release.CatalogNumber = releasesql.StringPointerFromNull(edition), releasesql.StringPointerFromNull(label), releasesql.StringPointerFromNull(catalogNumber)
		release.Barcode = releasesql.StringPointerFromNull(barcode)
		if err := json.Unmarshal([]byte(genresJSON), &release.Genres); err != nil {
			return ReleaseComparison{}, err
		}
		release.SourceType, release.MediaType = releasesql.StringPointerFromNull(sourceType), releasesql.StringPointerFromNull(mediaType)
		release.DurationSeconds = floatPointerFromNull(duration)
		release.Formats, release.CorrectedFields = []AudioFormat{}, []string{}
		comparison.Releases = append(comparison.Releases, release)
	}
	if err := rows.Err(); err != nil {
		return ReleaseComparison{}, err
	}
	if len(comparison.Releases) == 0 {
		return ReleaseComparison{}, ErrReleaseGroupNotFound
	}
	// 与组详情同一条规则：已存的默认版本生效，当且仅当它是排在最前的可见成员。
	if first := comparison.Releases[0].ID; stored.Valid && stored.String == first {
		comparison.DefaultReleaseID = &first
	}

	ids := make([]string, len(comparison.Releases))
	index := make(map[string]int, len(ids))
	for position, release := range comparison.Releases {
		ids[position] = release.ID
		index[release.ID] = position
	}
	if err := s.loadComparedFormats(ctx, ids, index, comparison.Releases); err != nil {
		return ReleaseComparison{}, err
	}
	if err := s.loadComparedCorrections(ctx, ids, index, comparison.Releases); err != nil {
		return ReleaseComparison{}, err
	}
	return comparison, nil
}

// loadComparedFormats 一条查询汇总这些版本里 present 曲目的 (编码, 位深, 采样率) 分组。
func (s *Store) loadComparedFormats(ctx context.Context, ids []string, index map[string]int, releases []ComparedRelease) error {
	rows, err := s.database.QueryContext(ctx, `SELECT media.release_id::text, NULLIF(BTRIM(tracks.codec), ''), NULLIF(tracks.bit_depth, 0), NULLIF(tracks.sample_rate, 0), COUNT(*)
		FROM tracks JOIN media ON media.id = tracks.medium_id
		WHERE media.release_id = ANY($1::text[]::uuid[]) AND tracks.source_status = 'present'
		GROUP BY 1, 2, 3, 4
		ORDER BY 1, 5 DESC, 2 NULLS LAST, 3 NULLS LAST, 4 NULLS LAST`, ids)
	if err != nil {
		return fmt.Errorf("%w: compare release formats: %w", ErrQuery, err)
	}
	defer rows.Close()
	for rows.Next() {
		var releaseID string
		var codec sql.NullString
		var bitDepth, sampleRate sql.NullInt64
		var format AudioFormat
		if err := rows.Scan(&releaseID, &codec, &bitDepth, &sampleRate, &format.TrackCount); err != nil {
			return err
		}
		format.Codec, format.BitDepth, format.SampleRate = releasesql.StringPointerFromNull(codec), releasesql.IntPointerFromNull(bitDepth), releasesql.IntPointerFromNull(sampleRate)
		position, known := index[releaseID]
		if !known {
			return errors.New("comparison formats name a release outside the compared set")
		}
		if len(releases[position].Formats) == maxComparedFormats {
			return errors.New("release format projection exceeds bound")
		}
		releases[position].Formats = append(releases[position].Formats, format)
	}
	return rows.Err()
}

// loadComparedCorrections 一条查询取出这些版本有人工修正的字段名，每个版本内按 CorrectableFields 的顺序。
func (s *Store) loadComparedCorrections(ctx context.Context, ids []string, index map[string]int, releases []ComparedRelease) error {
	rows, err := s.database.QueryContext(ctx, `SELECT release_id::text, field FROM release_corrections WHERE release_id = ANY($1::text[]::uuid[])`, ids)
	if err != nil {
		return fmt.Errorf("%w: compare release corrections: %w", ErrQuery, err)
	}
	defer rows.Close()
	corrected := make(map[string]map[string]bool, len(ids))
	for rows.Next() {
		var releaseID, field string
		if err := rows.Scan(&releaseID, &field); err != nil {
			return err
		}
		if corrected[releaseID] == nil {
			corrected[releaseID] = map[string]bool{}
		}
		corrected[releaseID][field] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	order := make(map[string]int, len(CorrectableFields))
	for position, field := range CorrectableFields {
		order[field.Name] = position
	}
	for releaseID, fields := range corrected {
		names := make([]string, 0, len(fields))
		for name := range fields {
			if _, known := order[name]; known {
				names = append(names, name)
			}
		}
		sort.Slice(names, func(left, right int) bool { return order[names[left]] < order[names[right]] })
		releases[index[releaseID]].CorrectedFields = names
	}
	return nil
}
