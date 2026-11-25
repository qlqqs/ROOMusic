package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// CurationCursor 是发行在按加入时间倒序排列的可见曲库中的位置。
type CurationCursor struct {
	ID      string
	AddedAt time.Time
}

func PageCurationReleases(ctx context.Context, q Queryer, after *CurationCursor, limit int) ([]CurationCursor, error) {
	query := "SELECT releases.id::text,releases.created_at FROM releases WHERE " + releasesql.ReleaseVisiblePredicate
	args := []any{limit}
	if after != nil {
		query += " AND (releases.created_at,releases.id)<($2::timestamptz,$3::uuid)"
		args = append(args, after.AddedAt, after.ID)
	}
	rows, err := q.QueryContext(ctx, query+" ORDER BY releases.created_at DESC,releases.id DESC LIMIT $1", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var page []CurationCursor
	for rows.Next() {
		var cursor CurationCursor
		if err := rows.Scan(&cursor.ID, &cursor.AddedAt); err != nil {
			return nil, err
		}
		page = append(page, cursor)
	}
	return page, rows.Err()
}

type CurationSuppression struct {
	TargetType, TargetID, Subject, ValueHash string
}

// CurationObservation 保留选值的字段来源和扫描推断位。
type CurationObservation struct {
	Source   string
	Inferred bool
}

type CurationTrack struct {
	Authenticity                                                     map[string]metadata.Authenticity `json:"-"`
	EffectiveTitleAuth, EffectiveArtistAuth                          metadata.Authenticity            `json:"-"`
	OtherIdentifier                                                  bool                             `json:"-"`
	TitleObservation, ArtistObservation, PositionObservation         CurationObservation              `json:"-"`
	TitleCorrected, ArtistCorrected, TitleAutomatic, ArtistAutomatic bool                             `json:"-"`
	EffectiveTitle, EffectiveArtist                                  string                           `json:"-"`
	TitleInferred                                                    bool                             `json:"-"`
	ID, Title, Artist                                                string
	TitlePlaceholder, ArtistPlaceholder                              bool
	Disc, DiscNumber, Position                                       int
	PositionInferred                                                 bool
	DurationSeconds                                                  int

	SourceKind, SourceRootID     string
	CueSheetPath, ParentFile     string
	CueIndexFrames, CueEndFrames *int
	CueISRC                      string

	ExternalID                  string
	Identifiers                 map[string][]string
	LocalLyrics, ExternalLyrics bool
}

type CurationEvidence struct {
	Authenticity                                      map[string]metadata.Authenticity `json:"-"`
	EffectiveTitleAuth, EffectiveArtistAuth           metadata.Authenticity            `json:"-"`
	EffectiveYear                                     int                              `json:"-"`
	BarcodeAuthenticity                               []metadata.Authenticity          `json:"-"`
	FieldSources                                      map[string]string                `json:"-"`
	RawCatalogNumber                                  string                           `json:"-"`
	ID, GroupID, CandidateKind                        string
	Title, Artist, AlbumArtist                        string
	TitlePlaceholder, ArtistPlaceholder               bool
	Barcode, CatalogNumber, Label, MediaType, Edition string
	Barcodes, Genres                                  []string
	Year                                              int
	CatalogNumberIsMCN                                bool
	Corrected, Automatic                              map[string]bool

	ExternalID, ExternalGroupID                string
	GroupExternalID, GroupChangeSource         string
	LocalArtwork, ExternalArtwork, ArtworkFile bool

	Media        []int
	Tracks       []CurationTrack
	Suppressions []CurationSuppression
}

// LoadCurationEvidence 按页读取修正层与扫描层。传入受管封面存储的 Exists 可同时核对外部文件。
// 数据库连接上的多个查询共用一个只读快照；调用者也可以直接传入已打开的只读事务。
func LoadCurationEvidence(ctx context.Context, q Queryer, releaseIDs []string, artworkExists ...func(string) bool) (map[string]CurationEvidence, error) {
	if len(releaseIDs) == 0 {
		return map[string]CurationEvidence{}, nil
	}
	if db, ok := q.(ReadTransactionBeginner); ok {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		result, err := LoadCurationEvidence(ctx, tx, releaseIDs, artworkExists...)
		if err != nil {
			return nil, err
		}
		return result, tx.Commit()
	}
	result, err := curationHeaders(ctx, q, releaseIDs, artworkExists)
	if err != nil {
		return nil, err
	}
	if err := loadCurationTracks(ctx, q, releaseIDs, result, true); err != nil {
		return nil, err
	}
	if err := loadCurationSuppressions(ctx, q, releaseIDs, result); err != nil {
		return nil, err
	}
	return result, nil
}

// 发行的外部专辑身份来自写入当前对应的操作，旧的无操作对应没有该证据。
const curationReleaseLinkJoin = `
 LEFT JOIN external_links rl ON rl.source='musicbrainz' AND rl.object_type='release' AND rl.object_id=releases.id
 LEFT JOIN operations op ON op.id=rl.operation_id AND op.operation_type='external_link'
     AND op.target_type='release' AND op.target_id=releases.id
 LEFT JOIN LATERAL (
     SELECT item.external_group_id FROM jsonb_to_recordset(op.after_state->'links')
         AS item(object_type text,object_id uuid,external_id text,external_group_id text)
     WHERE item.object_type='release' AND item.object_id=releases.id AND item.external_id=rl.external_id
     LIMIT 1
 ) rg ON true`

const curationHeaderSQL = `SELECT releases.id::text,releases.group_id::text,COALESCE(releases.candidate_kind,''),
 releases.artist,COALESCE(releases.media_type,''),
 jsonb_build_object('title',releases.title,'album_artist',releases.album_artist,'year',releases.year,
    'genre',releases.genres,'label',releases.label,'catalog_number',releases.catalog_number,
    'barcode',releases.barcode,'edition',releases.edition) || COALESCE(c.values,'{}'::jsonb),
 COALESCE(c.present,'{}'::jsonb),
 COALESCE(a.values,'{}'::jsonb),
 COALESCE((SELECT jsonb_object_agg(field_key,selected_source) FROM release_field_decisions
     WHERE release_id=releases.id),'{}'::jsonb),
 COALESCE((SELECT jsonb_agg(position ORDER BY position,id) FROM media WHERE release_id=releases.id),'[]'::jsonb),
 lo.release_id IS NOT NULL,ea.storage_key,COALESCE(rl.external_id,''),COALESCE(rg.external_group_id,''),
 COALESCE(gl.external_id,''),
 COALESCE((SELECT COALESCE(NULLIF(o.origin->>'source',''),'rest') FROM operations o
    WHERE o.operation_type='release_group_change' AND o.target_type='release' AND o.target_id=releases.id
      AND NOT EXISTS(SELECT 1 FROM operations undo WHERE undo.reverts_operation_id=o.id)
    ORDER BY o.result_revision DESC LIMIT 1),'')
 FROM releases
 LEFT JOIN LATERAL (
    SELECT jsonb_object_agg(field,COALESCE(to_jsonb(text_value),to_jsonb(year_value),to_jsonb(list_value))) AS values,
           jsonb_object_agg(field,true) AS present FROM release_corrections WHERE release_id=releases.id
 ) c ON true
 LEFT JOIN LATERAL (
    SELECT jsonb_object_agg(field,COALESCE(to_jsonb(text_value),to_jsonb(year_value),to_jsonb(list_value))) AS values
    FROM release_auto_values WHERE release_id=releases.id
 ) a ON true
 LEFT JOIN release_artworks lo ON lo.release_id=releases.id
 LEFT JOIN release_external_artworks ea ON ea.release_id=releases.id
 LEFT JOIN external_links gl ON gl.source='musicbrainz' AND gl.object_type='release_group' AND gl.object_id=releases.group_id` +
	curationReleaseLinkJoin + ` WHERE releases.id=ANY($1::uuid[]) AND ` + releasesql.ReleaseVisiblePredicate

func curationHeaders(ctx context.Context, q Queryer, ids []string, artworkExists []func(string) bool) (map[string]CurationEvidence, error) {
	rows, err := q.QueryContext(ctx, curationHeaderSQL, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]CurationEvidence, len(ids))
	for rows.Next() {
		var e CurationEvidence
		var values, corrected, automatic, decisions, media []byte
		var artworkKey sql.NullString
		if err := rows.Scan(&e.ID, &e.GroupID, &e.CandidateKind, &e.Artist, &e.MediaType, &values, &corrected,
			&automatic, &decisions, &media, &e.LocalArtwork, &artworkKey, &e.ExternalID, &e.ExternalGroupID,
			&e.GroupExternalID, &e.GroupChangeSource); err != nil {
			return nil, err
		}
		var fields struct {
			Title, Label, Barcode, Edition string
			AlbumArtist                    string `json:"album_artist"`
			CatalogNumber                  string `json:"catalog_number"`
			Genre                          []string
			Year                           int
		}
		var sources map[string]string
		var autoValues map[string]json.RawMessage
		for _, column := range []struct {
			raw    []byte
			target any
		}{{values, &fields}, {corrected, &e.Corrected}, {automatic, &autoValues}, {decisions, &sources}, {media, &e.Media}} {
			if err := json.Unmarshal(column.raw, column.target); err != nil {
				return nil, err
			}
		}
		e.Title, e.AlbumArtist, e.Year, e.Genres = fields.Title, fields.AlbumArtist, fields.Year, fields.Genre
		e.Barcode, e.CatalogNumber, e.Label, e.Edition = fields.Barcode, fields.CatalogNumber, fields.Label, fields.Edition
		e.FieldSources, e.RawCatalogNumber = sources, fields.CatalogNumber
		e.Automatic = make(map[string]bool, len(autoValues))
		for name := range autoValues {
			e.Automatic[name] = true
		}
		e.Authenticity = make(map[string]metadata.Authenticity)
		for name, value := range map[string]string{"title": e.Title, "artist": e.Artist, "album_artist": e.AlbumArtist,
			"label": e.Label, "barcode": e.Barcode, "catalog_number": e.CatalogNumber, "media_type": e.MediaType,
			"year": positiveNumber(e.Year), "edition": e.Edition, "genre": strings.Join(e.Genres, " ")} {
			field := name
			if name == "title" {
				field = "release_title"
			}
			e.Authenticity[name] = metadata.FieldAuthenticity(field, value, sources[name], false, e.Corrected[name])
		}
		if e.Corrected["album_artist"] && e.AlbumArtist != "" {
			e.Artist = e.AlbumArtist
			e.Authenticity["artist"] = e.Authenticity["album_artist"]
		}
		e.TitlePlaceholder = e.Authenticity["title"] == metadata.Placeholder
		e.ArtistPlaceholder = e.SelectedArtistAuth() == metadata.Placeholder
		e.EffectiveTitleAuth, e.EffectiveArtistAuth, e.EffectiveYear = e.Authenticity["title"], e.SelectedArtistAuth(), e.Year
		for name, dest := range map[string]*metadata.Authenticity{"title": &e.EffectiveTitleAuth, "album_artist": &e.EffectiveArtistAuth} {
			if raw, ok := autoValues[name]; ok && !e.Corrected[name] {
				var value string
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, err
				}
				*dest = metadata.FieldAuthenticity(name, value, "automatic", false, false)
			}
		}
		if raw, ok := autoValues["year"]; ok && !e.Corrected["year"] {
			e.EffectiveYear = 0
			if err := json.Unmarshal(raw, &e.EffectiveYear); err != nil {
				return nil, err
			}
		}
		e.setCurationBarcodes(fields.Barcode, sources["catalog_number"] == "cue_sheet" && !e.Corrected["catalog_number"])
		for _, barcode := range e.Barcodes {
			auth := e.Authenticity["catalog_number"]
			if barcode == strings.TrimSpace(fields.Barcode) {
				direct := e.Authenticity["barcode"]
				if !e.CatalogNumberIsMCN || barcode != strings.TrimSpace(e.RawCatalogNumber) || direct == metadata.Real || auth == metadata.Placeholder {
					auth = direct
				}
			}
			e.BarcodeAuthenticity = append(e.BarcodeAuthenticity, auth)
		}
		e.ExternalArtwork = artworkKey.Valid
		if artworkKey.Valid && len(artworkExists) != 0 && artworkExists[0] != nil {
			e.ArtworkFile = artworkExists[0](artworkKey.String)
		}
		result[e.ID] = e
	}
	return result, rows.Err()
}

