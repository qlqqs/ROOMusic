package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	pathpkg "path"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxReleaseRows          = 100
	maxReleaseMedia         = 256
	maxReleaseTrackRows     = 10000
	maxReleaseCredits       = 100
	maxReleaseEvidence      = 100
	maxEvidenceCandidates   = 20
	maxEvidenceSourceRefs   = 100
	maxEvidenceStringBytes  = 500
	legacyCandidateKind     = "legacy"
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
		   AND decision.action = 'uncertain_apply')
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
		` + releaseAttentionExpression
)

type releaseSummaryDTO struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Artist         string  `json:"artist"`
	AlbumArtist    *string `json:"album_artist"`
	Year           *int    `json:"year"`
	SourceType     *string `json:"source_type"`
	MediaType      *string `json:"media_type"`
	MediumCount    int64   `json:"medium_count"`
	TrackCount     int64   `json:"track_count"`
	AttentionCount int64   `json:"attention_count"`
}

type releaseCueDTO struct {
	IndexFrames  *int     `json:"index_frames"`
	EndFrames    *int     `json:"end_frames"`
	StartSeconds *float64 `json:"start_seconds"`
	EndSeconds   *float64 `json:"end_seconds"`
	ISRC         *string  `json:"isrc"`
}

type releaseTrackDTO struct {
	ID              string         `json:"id"`
	Title           string         `json:"title"`
	Artist          string         `json:"artist"`
	Position        int            `json:"position"`
	Source          string         `json:"source"`
	SourceKind      string         `json:"source_kind"`
	DurationSeconds *float64       `json:"duration_seconds"`
	Codec           *string        `json:"codec"`
	BitDepth        *int           `json:"bit_depth"`
	SampleRate      *int           `json:"sample_rate"`
	Channels        *int           `json:"channels"`
	Bitrate         *int           `json:"bitrate"`
	Cue             *releaseCueDTO `json:"cue"`
}

type releaseMediumDTO struct {
	ID       string            `json:"id"`
	Position int               `json:"position"`
	Title    string            `json:"title"`
	Tracks   []releaseTrackDTO `json:"tracks"`
}

type releaseArtworkDTO struct {
	ResourceID string `json:"resource_id"`
	MIME       string `json:"mime"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

type releaseCreditDTO struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

type releaseEvidenceSummaryDTO struct {
	Field      string `json:"field"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
	Action     string `json:"action"`
	RuleID     string `json:"rule_id"`
}

type releaseDetailDTO struct {
	releaseSummaryDTO
	CandidateKind string                      `json:"candidate_kind"`
	Genre         *string                     `json:"genre"`
	CatalogNumber *string                     `json:"catalog_number"`
	Media         []releaseMediumDTO          `json:"media"`
	Credits       []releaseCreditDTO          `json:"credits"`
	Artwork       *releaseArtworkDTO          `json:"artwork"`
	Evidence      []releaseEvidenceSummaryDTO `json:"evidence"`
}

type releaseFieldEvidenceDTO struct {
	releaseEvidenceSummaryDTO
	Value      *string  `json:"value"`
	Candidates []string `json:"candidates"`
	ReasonCode *string  `json:"reason_code"`
}

type releaseGroupingEvidenceDTO struct {
	CandidateKind string   `json:"candidate_kind"`
	RuleID        string   `json:"rule_id"`
	SourceRefs    []string `json:"source_refs"`
	ReasonCodes   []string `json:"reason_codes"`
}

type releaseEvidenceResponseDTO struct {
	ReleaseID string                      `json:"release_id"`
	Fields    []releaseFieldEvidenceDTO   `json:"fields"`
	Grouping  *releaseGroupingEvidenceDTO `json:"grouping"`
	Truncated bool                        `json:"truncated"`
}

func (application *roomusicApplication) listReleases(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	page, pageSize, err := parsePagination(request)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	searchQuery, err := parseReleaseSearch(request)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_query", "搜索条件无效")
		return
	}
	attentionRequired, err := parseReleaseAttention(request)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_attention", "检查筛选条件无效")
		return
	}

	whereClause := " WHERE " + releaseVisiblePredicate
	queryArgs := []any{}
	if searchQuery != "" {
		whereClause += ` AND (
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
		queryArgs = append(queryArgs, escapeLikePattern(searchQuery))
	}
	if attentionRequired {
		whereClause += " AND " + releaseAttentionExpression + " > 0"
	}

	var total int64
	if err := application.database.connection.QueryRowContext(request.Context(), "SELECT COUNT(*) FROM releases"+whereClause, queryArgs...).Scan(&total); err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
		return
	}
	listQuery := "SELECT " + releaseSummaryProjection + " FROM releases" + whereClause +
		" ORDER BY LOWER(releases.title),LOWER(releases.artist),releases.id LIMIT $" + strconv.Itoa(len(queryArgs)+1) +
		" OFFSET $" + strconv.Itoa(len(queryArgs)+2)
	listArgs := append(append([]any{}, queryArgs...), pageSize, (page-1)*pageSize)
	rows, err := application.database.connection.QueryContext(request.Context(), listQuery, listArgs...)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
		return
	}
	defer rows.Close()
	items := make([]releaseSummaryDTO, 0, pageSize)
	for rows.Next() {
		item, scanErr := scanReleaseSummary(rows)
		if scanErr != nil {
			writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本")
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"items":      items,
		"pagination": map[string]any{"page": page, "page_size": pageSize, "total": total},
	})
}

