package personal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

const orderStep int64 = 1 << 32

// 重整来源先按 owner 限定并只物化一次；目标只匹配这一有权限集合的全局唯一主键。
// 不在目标端重复 owner 过滤：新表无统计时它会错误优选 owner/order 索引，每项重扫整张歌单。
const rebalanceOrderSQL = `WITH ordered AS MATERIALIZED (
 SELECT id,row_number() OVER(ORDER BY order_key) AS position
 FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid
)
UPDATE personal_playlist_items i SET order_key=o.position*$3 FROM ordered o
WHERE i.id=o.id`

const itemColumns = `id::text,track_id::text,order_key,added_at`

func scanItem(row scanner) (playlistItem, error) {
	var i playlistItem
	err := row.Scan(&i.ID, &i.TrackID, &i.OrderKey, &i.AddedAt)
	i.AddedAt = i.AddedAt.UTC()
	return i, err
}
func loadItem(ctx context.Context, tx *sql.Tx, user, playlist, id string) (playlistItem, error) {
	item, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND id=$3::uuid`, user, playlist, id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return item, err
}
func itemPosition(ctx context.Context, tx *sql.Tx, user, playlist string, item playlistItem) (ItemRef, error) {
	err := tx.QueryRowContext(ctx, `SELECT count(*)+1 FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND order_key<$3`, user, playlist, item.OrderKey).Scan(&item.Position)
	return item.ItemRef, err
}

// neighbor 只有本地固定的比较符/排序方向参与 SQL，不拼接用户输入。
func neighbor(ctx context.Context, tx *sql.Tx, user, playlist, exclude string, key int64, after bool) (*playlistItem, error) {
	comparison, direction := "<", "DESC"
	if after {
		comparison, direction = ">", "ASC"
	}
	item, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND id<>$3::uuid AND order_key`+comparison+`$4 ORDER BY order_key `+direction+` LIMIT 1`, user, playlist, exclude, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}
func edge(ctx context.Context, tx *sql.Tx, user, playlist, exclude string, last bool) (*playlistItem, error) {
	direction := "ASC"
	if last {
		direction = "DESC"
	}
	item, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND id<>$3::uuid ORDER BY order_key `+direction+` LIMIT 1`, user, playlist, exclude))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}
func intervalKey(left, right *playlistItem) (int64, bool) {
	if left == nil && right == nil {
		return orderStep, true
	}
	if left == nil {
		if right.OrderKey < math.MinInt64+orderStep {
			return 0, false
		}
		return right.OrderKey - orderStep, true
	}
	if right == nil {
		if left.OrderKey > math.MaxInt64-orderStep {
			return 0, false
		}
		return left.OrderKey + orderStep, true
	}
	if left.OrderKey >= right.OrderKey {
		return 0, false
	}
	distance := uint64(right.OrderKey) - uint64(left.OrderKey)
	if distance <= 1 {
		return 0, false
	}
	return left.OrderKey + int64(distance/2), true
}

// rebalanceOrder 延迟唯一约束只覆盖本事务，排序与身份保持不变。
func rebalanceOrder(ctx context.Context, tx *sql.Tx, user, playlist string) error {
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid`, user, playlist).Scan(&count); err != nil {
		return err
	}
	step := min(orderStep, math.MaxInt64/(count+1))
	if step < 1 {
		return fmt.Errorf("personal order exhausted")
	}
	if _, err := tx.ExecContext(ctx, `SET CONSTRAINTS personal_playlist_items_order_uq DEFERRED`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, rebalanceOrderSQL, user, playlist, step); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `SET CONSTRAINTS personal_playlist_items_order_uq IMMEDIATE`)
	return err
}

// keyBefore 在逻辑移走 exclude 后找目标的左右邻居；末尾用 nil 表示。
func keyBefore(ctx context.Context, tx *sql.Tx, user, playlist, exclude string, before *string) (int64, error) {
	for attempt := 0; attempt < 2; attempt++ {
		var left, right *playlistItem
		var err error
		if before != nil {
			var item playlistItem
			item, err = loadItem(ctx, tx, user, playlist, *before)
			right = &item
			if err == nil {
				left, err = neighbor(ctx, tx, user, playlist, exclude, item.OrderKey, false)
			}
		} else {
			left, err = edge(ctx, tx, user, playlist, exclude, true)
		}
		if err != nil {
			return 0, err
		}
		if key, ok := intervalKey(left, right); ok {
			return key, nil
		}
		if err = rebalanceOrder(ctx, tx, user, playlist); err != nil {
			return 0, err
		}
	}
	return 0, fmt.Errorf("personal order interval unavailable")
}

type MoveResult struct {
	Playlist PlaylistState `json:"playlist"`
	Moved    ItemRef       `json:"moved"`
	Changed  bool          `json:"changed"`
}

func moveItem(ctx context.Context, tx *sql.Tx, user string, p PlaylistState, c Command) (MoveResult, error) {
	item, err := loadItem(ctx, tx, user, p.ID, c.ItemID)
	if err != nil {
		return MoveResult{}, err
	}
	var before *string
	var target *playlistItem
	switch c.Action {
	case "before":
		before = c.BeforeItemID
		if before != nil {
			var anchor playlistItem
			anchor, err = loadItem(ctx, tx, user, p.ID, *before)
			target = &anchor
		}
	case "up":
		target, err = neighbor(ctx, tx, user, p.ID, item.ID, item.OrderKey, false)
		if target == nil && err == nil {
			target = &item
		}
	case "down":
		var next *playlistItem
		next, err = neighbor(ctx, tx, user, p.ID, item.ID, item.OrderKey, true)
		if next != nil && err == nil {
			target, err = neighbor(ctx, tx, user, p.ID, item.ID, next.OrderKey, true)
		}
	case "first":
		target, err = edge(ctx, tx, user, p.ID, item.ID, false)
	case "last":
	}
	if err != nil {
		return MoveResult{}, err
	}
	if target != nil {
		before = &target.ID
	}
	unchanged := before != nil && *before == item.ID
	if !unchanged {
		if before == nil {
			next, e := neighbor(ctx, tx, user, p.ID, item.ID, item.OrderKey, true)
			err = e
			unchanged = next == nil
		} else {
			previous, e := neighbor(ctx, tx, user, p.ID, *before, target.OrderKey, false)
			err = e
			unchanged = previous != nil && previous.ID == item.ID
		}
	}
	if err != nil {
		return MoveResult{}, err
	}
	if !unchanged {
		key, e := keyBefore(ctx, tx, user, p.ID, item.ID, before)
		if e != nil {
			return MoveResult{}, e
		}
		if _, err = tx.ExecContext(ctx, `UPDATE personal_playlist_items SET order_key=$4 WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND id=$3::uuid`, user, p.ID, item.ID, key); err != nil {
			return MoveResult{}, err
		}
		item.OrderKey = key
		p, err = bumpPlaylist(ctx, tx, user, p, 0)
		if err != nil {
			return MoveResult{}, err
		}
	}
	ref, err := itemPosition(ctx, tx, user, p.ID, item)
	return MoveResult{p, ref, !unchanged}, err
}
