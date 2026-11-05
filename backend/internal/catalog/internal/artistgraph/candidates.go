package artistgraph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

type signal struct {
	Left      string  `json:"left_key_id"`
	Right     string  `json:"right_key_id"`
	Kind      string  `json:"kind"`
	ReleaseID string  `json:"release_id"`
	TrackID   *string `json:"track_id"`
	Role      *string `json:"role"`
	LeftName  string  `json:"left_name"`
	RightName string  `json:"right_name"`
}

func refreshCandidates(ctx context.Context, tx *sql.Tx, ids []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM artist_candidate_signals WHERE release_id=ANY($1::uuid[])`, ids); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.track_id::text,m.release_id::text,e.role,e.raw_value FROM artist_tag_evidence e
 JOIN tracks t ON t.id=e.track_id JOIN media m ON m.id=t.medium_id WHERE m.release_id=ANY($1::uuid[])
 AND t.source_status='present' AND e.role<>'album_artist' ORDER BY e.track_id,e.role,e.tag_key,e.occurrence_index`, ids)
	if err != nil {
		return err
	}
	type evidence struct{ track, release, role, raw string }
	var evidenceRows []evidence
	for rows.Next() {
		var e evidence
		if err = rows.Scan(&e.track, &e.release, &e.role, &e.raw); err != nil {
			rows.Close()
			return err
		}
		evidenceRows = append(evidenceRows, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	type name struct{ key, raw string }
	groups := map[string][]name{}
	signals := []signal{}
	seen := map[string]bool{}
	for _, e := range evidenceRows {
		names := metadata.CanonicalArtistNames(e.raw)
		if len(names) != 1 {
			continue
		}
		normalized := textnorm.Text(names[0])
		hash := sha256.Sum256([]byte(normalized))
		var key string
		err = tx.QueryRowContext(ctx, `SELECT id::text FROM artist_name_keys WHERE normalization_version=$1 AND key_hash=$2 AND normalized_key=$3`, textnorm.Version, hash[:], normalized).Scan(&key)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		g := e.track + ":" + e.role
		for _, previous := range groups[g] {
			if previous.key == key {
				continue
			}
			left, right := previous, name{key, names[0]}
			if left.key > right.key {
				left, right = right, left
			}
			dedup := g + left.key + right.key
			if seen[dedup] {
				continue
			}
			seen[dedup] = true
			track, role := e.track, e.role
			signals = append(signals, signal{Left: left.key, Right: right.key, Kind: "same_track_role", ReleaseID: e.release, TrackID: &track, Role: &role, LeftName: left.raw, RightName: right.raw})
		}
		groups[g] = append(groups[g], name{key, names[0]})
	}
	data, err := json.Marshal(signals)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artist_candidate_signals(left_key_id,right_key_id,kind,release_id,track_id,role,left_name,right_name)
 SELECT s.left_key_id,s.right_key_id,s.kind,s.release_id,s.track_id,s.role,s.left_name,s.right_name FROM jsonb_to_recordset($1::jsonb)
 AS s(left_key_id uuid,right_key_id uuid,kind text,release_id uuid,track_id uuid,role text,left_name text,right_name text) ON CONFLICT DO NOTHING`, string(data)); err != nil {
		return err
	}
	// 每首present轨都必须有真实单一Artist，且全部同键；缺任何一轨都不能补成1:1。
	_, err = tx.ExecContext(ctx, `WITH present_tracks AS (
 SELECT t.id,m.release_id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=ANY($1::uuid[]) AND t.source_status='present'
 ), per_track AS (
 SELECT t.id,t.release_id,min(n.key_id::text)::uuid AS key_id,count(DISTINCT n.key_id) AS n,
 min(n.spelling COLLATE "C") AS spelling FROM present_tracks t LEFT JOIN artist_effective_credits c ON c.track_id=t.id AND c.role='track_artist'
 LEFT JOIN artist_names n ON n.id=c.name_id GROUP BY t.id,t.release_id
 ), track_keys AS (
 SELECT release_id,min(key_id::text)::uuid AS key_id,min(spelling COLLATE "C") AS spelling FROM per_track
 GROUP BY release_id HAVING bool_and(n=1) AND count(DISTINCT key_id)=1
 ), album_keys AS (
 SELECT c.release_id,min(n.key_id::text)::uuid AS key_id,min(n.spelling COLLATE "C") AS spelling FROM artist_effective_credits c
 JOIN artist_names n ON n.id=c.name_id WHERE c.release_id=ANY($1::uuid[]) AND c.role='album_artist'
 GROUP BY c.release_id HAVING count(DISTINCT n.key_id)=1
 ) INSERT INTO artist_candidate_signals(left_key_id,right_key_id,kind,release_id,left_name,right_name)
 SELECT least(a.key_id,t.key_id),greatest(a.key_id,t.key_id),'album_track_one_to_one',a.release_id,
 CASE WHEN a.key_id<t.key_id THEN a.spelling ELSE t.spelling END, CASE WHEN a.key_id<t.key_id THEN t.spelling ELSE a.spelling END
 FROM album_keys a JOIN track_keys t ON t.release_id=a.release_id WHERE a.key_id<>t.key_id ON CONFLICT DO NOTHING`, ids)
	return err
}
