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

// MoveEntry 按当前队列顺序将条目移到从 0 起的目标下标，允许 position 有空隙。
// 本人本区无该条目、目标越界或位置相同时无操作；区间平移复用调用方事务及延迟唯一约束。
func (s *Store) MoveEntry(ctx context.Context, userID, zoneID, entryID string, toIndex int) (bool, error) {
	result, err := s.tx.ExecContext(ctx, `
WITH ordered AS (
 SELECT e.entry_id, e.position, row_number() OVER (ORDER BY e.position)-1 AS index
 FROM playback_zones z
 JOIN playback_queue_entries e ON e.zone_id=z.id
 WHERE z.user_id=$1::uuid AND z.id=$2::uuid
), movement AS (
 SELECT source.entry_id, source.position AS from_position, target.position AS to_position
 FROM ordered source, ordered target
 WHERE source.entry_id=$3::uuid AND target.index=$4 AND source.entry_id<>target.entry_id
)
UPDATE playback_queue_entries e
SET position=CASE
 WHEN e.entry_id=movement.entry_id THEN movement.to_position
 WHEN movement.from_position<movement.to_position THEN e.position-1
 ELSE e.position+1 END
FROM movement, playback_zones z
WHERE z.user_id=$1::uuid AND z.id=$2::uuid AND e.zone_id=z.id
 AND e.position BETWEEN LEAST(movement.from_position, movement.to_position)
                    AND GREATEST(movement.from_position, movement.to_position)`, userID, zoneID, entryID, toIndex)
	if err != nil {
		return false, fmt.Errorf("move playback queue entry: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count moved playback queue entries: %w", err)
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
