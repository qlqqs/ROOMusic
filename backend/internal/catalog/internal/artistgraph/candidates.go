package artistgraph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func refreshCandidates(ctx context.Context, tx *sql.Tx, ids []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM artist_candidate_signals WHERE release_id=ANY($1::uuid[])`, ids); err != nil {
		return err
	}
	// 每首present轨都必须有真实单一Artist，且全部同键；缺任何一轨都不能补成1:1。
	_, err := tx.ExecContext(ctx, `WITH present_tracks AS (
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
	if err != nil {
		return err
	}
	return refreshAliasSignals(ctx, tx, ids)
}

// refreshAliasSignals 在任一端发行刷新时，重算括号写法与单独写法的发行组合。
func refreshAliasSignals(ctx context.Context, tx *sql.Tx, ids []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM artist_alias_signals WHERE release_id=ANY($1::uuid[]) OR alias_release_id=ANY($1::uuid[])`, ids); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,normalized_key FROM artist_name_keys
 WHERE normalization_version=$1 AND normalized_key LIKE '%)'`, textnorm.Version)
	if err != nil {
		return err
	}
	type bracketKey struct{ id, key string }
	var brackets []bracketKey
	for rows.Next() {
		var b bracketKey
		if err = rows.Scan(&b.id, &b.key); err != nil {
			rows.Close()
			return err
		}
		brackets = append(brackets, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	type lookup struct{ bracket, text, part string }
	var lookups []lookup
	var hashes [][]byte
	textsByHash := map[string]string{}
	for _, b := range brackets {
		for _, a := range bracketAliases(b.key) {
			text := textnorm.Text(a.text)
			hash := sha256.Sum256([]byte(text))
			hashKey := string(hash[:])
			if previous, ok := textsByHash[hashKey]; ok {
				if previous != text {
					return errors.New("artist name hash collision")
				}
			} else {
				textsByHash[hashKey] = text
				hashes = append(hashes, hash[:])
			}
			lookups = append(lookups, lookup{bracket: b.id, text: text, part: a.part})
		}
	}
	if len(hashes) == 0 {
		return nil
	}
	rows, err = tx.QueryContext(ctx, `SELECT id::text,normalized_key,key_hash FROM artist_name_keys
 WHERE normalization_version=$1 AND key_hash=ANY($2::bytea[])`, textnorm.Version, hashes)
	if err != nil {
		return err
	}
	keysByText := map[string]string{}
	for rows.Next() {
		var id, text string
		var hash []byte
		if err = rows.Scan(&id, &text, &hash); err != nil {
			rows.Close()
			return err
		}
		if text != textsByHash[string(hash)] {
			rows.Close()
			return errors.New("artist name hash collision")
		}
		keysByText[text] = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	type pair struct {
		BracketKeyID string `json:"bracket_key_id"`
		AliasKeyID   string `json:"alias_key_id"`
		AliasPart    string `json:"alias_part"`
	}
	var pairs []pair
	for _, l := range lookups {
		if key := keysByText[l.text]; key != "" && key != l.bracket {
			pairs = append(pairs, pair{BracketKeyID: l.bracket, AliasKeyID: key, AliasPart: l.part})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	data, err := json.Marshal(pairs)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `WITH pairs AS (
 SELECT p.bracket_key_id,p.alias_key_id,p.alias_part FROM jsonb_to_recordset($2::jsonb)
 AS p(bracket_key_id uuid,alias_key_id uuid,alias_part text)
 ), keys AS (
 SELECT bracket_key_id AS key_id FROM pairs UNION SELECT alias_key_id FROM pairs
 ), occurrences AS (
 SELECT n.key_id,c.release_id,n.spelling FROM keys k JOIN artist_names n ON n.key_id=k.key_id
 JOIN artist_effective_credits c ON c.name_id=n.id WHERE c.release_id IS NOT NULL
 AND EXISTS(SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id
 WHERE m.release_id=c.release_id AND t.source_status='present')
 UNION ALL
 SELECT n.key_id,m.release_id,n.spelling FROM keys k JOIN artist_names n ON n.key_id=k.key_id
 JOIN artist_effective_credits c ON c.name_id=n.id JOIN tracks t ON t.id=c.track_id AND t.source_status='present'
 JOIN media m ON m.id=t.medium_id
 ), occ AS (
 SELECT key_id,release_id,min(spelling COLLATE "C") AS spelling FROM occurrences GROUP BY key_id,release_id
 ) INSERT INTO artist_alias_signals(left_key_id,right_key_id,bracket_key_id,alias_part,release_id,alias_release_id,left_name,right_name)
 SELECT least(p.bracket_key_id,p.alias_key_id),greatest(p.bracket_key_id,p.alias_key_id),p.bracket_key_id,p.alias_part,
 b.release_id,a.release_id,CASE WHEN p.bracket_key_id<p.alias_key_id THEN b.spelling ELSE a.spelling END,
 CASE WHEN p.bracket_key_id<p.alias_key_id THEN a.spelling ELSE b.spelling END
 FROM pairs p JOIN occ b ON b.key_id=p.bracket_key_id JOIN occ a ON a.key_id=p.alias_key_id
 WHERE b.release_id=ANY($1::uuid[]) OR a.release_id=ANY($1::uuid[]) ON CONFLICT DO NOTHING`, ids, string(data))
	return err
}
