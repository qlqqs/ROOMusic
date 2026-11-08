package artistgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"sort"
	"strings"
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

// refreshFacts 重建当前扫描事实与专辑艺人修正层，返回本次新建身份；不触碰任何艺人治理表。
func refreshFacts(ctx context.Context, tx *sql.Tx, ids []string) ([]string, error) {
	resolver := nameResolver{tx: tx, names: map[string]string{}}
	facts := []creditFact{}
	// 同一曲目同人同角色的多种细分只建一条事实，避免同批 upsert 重复命中。
	seenTrackCredits := make(map[[3]string]bool)
	add := func(releaseID, trackID, role, raw, layer string) error {
		if !nonempty(raw) {
			return nil
		}
		names := metadata.CanonicalArtistNames(raw)
		if len(names) > 100 {
			return fmt.Errorf("artist subject exceeds name budget")
		}
		for i, name := range names {
			if trackID != "" {
				key := [3]string{trackID, role, name}
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
	rows, err := tx.QueryContext(ctx, `SELECT r.id::text,r.artist,COALESCE(r.album_artist,''),COALESCE(c.text_value,''),
 EXISTS(SELECT 1 FROM release_field_decisions d WHERE d.release_id=r.id AND d.field_key='artist' AND d.selected_source='default_fallback') OR (r.artist='未知艺术家' AND NOT EXISTS(SELECT 1 FROM release_field_decisions d WHERE d.release_id=r.id AND d.field_key='artist'))
 FROM releases r LEFT JOIN release_corrections c ON c.release_id=r.id AND c.field='album_artist' WHERE r.id=ANY($1::uuid[]) ORDER BY r.id`, ids)
	if err != nil {
		return nil, err
	}
	type release struct {
		id, artist, album, correction string
		inferred                      bool
	}
	var releases []release
	for rows.Next() {
		var r release
		if err = rows.Scan(&r.id, &r.artist, &r.album, &r.correction, &r.inferred); err != nil {
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
		if !r.inferred && !metadata.IsCompilationPlaceholder(r.artist) {
			if err = add(r.id, "", "release_artist", r.artist, "scan"); err != nil {
				return nil, err
			}
		}
		// 明确AlbumArtist恰好叫“未知艺术家”仍是真实名字。
		if !metadata.IsCompilationPlaceholder(r.album) {
			if err = add(r.id, "", "album_artist", r.album, "scan"); err != nil {
				return nil, err
			}
		}
		if r.correction != "" && !metadata.IsCompilationPlaceholder(r.correction) {
			for _, role := range []string{"release_artist", "album_artist"} {
				if err = add(r.id, "", role, r.correction, "correction"); err != nil {
					return nil, err
				}
			}
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT t.id::text,t.artist,o.value,o.inferred,
 COALESCE((SELECT jsonb_agg(e.raw_value ORDER BY e.occurrence_index,e.tag_key) FROM artist_tag_evidence e WHERE e.track_id=t.id AND e.role='track_artist'),'[]'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('role',c.role,'name',c.name) ORDER BY c.role,c.position,c.id) FROM track_credits c WHERE c.track_id=t.id),'[]'::jsonb)
 FROM tracks t JOIN media m ON m.id=t.medium_id LEFT JOIN track_observations o ON o.track_id=t.id AND o.field_name='artist'
 WHERE m.release_id=ANY($1::uuid[]) ORDER BY t.id`, ids)
	if err != nil {
		return nil, err
	}
	type track struct {
		id, artist        string
		raw               sql.NullString
		inferred          sql.NullBool
		evidence, credits []byte
	}
	var tracks []track
	for rows.Next() {
		var t track
		if err = rows.Scan(&t.id, &t.artist, &t.raw, &t.inferred, &t.evidence, &t.credits); err != nil {
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
		} else if t.raw.Valid && !t.inferred.Bool {
			raw = t.raw.String
		}
		if !t.inferred.Bool && (t.raw.Valid || len(rawNames) > 0 || raw != "未知艺术家") {
			if err = add("", t.id, "track_artist", raw, "scan"); err != nil {
				return nil, err
			}
			display := metadata.CanonicalTrackArtist(raw)
			if display != t.artist {
				changes = append(changes, trackArtistChange{t.id, display})
			}
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

	names := make([]string, 0, len(facts))
	for _, f := range facts {
		names = append(names, f.Name)
	}
	if err = resolver.prefetch(ctx, names); err != nil {
		return nil, err
	}
	for i := range facts {
		origin := "scan"
		if facts[i].Layer == "correction" {
			origin = "correction"
		}
		facts[i].NameID, err = resolver.ensure(ctx, facts[i].Name, origin)
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
