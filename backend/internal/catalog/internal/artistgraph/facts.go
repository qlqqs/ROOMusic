package artistgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

type creditFact struct {
	ReleaseID *string `json:"release_id"`
	TrackID   *string `json:"track_id"`
	Role      string  `json:"role"`
	NameID    string  `json:"name_id"`
	Name      string  `json:"-"`
	Position  int     `json:"position"`
	Layer     string  `json:"layer"`
}

// 同一标签原文可以包含重复空白。用引用和原始字节找分段，展示文字仅用于选中当前采用的整组标签。
type localArtistSource struct {
	raw   []string
	names []string
}

func localArtistNames(ctx context.Context, tx *sql.Tx, ids []string) (map[string][]localArtistSource, error) {
	rows, err := tx.QueryContext(ctx, `WITH owners AS (
 SELECT a.operation_id,a.release_id AS target FROM release_auto_values a WHERE a.field='album_artist' AND a.release_id=ANY($1::uuid[])
 UNION ALL SELECT a.operation_id,t.id FROM media m JOIN tracks t ON t.medium_id=m.id JOIN track_auto_values a ON a.track_id=t.id AND a.field='artist' WHERE m.release_id=ANY($1::uuid[])
 ), parts AS (
 SELECT p.* FROM owners o JOIN local_credit_parts p ON (p.operation_id,p.target_id)=(o.operation_id,o.target)
 ), sources AS (
 SELECT DISTINCT p.operation_id,p.target_id,e.track_id,e.role FROM parts p JOIN artist_tag_evidence e
 ON e.track_id::text=split_part(p.source_ref,'/',1) AND p.source_ref=e.track_id::text||'/'||e.tag_key||'/'||e.occurrence_index::text
 AND p.raw=e.raw_value AND e.role=CASE p.role WHEN 'release_artist' THEN 'track_artist' ELSE p.role END
 ), entries AS (
 SELECT s.target_id,s.track_id::text||'/'||s.role AS source,e.occurrence_index,e.tag_key,e.raw_value,p.parts
 FROM sources s JOIN artist_tag_evidence e ON (e.track_id,e.role)=(s.track_id,s.role)
 LEFT JOIN parts p ON p.operation_id=s.operation_id AND p.target_id=s.target_id AND p.raw=e.raw_value
 AND p.source_ref=e.track_id::text||'/'||e.tag_key||'/'||e.occurrence_index::text
 AND e.role=CASE p.role WHEN 'release_artist' THEN 'track_artist' ELSE p.role END
 UNION ALL SELECT target_id,source_ref,0,'',raw,parts FROM parts WHERE source_ref=target_id::text||'/scan/'||role
 ) SELECT target_id::text,source,raw_value,parts FROM entries ORDER BY target_id,source,occurrence_index,tag_key`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]localArtistSource{}
	var previousID, previousSource string
	for rows.Next() {
		var id, source, raw string
		var data []byte
		if err := rows.Scan(&id, &source, &raw, &data); err != nil {
			return nil, err
		}
		if id != previousID || source != previousSource {
			result[id] = append(result[id], localArtistSource{})
			previousID, previousSource = id, source
		}
		entry := &result[id][len(result[id])-1]
		entry.raw = append(entry.raw, raw)
		names := metadata.CanonicalArtistNames(raw)
		if data != nil {
			if err := json.Unmarshal(data, &names); err != nil {
				return nil, err
			}
		}
		for _, name := range names {
			if !slices.Contains(entry.names, name) {
				entry.names = append(entry.names, name)
			}
		}
	}
	return result, rows.Err()
}

