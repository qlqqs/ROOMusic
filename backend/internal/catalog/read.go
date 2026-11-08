package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

const (
	maxArtworkResourceID   = 255
	maxReleaseMedia        = 256
	maxReleaseTrackRows    = 10000
	maxReleaseCredits      = 100
	maxTrackCredits        = 100
	maxReleaseTrackCredits = 10000
	MaxReleaseEvidence     = 100
	MaxEvidenceSourceRefs  = 100
	legacyCandidateKind    = "legacy"
	// releaseSearchPredicate 是 Release 搜索的命中条件（参数 $1 已转义 LIKE 通配符）：标题、艺人、专辑艺人，
	// 或任一 present 曲目的标题。/releases 与 /release-groups 共用它，保证搜索语义一致。
	releaseSearchPredicate = `(
		releases.title ILIKE '%' || $1 || '%' ESCAPE '~'
		OR releases.artist ILIKE '%' || $1 || '%' ESCAPE '~'
		OR COALESCE(releases.album_artist, '') ILIKE '%' || $1 || '%' ESCAPE '~'
		OR EXISTS (
			SELECT 1 FROM media search_media
			JOIN tracks search_track ON search_track.medium_id = search_media.id
			WHERE search_media.release_id = releases.id
			  AND search_track.source_status = 'present'
			  AND search_track.title ILIKE '%' || $1 || '%' ESCAPE '~'
		)
	)`
)

// Queryer 是读取 Release 图所需的最小数据库能力。
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Store 提供 Release 图的只读投影。它不写数据，也不做权限判断；传输层负责鉴权与响应映射。
type Store struct{ database Queryer }

// NewStore 返回读取 database 的 Store。
func NewStore(database Queryer) *Store { return &Store{database: database} }

var (
	// ErrReleaseNotFound 表示 Release 不存在，或它没有任何当前存在的曲目。
	ErrReleaseNotFound = errors.New("release not found")
	// ErrQuery 表示查询本身没能执行（数据库不可用）；不带它的错误表示读取行或投影失败。
	ErrQuery = errors.New("catalog query failed")
)

// ListParams 是 Release 列表的已校验参数：Search 已去除首尾空白，Page 与 PageSize 都大于 0。
type ListParams struct {
	// Sort 仅用于组列表；空值等价于标题排序。发行列表忽略它。
	Sort              ReleaseGroupSort
	Search            string
	AttentionRequired bool
	Page, PageSize    int
}

// ReleasePage 是一页 Release 摘要和满足条件的总数；Items 永远非 nil。
type ReleasePage struct {
	Items []ReleaseSummary
	Total int64
}

// ReleaseHeader 是详情里不属于摘要的 Release 字段；流派为空列表，其余空白文本以 nil 表示。
type ReleaseHeader struct {
	CandidateKind                          string
	Genres                                 []string
	CatalogNumber, Edition, Label, Barcode *string
	Revision                               int64
	CorrectedFields                        []string
}

// listReleasesLegacy 保留旧匹配与浏览顺序；非空 q 的调用方已开启同一读取快照。
func (s *Store) listReleasesLegacy(ctx context.Context, params ListParams) (ReleasePage, error) {
	whereClause := " WHERE " + releasesql.ReleaseVisiblePredicate
	queryArgs := []any{}
	if params.Search != "" {
		whereClause += " AND " + releaseSearchPredicate
		queryArgs = append(queryArgs, escapeLikePattern(params.Search))
	}
	if params.AttentionRequired {
		whereClause += " AND " + releasesql.ReleaseAttentionExpression + " > 0"
	}

	var total int64
	if err := s.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+releasesql.ReleaseSource+whereClause, queryArgs...).Scan(&total); err != nil {
		return ReleasePage{}, fmt.Errorf("%w: count releases: %w", ErrQuery, err)
	}
	listQuery := "SELECT " + releasesql.ReleaseSummaryProjection + " FROM " + releasesql.ReleaseSource + releasesql.ReleaseSummaryArtworkJoin + whereClause +
		" ORDER BY LOWER(releases.title),LOWER(releases.artist),releases.id LIMIT $" + strconv.Itoa(len(queryArgs)+1) +
		" OFFSET $" + strconv.Itoa(len(queryArgs)+2)
	listArgs := append(append([]any{}, queryArgs...), params.PageSize, (params.Page-1)*params.PageSize)
	rows, err := s.database.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return ReleasePage{}, fmt.Errorf("%w: list releases: %w", ErrQuery, err)
	}
	defer rows.Close()
	items := make([]ReleaseSummary, 0, params.PageSize)
	for rows.Next() {
		item, scanErr := scanReleaseSummary(rows)
		if scanErr != nil {
			return ReleasePage{}, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ReleasePage{}, err
	}
	return ReleasePage{Items: items, Total: total}, nil
}

