package catalog

import (
	"context"
	"database/sql"
)

type CurationArtistCredit struct {
	Position       int
	Name, ArtistID string
}

type CurationCreditRole struct {
	TrackID  string `json:"track_id"`
	ArtistID string `json:"artist_id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
}

type CurationArtistCredits struct {
	Roles                        []CurationCreditRole
	ReleaseArtists, AlbumArtists []CurationArtistCredit
	Tracks                       map[string][]CurationArtistCredit
}

// LoadCurationArtistCredits 读取可用于匹配的原署名和当前身份，自动层不参与取证。
func LoadCurationArtistCredits(ctx context.Context, q Queryer, releaseID string) (CurationArtistCredits, error) {
	if db, ok := q.(ReadTransactionBeginner); ok {
		return readWithLocalJITOff(ctx, db, func(tx *sql.Tx) (CurationArtistCredits, error) {
			return LoadCurationArtistCredits(ctx, tx, releaseID)
		})
	}
	if err := NewStore(q).requireArtistsReady(ctx); err != nil {
		return CurationArtistCredits{}, err
	}
	rows, err := q.QueryContext(ctx, `WITH scoped AS (
 SELECT c.track_id,c.role,c.name_id,c.position FROM artist_credits c
 WHERE c.release_id=$1 AND c.layer=CASE WHEN EXISTS(
 SELECT 1 FROM release_corrections WHERE release_id=$1 AND field='album_artist') THEN 'correction' ELSE 'scan' END
 UNION ALL
 SELECT c.track_id,c.role,c.name_id,c.position FROM media m JOIN tracks t ON t.medium_id=m.id
 JOIN artist_credits c ON c.track_id=t.id WHERE m.release_id=$1 AND t.source_status='present' AND c.role IN ('track_artist','composer','performer','conductor')
 AND c.layer=CASE WHEN c.role<>'track_artist' THEN 'scan' WHEN EXISTS(SELECT 1 FROM track_corrections WHERE track_id=t.id AND field='artist')
 THEN 'correction' ELSE 'scan' END)
 SELECT COALESCE(c.track_id::text,''),c.role,c.position,n.spelling,COALESCE(o.redirect_artist_id,route.artist_id)::text
 FROM scoped c JOIN artist_names n ON n.id=c.name_id JOIN artist_name_keys k ON k.id=n.key_id
 LEFT JOIN artist_assignment_overrides ot ON ot.track_id=c.track_id AND ot.key_id=k.id
 LEFT JOIN artist_assignment_overrides ore ON ore.release_id=$1 AND ore.key_id=k.id
 LEFT JOIN artist_name_bindings b ON b.key_id=k.id
 CROSS JOIN LATERAL(SELECT COALESCE(ot.artist_id,ore.artist_id,b.artist_id,k.initial_artist_id) AS artist_id) route
 LEFT JOIN artist_overrides o ON o.artist_id=route.artist_id
 ORDER BY c.track_id,c.role,c.position,c.name_id`, releaseID)
	if err != nil {
		return CurationArtistCredits{}, err
	}
	defer rows.Close()
	out := CurationArtistCredits{Tracks: map[string][]CurationArtistCredit{}}
	for rows.Next() {
		var track, role string
		var c CurationArtistCredit
		if err := rows.Scan(&track, &role, &c.Position, &c.Name, &c.ArtistID); err != nil {
			return CurationArtistCredits{}, err
		}
		switch role {
		case "release_artist":
			out.ReleaseArtists = append(out.ReleaseArtists, c)
		case "album_artist":
			out.AlbumArtists = append(out.AlbumArtists, c)
		case "track_artist":
			out.Tracks[track] = append(out.Tracks[track], c)
		case "composer", "performer", "conductor":
			out.Roles = append(out.Roles, CurationCreditRole{track, c.ArtistID, c.Name, role})
		}
	}
	return out, rows.Err()
}

// CurationCanonicalArtists 返回外部对应所指的不同现行身份，稳定排序用于派生艺人对的幂等键。
func CurationCanonicalArtists(ctx context.Context, q Queryer, ids []string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT COALESCE(o.redirect_artist_id,a.id)::text AS id
 FROM artists a LEFT JOIN artist_overrides o ON o.artist_id=a.id WHERE a.id=ANY($1::uuid[]) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// EffectiveAutoArtistCredits 返回本次字段写入后真正生效的有序署名，沿用目录已解析的本地身份。
func EffectiveAutoArtistCredits(ctx context.Context, q Queryer, releaseID, trackID string) ([]CurationArtistCredit, error) {
	predicate := "c.release_id=$1 AND c.role='album_artist'"
	id := releaseID
	if trackID != "" {
		predicate = "c.track_id=$1 AND c.role='track_artist'"
		id = trackID
	}
	rows, err := q.QueryContext(ctx, `SELECT c.position,c.artist_id,n.spelling FROM artist_effective_credits c
	JOIN artist_credits fact ON fact.id=c.credit_id AND fact.layer='auto'
	JOIN artist_names n ON n.id=c.name_id WHERE `+predicate+` ORDER BY c.position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var group []CurationArtistCredit
	for rows.Next() {
		var c CurationArtistCredit
		if err := rows.Scan(&c.Position, &c.ArtistID, &c.Name); err != nil {
			return nil, err
		}
		group = append(group, c)
	}
	return group, rows.Err()
}

// EffectiveExternalAutoCreditsSQL 按艺人读取生效的外部自动署名位置和字段写入者。
const EffectiveExternalAutoCreditsSQL = `SELECT c.artist_id,c.position,c.release_id,a.operation_id FROM artist_effective_credits c
	JOIN release_auto_values a ON a.release_id=c.release_id AND a.field='album_artist' AND a.writer_level IN (3,4)
	JOIN artist_credits fact ON fact.id=c.credit_id AND fact.layer='auto'
	WHERE c.artist_id=ANY($1::uuid[]) AND c.role='album_artist'
	UNION ALL
	SELECT c.artist_id,c.position,m.release_id,a.operation_id FROM artist_effective_credits c
	JOIN track_auto_values a ON a.track_id=c.track_id AND a.field='artist' AND a.writer_level IN (3,4)
	JOIN artist_credits fact ON fact.id=c.credit_id AND fact.layer='auto'
	JOIN tracks t ON t.id=c.track_id AND t.source_status='present' JOIN media m ON m.id=t.medium_id
	WHERE c.artist_id=ANY($1::uuid[]) AND c.role='track_artist'`