// refreshFacts 重建当前扫描事实与发行和曲目艺人的自动层、修正层，返回本次新建身份；不触碰任何艺人治理表。
func refreshFacts(ctx context.Context, tx *sql.Tx, ids []string) ([]string, error) {
	resolver := nameResolver{tx: tx, names: map[string]string{}}
	facts := []creditFact{}
	// 同一曲目同人同角色的多种细分只建一条事实，避免同批 upsert 重复命中。
	seenTrackCredits := make(map[[4]string]bool)
	localSources, err := localArtistNames(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	add := func(releaseID, trackID, role, raw, layer string) error {
		if !nonempty(raw) {
			return nil
		}
		names := metadata.CanonicalArtistNames(raw)
		if layer == "auto" {
			id := trackID
			if id == "" {
				id = releaseID
			}
			for _, source := range localSources[id] {
				if metadata.CanonicalTrackArtist(metadata.NormalizeValue(strings.Join(source.raw, " / "))) == metadata.CanonicalTrackArtist(metadata.NormalizeValue(raw)) {
					names = source.names
					break
				}
			}
		}
		if len(names) > 100 {
			return fmt.Errorf("artist subject exceeds name budget")
		}
		for i, name := range names {
			if layer == "scan" && metadata.FieldAuthenticity("artist", name, "", false, false) == metadata.Placeholder {
				continue
			}
			if trackID != "" {
				key := [4]string{trackID, role, name, layer}
				if seenTrackCredits[key] {
					continue
				}
				seenTrackCredits[key] = true
			}
			f := creditFact{Role: role, Name: name, Position: i + 1, Layer: layer}
			if trackID != "" {
				v := trackID
				f.TrackID = &v
			} else {
				v := releaseID
				f.ReleaseID = &v
			}
			facts = append(facts, f)
		}
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id::text,r.artist,COALESCE(r.album_artist,''),COALESCE(c.text_value,''),COALESCE(a.text_value,''),
 COALESCE((SELECT selected_source FROM release_field_decisions WHERE release_id=r.id AND field_key='artist'),''),
 COALESCE((SELECT selected_source FROM release_field_decisions WHERE release_id=r.id AND field_key='album_artist'),'')
 FROM releases r LEFT JOIN release_corrections c ON c.release_id=r.id AND c.field='album_artist'
 LEFT JOIN release_auto_values a ON a.release_id=r.id AND a.field='album_artist' WHERE r.id=ANY($1::uuid[]) ORDER BY r.id`, ids)
	if err != nil {
		return nil, err
	}
	type release struct {
		id, artist, album, correction, auto string
		artistSource, albumSource           string
	}
	var releases []release
	for rows.Next() {
		var r release
		if err = rows.Scan(&r.id, &r.artist, &r.album, &r.correction, &r.auto, &r.artistSource, &r.albumSource); err != nil {
			rows.Close()
			return nil, err
		}
		releases = append(releases, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, r := range releases {
		if metadata.FieldAuthenticity("artist", r.artist, r.artistSource, false, false) != metadata.Placeholder && !metadata.IsCompilationPlaceholder(r.artist) {
			if err = add(r.id, "", "release_artist", r.artist, "scan"); err != nil {
				return nil, err
			}
		}
		if metadata.FieldAuthenticity("album_artist", r.album, r.albumSource, false, false) != metadata.Placeholder && !metadata.IsCompilationPlaceholder(r.album) {
			if err = add(r.id, "", "album_artist", r.album, "scan"); err != nil {
				return nil, err
			}
		}
		for _, credit := range []struct{ layer, name string }{{"correction", r.correction}, {"auto", r.auto}} {
			if credit.name == "" || metadata.IsCompilationPlaceholder(credit.name) {
				continue
			}
			for _, role := range []string{"release_artist", "album_artist"} {
				if err = add(r.id, "", role, credit.name, credit.layer); err != nil {
					return nil, err
				}
			}
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT t.id::text,t.artist,COALESCE(tc.text_value,''),COALESCE(ta.text_value,''),o.value,COALESCE(o.source_kind,''), COALESCE(o.inferred,false),
 COALESCE((SELECT jsonb_agg(e.raw_value ORDER BY e.occurrence_index,e.tag_key) FROM artist_tag_evidence e WHERE e.track_id=t.id AND e.role='track_artist'),'[]'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('role',c.role,'name',c.name) ORDER BY c.role,c.position,c.id) FROM track_credits c WHERE c.track_id=t.id),'[]'::jsonb)
 FROM tracks t JOIN media m ON m.id=t.medium_id LEFT JOIN track_observations o ON o.track_id=t.id AND o.field_name='artist'
 LEFT JOIN track_corrections tc ON tc.track_id=t.id AND tc.field='artist'
 LEFT JOIN track_auto_values ta ON ta.track_id=t.id AND ta.field='artist'
 WHERE m.release_id=ANY($1::uuid[]) ORDER BY t.id`, ids)
	if err != nil {
		return nil, err
	}
	type track struct {
		id, artist, correction, automatic string
		raw                               sql.NullString
		source                            string
		inferred                          bool
		evidence, credits                 []byte
	}
	var tracks []track
	for rows.Next() {
		var t track
		if err = rows.Scan(&t.id, &t.artist, &t.correction, &t.automatic, &t.raw, &t.source, &t.inferred, &t.evidence, &t.credits); err != nil {
			rows.Close()
			return nil, err
		}
		tracks = append(tracks, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	type trackArtistChange struct {
		ID     string `json:"id"`
		Artist string `json:"artist"`
	}
	changes := []trackArtistChange{}
	for _, t := range tracks {
		var rawNames []string
		var credits []metadata.Credit
		if err = json.Unmarshal(t.evidence, &rawNames); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(t.credits, &credits); err != nil {
			return nil, err
		}
		raw := t.artist
		if len(rawNames) > 0 {
			raw = strings.Join(rawNames, " / ")
		} else if t.raw.Valid {
			raw = t.raw.String
		}
		if metadata.FieldAuthenticity("artist", raw, t.source, t.inferred, false) != metadata.Placeholder {
			if err = add("", t.id, "track_artist", raw, "scan"); err != nil {
				return nil, err
			}
			display := metadata.CanonicalTrackArtist(raw)
			if display != t.artist {
				changes = append(changes, trackArtistChange{t.id, display})
			}
		}
		if err = add("", t.id, "track_artist", t.correction, "correction"); err != nil {
			return nil, err
		}
		if err = add("", t.id, "track_artist", t.automatic, "auto"); err != nil {
			return nil, err
		}
		for _, c := range credits {
			if err = add("", t.id, c.Role, c.Name, "scan"); err != nil {
				return nil, err
			}
		}
	}
	// 只写实际恢复了显示署名的轨，按固定小批完整推进；搜索私表及 revision 仍由 searchindex 拥有。
	changedIDs := make([]string, 0, len(changes))
	for start := 0; start < len(changes); start += 128 {
		batch := changes[start:min(start+128, len(changes))]
		data, err := json.Marshal(batch)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE tracks t SET artist=v.artist
 FROM jsonb_to_recordset($1::jsonb) AS v(id uuid,artist text) WHERE t.id=v.id`, string(data)); err != nil {
			return nil, err
		}
		for _, c := range batch {
			changedIDs = append(changedIDs, c.ID)
		}
	}
	if err = searchindex.RefreshTracks(ctx, tx, changedIDs); err != nil {
		return nil, err
	}

	// 扫描全文或字段层变化即撤下旧依据；保留原扫描事实以便恢复。
	if _, err = tx.ExecContext(ctx, `DELETE FROM release_match_credit_parts p USING releases r
 WHERE r.id=p.release_id AND r.id=ANY($1::uuid[]) AND
 CASE WHEN p.track_id IS NULL THEN
    EXISTS(SELECT FROM release_corrections c WHERE c.release_id=r.id AND c.field='album_artist')
    OR EXISTS(SELECT FROM release_auto_values a WHERE a.release_id=r.id AND a.field='album_artist')
    OR p.local_text<>btrim(CASE p.role WHEN 'release_artist' THEN r.artist ELSE coalesce(r.album_artist,'') END)
 ELSE NOT EXISTS(SELECT FROM tracks t JOIN media m ON m.id=t.medium_id
    WHERE t.id=p.track_id AND m.release_id=p.release_id AND t.source_status='present' AND btrim(t.artist)=p.local_text
    AND NOT EXISTS(SELECT FROM track_corrections c WHERE c.track_id=t.id AND c.field='artist')
    AND NOT EXISTS(SELECT FROM track_auto_values a WHERE a.track_id=t.id AND a.field='artist')) END`, ids); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT CASE WHEN track_id IS NULL THEN release_id::text END,track_id::text,role,name,position
 FROM release_match_credit_parts WHERE release_id=ANY($1::uuid[]) ORDER BY release_id,track_id,role,position`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		f := creditFact{Layer: "auto"}
		if err := rows.Scan(&f.ReleaseID, &f.TrackID, &f.Role, &f.Name, &f.Position); err != nil {
			rows.Close()
			return nil, err
		}
		facts = append(facts, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(facts))
	for _, f := range facts {
		names = append(names, f.Name)
	}
	if err = resolver.prefetch(ctx, names); err != nil {
		return nil, err
	}
	for i := range facts {
		facts[i].NameID, err = resolver.ensure(ctx, facts[i].Name, facts[i].Layer)
		if err != nil {
			return nil, err
		}
	}
	// 以事实键upsert保留行ID，使无变化重扫不会制造新的候选依据。
	data, err := json.Marshal(facts)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `WITH scoped AS MATERIALIZED (
 SELECT id,release_id,track_id,role,name_id,layer FROM artist_credits WHERE release_id=ANY($1::uuid[])
 UNION ALL SELECT c.id,c.release_id,c.track_id,c.role,c.name_id,c.layer FROM media m JOIN tracks t ON t.medium_id=m.id
 JOIN artist_credits c ON c.track_id=t.id WHERE m.release_id=ANY($1::uuid[])
 ) DELETE FROM artist_credits target USING scoped c WHERE target.id=c.id AND NOT EXISTS
 (SELECT 1 FROM jsonb_to_recordset($2::jsonb) AS f(release_id uuid,track_id uuid,role text,name_id uuid,position int,layer text)
 WHERE f.release_id IS NOT DISTINCT FROM c.release_id AND f.track_id IS NOT DISTINCT FROM c.track_id AND f.role=c.role AND f.name_id=c.name_id AND f.layer=c.layer)`, ids, string(data)); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO artist_credits(id,release_id,track_id,role,name_id,position,layer)
 SELECT gen_random_uuid(),f.release_id,f.track_id,f.role,f.name_id,f.position,f.layer
 FROM jsonb_to_recordset($1::jsonb) AS f(release_id uuid,track_id uuid,role text,name_id uuid,position int,layer text)
 ON CONFLICT(release_id,track_id,role,name_id,layer) DO UPDATE SET position=EXCLUDED.position`, string(data))
	return resolver.createdArtistIDs, err
}
func uniqueIDs(ids []string) []string {
	m := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			m[id] = true
		}
	}
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