// LoadReleaseHeader 读取详情里摘要以外的 Release 字段。
func (s *Store) LoadReleaseHeader(ctx context.Context, releaseID string) (ReleaseHeader, error) {
	var candidateKind, catalogNumber, edition, label, barcode sql.NullString
	var genresJSON string
	genres := []string{}
	var revision int64
	err := s.database.QueryRowContext(ctx, `SELECT
		candidate_kind,
		to_jsonb(genres)::text,
		NULLIF(BTRIM(catalog_number),''),
		NULLIF(BTRIM(edition),''),
		NULLIF(BTRIM(label),''),
		NULLIF(BTRIM(barcode),''),
		edit_revision
		FROM release_effective WHERE id=$1::uuid`, releaseID).Scan(&candidateKind, &genresJSON, &catalogNumber, &edition, &label, &barcode, &revision)
	if err != nil {
		return ReleaseHeader{}, err
	}
	if err := json.Unmarshal([]byte(genresJSON), &genres); err != nil {
		return ReleaseHeader{}, err
	}
	corrected, err := loadCorrectedFieldNames(ctx, s.database, releaseID)
	if err != nil {
		return ReleaseHeader{}, err
	}
	return ReleaseHeader{
		CandidateKind:   normalizeCandidateKind(candidateKind.String),
		Genres:          genres,
		CatalogNumber:   releasesql.StringPointerFromNull(catalogNumber),
		Edition:         releasesql.StringPointerFromNull(edition),
		Label:           releasesql.StringPointerFromNull(label),
		Barcode:         releasesql.StringPointerFromNull(barcode),
		Revision:        revision,
		CorrectedFields: corrected,
	}, nil
}

