package zones

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

// InsertEntries 在指定条目之后插入；afterEntryID 为空时插入队首。
// 播放区或非空锚点不属于本人本区时返回 ErrNotFound；空 entries 不改变队列。
func (s *Store) InsertEntries(ctx context.Context, userID, zoneID, afterEntryID string, entries []queue.Entry) error {
	var position int
	err := s.tx.QueryRowContext(ctx, `
SELECT COALESCE(e.position, -1)
FROM playback_zones z
LEFT JOIN playback_queue_entries e ON e.zone_id=z.id AND e.entry_id=NULLIF($3, '')::uuid
WHERE z.user_id=$1::uuid AND z.id=$2::uuid AND ($3='' OR e.entry_id IS NOT NULL)`,
		userID, zoneID, afterEntryID).Scan(&position)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load playback insertion position: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := s.tx.ExecContext(ctx, `
UPDATE playback_queue_entries e SET position=e.position+$4
FROM playback_zones z
WHERE z.user_id=$1::uuid AND z.id=$2::uuid AND e.zone_id=z.id AND e.position>$3`,
		userID, zoneID, position, len(entries)); err != nil {
		return fmt.Errorf("shift playback queue entries: %w", err)
	}
	return s.insertEntries(ctx, userID, zoneID, position+1, entries)
}

// DeleteEntry 只删除该条目，不重排剩余位置，也不更新当前项、历史或版本号。
// 播放区或条目不存在、不属于本人时均返回 false。
func (s *Store) DeleteEntry(ctx context.Context, userID, zoneID, entryID string) (bool, error) {
	result, err := s.tx.ExecContext(ctx, `
DELETE FROM playback_queue_entries e USING playback_zones z
WHERE z.user_id=$1::uuid AND z.id=$2::uuid AND e.zone_id=z.id AND e.entry_id=$3::uuid`,
		userID, zoneID, entryID)
	if err != nil {
		return false, fmt.Errorf("delete playback queue entry: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted playback queue entries: %w", err)
	}
	return count != 0, nil
}

// SwapEntries 交换调用者选定的两个相邻条目的位置，允许位置之间有空隙。
// 任一条目不在本人本区或两个 ID 相同时无操作；相邻判定由队列逻辑负责。
func (s *Store) SwapEntries(ctx context.Context, userID, zoneID, firstEntryID, secondEntryID string) (bool, error) {
	result, err := s.tx.ExecContext(ctx, `
WITH pair AS (
 SELECT a.entry_id AS first_id, a.position AS first_position,
        b.entry_id AS second_id, b.position AS second_position
 FROM playback_zones z
 JOIN playback_queue_entries a ON a.zone_id=z.id
 JOIN playback_queue_entries b ON b.zone_id=z.id
 WHERE z.user_id=$1::uuid AND z.id=$2::uuid
   AND a.entry_id=$3::uuid AND b.entry_id=$4::uuid AND a.entry_id<>b.entry_id
)
UPDATE playback_queue_entries e
SET position=CASE WHEN e.entry_id=pair.first_id THEN pair.second_position ELSE pair.first_position END
FROM pair, playback_zones z
WHERE z.user_id=$1::uuid AND z.id=$2::uuid AND e.zone_id=z.id
 AND e.entry_id IN (pair.first_id, pair.second_id)`, userID, zoneID, firstEntryID, secondEntryID)
	if err != nil {
		return false, fmt.Errorf("swap playback queue entries: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count swapped playback queue entries: %w", err)
	}
	return count != 0, nil
}

// ReplaceEntries 删除本区原有条目，再按输入顺序批量插入；空 entries 即清空。
// 本层不生成条目 ID，不替调用者更新播放区行。
func (s *Store) ReplaceEntries(ctx context.Context, userID, zoneID string, entries []queue.Entry) error {
	var id string
	err := s.tx.QueryRowContext(ctx, `SELECT id FROM playback_zones WHERE user_id=$1::uuid AND id=$2::uuid`,
		userID, zoneID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load playback replacement zone: %w", err)
	}
	if _, err := s.tx.ExecContext(ctx, `
DELETE FROM playback_queue_entries e USING playback_zones z
WHERE z.user_id=$1::uuid AND z.id=$2::uuid AND e.zone_id=z.id`, userID, zoneID); err != nil {
		return fmt.Errorf("clear playback queue entries: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}
	return s.insertEntries(ctx, userID, zoneID, 0, entries)
}

func (s *Store) insertEntries(ctx context.Context, userID, zoneID string, firstPosition int, entries []queue.Entry) error {
	entryIDs := make([]string, len(entries))
	trackIDs := make([]string, len(entries))
	for i, entry := range entries {
		entryIDs[i], trackIDs[i] = entry.EntryID, entry.TrackID
	}
	result, err := s.tx.ExecContext(ctx, `
INSERT INTO playback_queue_entries (zone_id, entry_id, position, track_id)
SELECT z.id, x.entry_id, ($5::bigint+x.ordinality-1)::integer, x.track_id
FROM playback_zones z
CROSS JOIN unnest($3::text[]::uuid[], $4::text[]::uuid[]) WITH ORDINALITY AS x(entry_id, track_id, ordinality)
WHERE z.user_id=$1::uuid AND z.id=$2::uuid`, userID, zoneID, entryIDs, trackIDs, firstPosition)
	if err != nil {
		return fmt.Errorf("insert playback queue entries: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count inserted playback queue entries: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
