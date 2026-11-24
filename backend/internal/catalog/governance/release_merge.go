package governance

import (
	"context"
	"database/sql"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/personal/albumfavorites"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type MergeReleaseRef struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	GroupID string `json:"group_id"`
}

type MergePreviewMedium struct {
	MediumID       string `json:"medium_id"`
	FromRelease    string `json:"from_release"`
	Title          string `json:"title"`
	PositionAfter  int    `json:"position_after"`
	PositionBefore int    `json:"-"`
	OriginPosition int    `json:"-"`
	TrackCount     int    `json:"track_count"`
}

type MergeHidden struct {
	Corrections     int  `json:"corrections"`
	Reviews         int  `json:"reviews"`
	DefaultVersion  bool `json:"default_version"`
	ExternalArtwork bool `json:"external_artwork"`
	Links           int  `json:"links"`
	ArtistOverrides int  `json:"artist_overrides"`
}

type ReleaseMergePreview struct {
	Target         MergeReleaseRef      `json:"target"`
	Source         MergeReleaseRef      `json:"source"`
	Media          []MergePreviewMedium `json:"media"`
	Hidden         MergeHidden          `json:"hidden"`
	FavoritesMoved int                  `json:"favorites_moved"`
	Token          string               `json:"token"`
}

type mergeMedium struct {
	MergePreviewMedium
	trackDigest string
}

type mergeSnapshot struct {
	target, source     MergeReleaseRef
	sourceRevision     int64
	sourceGroupVisible bool
	media              []mergeMedium
	hidden             MergeHidden
	favorites          int
}

func validMergeIDs(target, source string) error {
	if !uuid.Valid(target) || !uuid.Valid(source) || strings.EqualFold(target, source) {
		return operations.Invalid("invalid_input", "请选择两个不同的有效发行版本")
	}
	return nil
}

func loadMergeSnapshot(ctx context.Context, q operations.Queryer, targetID, sourceID string) (mergeSnapshot, error) {
	var state mergeSnapshot
	infos, err := readReleaseInfos(ctx, q, []string{targetID, sourceID})
	if err != nil {
		return state, err
	}
	target, hasTarget := infos[targetID]
	source, hasSource := infos[sourceID]
	if !hasTarget || !hasSource {
		return state, operations.NotFound("not_found", "发行版本不存在")
	}

	var merged, chain bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM release_merges WHERE source_release_id IN ($1,$2)),
		EXISTS(SELECT 1 FROM release_merges WHERE target_release_id=$2)`, targetID, sourceID).Scan(&merged, &chain); err != nil {
		return state, err
	}
	switch {
	case merged:
		return state, operations.Conflict("release_merged", "发行已经并入其他发行")
	case chain:
		return state, operations.Conflict("merge_chain", "已有其他发行并入来源发行")
	case !target.info.Visible || !source.info.Visible:
		return state, operations.NotFound("not_found", "发行版本不存在")
	}
	state.target = MergeReleaseRef{targetID, target.info.Title, target.info.GroupID}
	state.source = MergeReleaseRef{sourceID, source.info.Title, source.info.GroupID}
	state.sourceRevision = source.revision
	if err := checkMergeLimits(ctx, q, targetID, sourceID); err != nil {
		return state, err
	}

	// 已并入的碟取回扫描碟号；新来源排在已有来源之后。
	rows, err := q.QueryContext(ctx, `SELECT m.id::text, COALESCE(m.origin_release_id,m.release_id)::text,m.title,
		m.position,COALESCE(m.origin_position,m.position),members.count,members.digest
		FROM media m LEFT JOIN release_merges r ON r.source_release_id=m.origin_release_id
		CROSS JOIN LATERAL (SELECT count(*) AS count,
			encode(sha256(convert_to(COALESCE(string_agg(t.id::text,',' ORDER BY t.id),''),'UTF8')),'hex') AS digest
			FROM tracks t WHERE t.medium_id=m.id AND t.source_status='present') members
		WHERE m.release_id IN ($1,$2)
		ORDER BY m.release_id=$2,m.origin_release_id IS NOT NULL,r.merged_at,m.origin_release_id,
			COALESCE(m.origin_position,m.position),m.id`, targetID, sourceID)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	for rows.Next() {
		var m mergeMedium
		if err := rows.Scan(&m.MediumID, &m.FromRelease, &m.Title, &m.PositionBefore, &m.OriginPosition, &m.TrackCount, &m.trackDigest); err != nil {
			return state, err
		}
		state.media = append(state.media, m)
	}
	if err := rows.Err(); err != nil {
		return state, err
	}

	h := &state.hidden
	err = q.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM releases WHERE group_id=$2 AND id<>$1 AND `+releasesql.ReleaseVisiblePredicate+`),
		(SELECT count(*) FROM release_corrections WHERE release_id=$1),
		(SELECT count(*) FROM release_issue_reviews WHERE release_id=$1),
		EXISTS(SELECT 1 FROM release_groups WHERE id=$2 AND default_release_id=$1),
		EXISTS(SELECT 1 FROM release_external_artworks WHERE release_id=$1),
		(SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1),
		(SELECT count(*) FROM artist_assignment_overrides WHERE release_id=$1)`, sourceID, state.source.GroupID).
		Scan(&state.sourceGroupVisible, &h.Corrections, &h.Reviews, &h.DefaultVersion, &h.ExternalArtwork, &h.Links, &h.ArtistOverrides)
	if err != nil {
		return state, err
	}
	if !state.sourceGroupVisible {
		state.favorites, err = albumfavorites.CountAlbumFavorites(ctx, q, state.source.GroupID)
	}
	return state, err
}

