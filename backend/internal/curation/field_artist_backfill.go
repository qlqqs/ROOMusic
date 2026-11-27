package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// 旧字段回执没有位置依据。只取当前版本对应存续期间由整理写入、仍然生效且有艺人缺对应的字段。
func BackfillFieldArtistLinks(ctx context.Context, tx *sql.Tx, releaseID, externalID string, sourcesByRecording map[string]*ReleaseArtistCredit) error {
	if _, err := governance.LockAutoRelease(ctx, tx, releaseID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `WITH fields AS (
		SELECT '' AS track, '' AS recording, a.operation_id,a.text_value,a.writer_level,r.created_at AS linked_at
		FROM external_links r JOIN release_auto_values a ON a.release_id=r.object_id AND a.field='album_artist'
		WHERE r.source='musicbrainz' AND r.object_type='release' AND r.object_id=$1 AND r.external_id=$2 AND a.mode='set'
		UNION ALL
		SELECT t.id::text,l.external_id,a.operation_id,a.text_value,a.writer_level,greatest(r.created_at,l.created_at)
		FROM external_links r JOIN media m ON m.release_id=r.object_id JOIN tracks t ON t.medium_id=m.id AND t.source_status='present'
		JOIN external_links l ON l.source='musicbrainz' AND l.object_type='track' AND l.object_id=t.id
		JOIN track_auto_values a ON a.track_id=t.id AND a.field='artist' AND a.mode='set'
		WHERE r.source='musicbrainz' AND r.object_type='release' AND r.object_id=$1 AND r.external_id=$2
	)
	SELECT f.track,f.recording,o.id,o.idempotency_key,owner.idempotency_key,f.text_value,f.writer_level,
	o.origin->>'basis',o.origin->>'provider' FROM fields f
	JOIN operations o ON o.id=f.operation_id AND o.target_id=$1 AND o.created_at>=f.linked_at
	JOIN external_links r ON r.source='musicbrainz' AND r.object_type='release' AND r.object_id=$1 AND r.external_id=$2
	JOIN operations owner ON owner.id=r.operation_id
	WHERE NOT (o.response ? 'artist_credit') AND (o.origin->>'source'='curation' OR (o.origin->>'source' IN ('chat','mcp') AND o.origin->>'provider'='steward')) AND
	((f.writer_level=4 AND o.origin->>'provider_kind'='source') OR (f.writer_level=3 AND o.origin->>'provider_kind'='model'))
	AND EXISTS (SELECT FROM artist_effective_credits c JOIN artist_credits fact ON fact.id=c.credit_id AND fact.layer='auto'
	WHERE ((f.track='' AND c.role='album_artist' AND c.release_id=$1) OR (c.role='track_artist' AND c.track_id=NULLIF(f.track,'')::uuid))
	AND NOT EXISTS (SELECT FROM external_links a WHERE a.source='musicbrainz' AND a.object_type='artist' AND a.object_id=c.artist_id))
	ORDER BY f.track`, releaseID, externalID)
	if err != nil {
		return err
	}
	type field struct {
		track, recording, op, key, owner, value, basis, provider string
		level                                                    catalog.AutoWriterLevel
	}
	var fields []field
	for rows.Next() {
		var f field
		if err := rows.Scan(&f.track, &f.recording, &f.op, &f.key, &f.owner, &f.value, &f.level, &f.basis, &f.provider); err != nil {
			rows.Close()
			return err
		}
		fields = append(fields, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var ids []string
	for _, f := range fields {
		src := sourcesByRecording[f.recording]
		if src == nil {
			continue
		}
		step := "fields"
		if f.track != "" {
			step = "track_fields/" + f.track
		}
		// 程序回执保留发行编号，旧曲目回填只按当时生效的对应写入。
		// 模型匹配字段键从版本对应的操作键派生；名字核实采用 external_value 时已核对当前对应。
		fromRelease := f.provider == "musicbrainz" && f.level == catalog.LevelProgramExternal &&
			(strings.HasPrefix(f.basis, "MusicBrainz 发行 "+externalID+"，依据") || f.track != "" && f.basis == "按已有 MusicBrainz 对应补全曲目字段")
		verified := f.level == catalog.LevelModelExternal && f.basis == "external_value"
		fromMatch := (f.level == catalog.LevelModelExternal || f.level == catalog.LevelProgramExternal && f.provider == "steward") && f.key == DerivedKey(f.owner, step)
		if !fromRelease && !verified && !fromMatch {
			continue
		}
		var name strings.Builder
		for _, a := range src.Artists {
			name.WriteString(a.Name)
			name.WriteString(a.JoinPhrase)
		}
		if metadata.NormalizeValue(name.String()) != f.value && !(verified && normalizedName(name.String()) == normalizedName(f.value)) {
			continue
		}
		proof, err := fieldArtistCredit(ctx, tx, src, f.level, releaseID, f.track)
		if err != nil {
			return err
		}
		if proof == nil {
			continue
		}
		raw, err := json.Marshal(proof)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET response=jsonb_set(response,'{artist_credit}',$2::jsonb) WHERE id=$1`, f.op, raw); err != nil {
			return err
		}
		var targets []string
		for _, link := range proof.Links {
			targets = append(targets, link.ObjectID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operations_artist_targets(operation_id,artist_id) SELECT $1,unnest($2::uuid[]) ON CONFLICT DO NOTHING`, f.op, targets); err != nil {
			return err
		}
		ids = append(ids, targets...)
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	return reconcileFieldArtistLinks(ctx, tx, slices.Compact(ids), "", nil)
}
