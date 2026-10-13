package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	pathpkg "path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxArtworkResourceID   = 255
	maxReleaseMedia        = 256
	maxReleaseTrackRows    = 10000
	maxReleaseCredits      = 100
	maxTrackCredits        = 100
	maxReleaseTrackCredits = 10000
	maxReleaseEvidence     = 100
	maxEvidenceCandidates  = 20
	maxEvidenceSourceRefs  = 100
	maxEvidenceStringBytes = 500
	legacyCandidateKind    = "legacy"
	// releaseSource 是所有读取投影的 Release 来源：视图 release_effective 在扫描值之上叠加人工修正（迁移 0017），
	// 别名仍叫 releases，所以下面的谓词、投影与 groups.go、track_placement.go 里的 releases.* 列都解析到有效值。
	// 没有修正时它与 releases 表的读取结果逐列相同。扫描与身份规划不经过它，仍读写 releases 本表。
	releaseSource           = `release_effective releases`
	releaseVisiblePredicate = `EXISTS (
		SELECT 1
		FROM tracks visible_tracks
		JOIN media visible_media ON visible_media.id = visible_tracks.medium_id
		WHERE visible_media.release_id = releases.id
		  AND visible_tracks.source_status = 'present'
	)`
	releaseAttentionExpression = `(
		(SELECT COUNT(DISTINCT decision.field_key)
		 FROM release_field_decisions decision
		 WHERE decision.release_id = releases.id
		   AND decision.action = 'uncertain_apply'
		   AND NOT EXISTS (
			SELECT 1 FROM release_corrections correction
			WHERE correction.release_id = decision.release_id
			  AND correction.field = CASE decision.field_key WHEN 'artist' THEN 'album_artist' ELSE decision.field_key END
		   ))
		+ CASE WHEN EXISTS (
			SELECT 1 FROM release_grouping_evidence grouping
			WHERE grouping.release_id = releases.id
			  AND NULLIF(BTRIM(grouping.reason), '') IS NOT NULL
		  ) THEN 1 ELSE 0 END
		+ (SELECT COUNT(DISTINCT COALESCE(
			NULLIF(missing_track.source_identity, ''),
			missing_track.source_root_id::text || ':' || missing_track.relative_path
		  ))
		  FROM tracks missing_track
		  JOIN media missing_medium ON missing_medium.id = missing_track.medium_id
		  WHERE missing_medium.release_id = releases.id
		    AND missing_track.source_status = 'missing')
	)`
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
	releaseSummaryProjection = `releases.id::text,
		releases.title,
		releases.artist,
		NULLIF(BTRIM(releases.album_artist), ''),
		NULLIF(releases.year, 0),
		NULLIF(BTRIM(releases.source_type), ''),
		NULLIF(BTRIM(releases.media_type), ''),
		(SELECT COUNT(DISTINCT present_medium.id)
		 FROM media present_medium
		 JOIN tracks present_track ON present_track.medium_id = present_medium.id
		 WHERE present_medium.release_id = releases.id
		   AND present_track.source_status = 'present'),
		(SELECT COUNT(*)
		 FROM tracks present_track
		 JOIN media present_medium ON present_medium.id = present_track.medium_id
		 WHERE present_medium.release_id = releases.id
		   AND present_track.source_status = 'present'),
		` + releaseAttentionExpression + `,
		release_artwork.storage_key,
		release_artwork.mime_type,
		release_artwork.width,
		release_artwork.height`
	releaseSummaryArtworkJoin = ` LEFT JOIN release_artworks release_artwork
		ON release_artwork.release_id = releases.id`
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
	Search            string
	AttentionRequired bool
	Page, PageSize    int
}

// ReleasePage 是一页 Release 摘要和满足条件的总数；Items 永远非 nil。
type ReleasePage struct {
	Items []ReleaseSummary
	Total int64
}

// ReleaseHeader 是详情里不属于摘要的 Release 字段；空白值以 nil 表示。
type ReleaseHeader struct {
	CandidateKind                                 string
	Genre, CatalogNumber, Edition, Label, Barcode *string
}