func checkMergeLimits(ctx context.Context, q operations.Queryer, target, source string) error {
	var media, tracks, credits int
	err := q.QueryRowContext(ctx, `WITH discs AS MATERIALIZED (SELECT id FROM media WHERE release_id IN ($1,$2)),
		present AS MATERIALIZED (SELECT t.id FROM tracks t JOIN discs d ON d.id=t.medium_id WHERE t.source_status='present')
		SELECT (SELECT count(*) FROM discs),(SELECT count(*) FROM present),
			(SELECT count(*) FROM track_credits c JOIN present t ON t.id=c.track_id)`, target, source).Scan(&media, &tracks, &credits)
	if err != nil {
		return err
	}
	if media > catalog.MaxReleaseMedia || tracks > catalog.MaxReleaseTrackRows || credits > catalog.MaxReleaseTrackCredits {
		return operations.Conflict("release_too_large", "并入后超过发行的碟、曲目或曲目署名上限")
	}
	return nil
}

func buildMergePlan(s mergeSnapshot) ReleaseMergePreview {
	p := ReleaseMergePreview{Target: s.target, Source: s.source, Hidden: s.hidden, FavoritesMoved: s.favorites, Media: make([]MergePreviewMedium, 0, len(s.media))}
	type mediumToken struct {
		ID, From, TrackDigest    string
		Position, OriginPosition int
	}
	basis := make([]mediumToken, 0, len(s.media))
	last := 0
	for _, m := range s.media {
		pos := m.PositionBefore
		if m.FromRelease != s.target.ID {
			pos = ingest.NextMergedMediaPosition(last, m.OriginPosition)
		}
		last = max(last, pos)
		m.PositionAfter = pos
		p.Media = append(p.Media, m.MergePreviewMedium)
		basis = append(basis, mediumToken{m.MediumID, m.FromRelease, m.trackDigest, m.PositionBefore, m.OriginPosition})
	}
	p.Token = operations.Fingerprint(map[string]any{"target": s.target.ID, "source": s.source.ID,
		"target_group": s.target.GroupID, "source_group": s.source.GroupID, "source_group_visible": s.sourceGroupVisible, "media": basis})
	return p
}

func (s *Store) PreviewReleaseMerge(ctx context.Context, targetID, sourceID string) (ReleaseMergePreview, error) {
	if err := validMergeIDs(targetID, sourceID); err != nil {
		return ReleaseMergePreview{}, err
	}
	read := func(q operations.Queryer) (ReleaseMergePreview, error) {
		state, err := loadMergeSnapshot(ctx, q, strings.ToLower(targetID), strings.ToLower(sourceID))
		if err != nil {
			return ReleaseMergePreview{}, err
		}
		return buildMergePlan(state), nil
	}
	if db, ok := s.database.(catalog.ReadTransactionBeginner); ok {
		return catalog.ReadWithLocalJITOff(ctx, db, func(tx *sql.Tx) (ReleaseMergePreview, error) { return read(tx) })
	}
	return read(s.database)
}
