package personal

import (
	"context"
	"database/sql"
	"fmt"
)

// MoveAlbumFavorites 在调用方事务里转移专辑收藏，已收藏目标组时只删除来源收藏。
// 调用方须已持有 CATP，传入被腾空组到非空接收组的映射，并在失败时回滚事务。
// 按用户 UUID 升序锁收藏版本行，每个实际受影响用户的版本只递增一次；返回来源收藏行数。
func (s *Service) MoveAlbumFavorites(ctx context.Context, tx *sql.Tx, moves map[string]string) (int, error) {
	if len(moves) == 0 {
		return 0, nil
	}
	from, to := make([]string, 0, len(moves)), make([]string, 0, len(moves))
	for source, target := range moves {
		from, to = append(from, source), append(to, target)
	}

	rows, err := tx.QueryContext(ctx, `SELECT state.user_id::text FROM personal_favorite_state AS state
   WHERE state.user_id IN (SELECT DISTINCT user_id FROM favorite_albums WHERE group_id=ANY($1::text[]::uuid[]))
   ORDER BY state.user_id FOR UPDATE`, from)
	if err != nil {
		return 0, fmt.Errorf("lock moved album favorite states: %w", err)
	}
	defer rows.Close()
	users := make([]string, 0)
	for rows.Next() {
		var user string
		if err := rows.Scan(&user); err != nil {
			return 0, fmt.Errorf("read moved album favorite state: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read moved album favorite states: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close moved album favorite states: %w", err)
	}
	if len(users) == 0 {
		return 0, nil
	}

	// 专辑收藏切换不持 CATP；锁后用新语句读当前关系，且只修改已锁定用户的收藏。
	// 多个来源合到同一目标时按原收藏时间、来源 UUID 决定保留哪份快照。
	if _, err := tx.ExecContext(ctx, `INSERT INTO favorite_albums(user_id,group_id,created_at,saved_title,saved_artist)
   SELECT favorites.user_id,mapping.target,favorites.created_at,favorites.saved_title,favorites.saved_artist
   FROM favorite_albums AS favorites
   JOIN unnest($1::text[]::uuid[],$2::text[]::uuid[]) AS mapping(source,target) ON favorites.group_id=mapping.source
   WHERE favorites.user_id=ANY($3::text[]::uuid[])
   ORDER BY favorites.user_id,favorites.created_at,favorites.group_id
   ON CONFLICT(user_id,group_id) DO NOTHING`, from, to, users); err != nil {
		return 0, fmt.Errorf("insert moved album favorites: %w", err)
	}

	// 等锁期间来源收藏可能已被取消，只递增实际删除了来源行的用户版本。
	var moved int
	if err := tx.QueryRowContext(ctx, `WITH removed AS (
   DELETE FROM favorite_albums WHERE group_id=ANY($1::text[]::uuid[]) AND user_id=ANY($2::text[]::uuid[])
   RETURNING user_id
), advanced AS (
   UPDATE personal_favorite_state SET revision=revision+1 WHERE user_id IN (SELECT user_id FROM removed)
   RETURNING user_id
)
SELECT COUNT(*) FROM removed`, from, users).Scan(&moved); err != nil {
		return 0, fmt.Errorf("remove source album favorites and advance states: %w", err)
	}
	return moved, nil
}