func parseReleaseSearch(request *http.Request) (string, error) {
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	if len(query) > 200 {
		return "", fmt.Errorf("query too long")
	}
	return query, nil
}

func parseReleaseAttention(request *http.Request) (bool, error) {
	values, present := request.URL.Query()["attention"]
	if !present {
		return false, nil
	}
	if len(values) != 1 || values[0] != "required" {
		return false, fmt.Errorf("unsupported attention filter")
	}
	return true, nil
}

func escapeLikePattern(query string) string {
	query = strings.ReplaceAll(query, `~`, `~~`)
	query = strings.ReplaceAll(query, `%`, `~%`)
	return strings.ReplaceAll(query, `_`, `~_`)
}

func parsePagination(request *http.Request) (int, int, error) {
	page, pageSize := 1, 50
	var err error
	if pageValue := request.URL.Query().Get("page"); pageValue != "" {
		page, err = strconv.Atoi(pageValue)
		if err != nil || page < 1 {
			return 0, 0, fmt.Errorf("invalid page")
		}
	}
	if pageSizeValue := request.URL.Query().Get("page_size"); pageSizeValue != "" {
		pageSize, err = strconv.Atoi(pageSizeValue)
		if err != nil || pageSize < 1 || pageSize > maxReleaseRows {
			return 0, 0, fmt.Errorf("invalid page size")
		}
	}
	if page-1 > int(^uint(0)>>1)/pageSize {
		return 0, 0, fmt.Errorf("page offset exceeds bound")
	}
	return page, pageSize, nil
}

func scanReleaseSummary(row interface{ Scan(...any) error }) (releaseSummaryDTO, error) {
	var result releaseSummaryDTO
	var albumArtist, sourceType, mediaType sql.NullString
	var year sql.NullInt64
	err := row.Scan(
		&result.ID,
		&result.Title,
		&result.Artist,
		&albumArtist,
		&year,
		&sourceType,
		&mediaType,
		&result.MediumCount,
		&result.TrackCount,
		&result.AttentionCount,
	)
	if err != nil {
		return releaseSummaryDTO{}, err
	}
	result.AlbumArtist = stringPointerFromNull(albumArtist)
	result.Year = intPointerFromNull(year)
	result.SourceType = stringPointerFromNull(sourceType)
	result.MediaType = stringPointerFromNull(mediaType)
	return result, nil
}

