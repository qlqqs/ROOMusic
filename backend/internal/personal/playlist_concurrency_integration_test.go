package personal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 等待指定幂等键的事务真正阻塞在歌单行锁，不靠 sleep 假设请求已经到达。
func waitPlaylistRowLock(t *testing.T, f *personalFixture, ctx context.Context, user, key string) {
	t.Helper()
	sum := sha256.Sum256([]byte(user + ":" + key))
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := f.database.QueryRowContext(ctx, `SELECT EXISTS(
   SELECT 1 FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid
   WHERE l.locktype='advisory' AND l.classid=$1::oid AND l.objid=$2::oid AND l.objsubid=2 AND l.granted
   AND a.wait_event_type='Lock' AND a.query LIKE '%FROM personal_playlists%FOR UPDATE%')`, uint32(personalKeyLock), binary.BigEndian.Uint32(sum[:4])).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func holdPlaylist(t *testing.T, f *personalFixture, ctx context.Context, user, id string) (*sql.Tx, PlaylistState) {
	t.Helper()
	tx, err := f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	p, err := scanPlaylist(tx.QueryRowContext(ctx, `SELECT `+playlistColumns+` FROM personal_playlists WHERE user_id=$1::uuid AND id=$2::uuid FOR UPDATE`, user, id))
	if err != nil {
		t.Fatal(err)
	}
	return tx, p
}

type playlistAsyncResult struct {
	result Result
	err    error
}

func asyncPlaylist(ctx context.Context, f *personalFixture, user, key string, c Command) <-chan playlistAsyncResult {
	done := make(chan playlistAsyncResult, 1)
	go func() { r, err := f.service.Execute(ctx, user, key, c); done <- playlistAsyncResult{r, err} }()
	return done
}
func finishPlaylist(t *testing.T, ctx context.Context, done <-chan playlistAsyncResult) playlistAsyncResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return playlistAsyncResult{}
	}
}

func TestPostgreSQLPlaylistRestoreWaitsForLatestMembersAndReplaysToken(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	u := f.user(t)
	r := f.release(t, "串行恢复")
	ids := []string{f.track(t, r, "甲", 1), f.track(t, r, "乙", 1), f.track(t, r, "丙", 1)}
	p := createPersonal(t, f, u, ids...).Playlist
	refs := playlistRefs(t, f, u, p.ID)
	removed := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[1].ID, ExpectedRevision: p.Revision})
	blocker, p := holdPlaylist(t, f, ctx, u, p.ID)
	c := Command{Kind: "restore_item", PlaylistID: p.ID, Token: removed.Undo.Token}
	key1, key2 := uuid.New(), uuid.New()
	first := asyncPlaylist(ctx, f, u, key1, c)
	waitPlaylistRowLock(t, f, ctx, u, key1)
	second := asyncPlaylist(ctx, f, u, key2, c)
	waitPlaylistRowLock(t, f, ctx, u, key2)
	// 在恢复请求已开始读取歌单之后，另一事务重新加入曲目并提交。
	added, err := f.service.addItems(ctx, blocker, u, p, []string{ids[1]})
	if err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	a, b := finishPlaylist(t, ctx, first), finishPlaylist(t, ctx, second)
	if a.err != nil || b.err != nil || a.result.Status != b.result.Status || !bytes.Equal(a.result.Body, b.result.Body) {
		t.Fatalf("concurrent token: %v / %v", a.err, b.err)
	}
	var result RestoreResult
	if err = json.Unmarshal(a.result.Body, &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "already_present" || result.Placement != "unchanged" || result.Playlist.Revision != added.Playlist.Revision || result.Item.Position != 3 || result.Item.ID == refs[1].ID {
		t.Fatalf("restore used pre-lock member snapshot: %+v", result)
	}
	if !reflect.DeepEqual(playlistState(t, f, u, p.ID), added.Playlist) {
		t.Fatal("already-present restore changed revision or time")
	}
	if f.count(t, `SELECT count(*) FROM personal_playlist_requests WHERE user_id=$1 AND command='restore_item'`, u) != 2 {
		t.Fatal("new key did not get same token receipt")
	}
	// 后续再移除，已消费凭证的新键仍回放首次成功，而非复活刚删掉的条目。
	executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: result.Item.ID, ExpectedRevision: result.Playlist.Revision})
	replay, err := f.service.Execute(ctx, u, uuid.New(), c)
	if err != nil || !bytes.Equal(replay.Body, a.result.Body) || f.count(t, `SELECT count(*) FROM personal_playlist_items WHERE playlist_id=$1 AND track_id=$2`, p.ID, ids[1]) != 0 {
		t.Fatal("consumed already-present token changed current members")
	}
}

func TestPostgreSQLPlaylistRestoreWaitsForMovedPredecessor(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	u := f.user(t)
	r := f.release(t, "前驱并发")
	ids := []string{f.track(t, r, "甲", 1), f.track(t, r, "乙", 1), f.track(t, r, "丙", 1)}
	p := createPersonal(t, f, u, ids...).Playlist
	refs := playlistRefs(t, f, u, p.ID)
	removed := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[1].ID, ExpectedRevision: p.Revision})
	tx, p := holdPlaylist(t, f, ctx, u, p.ID)
	key := uuid.New()
	result := asyncPlaylist(ctx, f, u, key, Command{Kind: "restore_item", PlaylistID: p.ID, Token: removed.Undo.Token})
	waitPlaylistRowLock(t, f, ctx, u, key)
	moved, err := moveItem(ctx, tx, u, p, Command{Kind: "move_item", ItemID: refs[0].ID, Action: "last"})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got := finishPlaylist(t, ctx, result)
	if got.err != nil {
		t.Fatal(got.err)
	}
	var restored RestoreResult
	if err = json.Unmarshal(got.result.Body, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Placement != "after_previous" || restored.Item.Position != 3 || restored.Playlist.Revision != moved.Playlist.Revision+1 {
		t.Fatalf("stale predecessor: %+v", restored)
	}
	gotRefs := playlistRefs(t, f, u, p.ID)
	if gotRefs[0].TrackID != ids[2] || gotRefs[1].TrackID != ids[0] || gotRefs[2].TrackID != ids[1] {
		t.Fatalf("order: %+v", gotRefs)
	}
}