// ListReleases 按标题、艺人排序返回可见 Release 的一页。查询执行失败时错误包含 ErrQuery。
func (s *Store) ListReleases(ctx context.Context, params ListParams) (ReleasePage, error) {
	whereClause := " WHERE " + releaseVisiblePredicate
	queryArgs := []any{}
	if params.Search != "" {
		whereClause += " AND " + releaseSearchPredicate
		queryArgs = append(queryArgs, escapeLikePattern(params.Search))
	}
	if params.AttentionRequired {
		whereClause += " AND " + releaseAttentionExpression + " > 0"
	}

	var total int64
	if err := s.database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+releaseSource+whereClause, queryArgs...).Scan(&total); err != nil {
		return ReleasePage{}, fmt.Errorf("%w: count releases: %w", ErrQuery, err)
	}
	listQuery := "SELECT " + releaseSummaryProjection + " FROM " + releaseSource + releaseSummaryArtworkJoin + whereClause +
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
	var candidateKind, genre, catalogNumber, edition, label, barcode sql.NullString
	err := s.database.QueryRowContext(ctx, `SELECT
		candidate_kind,
		NULLIF(BTRIM(genre),''),
		NULLIF(BTRIM(catalog_number),''),
		NULLIF(BTRIM(edition),''),
		NULLIF(BTRIM(label),''),
		NULLIF(BTRIM(barcode),'')
		FROM release_effective WHERE id=$1::uuid`, releaseID).Scan(&candidateKind, &genre, &catalogNumber, &edition, &label, &barcode)
	if err != nil {
		return ReleaseHeader{}, err
	}
	return ReleaseHeader{
		CandidateKind: normalizeCandidateKind(candidateKind.String),
		Genre:         stringPointerFromNull(genre),
		CatalogNumber: stringPointerFromNull(catalogNumber),
		Edition:       stringPointerFromNull(edition),
		Label:         stringPointerFromNull(label),
		Barcode:       stringPointerFromNull(barcode),
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
		"SELECT "+releaseSummaryProjection+" FROM "+releaseSource+releaseSummaryArtworkJoin+" WHERE releases.id=$1::uuid AND "+releaseVisiblePredicate,
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
		NULLIF(t.duration_seconds,0),NULLIF(BTRIM(t.codec),''),NULLIF(t.bit_depth,0),NULLIF(t.sample_rate,0),NULLIF(t.channels,0),NULLIF(t.bitrate,0),
		t.cue_index_frames,t.cue_end_frames,NULLIF(BTRIM(t.cue_isrc),'')
		FROM media m
		JOIN tracks t ON t.medium_id=m.id AND t.source_status='present'
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
		var duration sql.NullFloat64
		var codec, isrc sql.NullString
		var bitDepth, sampleRate, channels, bitrate, cueIndex, cueEnd sql.NullInt64
		if err := rows.Scan(
			&mediumID, &mediumPosition, &mediumTitle,
			&trackID, &trackTitle, &trackArtist, &trackPosition, &sourcePath, &sourceKind,
			&duration, &codec, &bitDepth, &sampleRate, &channels, &bitrate,
			&cueIndex, &cueEnd, &isrc,
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
			Source:          safeSourceLabel(sourcePath),
			SourceKind:      sourceKind,
			DurationSeconds: floatPointerFromNull(duration),
			Codec:           stringPointerFromNull(codec),
			BitDepth:        intPointerFromNull(bitDepth),
			SampleRate:      intPointerFromNull(sampleRate),
			Channels:        intPointerFromNull(channels),
			Bitrate:         intPointerFromNull(bitrate),
			Credits:         []ReleaseCredit{},
		}
		if strings.Contains(strings.ToLower(sourceKind), "cue") || cueIndex.Valid || cueEnd.Valid || isrc.Valid {
			track.Cue = &ReleaseCue{
				IndexFrames: intPointerFromNull(cueIndex),
				EndFrames:   intPointerFromNull(cueEnd),
				ISRC:        stringPointerFromNull(isrc),
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
		t.id::text,c.role,c.name
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
		if err := creditRows.Scan(&trackID, &credit.Role, &credit.Name); err != nil {
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
	rows, err := s.database.QueryContext(ctx, `SELECT role,name FROM (
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
		if err := rows.Scan(&credit.Role, &credit.Name); err != nil {
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
	rows, err := s.database.QueryContext(ctx, `SELECT field_key,selected_source,confidence,action,rule_id FROM release_field_decisions WHERE release_id=$1::uuid ORDER BY field_key LIMIT $2`, releaseID, maxReleaseEvidence+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	evidence := make([]ReleaseEvidenceSummary, 0)
	for rows.Next() {
		if len(evidence) == maxReleaseEvidence {
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
		LIMIT $2`, releaseID, maxReleaseEvidence+1)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	for rows.Next() {
		if len(result.Fields) == maxReleaseEvidence {
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
			bounded := boundedDisplayString(value)
			item.Value = &bounded
			result.Truncated = result.Truncated || bounded != value
		}
		item.Candidates = []string{}
		if candidatesJSON.Valid {
			candidates, truncated, decodeErr := decodeBoundedStringArray(candidatesJSON.String, maxEvidenceCandidates, false)
			if decodeErr != nil {
				rows.Close()
				return ReleaseEvidence{}, decodeErr
			}
			item.Candidates = candidates
			result.Truncated = result.Truncated || truncated
		}
		if strings.TrimSpace(reason) != "" {
			bounded := boundedDisplayString(reason)
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
	sourceRefs, truncated, err := decodeBoundedStringArray(sourceRefsJSON, maxEvidenceSourceRefs, true)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	grouping.SourceRefs = sourceRefs
	result.Truncated = result.Truncated || truncated
	grouping.ReasonCodes, truncated = splitBoundedReasonCodes(reason)
	result.Truncated = result.Truncated || truncated
	result.Grouping = &grouping
	return result, nil
}

func scanReleaseSummary(row interface{ Scan(...any) error }) (ReleaseSummary, error) {
	var columns releaseSummaryColumns
	if err := row.Scan(columns.targets()...); err != nil {
		return ReleaseSummary{}, err
	}
	return columns.summary()
}

// releaseSummaryColumns 收集 releaseSummaryProjection 的原始列。targets 给出 Scan 的目标（调用方可以在后面追加自己的列），
// summary 再解释 nullable 列并校验封面；列表、详情与按组投影都经过它，只有一处解释这些列。
type releaseSummaryColumns struct {
	result                                                             ReleaseSummary
	albumArtist, sourceType, mediaType, artworkResourceID, artworkMIME sql.NullString
	year, artworkWidth, artworkHeight                                  sql.NullInt64
}

func (columns *releaseSummaryColumns) targets() []any {
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
	}
}

func (columns *releaseSummaryColumns) summary() (ReleaseSummary, error) {
	result := columns.result
	result.AlbumArtist = stringPointerFromNull(columns.albumArtist)
	result.Year = intPointerFromNull(columns.year)
	result.SourceType = stringPointerFromNull(columns.sourceType)
	result.MediaType = stringPointerFromNull(columns.mediaType)
	var err error
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

func decodeBoundedStringArray(raw string, limit int, sourceRefs bool) ([]string, bool, error) {
	var decoded []string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, false, fmt.Errorf("decode evidence string array: %w", err)
	}
	result := make([]string, 0, min(len(decoded), limit))
	truncated := len(decoded) > limit
	for _, value := range decoded {
		if len(result) == limit {
			truncated = true
			break
		}
		if sourceRefs {
			safe, ok := SafeRelativeSourceRef(value)
			if !ok {
				truncated = true
				continue
			}
			normalizedInput := strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
			truncated = truncated || safe != normalizedInput
			result = append(result, safe)
			continue
		}
		bounded := boundedDisplayString(value)
		if bounded == "" {
			truncated = true
			continue
		}
		truncated = truncated || bounded != value
		result = append(result, bounded)
	}
	return result, truncated, nil
}

func SafeRelativeSourceRef(value string) (string, bool) {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	value = boundedDisplayString(value)
	if value == "" || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "", false
	}
	firstSegment := strings.SplitN(value, "/", 2)[0]
	if strings.Contains(firstSegment, ":") {
		return "", false
	}
	cleaned := pathpkg.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, cleaned != ""
}

func splitBoundedReasonCodes(raw string) ([]string, bool) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, false
	}
	parts := strings.Split(raw, ";")
	result := make([]string, 0, min(len(parts), maxEvidenceCandidates))
	truncated := len(parts) > maxEvidenceCandidates
	for _, part := range parts {
		if len(result) == maxEvidenceCandidates {
			break
		}
		part = boundedDisplayString(strings.TrimSpace(part))
		if part != "" {
			result = append(result, part)
		}
	}
	return result, truncated
}

func boundedDisplayString(value string) string {
	value = strings.TrimSpace(strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, value))
	if len(value) <= maxEvidenceStringBytes {
		return value
	}
	value = value[:maxEvidenceStringBytes]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func normalizeCandidateKind(value string) string {
	switch value {
	case "ordinary_directory", "strict_multidisc", "box_leaf", "same_dir_split", "loose_album", "loose_unknown":
		return value
	default:
		return legacyCandidateKind
	}
}

func safeSourceLabel(relativePath string) string {
	relativePath = strings.ReplaceAll(relativePath, `\`, "/")
	label := boundedDisplayString(pathpkg.Base(relativePath))
	if label == "" || label == "." || label == "/" {
		return "未知来源"
	}
	return label
}

func stringPointerFromNull(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func intPointerFromNull(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
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