func positiveNumber(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func (e CurationEvidence) SelectedArtistAuth() metadata.Authenticity {
	if e.AlbumArtist != "" {
		return e.Authenticity["album_artist"]
	}
	return e.Authenticity["artist"]
}

func (e *CurationEvidence) setCurationBarcodes(barcode string, fromCue bool) {
	barcode = strings.TrimSpace(barcode)
	if barcode != "" && strings.Trim(barcode, "0") != "" {
		e.Barcodes = append(e.Barcodes, barcode)
	}
	number := strings.TrimSpace(e.CatalogNumber)
	if number == "" || (!fromCue && len(number) != 12 && len(number) != 13) || strings.Trim(number, "0123456789") != "" {
		return
	}
	e.CatalogNumberIsMCN = true
	e.CatalogNumber = ""
	if strings.Trim(number, "0") != "" && number != barcode {
		e.Barcodes = append(e.Barcodes, number)
	}
}

const curationTrackSQL = `SELECT m.release_id::text,t.id::text,COALESCE(ct.text_value,t.title),COALESCE(ca.text_value,t.artist),
 COALESCE(ct.text_value,atitle.text_value,t.title),COALESCE(ca.text_value,aa.text_value,t.artist),
 COALESCE((SELECT jsonb_object_agg(field_name,jsonb_build_object('source',source_kind,'inferred',inferred,'value',value))
    FROM track_observations WHERE track_id=t.id AND field_name IS NOT NULL
    AND ($2 OR field_name IN ('title','artist','track_number','disc_number'))),'{}'::jsonb),
 ct.track_id IS NOT NULL,ca.track_id IS NOT NULL,atitle.track_id IS NOT NULL,aa.track_id IS NOT NULL,
 m.position,t.disc_number,t.position,
 EXISTS(SELECT FROM track_identifiers WHERE track_id=t.id AND kind IN ('mb_work','acoustid') AND value<>''),
 COALESCE(round((CASE WHEN dd.duration_seconds>0 AND dd.duration_seconds<'Infinity'::float8
     THEN dd.duration_seconds END)::numeric),0)::bigint,
 t.source_kind,t.source_root_id::text,COALESCE(t.cue_sheet_path,''),COALESCE(t.cue_parent_relative_path,''),COALESCE(t.cue_isrc,''),
 t.cue_index_frames,t.cue_end_frames,CASE WHEN $2 THEN t.lyrics END,el.track_id IS NOT NULL,COALESCE(tl.external_id,''),
 COALESCE((SELECT jsonb_object_agg(tags.kind,tags.ids) FROM (
     SELECT kind,jsonb_agg(value ORDER BY ordinal,value) AS ids FROM track_identifiers
     WHERE track_id=t.id AND kind IN ('mb_release','mb_release_group','mb_recording','mb_track','mb_artist','mb_album_artist','mb_disc','isrc')
     GROUP BY kind) tags),'{}'::jsonb)
 FROM media m JOIN tracks t ON t.medium_id=m.id AND t.source_status='present'
 LEFT JOIN track_corrections ct ON ct.track_id=t.id AND ct.field='title'
 LEFT JOIN track_corrections ca ON ca.track_id=t.id AND ca.field='artist'
 LEFT JOIN track_auto_values atitle ON atitle.track_id=t.id AND atitle.field='title'
 LEFT JOIN track_auto_values aa ON aa.track_id=t.id AND aa.field='artist'
 LEFT JOIN track_display_durations dd ON dd.track_id=t.id
 LEFT JOIN track_external_lyrics el ON el.track_id=t.id
 LEFT JOIN external_links tl ON tl.source='musicbrainz' AND tl.object_type='track' AND tl.object_id=t.id
 WHERE m.release_id=ANY($1::uuid[]) ORDER BY m.release_id,m.position,t.position,t.id`

func loadCurationTracks(ctx context.Context, q Queryer, ids []string, result map[string]CurationEvidence, withDetails bool) error {
	rows, err := q.QueryContext(ctx, curationTrackSQL, ids, withDetails)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var t CurationTrack
		var releaseID string
		var lyrics sql.NullString
		var identifiers, observations []byte
		if err := rows.Scan(&releaseID, &t.ID, &t.Title, &t.Artist, &t.EffectiveTitle, &t.EffectiveArtist,
			&observations, &t.TitleCorrected, &t.ArtistCorrected, &t.TitleAutomatic, &t.ArtistAutomatic, &t.Disc, &t.DiscNumber, &t.Position,
			&t.OtherIdentifier, &t.DurationSeconds, &t.SourceKind, &t.SourceRootID, &t.CueSheetPath, &t.ParentFile, &t.CueISRC,
			&t.CueIndexFrames, &t.CueEndFrames, &lyrics, &t.ExternalLyrics, &t.ExternalID, &identifiers); err != nil {
			return err
		}
		var observed map[string]struct {
			CurationObservation
			Value string
		}
		if err := json.Unmarshal(observations, &observed); err != nil {
			return err
		}
		t.TitleObservation = observed["title"].CurationObservation
		t.ArtistObservation = observed["artist"].CurationObservation
		t.PositionObservation = observed["track_number"].CurationObservation
		auth := func(name, value string, corrected bool) metadata.Authenticity {
			o := observed[name]
			return metadata.FieldAuthenticity(name, value, o.Source, o.Inferred, corrected)
		}
		t.Authenticity = make(map[string]metadata.Authenticity, len(observed)+3)
		for name, o := range observed {
			t.Authenticity[name] = auth(name, o.Value, false)
		}
		t.Authenticity["title"] = auth("title", t.Title, t.TitleCorrected)
		t.Authenticity["artist"] = auth("artist", t.Artist, t.ArtistCorrected)
		t.Authenticity["position"] = auth("track_number", positiveNumber(t.Position), false)
		t.Authenticity["disc"] = auth("disc_number", positiveNumber(t.DiscNumber), false)
		t.Authenticity["duration_seconds"] = metadata.FieldAuthenticity("duration", positiveNumber(t.DurationSeconds), "", false, false)
		t.TitlePlaceholder = t.Authenticity["title"] == metadata.Placeholder
		t.ArtistPlaceholder = t.Authenticity["artist"] == metadata.Placeholder
		t.TitleInferred = t.Authenticity["title"] == metadata.Inferred
		t.PositionInferred = t.Authenticity["position"] != metadata.Real
		t.EffectiveTitleAuth, t.EffectiveArtistAuth = t.Authenticity["title"], t.Authenticity["artist"]
		if t.TitleAutomatic && !t.TitleCorrected {
			t.EffectiveTitleAuth = metadata.Inferred
		}
		if t.ArtistAutomatic && !t.ArtistCorrected {
			t.EffectiveArtistAuth = metadata.Inferred
		}
		if err := json.Unmarshal(identifiers, &t.Identifiers); err != nil {
			return err
		}
		for kind := range t.Identifiers {
			t.Authenticity[kind] = metadata.Real
		}
		if t.CueISRC != "" {
			t.Authenticity["cue_isrc"] = metadata.Real
		}
		if lyrics.Valid {
			_, t.LocalLyrics = ParseLyrics(lyrics.String)
		}
		if e, visible := result[releaseID]; visible {
			e.Tracks = append(e.Tracks, t)
			result[releaseID] = e
		}
	}
	return rows.Err()
}

