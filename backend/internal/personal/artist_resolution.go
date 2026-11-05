package personal

import (
	"context"
	"database/sql"
	"fmt"
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

// RefreshArtistResolution 由 app 注入治理，仅刷新解析投影，不改变原收藏意图和时间。
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
	users, err := affectedArtistFavoriteUsers(ctx, tx, batches)
	if err != nil {
		return err
	}
	// 先收集全部批次受影响用户，再按统一 UUID 顺序锁 state，避免局部排序倒转锁序。
	for start := 0; start < len(users); start += artistResolutionBatchSize {
		batch := users[start:min(start+artistResolutionBatchSize, len(users))]
		if err := lockArtistFavoriteStates(ctx, tx, batch); err != nil {
			return err
		}
	}
	for _, batch := range batches {
		if _, err := tx.ExecContext(ctx, `UPDATE artist_favorites AS favorites SET resolved_artist_id=mapping.resolved
   FROM unnest($1::text[]::uuid[],$2::text[]::uuid[]) AS mapping(original,resolved)
   WHERE favorites.artist_id=mapping.original AND favorites.resolved_artist_id<>mapping.resolved`, batch.originals, batch.resolved); err != nil {
			return fmt.Errorf("refresh artist favorite resolution: %w", err)
		}
	}
	for start := 0; start < len(users); start += artistResolutionBatchSize {
		batch := users[start:min(start+artistResolutionBatchSize, len(users))]
		if _, err := tx.ExecContext(ctx, `UPDATE personal_favorite_state SET revision=revision+1 WHERE user_id=ANY($1::text[]::uuid[])`, batch); err != nil {
			return fmt.Errorf("advance resolved artist favorite states: %w", err)
		}
	}
	return nil
}

func affectedArtistFavoriteUsers(ctx context.Context, tx *sql.Tx, batches []artistResolutionBatch) ([]string, error) {
	seen := map[string]bool{}
	for _, batch := range batches {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT favorites.user_id::text FROM artist_favorites AS favorites
   JOIN unnest($1::text[]::uuid[],$2::text[]::uuid[]) AS mapping(original,resolved)
   ON favorites.artist_id=mapping.original WHERE favorites.resolved_artist_id<>mapping.resolved`, batch.originals, batch.resolved)
		if err != nil {
			return nil, fmt.Errorf("read affected artist favorite users: %w", err)
		}
		for rows.Next() {
			var user string
			if err := rows.Scan(&user); err != nil {
				rows.Close()
				return nil, err
			}
			seen[user] = true
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	users := make([]string, 0, len(seen))
	for user := range seen {
		users = append(users, user)
	}
	sort.Strings(users)
	return users, nil
}

func lockArtistFavoriteStates(ctx context.Context, tx *sql.Tx, users []string) error {
	rows, err := tx.QueryContext(ctx, `SELECT revision FROM personal_favorite_state WHERE user_id=ANY($1::text[]::uuid[]) ORDER BY user_id FOR UPDATE`, users)
	if err != nil {
		return fmt.Errorf("lock resolved artist favorite states: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var revision int64
		if err := rows.Scan(&revision); err != nil {
			return err
		}
		if revision >= MaxRevision {
			return fmt.Errorf("favorite revision exhausted")
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