func (application *roomusicApplication) releaseDetail(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	releaseID := request.PathValue("id")
	if !isValidIdentifier(releaseID) {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行版本标识无效")
		return
	}

	summary, err := application.loadReleaseSummary(request.Context(), releaseID)
	if err != nil {
		application.writeReleaseLoadError(responseWriter, request, err)
		return
	}
	var candidateKind, genre, catalogNumber sql.NullString
	err = application.database.connection.QueryRowContext(request.Context(), `SELECT candidate_kind,NULLIF(BTRIM(genre),''),NULLIF(BTRIM(catalog_number),'') FROM releases WHERE id=$1::uuid`, releaseID).Scan(&candidateKind, &genre, &catalogNumber)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取发行版本详情")
		return
	}

	media, err := application.loadReleaseMedia(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本详情")
		return
	}
	credits, err := application.loadReleaseCredits(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本署名")
		return
	}
	evidence, err := application.loadReleaseEvidenceSummary(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取整理证据")
		return
	}
	artwork, err := application.loadReleaseArtwork(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本封面")
		return
	}

	writeJSON(responseWriter, http.StatusOK, releaseDetailDTO{
		releaseSummaryDTO: summary,
		CandidateKind:     normalizeCandidateKind(candidateKind.String),
		Genre:             stringPointerFromNull(genre),
		CatalogNumber:     stringPointerFromNull(catalogNumber),
		Media:             media,
		Credits:           credits,
		Artwork:           artwork,
		Evidence:          evidence,
	})
}

func (application *roomusicApplication) loadReleaseSummary(ctx context.Context, releaseID string) (releaseSummaryDTO, error) {
	return scanReleaseSummary(application.database.connection.QueryRowContext(ctx,
		"SELECT "+releaseSummaryProjection+" FROM releases WHERE releases.id=$1::uuid AND "+releaseVisiblePredicate,
		releaseID,
	))
}

func (application *roomusicApplication) writeReleaseLoadError(responseWriter http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
		return
	}
	writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取发行版本")
}

