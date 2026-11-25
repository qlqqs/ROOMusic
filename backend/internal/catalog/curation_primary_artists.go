package catalog

import "context"

// 有效署名可能来自旧艺人图。取原署名的层与字段来源，避免展示用署名参与整理。
const CurationCreditJoins = `
 JOIN artist_credits fact ON fact.id=c.credit_id
 JOIN artist_names spelling ON spelling.id=c.name_id
 LEFT JOIN track_observations obs ON obs.track_id=c.track_id AND obs.field_name='artist'
 LEFT JOIN release_field_decisions decision ON decision.release_id=c.release_id
  AND decision.field_key=CASE c.role WHEN 'album_artist' THEN 'album_artist' ELSE 'artist' END`

const UsableCurationCredit = `(fact.layer<>'scan' OR (
 COALESCE(obs.source_kind,decision.selected_source,'') NOT IN ('default_fallback','display_fallback','compilation')
 AND (spelling.spelling<>'未知艺术家' OR COALESCE(obs.source_kind,decision.selected_source,'')<>'')))`

// CurationMissingArtistLinks 为一页发行返回有在库主署名且其艺人家族缺少 MB 对应的目标。
func CurationMissingArtistLinks(ctx context.Context, q Queryer, ids []string) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `WITH `+ArtistMediaFamilyCTE+`, subjects AS (
 SELECT id release_id,id subject,false is_track FROM releases WHERE id=ANY($1::uuid[])
 UNION ALL SELECT m.release_id,t.id,true FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=ANY($1::uuid[]) AND t.source_status='present'
 ) SELECT DISTINCT s.release_id FROM subjects s JOIN artist_effective_credits c ON
 ((NOT s.is_track AND c.release_id=s.subject) OR (s.is_track AND c.track_id=s.subject))
 `+CurationCreditJoins+`
 JOIN artist_media_family owner ON owner.artist_id=c.artist_id
 WHERE c.role IN ('release_artist','album_artist','track_artist') AND `+UsableCurationCredit+`
 AND NOT EXISTS (SELECT FROM artist_media_family member JOIN external_links l ON l.object_id=member.artist_id
 WHERE member.canonical_id=owner.canonical_id AND l.object_type='artist' AND l.source='musicbrainz')`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}
