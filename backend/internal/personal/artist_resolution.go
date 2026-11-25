package personal

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const artistResolutionBatchSize = 1000

type artistResolutionBatch struct{ originals, resolved []string }

func artistResolutionBatches(mapping map[string]string) ([]artistResolutionBatch, error) {
	normalized := make(map[string]string, len(mapping))
	for original, resolved := range mapping {
		if !uuid.Valid(original) || !uuid.Valid(resolved) {
			return nil, ErrInvalidInput
		}
		original, resolved = strings.ToLower(original), strings.ToLower(resolved)
		if previous, exists := normalized[original]; exists && previous != resolved {
			return nil, ErrInvalidInput
		}
		normalized[original] = resolved
	}
	originals := make([]string, 0, len(normalized))
	for id := range normalized {
		originals = append(originals, id)
	}
	sort.Strings(originals)
	batches := make([]artistResolutionBatch, 0)
	for start := 0; start < len(originals); start += artistResolutionBatchSize {
		end := min(start+artistResolutionBatchSize, len(originals))
		batch := artistResolutionBatch{originals: originals[start:end], resolved: make([]string, end-start)}
		for i, original := range batch.originals {
			batch.resolved[i] = normalized[original]
		}
		batches = append(batches, batch)
	}
	return batches, nil
}

// RefreshArtistResolution 由 app 注入治理，仅刷新解析投影，保留收藏和评分的原锚点及时间。
// 调用方须已持有 CATP，传入完整原 ID 映射，并在任何失败时回滚同一事务。
// 本函数不自行提交，也不按映射批次分别递增用户版本。
func RefreshArtistResolution(ctx context.Context, tx *sql.Tx, mapping map[string]string) error {
	batches, err := artistResolutionBatches(mapping)
	if err != nil {
		return err
	}
	if len(batches) == 0 {
		return nil
	}
	users, favoriteUsers, err := affectedArtistUsers(ctx, tx, batches)
	if err != nil {
		return err
	}
	// 先收集全部批次受影响用户，再按统一 UUID 顺序锁 state，避免局部排序倒转锁序。
	for start := 0; start < len(users); start += artistResolutionBatchSize {
		batch := users[start:min(start+artistResolutionBatchSize, len(users))]
		if err := lockArtistFavoriteStates(ctx, tx, batch, favoriteUsers); err != nil {
			return err
		}
	}
	for _, batch := range batches {
		if _, err := tx.ExecContext(ctx, `UPDATE artist_favorites AS favorites SET resolved_artist_id=mapping.resolved
   FROM unnest($1::text[]::uuid[],$2::text[]::uuid[]) AS mapping(original,resolved)
   WHERE favorites.artist_id=mapping.original AND favorites.resolved_artist_id<>mapping.resolved`, batch.originals, batch.resolved); err != nil {
			return fmt.Errorf("refresh artist favorite resolution: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE rating_artists AS ratings SET resolved_artist_id=mapping.resolved
   FROM unnest($1::uuid[],$2::uuid[]) AS mapping(original,resolved)
   WHERE ratings.artist_id=mapping.original AND ratings.resolved_artist_id<>mapping.resolved`, batch.originals, batch.resolved); err != nil {
			return fmt.Errorf("refresh artist rating resolution: %w", err)
		}
	}
	for start := 0; start < len(favoriteUsers); start += artistResolutionBatchSize {
		batch := favoriteUsers[start:min(start+artistResolutionBatchSize, len(favoriteUsers))]
		if _, err := tx.ExecContext(ctx, `UPDATE personal_favorite_state SET revision=revision+1 WHERE user_id=ANY($1::text[]::uuid[])`, batch); err != nil {
			return fmt.Errorf("advance resolved artist favorite states: %w", err)
		}
	}
	return nil
}

func affectedArtistUsers(ctx context.Context, tx *sql.Tx, batches []artistResolutionBatch) (users, favoriteUsers []string, err error) {
	hasFavorite := map[string]bool{}
	for _, batch := range batches {
		rows, err := tx.QueryContext(ctx, `WITH mapping(original,resolved) AS (
			SELECT * FROM unnest($1::uuid[],$2::uuid[])
		)
		SELECT f.user_id,true FROM artist_favorites f JOIN mapping m ON f.artist_id=m.original
		WHERE f.resolved_artist_id<>m.resolved
		UNION
		SELECT r.user_id,false FROM rating_artists r JOIN mapping m ON r.artist_id=m.original
		WHERE r.resolved_artist_id<>m.resolved`, batch.originals, batch.resolved)
		if err != nil {
			return nil, nil, fmt.Errorf("read affected artist users: %w", err)
		}
		for rows.Next() {
			var user string
			var favorite bool
			if err := rows.Scan(&user, &favorite); err != nil {
				rows.Close()
				return nil, nil, err
			}
			hasFavorite[user] = hasFavorite[user] || favorite
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, nil, err
		}
	}
	for user := range hasFavorite {
		users = append(users, user)
	}
	sort.Strings(users)
	for _, user := range users {
		if hasFavorite[user] {
			favoriteUsers = append(favoriteUsers, user)
		}
	}
	return users, favoriteUsers, nil
}

func lockArtistFavoriteStates(ctx context.Context, tx *sql.Tx, users, favoriteUsers []string) error {
	rows, err := tx.QueryContext(ctx, `SELECT user_id::text, revision FROM personal_favorite_state WHERE user_id=ANY($1::text[]::uuid[]) ORDER BY personal_favorite_state.user_id FOR UPDATE`, users)
	if err != nil {
		return fmt.Errorf("lock resolved artist favorite states: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var user string
		var revision int64
		if err := rows.Scan(&user, &revision); err != nil {
			return err
		}
		if revision >= MaxRevision {
			if _, advances := slices.BinarySearch(favoriteUsers, user); advances {
				return fmt.Errorf("favorite revision exhausted")
			}
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(users) {
		return fmt.Errorf("artist favorite state missing")
	}
	return rows.Close()
}