// ReleaseExists 报告 Release 行是否存在（不要求有可见曲目，供管理员读取证据时使用）。
func (s *Store) ReleaseExists(ctx context.Context, releaseID string) (bool, error) {
	var exists bool
	err := s.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM releases WHERE id=$1::uuid)`, releaseID).Scan(&exists)
	return exists, err
}

// LoadReleaseSummary 读取可见 Release 的摘要；不存在或没有可见曲目时返回 ErrReleaseNotFound。
func (s *Store) LoadReleaseSummary(ctx context.Context, releaseID string) (ReleaseSummary, error) {
	summary, err := scanReleaseSummary(s.database.QueryRowContext(ctx,
		"SELECT "+releasesql.ReleaseSummaryProjection+" FROM "+releasesql.ReleaseSource+releasesql.ReleaseSummaryArtworkJoin+" WHERE releases.id=$1::uuid AND "+releasesql.ReleaseVisiblePredicate,
		releaseID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return ReleaseSummary{}, ErrReleaseNotFound
	}
	return summary, err
}

func (s *Store) LoadReleaseMedia(ctx context.Context, releaseID string) ([]ReleaseMedium, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT
		m.id::text,m.position,m.title,
		t.id::text,t.title,t.artist,t.position,t.relative_path,t.source_kind,
		NULLIF(dd.duration_seconds,0),NULLIF(BTRIM(t.codec),''),NULLIF(t.bit_depth,0),NULLIF(t.sample_rate,0),NULLIF(t.channels,0),NULLIF(t.bitrate,0),
		t.cue_index_frames,t.cue_end_frames,NULLIF(BTRIM(t.cue_isrc),''),`+releasesql.TrackArtistRefsProjection+`
		FROM media m
		JOIN tracks t ON t.medium_id=m.id AND t.source_status='present'
		LEFT JOIN track_display_durations dd ON dd.track_id=t.id
		WHERE m.release_id=$1::uuid
		ORDER BY m.position,m.id,t.position,t.id
		LIMIT $2`, releaseID, maxReleaseTrackRows+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	media := make([]ReleaseMedium, 0)
	mediumIndexes := make(map[string]int)
	type trackLocation struct {
		medium int
		track  int
	}
	trackLocations := make(map[string]trackLocation)
	rowCount := 0
	for rows.Next() {
		rowCount++
		if rowCount > maxReleaseTrackRows {
			return nil, errors.New("release track projection exceeds bound")
		}
		var mediumID, mediumTitle, trackID, trackTitle, trackArtist, sourcePath, sourceKind string
		var mediumPosition, trackPosition int
		var artistRefs []byte
		var duration sql.NullFloat64
		var codec, isrc sql.NullString
		var bitDepth, sampleRate, channels, bitrate, cueIndex, cueEnd sql.NullInt64
		if err := rows.Scan(
			&mediumID, &mediumPosition, &mediumTitle,
			&trackID, &trackTitle, &trackArtist, &trackPosition, &sourcePath, &sourceKind,
			&duration, &codec, &bitDepth, &sampleRate, &channels, &bitrate,
			&cueIndex, &cueEnd, &isrc, &artistRefs,
		); err != nil {
			return nil, err
		}
		mediumIndex, exists := mediumIndexes[mediumID]
		if !exists {
			if len(media) == maxReleaseMedia {
				return nil, errors.New("release medium projection exceeds bound")
			}
			mediumIndex = len(media)
			mediumIndexes[mediumID] = mediumIndex
			media = append(media, ReleaseMedium{ID: mediumID, Position: mediumPosition, Title: mediumTitle, Tracks: []ReleaseTrack{}})
		}
		track := ReleaseTrack{
			ID:              trackID,
			Title:           trackTitle,
			Artist:          trackArtist,
			Position:        trackPosition,
			Source:          releasesql.SafeSourceLabel(sourcePath),
			SourceKind:      sourceKind,
			DurationSeconds: floatPointerFromNull(duration),
			Codec:           releasesql.StringPointerFromNull(codec),
			BitDepth:        releasesql.IntPointerFromNull(bitDepth),
			SampleRate:      releasesql.IntPointerFromNull(sampleRate),
			Channels:        releasesql.IntPointerFromNull(channels),
			Bitrate:         releasesql.IntPointerFromNull(bitrate),
			Credits:         []ReleaseCredit{},
		}
		track.ArtistRefs, err = decodeArtistRefs(artistRefs)
		if err != nil {
			return nil, err
		}
		if strings.Contains(strings.ToLower(sourceKind), "cue") || cueIndex.Valid || cueEnd.Valid || isrc.Valid {
			track.Cue = &ReleaseCue{
				IndexFrames: releasesql.IntPointerFromNull(cueIndex),
				EndFrames:   releasesql.IntPointerFromNull(cueEnd),
				ISRC:        releasesql.StringPointerFromNull(isrc),
			}
			if cueIndex.Valid {
				seconds := float64(cueIndex.Int64) / 75
				track.Cue.StartSeconds = &seconds
			}
			if cueEnd.Valid {
				seconds := float64(cueEnd.Int64) / 75
				track.Cue.EndSeconds = &seconds
			}
		}
		trackIndex := len(media[mediumIndex].Tracks)
		media[mediumIndex].Tracks = append(media[mediumIndex].Tracks, track)
		trackLocations[trackID] = trackLocation{medium: mediumIndex, track: trackIndex}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	creditRows, err := s.database.QueryContext(ctx, `SELECT
		t.id::text,c.role,c.name,`+releasesql.TrackCreditArtistRefsProjection+`
		FROM track_credits c
		JOIN tracks t ON t.id=c.track_id AND t.source_status='present'
		JOIN media m ON m.id=t.medium_id
		WHERE m.release_id=$1::uuid
		ORDER BY m.position,m.id,t.position,t.id,c.position,c.role,c.name
		LIMIT $2`, releaseID, maxReleaseTrackCredits+1)
	if err != nil {
		return nil, err
	}
	defer creditRows.Close()
	creditCounts := make(map[string]int)
	totalCredits := 0
	for creditRows.Next() {
		if totalCredits == maxReleaseTrackCredits {
			return nil, errors.New("release track credit projection exceeds bound")
		}
		var trackID string
		var credit ReleaseCredit
		var refs []byte
		if err := creditRows.Scan(&trackID, &credit.Role, &credit.Name, &refs); err != nil {
			return nil, err
		}
		credit.ArtistRefs, err = decodeArtistRefs(refs)
		if err != nil {
			return nil, err
		}
		location, exists := trackLocations[trackID]
		if !exists {
			return nil, errors.New("track credit projection references an unavailable track")
		}
		if creditCounts[trackID] == maxTrackCredits {
			return nil, errors.New("track credit projection exceeds per-track bound")
		}
		media[location.medium].Tracks[location.track].Credits = append(
			media[location.medium].Tracks[location.track].Credits,
			credit,
		)
		creditCounts[trackID]++
		totalCredits++
	}
	if err := creditRows.Err(); err != nil {
		return nil, err
	}
	return media, nil
}