func (application *roomusicApplication) loadReleaseMedia(ctx context.Context, releaseID string) ([]releaseMediumDTO, error) {
	rows, err := application.database.connection.QueryContext(ctx, `SELECT
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
	media := make([]releaseMediumDTO, 0)
	mediumIndexes := make(map[string]int)
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
			media = append(media, releaseMediumDTO{ID: mediumID, Position: mediumPosition, Title: mediumTitle, Tracks: []releaseTrackDTO{}})
		}
		track := releaseTrackDTO{
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
		}
		if strings.Contains(strings.ToLower(sourceKind), "cue") || cueIndex.Valid || cueEnd.Valid || isrc.Valid {
			track.Cue = &releaseCueDTO{
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
		media[mediumIndex].Tracks = append(media[mediumIndex].Tracks, track)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return media, nil
}

func (application *roomusicApplication) loadReleaseCredits(ctx context.Context, releaseID string) ([]releaseCreditDTO, error) {
	rows, err := application.database.connection.QueryContext(ctx, `SELECT role,name FROM release_credits WHERE release_id=$1::uuid ORDER BY position,role,name LIMIT $2`, releaseID, maxReleaseCredits+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	credits := make([]releaseCreditDTO, 0)
	for rows.Next() {
		if len(credits) == maxReleaseCredits {
			return nil, errors.New("release credit projection exceeds bound")
		}
		var credit releaseCreditDTO
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

func (application *roomusicApplication) loadReleaseEvidenceSummary(ctx context.Context, releaseID string) ([]releaseEvidenceSummaryDTO, error) {
	rows, err := application.database.connection.QueryContext(ctx, `SELECT field_key,selected_source,confidence,action,rule_id FROM release_field_decisions WHERE release_id=$1::uuid ORDER BY field_key LIMIT $2`, releaseID, maxReleaseEvidence+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	evidence := make([]releaseEvidenceSummaryDTO, 0)
	for rows.Next() {
		if len(evidence) == maxReleaseEvidence {
			return nil, errors.New("release evidence projection exceeds bound")
		}
		var item releaseEvidenceSummaryDTO
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

func (application *roomusicApplication) loadReleaseArtwork(ctx context.Context, releaseID string) (*releaseArtworkDTO, error) {
	var artwork releaseArtworkDTO
	err := application.database.connection.QueryRowContext(ctx, `SELECT storage_key,mime_type,width,height FROM release_artworks WHERE release_id=$1::uuid`, releaseID).Scan(&artwork.ResourceID, &artwork.MIME, &artwork.Width, &artwork.Height)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if artwork.ResourceID == "" || strings.ContainsAny(artwork.ResourceID, `/\\`) {
		return nil, errors.New("unsafe artwork resource identifier")
	}
	return &artwork, nil
}

func (application *roomusicApplication) releaseEvidence(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := application.requireAdmin(responseWriter, request); !ok {
		return
	}
	releaseID := request.PathValue("id")
	if !isValidIdentifier(releaseID) {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行版本标识无效")
		return
	}
	var exists bool
	if err := application.database.connection.QueryRowContext(request.Context(), `SELECT EXISTS(SELECT 1 FROM releases WHERE id=$1::uuid)`, releaseID).Scan(&exists); err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取整理证据")
		return
	}
	if !exists {
		writeAPIError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
		return
	}

	result, err := application.loadReleaseEvidence(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取整理证据")
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}

func (application *roomusicApplication) loadReleaseEvidence(ctx context.Context, releaseID string) (releaseEvidenceResponseDTO, error) {
	result := releaseEvidenceResponseDTO{ReleaseID: releaseID, Fields: []releaseFieldEvidenceDTO{}}
	rows, err := application.database.connection.QueryContext(ctx, `SELECT
		field_key,COALESCE(selected_value #>> '{}',''),selected_source,confidence,action,rule_id,candidates::text,COALESCE(reason,'')
		FROM release_field_decisions
		WHERE release_id=$1::uuid
		ORDER BY field_key
		LIMIT $2`, releaseID, maxReleaseEvidence+1)
	if err != nil {
		return releaseEvidenceResponseDTO{}, err
	}
	for rows.Next() {
		if len(result.Fields) == maxReleaseEvidence {
			result.Truncated = true
			break
		}
		var item releaseFieldEvidenceDTO
		var value, reason string
		var candidatesJSON sql.NullString
		if err := rows.Scan(&item.Field, &value, &item.Source, &item.Confidence, &item.Action, &item.RuleID, &candidatesJSON, &reason); err != nil {
			rows.Close()
			return releaseEvidenceResponseDTO{}, err
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
				return releaseEvidenceResponseDTO{}, decodeErr
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
		return releaseEvidenceResponseDTO{}, err
	}
	rows.Close()

	var grouping releaseGroupingEvidenceDTO
	var sourceRefsJSON, reason string
	err = application.database.connection.QueryRowContext(ctx, `SELECT candidate_kind,rule_id,source_refs::text,COALESCE(reason,'') FROM release_grouping_evidence WHERE release_id=$1::uuid ORDER BY observed_at DESC,id DESC LIMIT 1`, releaseID).Scan(&grouping.CandidateKind, &grouping.RuleID, &sourceRefsJSON, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return releaseEvidenceResponseDTO{}, err
	}
	grouping.CandidateKind = normalizeCandidateKind(grouping.CandidateKind)
	sourceRefs, truncated, err := decodeBoundedStringArray(sourceRefsJSON, maxEvidenceSourceRefs, true)
	if err != nil {
		return releaseEvidenceResponseDTO{}, err
	}
	grouping.SourceRefs = sourceRefs
	result.Truncated = result.Truncated || truncated
	grouping.ReasonCodes, truncated = splitBoundedReasonCodes(reason)
	result.Truncated = result.Truncated || truncated
	result.Grouping = &grouping
	return result, nil
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
			safe, ok := safeRelativeSourceRef(value)
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

func safeRelativeSourceRef(value string) (string, bool) {
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

func isValidIdentifier(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if value[index] != '-' {
				return false
			}
			continue
		}
		character := value[index]
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}