func TestPostgreSQLPlaylistWaitingNoopChecksCurrentRevision(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	u := f.user(t)
	p := createPersonal(t, f, u).Playlist
	tx, p := holdPlaylist(t, f, ctx, u, p.ID)
	key := uuid.New()
	done := asyncPlaylist(ctx, f, u, key, Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: p.Revision, Name: "新名字"})
	waitPlaylistRowLock(t, f, ctx, u, key)
	changed, err := renamePlaylist(ctx, tx, u, p, "新名字")
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got := finishPlaylist(t, ctx, done)
	if !errors.Is(got.err, ErrRevisionConflict) {
		t.Fatalf("waiting noop skipped revision: %v", got.err)
	}
	if !reflect.DeepEqual(playlistState(t, f, u, p.ID), changed.Playlist) || f.count(t, `SELECT count(*) FROM personal_playlist_requests WHERE user_id=$1 AND idempotency_key=$2`, u, key) != 0 {
		t.Fatal("conflicting noop changed state")
	}
}

func TestPostgreSQLPlaylistQuotaPrecedesDeleteRowAndFreshCreateCount(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	u := f.user(t)
	p := createPersonal(t, f, u).Playlist
	tx, err := f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = lockPersonal(ctx, tx, personalQuotaLock, u); err != nil {
		t.Fatal(err)
	}
	key := uuid.New()
	done := asyncPlaylist(ctx, f, u, key, Command{Kind: "delete", PlaylistID: p.ID, ExpectedRevision: p.Revision})
	sum := sha256.Sum256([]byte(u))
	waitPersonalLocks(t, f, ctx, personalQuotaLock, binary.BigEndian.Uint32(sum[:4]), 1)
	var id string
	// 若 delete 先拿了歌单行再等配额，这里 NOWAIT 会报 55P03。
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM personal_playlists WHERE user_id=$1 AND id=$2 FOR UPDATE NOWAIT`, u, p.ID).Scan(&id); err != nil {
		t.Fatalf("quota/row lock order: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := finishPlaylist(t, ctx, done); got.err != nil {
		t.Fatal(got.err)
	}
	f.exec(t, `INSERT INTO personal_playlists(id,user_id,name) SELECT gen_random_uuid(),$1::uuid,'满额' FROM generate_series(1,1000)`, u)
	tx, err = f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = lockPersonal(ctx, tx, personalQuotaLock, u); err != nil {
		t.Fatal(err)
	}
	done = asyncPlaylist(ctx, f, u, uuid.New(), Command{Kind: "create", Name: "释放后创建"})
	waitPersonalLocks(t, f, ctx, personalQuotaLock, binary.BigEndian.Uint32(sum[:4]), 1)
	if _, err = tx.ExecContext(ctx, `DELETE FROM personal_playlists WHERE user_id=$1 AND id=(SELECT id FROM personal_playlists WHERE user_id=$1 ORDER BY id LIMIT 1)`, u); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := finishPlaylist(t, ctx, done); got.err != nil {
		t.Fatalf("create counted pre-quota snapshot: %v", got.err)
	}
	if f.count(t, `SELECT count(*) FROM personal_playlists WHERE user_id=$1`, u) != 1000 {
		t.Fatal("quota count drift")
	}
}

func TestPostgreSQLPlaylistCancelQuotaAndRowWait(t *testing.T) {
	for _, where := range []string{"quota", "row"} {
		t.Run(where, func(t *testing.T) {
			f := newPersonalFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			u := f.user(t)
			p := createPersonal(t, f, u).Playlist
			tx, err := f.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			c := Command{Kind: "create", Name: "等待配额"}
			if where == "quota" {
				if err = lockPersonal(ctx, tx, personalQuotaLock, u); err != nil {
					t.Fatal(err)
				}
			} else {
				var id string
				if err = tx.QueryRowContext(ctx, `SELECT id::text FROM personal_playlists WHERE user_id=$1 AND id=$2 FOR UPDATE`, u, p.ID).Scan(&id); err != nil {
					t.Fatal(err)
				}
				c = Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: p.Revision, Name: "等待行"}
			}
			key := uuid.New()
			waiting, stop := context.WithCancel(ctx)
			defer stop()
			done := asyncPlaylist(waiting, f, u, key, c)
			if where == "quota" {
				sum := sha256.Sum256([]byte(u))
				waitPersonalLocks(t, f, ctx, personalQuotaLock, binary.BigEndian.Uint32(sum[:4]), 1)
			} else {
				waitPlaylistRowLock(t, f, ctx, u, key)
			}
			stop()
			if got := finishPlaylist(t, ctx, done); got.err == nil {
				t.Fatal("cancellation succeeded")
			}
			if f.count(t, `SELECT count(*) FROM personal_playlist_requests WHERE user_id=$1 AND idempotency_key=$2`, u, key) != 0 {
				t.Fatal("canceled request left receipt")
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if _, err = f.service.Execute(ctx, u, key, c); err != nil {
				t.Fatalf("cancel leaked key or lock: %v", err)
			}
		})
	}
}