func loadCurationSuppressions(ctx context.Context, q Queryer, ids []string, result map[string]CurationEvidence) error {
	rows, err := q.QueryContext(ctx, `WITH targets AS (
	    SELECT id AS release_id,'release' AS kind,id AS target FROM releases WHERE id=ANY($1::uuid[])
	    UNION ALL SELECT id,'release_group',group_id FROM releases WHERE id=ANY($1::uuid[])
	    UNION ALL SELECT m.release_id,'track',t.id FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=ANY($1::uuid[])
	) SELECT t.release_id::text,s.target_type,s.target_id::text,s.subject,s.value_hash
	FROM targets t JOIN curation_suppressions s ON s.target_type=t.kind AND s.target_id=t.target
	ORDER BY t.release_id,s.target_type,s.target_id,s.subject,s.value_hash`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var s CurationSuppression
		if err := rows.Scan(&id, &s.TargetType, &s.TargetID, &s.Subject, &s.ValueHash); err != nil {
			return err
		}
		if e, visible := result[id]; visible {
			e.Suppressions = append(e.Suppressions, s)
			result[id] = e
		}
	}
	return rows.Err()
}

func VisibleCurationGroups(ctx context.Context, q Queryer, groupIDs []string) ([]string, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT releases.group_id FROM releases
	WHERE releases.group_id=ANY($1::uuid[]) AND `+releasesql.ReleaseVisiblePredicate+` ORDER BY releases.group_id`, groupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		groups = append(groups, id)
	}
	return groups, rows.Err()
}

// CurationGroupMembersAgree 核对同组其他可见且已匹配的成员，包括缺少外部专辑证据的旧对应。
func CurationGroupMembersAgree(ctx context.Context, q Queryer, groupID, exceptRelease, externalGroupID string) (bool, error) {
	var agree bool
	err := q.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM releases `+curationReleaseLinkJoin+`
	 WHERE releases.group_id=$1::uuid AND releases.id<>$2::uuid AND rl.object_id IS NOT NULL
	   AND rg.external_group_id IS DISTINCT FROM $3::text AND `+releasesql.ReleaseVisiblePredicate+`)`, groupID, exceptRelease, externalGroupID).Scan(&agree)
	return agree, err
}