// LoadReleaseCredits 读取专辑级署名。专辑艺人有人工修正时，扫描写入的 album_artist 署名被修正值取代，
// 与头部显示的有效专辑艺人一致；其余署名原样。
func (s *Store) LoadReleaseCredits(ctx context.Context, releaseID string) ([]ReleaseCredit, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT role,name,`+releasesql.ReleaseCreditArtistRefsProjection+` FROM (
			SELECT credit.role,credit.name,credit.position
			FROM release_credits credit
			WHERE credit.release_id=$1::uuid
			  AND NOT (credit.role='album_artist' AND EXISTS (
				SELECT 1 FROM release_corrections correction
				WHERE correction.release_id=credit.release_id AND correction.field='album_artist'))
			UNION ALL
			SELECT 'album_artist',correction.text_value,1
			FROM release_corrections correction
			WHERE correction.release_id=$1::uuid AND correction.field='album_artist'
		) credits ORDER BY position,role,name LIMIT $2`, releaseID, maxReleaseCredits+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	credits := make([]ReleaseCredit, 0)
	for rows.Next() {
		if len(credits) == maxReleaseCredits {
			return nil, errors.New("release credit projection exceeds bound")
		}
		var credit ReleaseCredit
		var refs []byte
		if err := rows.Scan(&credit.Role, &credit.Name, &refs); err != nil {
			return nil, err
		}
		credit.ArtistRefs, err = decodeArtistRefs(refs)
		if err != nil {
			return nil, err
		}
		credits = append(credits, credit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return credits, nil
}

func (s *Store) LoadReleaseEvidenceSummary(ctx context.Context, releaseID string) ([]ReleaseEvidenceSummary, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT field_key,selected_source,confidence,action,rule_id FROM release_field_decisions WHERE release_id=$1::uuid ORDER BY field_key LIMIT $2`, releaseID, MaxReleaseEvidence+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	evidence := make([]ReleaseEvidenceSummary, 0)
	for rows.Next() {
		if len(evidence) == MaxReleaseEvidence {
			return nil, errors.New("release evidence projection exceeds bound")
		}
		var item ReleaseEvidenceSummary
		if err := rows.Scan(&item.Field, &item.Source, &item.Confidence, &item.Action, &item.RuleID); err != nil {
			return nil, err
		}
		evidence = append(evidence, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return evidence, nil
}

func (s *Store) LoadReleaseEvidence(ctx context.Context, releaseID string) (ReleaseEvidence, error) {
	result := ReleaseEvidence{ReleaseID: releaseID, Fields: []ReleaseFieldEvidence{}}
	rows, err := s.database.QueryContext(ctx, `SELECT
		field_key,COALESCE(selected_value #>> '{}',''),selected_source,confidence,action,rule_id,candidates::text,COALESCE(reason,'')
		FROM release_field_decisions
		WHERE release_id=$1::uuid
		ORDER BY field_key
		LIMIT $2`, releaseID, MaxReleaseEvidence+1)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	for rows.Next() {
		if len(result.Fields) == MaxReleaseEvidence {
			result.Truncated = true
			break
		}
		var item ReleaseFieldEvidence
		var value, reason string
		var candidatesJSON sql.NullString
		if err := rows.Scan(&item.Field, &value, &item.Source, &item.Confidence, &item.Action, &item.RuleID, &candidatesJSON, &reason); err != nil {
			rows.Close()
			return ReleaseEvidence{}, err
		}
		if strings.TrimSpace(value) != "" {
			bounded := releasesql.BoundedDisplayString(value)
			item.Value = &bounded
			result.Truncated = result.Truncated || bounded != value
		}
		item.Candidates = []string{}
		if candidatesJSON.Valid {
			candidates, truncated, decodeErr := releasesql.DecodeBoundedStringArray(candidatesJSON.String, releasesql.MaxEvidenceCandidates, false)
			if decodeErr != nil {
				rows.Close()
				return ReleaseEvidence{}, decodeErr
			}
			item.Candidates = candidates
			result.Truncated = result.Truncated || truncated
		}
		if strings.TrimSpace(reason) != "" {
			bounded := releasesql.BoundedDisplayString(reason)
			item.ReasonCode = &bounded
			result.Truncated = result.Truncated || bounded != reason
		}
		result.Fields = append(result.Fields, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ReleaseEvidence{}, err
	}
	rows.Close()

	var grouping ReleaseGroupingEvidence
	var sourceRefsJSON, reason string
	err = s.database.QueryRowContext(ctx, `SELECT candidate_kind,rule_id,source_refs::text,COALESCE(reason,'') FROM release_grouping_evidence WHERE release_id=$1::uuid ORDER BY observed_at DESC,id DESC LIMIT 1`, releaseID).Scan(&grouping.CandidateKind, &grouping.RuleID, &sourceRefsJSON, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return ReleaseEvidence{}, err
	}
	grouping.CandidateKind = normalizeCandidateKind(grouping.CandidateKind)
	sourceRefs, truncated, err := releasesql.DecodeBoundedStringArray(sourceRefsJSON, MaxEvidenceSourceRefs, true)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	grouping.SourceRefs = sourceRefs
	result.Truncated = result.Truncated || truncated
	grouping.ReasonCodes, truncated = releasesql.SplitBoundedReasonCodes(reason)
	result.Truncated = result.Truncated || truncated
	result.Grouping = &grouping
	return result, nil
}

func scanReleaseSummary(row interface{ Scan(...any) error }) (ReleaseSummary, error) {
	var columns ReleaseSummaryColumns
	if err := row.Scan(columns.ScanTargets()...); err != nil {
		return ReleaseSummary{}, err
	}
	return columns.Summary()
}

// ReleaseSummaryColumns 收集 releasesql.ReleaseSummaryProjection 的原始列。ScanTargets 给出 Scan 的目标（调用方可以在后面追加自己的列），
// Summary 再解释 nullable 列并校验封面；列表、详情与按组投影都经过它，只有一处解释这些列。
type ReleaseSummaryColumns struct {
	result                                                             ReleaseSummary
	artistRefs, albumArtistRefs                                        []byte
	albumArtist, sourceType, mediaType, artworkResourceID, artworkMIME sql.NullString
	year, artworkWidth, artworkHeight                                  sql.NullInt64
}

func (columns *ReleaseSummaryColumns) ScanTargets() []any {
	return []any{
		&columns.result.ID,
		&columns.result.Title,
		&columns.result.Artist,
		&columns.albumArtist,
		&columns.year,
		&columns.sourceType,
		&columns.mediaType,
		&columns.result.MediumCount,
		&columns.result.TrackCount,
		&columns.result.AttentionCount,
		&columns.artworkResourceID,
		&columns.artworkMIME,
		&columns.artworkWidth,
		&columns.artworkHeight,
		&columns.artistRefs,
		&columns.albumArtistRefs,
	}
}

func (columns *ReleaseSummaryColumns) Summary() (ReleaseSummary, error) {
	result := columns.result
	result.AlbumArtist = releasesql.StringPointerFromNull(columns.albumArtist)
	result.Year = releasesql.IntPointerFromNull(columns.year)
	result.SourceType = releasesql.StringPointerFromNull(columns.sourceType)
	result.MediaType = releasesql.StringPointerFromNull(columns.mediaType)
	var err error
	result.ArtistRefs, err = decodeArtistRefs(columns.artistRefs)
	if err != nil {
		return ReleaseSummary{}, err
	}
	result.AlbumArtistRefs, err = decodeArtistRefs(columns.albumArtistRefs)
	if err != nil {
		return ReleaseSummary{}, err
	}
	result.Artwork, err = releaseArtworkFromProjection(columns.artworkResourceID, columns.artworkMIME, columns.artworkWidth, columns.artworkHeight)
	if err != nil {
		return ReleaseSummary{}, err
	}
	return result, nil
}

func releaseArtworkFromProjection(resourceID, mime sql.NullString, width, height sql.NullInt64) (*ReleaseArtwork, error) {
	presentColumns := 0
	for _, present := range []bool{resourceID.Valid, mime.Valid, width.Valid, height.Valid} {
		if present {
			presentColumns++
		}
	}
	if presentColumns == 0 {
		return nil, nil
	}
	if presentColumns != 4 {
		return nil, errors.New("incomplete artwork projection")
	}
	if resourceID.String == "" || len(resourceID.String) > maxArtworkResourceID ||
		resourceID.String == "." || resourceID.String == ".." ||
		!utf8.ValidString(resourceID.String) || strings.ContainsAny(resourceID.String, `/\\`) ||
		strings.IndexFunc(resourceID.String, unicode.IsControl) >= 0 {
		return nil, errors.New("unsafe artwork resource identifier")
	}
	switch mime.String {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return nil, errors.New("unsupported artwork MIME type")
	}
	const maxArtworkDimension = int64(1<<31 - 1)
	if width.Int64 <= 0 || height.Int64 <= 0 || width.Int64 > maxArtworkDimension || height.Int64 > maxArtworkDimension {
		return nil, errors.New("invalid artwork dimensions")
	}
	return &ReleaseArtwork{
		ResourceID: resourceID.String,
		MIME:       mime.String,
		Width:      int(width.Int64),
		Height:     int(height.Int64),
	}, nil
}

func SafeRelativeSourceRef(value string) (string, bool) {
	return releasesql.SafeRelativeSourceRef(value)
}

func normalizeCandidateKind(value string) string {
	switch value {
	case "ordinary_directory", "strict_multidisc", "box_leaf", "same_dir_split", "loose_album", "loose_unknown":
		return value
	default:
		return legacyCandidateKind
	}
}

func floatPointerFromNull(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}

func escapeLikePattern(query string) string {
	query = strings.ReplaceAll(query, `~`, `~~`)
	query = strings.ReplaceAll(query, `%`, `~%`)
	return strings.ReplaceAll(query, `_`, `~_`)
}
