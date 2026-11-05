package artistgraph

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// Refresh 调用者须已取CATP；每次从锁内当前事实重建，没有扫描前快照覆盖新意图的窗口。
func Refresh(ctx context.Context, tx *sql.Tx, releaseIDs []string) error {
	ids := uniqueIDs(releaseIDs)
	if len(ids) == 0 {
		return nil
	}
	if err := textnorm.LoadEmbedded(); err != nil {
		return err
	}
	gen, err := generation(ctx, tx)
	if err != nil {
		return err
	}
	old, err := affected(ctx, tx, ids)
	if err != nil {
		return err
	}
	created, err := refreshFacts(ctx, tx, ids)
	if err != nil {
		return fmt.Errorf("artist facts: %w", err)
	}
	if err = refreshEffective(ctx, tx, ids); err != nil {
		return fmt.Errorf("artist effective: %w", err)
	}
	current, err := affected(ctx, tx, ids)
	if err != nil {
		return err
	}
	artists := uniqueIDs(append(append(old, current...), created...))
	if err = refreshProfiles(ctx, tx, artists, gen); err != nil {
		return fmt.Errorf("artist profiles: %w", err)
	}
	if err = refreshCandidates(ctx, tx, ids); err != nil {
		return fmt.Errorf("artist candidates: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO artist_projection_releases(release_id,applied_generation)
  SELECT id,$2 FROM releases WHERE id=ANY($1::uuid[]) ON CONFLICT(release_id) DO UPDATE SET applied_generation=EXCLUDED.applied_generation`, ids, gen)
	return err
}
func affected(ctx context.Context, tx *sql.Tx, ids []string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT artist_id::text FROM artist_effective_credits WHERE release_id=ANY($1::uuid[])
 UNION SELECT c.artist_id::text FROM media m JOIN tracks t ON t.medium_id=m.id
 JOIN artist_effective_credits c ON c.track_id=t.id WHERE m.release_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func refreshEffective(ctx context.Context, tx *sql.Tx, ids []string) error {
	// release 与 track 署名互斥，先沿所属发行索引取本批，避免跨表 OR 在全库连接后才过滤。
	_, err := tx.ExecContext(ctx, `DELETE FROM artist_effective_credits WHERE release_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM artist_effective_credits c USING tracks t,media m
 WHERE c.track_id=t.id AND t.medium_id=m.id AND m.release_id=ANY($1::uuid[])`, ids)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `WITH scoped AS MATERIALIZED (
 SELECT c.id,c.release_id,c.track_id,c.role,c.name_id,c.position,c.release_id AS owner_release_id
 FROM artist_credits c WHERE c.release_id=ANY($1::uuid[]) AND c.layer=CASE WHEN EXISTS(
 SELECT 1 FROM release_corrections rc WHERE rc.release_id=c.release_id AND rc.field='album_artist') THEN 'correction' ELSE 'scan' END
 UNION ALL
 SELECT c.id,c.release_id,c.track_id,c.role,c.name_id,c.position,m.release_id
 FROM media m JOIN tracks t ON t.medium_id=m.id JOIN artist_credits c ON c.track_id=t.id WHERE m.release_id=ANY($1::uuid[])
 ) INSERT INTO artist_effective_credits(credit_id,release_id,track_id,role,name_id,artist_id,position)
 SELECT c.id,c.release_id,c.track_id,c.role,c.name_id,COALESCE(o.redirect_artist_id,route.artist_id),c.position
 FROM scoped c JOIN artist_names n ON n.id=c.name_id JOIN artist_name_keys k ON k.id=n.key_id
 LEFT JOIN artist_assignment_overrides ot ON ot.track_id=c.track_id AND ot.key_id=k.id
 LEFT JOIN artist_assignment_overrides ore ON ore.release_id=c.owner_release_id AND ore.key_id=k.id
 LEFT JOIN artist_name_bindings b ON b.key_id=k.id
 CROSS JOIN LATERAL(SELECT COALESCE(ot.artist_id,ore.artist_id,b.artist_id,k.initial_artist_id) AS artist_id) route
 LEFT JOIN artist_overrides o ON o.artist_id=route.artist_id`, ids)
	return err
}
