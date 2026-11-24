package catalog

import (
	"context"
	"database/sql"
	"errors"
)

// ArtistMediaFamilyCTE 共用治理写入时已扁平化的重定向。A→B→C 时，A 与 B 的
// redirect_operation_id 都指向最后一次合并，资料排序要取各自作为来源身份的合并时刻。
const ArtistMediaFamilyCTE = `artist_media_family(canonical_id,artist_id,merged_at) AS NOT MATERIALIZED (
 SELECT a.id,a.id,NULL::timestamptz FROM artists a
 WHERE NOT EXISTS (SELECT 1 FROM artist_overrides o WHERE o.artist_id=a.id AND o.redirect_artist_id IS NOT NULL)
 UNION ALL
 SELECT o.redirect_artist_id,o.artist_id,
 CASE WHEN op.response #>> '{source,id}'=o.artist_id::text THEN op.created_at
 ELSE COALESCE((
   SELECT m.created_at FROM operations_artist_targets t JOIN operations m ON m.id=t.operation_id
   WHERE t.artist_id=o.artist_id AND m.operation_type='artist_merge'
     AND m.response #>> '{source,id}'=o.artist_id::text AND (m.created_at,m.id)<=(op.created_at,op.id)
     AND NOT EXISTS (SELECT 1 FROM operations r WHERE r.reverts_operation_id=m.id)
   ORDER BY m.created_at DESC,m.id DESC LIMIT 1
 ),op.created_at) END
 FROM artist_overrides o
 LEFT JOIN operations op ON op.id=o.redirect_operation_id WHERE o.redirect_artist_id IS NOT NULL
)`

// ArtistMediaOrder 在同层内先取有效身份，再按并入时间选择资料。
const ArtistMediaOrder = `(f.artist_id=f.canonical_id) DESC, f.merged_at DESC NULLS LAST, f.artist_id`

// ArtistImageRowsSQL 供摘要、详情与“标记不对”共用两层图片的字段。
const ArtistImageRowsSQL = `SELECT artist_id,operation_id,storage_key,'manual'::text AS source,
 NULL::text AS source_url,NULL::text AS author,NULL::text AS license,NULL::text AS license_url
 FROM artist_manual_artworks
 UNION ALL SELECT artist_id,operation_id,storage_key,source,source_url,author,license,license_url
 FROM artist_external_artworks`

// ArtistBioRowsSQL 供详情与“标记不对”共用两层简介的字段。
const ArtistBioRowsSQL = `SELECT artist_id,operation_id,text,lang,'manual'::text AS source,
 NULL::text AS source_url,NULL::text AS license,NULL::text AS license_url FROM artist_manual_bios
 UNION ALL SELECT artist_id,operation_id,text,lang,source,source_url,license,license_url FROM artist_external_bios`

const artistLayerOrder = `(m.source='manual') DESC, ` + ArtistMediaOrder

func (s *Store) readArtistMedia(ctx context.Context, canonical string, result *ArtistDetail) error {
	var image ArtistImage
	err := s.database.QueryRowContext(ctx, `WITH `+ArtistMediaFamilyCTE+`
 SELECT m.storage_key,m.source,m.source_url,m.author,m.license,m.license_url
 FROM artist_media_family f JOIN (`+ArtistImageRowsSQL+`) m ON m.artist_id=f.artist_id
 WHERE f.canonical_id=$1::uuid ORDER BY `+artistLayerOrder+` LIMIT 1`, canonical).
		Scan(&image.Resource, &image.Source, &image.SourceURL, &image.Author, &image.License, &image.LicenseURL)
	if err == nil {
		result.Image = &image
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var bio ArtistBio
	err = s.database.QueryRowContext(ctx, `WITH `+ArtistMediaFamilyCTE+`
 SELECT m.text,m.lang,m.source,m.source_url,m.license,m.license_url
 FROM artist_media_family f JOIN (`+ArtistBioRowsSQL+`) m ON m.artist_id=f.artist_id
 WHERE f.canonical_id=$1::uuid ORDER BY `+artistLayerOrder+` LIMIT 1`, canonical).
		Scan(&bio.Text, &bio.Lang, &bio.Source, &bio.SourceURL, &bio.License, &bio.LicenseURL)
	if err == nil {
		result.Bio = &bio
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return s.database.QueryRowContext(ctx, `WITH `+ArtistMediaFamilyCTE+`, links AS (
 SELECT DISTINCT ON (l.source) l.source,l.external_id
 FROM artist_media_family f JOIN external_links l ON l.object_id=f.artist_id
 WHERE f.canonical_id=$1::uuid AND l.object_type='artist' AND l.source IN ('musicbrainz','wikidata')
 ORDER BY l.source,`+ArtistMediaOrder+`
 ) SELECT max(external_id) FILTER (WHERE source='musicbrainz'),
          max(external_id) FILTER (WHERE source='wikidata') FROM links`, canonical).
		Scan(&result.Links.MusicBrainz, &result.Links.Wikidata)
}
