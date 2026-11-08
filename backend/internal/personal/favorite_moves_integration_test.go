package personal

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

type movedFavoriteSnapshot struct {
	user, group, createdAt, title, artist string
}

func albumFavoriteSnapshots(t *testing.T, database *sql.DB) []movedFavoriteSnapshot {
	t.Helper()
	rows, err := database.Query(`SELECT user_id::text,group_id::text,created_at,saved_title,saved_artist
 FROM favorite_albums ORDER BY user_id,group_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]movedFavoriteSnapshot, 0)
	for rows.Next() {
		var item movedFavoriteSnapshot
		var createdAt time.Time
		if err := rows.Scan(&item.user, &item.group, &createdAt, &item.title, &item.artist); err != nil {
			t.Fatal(err)
		}
		item.createdAt = createdAt.UTC().Format(time.RFC3339Nano)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPostgreSQLMoveAlbumFavorites(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	moving, merging, untouched, noState := f.user(t), f.user(t), f.user(t), f.user(t)
	sources := []fixtureRelease{f.release(t, "旧专辑甲"), f.release(t, "旧专辑乙"), f.release(t, "旧专辑丙")}
	target, unrelated, uncollected := f.release(t, "接收专辑"), f.release(t, "无关专辑"), f.release(t, "无人收藏的来源")
	track := f.track(t, target, "不受转移影响的曲目收藏", 12)
	oldest := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	targetTime := oldest.Add(48 * time.Hour)
	for _, user := range []string{moving, merging} {
		for i, source := range sources {
			if _, err := f.service.SetFavorite(ctx, user, "album", source.groupID, true); err != nil {
				t.Fatal(err)
			}
			createdAt := oldest
			if i == 1 {
				createdAt = oldest.Add(time.Hour)
			}
			// 甲、丙时间相同，必须按来源 UUID 选快照；乙较晚，不应胜出。
			f.exec(t, `UPDATE favorite_albums SET created_at=$3,saved_title=$4,saved_artist=$5
 WHERE user_id=$1::uuid AND group_id=$2::uuid`, user, source.groupID, createdAt, "保存标题-"+source.groupID, "保存艺人-"+source.groupID)
		}
	}
	for _, user := range []string{merging, untouched} {
		if _, err := f.service.SetFavorite(ctx, user, "album", target.groupID, true); err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE favorite_albums SET created_at=$3,saved_title='已收藏目标标题',saved_artist='已收藏目标艺人'
 WHERE user_id=$1::uuid AND group_id=$2::uuid`, user, target.groupID, targetTime)
	}
	if _, err := f.service.SetFavorite(ctx, moving, "album", unrelated.groupID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SetFavorite(ctx, moving, "track", track, true); err != nil {
		t.Fatal(err)
	}
	before := albumFavoriteSnapshots(t, f.database)
	revisions := map[string]int{}
	for _, user := range []string{moving, merging, untouched} {
		revisions[user] = f.count(t, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, user)
	}
	var trackCreatedAt time.Time
	if err := f.database.QueryRow(`SELECT created_at FROM favorite_tracks WHERE user_id=$1::uuid AND track_id=$2::uuid`, moving, track).Scan(&trackCreatedAt); err != nil {
		t.Fatal(err)
	}
	moves := map[string]string{uncollected.groupID: target.groupID}
	for _, source := range sources {
		moves[source.groupID] = target.groupID
	}
	move := func(mapping map[string]string) int {
		t.Helper()
		tx, err := f.database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := ingest.LockProjectionWrites(ctx, tx); err != nil {
			t.Fatal(err)
		}
		count, err := f.service.MoveAlbumFavorites(ctx, tx, mapping)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if count := move(moves); count != 6 {
		t.Fatalf("moved source favorite rows=%d, want 6", count)
	}
	// 逐行对比整个收藏集合：只把来源关系换成目标；无关关系与已有目标快照完全不变。
	winner := sources[0].groupID
	if sources[2].groupID < winner {
		winner = sources[2].groupID
	}
	var want []movedFavoriteSnapshot
	for _, item := range before {
		if _, isSource := moves[item.group]; !isSource {
			want = append(want, item)
		} else if item.user == moving && item.group == winner {
			item.group = target.groupID
			want = append(want, item)
		}
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].user != want[j].user {
			return want[i].user < want[j].user
		}
		return want[i].group < want[j].group
	})
	if got := albumFavoriteSnapshots(t, f.database); !reflect.DeepEqual(got, want) {
		t.Fatalf("favorites after transfer=%+v, want %+v", got, want)
	}
	assertRevisions := func() {
		t.Helper()
		for user, previous := range revisions {
			wantRevision := previous
			if user == moving || user == merging {
				wantRevision++
			}
			if got := f.count(t, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, user); got != wantRevision {
				t.Errorf("user %s revision=%d, want %d", user, got, wantRevision)
			}
		}
		if got := f.count(t, `SELECT COUNT(*) FROM personal_favorite_state WHERE user_id=$1::uuid`, noState); got != 0 {
			t.Error("transfer created favorite state for an unrelated user")
		}
	}
	assertRevisions()
	for _, mapping := range []map[string]string{moves, {}, {uncollected.groupID: target.groupID}} {
		if count := move(mapping); count != 0 {
			t.Errorf("no-op moved rows=%d, want 0", count)
		}
	}
	assertRevisions()
	if got := albumFavoriteSnapshots(t, f.database); !reflect.DeepEqual(got, want) {
		t.Errorf("repeat or empty transfer changed snapshots: got=%+v want=%+v", got, want)
	}
	var afterTrackCreatedAt time.Time
	if err := f.database.QueryRow(`SELECT created_at FROM favorite_tracks WHERE user_id=$1::uuid AND track_id=$2::uuid`, moving, track).Scan(&afterTrackCreatedAt); err != nil {
		t.Fatal(err)
	}
	if !afterTrackCreatedAt.Equal(trackCreatedAt) || f.count(t, `SELECT COUNT(*) FROM favorite_tracks`) != 1 {
		t.Error("album transfer changed track favorites")
	}
}
