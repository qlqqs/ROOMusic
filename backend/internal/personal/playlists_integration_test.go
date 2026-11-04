package personal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func executePersonal[T any](t *testing.T, f *personalFixture, user string, c Command) T {
	t.Helper()
	r, err := f.service.Execute(context.Background(), user, uuid.New(), c)
	if err != nil {
		t.Fatalf("%s: %v", c.Kind, err)
	}
	var result T
	if err = json.Unmarshal(r.Body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func createPersonal(t *testing.T, f *personalFixture, user string, ids ...string) AddResult {
	t.Helper()
	return executePersonal[AddResult](t, f, user, Command{Kind: "create", Name: "测试歌单", TrackIDs: ids})
}
func playlistRefs(t *testing.T, f *personalFixture, user, playlist string) []ItemRef {
	t.Helper()
	rows, err := f.database.Query(`SELECT id::text,track_id::text,row_number() OVER(ORDER BY order_key),added_at FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid ORDER BY order_key`, user, playlist)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := []ItemRef{}
	for rows.Next() {
		var i ItemRef
		if err = rows.Scan(&i.ID, &i.TrackID, &i.Position, &i.AddedAt); err != nil {
			t.Fatal(err)
		}
		result = append(result, i)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
func bulkPersonalTracks(t *testing.T, f *personalFixture, n int) []string {
	t.Helper()
	r := f.release(t, "批量合成发行")
	f.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
 SELECT ('10000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,$1::uuid,i,'合成曲目','合成艺人',1,$2::uuid,'bulk-'||i||'.flac','present',NOW(),'physical',200 FROM generate_series(1,$3::int) i`, r.mediumID, f.rootID, n)
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("10000000-0000-4000-8000-%012d", i+1)
	}
	return ids
}
func TestPostgreSQLPersonalPlaylistCRUDRetryAndIsolation(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	a, b := f.user(t), f.user(t)
	r := f.release(t, "专辑")
	x, y := f.track(t, r, "一", 200), f.track(t, r, "二", 0)
	c := Command{Kind: "create", Name: "  晚间聆听  ", TrackIDs: []string{x, y, y}}
	key := uuid.New()
	raw, err := f.service.Execute(ctx, a, key, c)
	if err != nil {
		t.Fatal(err)
	}
	var created AddResult
	if err := json.Unmarshal(raw.Body, &created); err != nil {
		t.Fatal(err)
	}
	if raw.Status != 201 || created.AddedCount != 2 || created.ExistingCount != 0 || created.DuplicateCount != 1 || created.Playlist.Name != "晚间聆听" || created.Playlist.Revision != 1 {
		t.Fatalf("create: %+v", created)
	}
	replay, err := f.service.Execute(ctx, a, key, c)
	if err != nil || !bytes.Equal(raw.Body, replay.Body) || raw.Status != replay.Status {
		t.Fatalf("replay: %v", err)
	}
	c.TrackIDs = []string{x, y}
	if _, err = f.service.Execute(ctx, a, key, c); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different duplicate count: %v", err)
	}
	if _, err = f.service.Execute(ctx, b, key, c); err != nil {
		t.Fatalf("other owner same key: %v", err)
	}
	p := created.Playlist
	if _, err = f.service.Execute(ctx, b, uuid.New(), Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: 1, Name: "不允许"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ownership: %v", err)
	}
	same := executePersonal[RenameResult](t, f, a, Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: 1, Name: p.Name})
	if same.Changed || same.Playlist.Revision != 1 || !same.Playlist.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatal("same name changed")
	}
	renamed := executePersonal[RenameResult](t, f, a, Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: 1, Name: "周末"})
	if !renamed.Changed || renamed.Playlist.Revision != 2 {
		t.Fatal("rename")
	}
	if _, err = f.service.Execute(ctx, a, uuid.New(), Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: 1, Name: "周末"}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale noop: %v", err)
	}
	nonexistent := uuid.New()
	before := f.count(t, `SELECT count(*) FROM personal_playlists WHERE user_id=$1`, a)
	if _, err = f.service.Execute(ctx, a, uuid.New(), Command{Kind: "create", Name: "原子", TrackIDs: []string{x, nonexistent}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing target: %v", err)
	}
	if f.count(t, `SELECT count(*) FROM personal_playlists WHERE user_id=$1`, a) != before {
		t.Fatal("partial create")
	}
	deletion := Command{Kind: "delete", PlaylistID: p.ID, ExpectedRevision: 2}
	deleteKey := uuid.New()
	deleted, err := f.service.Execute(ctx, a, deleteKey, deletion)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.service.Execute(ctx, a, deleteKey, deletion)
	if err != nil || !bytes.Equal(deleted.Body, again.Body) {
		t.Fatal("delete replay")
	}
	c = Command{Kind: "create", Name: "晚间聆听", TrackIDs: []string{x, y, y}}
	old, err := f.service.Execute(ctx, a, key, c)
	if err != nil || !bytes.Equal(old.Body, raw.Body) {
		t.Fatal("create receipt lost after deletion")
	}
	if f.count(t, `SELECT count(*) FROM personal_playlists WHERE user_id=$1 AND id=$2`, a, p.ID) != 0 {
		t.Fatal("replayed creation resurrected playlist")
	}
}

func TestPostgreSQLPersonalPlaylistUndoPolicies(t *testing.T) {
	for _, scenario := range []string{"original", "after_previous", "previous_readded", "original_index", "end", "already_present"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPersonalFixture(t)
			u := f.user(t)
			r := f.release(t, "撤销")
			ids := []string{f.track(t, r, "A", 1), f.track(t, r, "B", 1), f.track(t, r, "C", 1), f.track(t, r, "D", 1)}
			p := createPersonal(t, f, u, ids...).Playlist
			refs := playlistRefs(t, f, u, p.ID)
			removed := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[1].ID, ExpectedRevision: p.Revision})
			p = removed.Playlist
			wantPlacement := scenario
			switch scenario {
			case "after_previous":
				moved := executePersonal[MoveResult](t, f, u, Command{Kind: "move_item", PlaylistID: p.ID, ItemID: refs[0].ID, ExpectedRevision: p.Revision, Action: "last"})
				p = moved.Playlist
			case "previous_readded":
				v := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[0].ID, ExpectedRevision: p.Revision})
				p = v.Playlist
				p = executePersonal[AddResult](t, f, u, Command{Kind: "add_items", PlaylistID: p.ID, ExpectedRevision: p.Revision, TrackIDs: ids[:1]}).Playlist
				wantPlacement = "after_previous"
			case "original_index", "end":
				v := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[0].ID, ExpectedRevision: p.Revision})
				p = v.Playlist
				if scenario == "end" {
					for _, ref := range playlistRefs(t, f, u, p.ID) {
						p = executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: ref.ID, ExpectedRevision: p.Revision}).Playlist
					}
				}
			case "already_present":
				p = executePersonal[AddResult](t, f, u, Command{Kind: "add_items", PlaylistID: p.ID, ExpectedRevision: p.Revision, TrackIDs: []string{ids[1]}}).Playlist
				wantPlacement = "unchanged"
			}
			result := executePersonal[RestoreResult](t, f, u, Command{Kind: "restore_item", PlaylistID: p.ID, Token: removed.Undo.Token})
			if result.Placement != wantPlacement {
				t.Fatalf("placement: %+v", result)
			}
			if scenario == "already_present" {
				if result.Outcome != "already_present" || result.Playlist.Revision != p.Revision || result.Item.Position != p.TrackCount {
					t.Fatalf("already present: %+v", result)
				}
			} else {
				if result.Outcome != "restored" || result.Item.ID != refs[1].ID || !result.Item.AddedAt.Equal(refs[1].AddedAt) || result.Playlist.Revision != p.Revision+1 {
					t.Fatalf("restored: %+v", result)
				}
			}
			if scenario == "after_previous" || scenario == "previous_readded" {
				if result.Item.Position != result.Playlist.TrackCount {
					t.Fatalf("not after moved predecessor: %+v", result)
				}
			}
			twice := executePersonal[RestoreResult](t, f, u, Command{Kind: "restore_item", PlaylistID: p.ID, Token: removed.Undo.Token})
			if !reflect.DeepEqual(twice, result) {
				t.Fatalf("new key token replay changed result")
			}
			if f.count(t, `SELECT count(*) FROM personal_playlist_items WHERE user_id=$1 AND playlist_id=$2`, u, p.ID) != int(result.Playlist.TrackCount) {
				t.Fatal("restore duplicated item")
			}
		})
	}
}

func TestPostgreSQLPersonalPlaylistOverLimitRestore(t *testing.T) {
	f := newPersonalFixture(t)
	u := f.user(t)
	ids := bulkPersonalTracks(t, f, 10003)
	p := createPersonal(t, f, u, ids[:10000]...).Playlist
	refs := playlistRefs(t, f, u, p.ID)
	removals := []RemoveResult{}
	for i := 0; i < 2; i++ {
		v := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[i+1].ID, ExpectedRevision: p.Revision})
		p = v.Playlist
		removals = append(removals, v)
	}
	p = executePersonal[AddResult](t, f, u, Command{Kind: "add_items", PlaylistID: p.ID, ExpectedRevision: p.Revision, TrackIDs: ids[10000:10002]}).Playlist
	for i, v := range removals {
		if i == 1 {
			// 已超限的 10,001 项也须完整重整，不能截成普通上限或拒绝第二次恢复。
			f.exec(t, `WITH x AS(SELECT id,row_number() OVER(ORDER BY order_key) n FROM personal_playlist_items WHERE playlist_id=$1) UPDATE personal_playlist_items i SET order_key=x.n+$2 FROM x WHERE x.id=i.id`, p.ID, orderStep)
		}
		previousRevision := p.Revision
		p = executePersonal[RestoreResult](t, f, u, Command{Kind: "restore_item", PlaylistID: p.ID, Token: v.Undo.Token}).Playlist
		if p.TrackCount != int64(10001+i) || p.OverLimitCount != int64(i+1) || p.Revision != previousRevision+1 {
			t.Fatalf("over limit: %+v", p)
		}
	}
	current := playlistRefs(t, f, u, p.ID)
	byID := make(map[string]ItemRef, len(current))
	for _, item := range current {
		byID[item.ID] = item
	}
	for i, original := range refs {
		item := byID[original.ID]
		if item.TrackID != original.TrackID || !item.AddedAt.Equal(original.AddedAt) {
			t.Fatalf("over-limit rebalance changed original item %d", i)
		}
	}
	// 两次移除都以原首项为前驱，第二次恢复须插在其当前之后，而非复原整张旧快照。
	if current[0].ID != refs[0].ID || current[1].ID != refs[2].ID || current[2].ID != refs[1].ID {
		t.Fatal("over-limit restore ignored current predecessor")
	}
	var lastKey int64
	if err := f.database.QueryRow(`SELECT max(order_key) FROM personal_playlist_items WHERE playlist_id=$1`, p.ID).Scan(&lastKey); err != nil {
		t.Fatal(err)
	}
	if lastKey != (p.TrackCount-1)*orderStep {
		t.Fatal("over-limit restore did not rebalance the full interval")
	}
	p = executePersonal[RenameResult](t, f, u, Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: p.Revision, Name: "超限仍可编辑"}).Playlist
	p = executePersonal[MoveResult](t, f, u, Command{Kind: "move_item", PlaylistID: p.ID, ItemID: current[len(current)-1].ID, ExpectedRevision: p.Revision, Action: "first"}).Playlist
	if _, err := f.service.Execute(context.Background(), u, uuid.New(), Command{Kind: "add_items", PlaylistID: p.ID, ExpectedRevision: p.Revision, TrackIDs: ids[10002:]}); !errors.Is(err, ErrTrackLimit) {
		t.Fatalf("added during overlimit: %v", err)
	}
	same := executePersonal[AddResult](t, f, u, Command{Kind: "add_items", PlaylistID: p.ID, ExpectedRevision: p.Revision, TrackIDs: []string{ids[0], ids[0]}})
	if same.AddedCount != 0 || same.ExistingCount != 1 || same.DuplicateCount != 1 || same.Playlist.Revision != p.Revision {
		t.Fatal("overlimit noop")
	}
	queue, err := f.service.PlaylistQueue(context.Background(), u, p.ID)
	if err != nil || queue.Total != 10002 || queue.ReturnedCount != 10000 || queue.OmittedCount != 2 || !queue.Truncated {
		t.Fatalf("overlimit queue: total=%d omitted=%d err=%v", queue.Total, queue.OmittedCount, err)
	}
	for _, ref := range playlistRefs(t, f, u, p.ID)[:3] {
		p = executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: ref.ID, ExpectedRevision: p.Revision}).Playlist
	}
	p = executePersonal[AddResult](t, f, u, Command{Kind: "add_items", PlaylistID: p.ID, ExpectedRevision: p.Revision, TrackIDs: ids[10002:]}).Playlist
	if p.TrackCount != 10000 || p.OverLimitCount != 0 {
		t.Fatal("capacity did not recover")
	}
}

func TestPostgreSQLPersonalPlaylistOrderReferenceAndRebalance(t *testing.T) {
	f := newPersonalFixture(t)
	u := f.user(t)
	r := f.release(t, "排序")
	ids := []string{}
	for i := 0; i < 12; i++ {
		ids = append(ids, f.track(t, r, fmt.Sprint(i), 1))
	}
	p := createPersonal(t, f, u, ids...).Playlist
	refs := playlistRefs(t, f, u, p.ID)
	expected := append([]ItemRef(nil), refs...)
	random := rand.New(rand.NewSource(34))
	actions := []string{"up", "down", "first", "last", "before"}
	for step := 0; step < 160; step++ {
		source := random.Intn(len(expected))
		action := actions[random.Intn(len(actions))]
		c := Command{Kind: "move_item", PlaylistID: p.ID, ItemID: expected[source].ID, ExpectedRevision: p.Revision, Action: action}
		dest := source
		switch action {
		case "up":
			dest = max(source-1, 0)
		case "down":
			dest = min(source+1, len(expected)-1)
		case "first":
			dest = 0
		case "last":
			dest = len(expected) - 1
		case "before":
			anchor := random.Intn(len(expected) + 1)
			if anchor < len(expected) {
				id := expected[anchor].ID
				c.BeforeItemID = &id
			}
			dest = anchor
			if anchor > source {
				dest--
			}
		}
		if step == 30 {
			f.exec(t, `WITH x AS(SELECT id,row_number() OVER(ORDER BY order_key) n FROM personal_playlist_items WHERE playlist_id=$1) UPDATE personal_playlist_items i SET order_key=x.n FROM x WHERE x.id=i.id`, p.ID)
		}
		if step == 60 {
			last := expected[len(expected)-1].ID
			f.exec(t, `UPDATE personal_playlist_items SET order_key=$2 WHERE id=$1`, last, int64(math.MaxInt64))
		}
		if step == 90 {
			first := expected[0].ID
			f.exec(t, `UPDATE personal_playlist_items SET order_key=$2 WHERE id=$1`, first, int64(math.MinInt64))
		}
		result := executePersonal[MoveResult](t, f, u, c)
		if result.Changed != (source != dest) || result.Moved.Position != int64(dest+1) {
			t.Fatalf("step %d action %s source %d dest %d: %+v", step, action, source, dest, result)
		}
		if source != dest {
			item := expected[source]
			expected = append(expected[:source], expected[source+1:]...)
			expected = append(expected, ItemRef{})
			copy(expected[dest+1:], expected[dest:])
			expected[dest] = item
		}
		got := playlistRefs(t, f, u, p.ID)
		for i := range expected {
			if got[i].ID != expected[i].ID || got[i].Position != int64(i+1) || !got[i].AddedAt.Equal(expected[i].AddedAt) {
				t.Fatalf("step %d order at %d", step, i)
			}
		}
		if result.Playlist.Revision != p.Revision+boolInt(result.Changed) {
			t.Fatal("revision advanced incorrectly")
		}
		p = result.Playlist
	}
}
func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func TestPostgreSQLPersonalPlaylistConcurrentKeysQuotaAndCancellation(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	u := f.user(t)
	key := uuid.New()
	cmd := Command{Kind: "create", Name: "并发"}
	tx, err := f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = lockPersonal(ctx, tx, personalKeyLock, u+":"+key); err != nil {
		t.Fatal(err)
	}
	outputs := make(chan Result, 2)
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := f.service.Execute(ctx, u, key, cmd); outputs <- r; failures <- e }()
	}
	sum := sha256.Sum256([]byte(u + ":" + key))
	waitPersonalLocks(t, f, ctx, personalKeyLock, binary.BigEndian.Uint32(sum[:4]), 2)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if e := <-failures; e != nil {
		t.Fatal(e)
	}
	if e := <-failures; e != nil {
		t.Fatal(e)
	}
	a, b := <-outputs, <-outputs
	if !bytes.Equal(a.Body, b.Body) || f.count(t, `SELECT count(*) FROM personal_playlists WHERE user_id=$1`, u) != 1 {
		t.Fatal("concurrent retry duplicated")
	}
	// 同一普通修订号只能提交一次实际改动。
	var state AddResult
	if err := json.Unmarshal(a.Body, &state); err != nil {
		t.Fatal(err)
	}
	p := state.Playlist
	for _, name := range []string{"甲", "乙"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, e := f.service.Execute(ctx, u, uuid.New(), Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: p.Revision, Name: name})
			failures <- e
		}(name)
	}
	wg.Wait()
	ok, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		e := <-failures
		if e == nil {
			ok++
		} else if errors.Is(e, ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatal("stale revision accepted")
	}
	// 999→1000 的配额锁在提交后新语句计数。
	f.exec(t, `INSERT INTO personal_playlists(id,user_id,name) SELECT gen_random_uuid(),$1::uuid,'配额' FROM generate_series(1,998)`, u)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := f.service.Execute(ctx, u, uuid.New(), cmd); failures <- e }()
	}
	wg.Wait()
	ok, conflicts = 0, 0
	for i := 0; i < 2; i++ {
		e := <-failures
		if e == nil {
			ok++
		} else if errors.Is(e, ErrPlaylistLimit) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflicts != 1 || f.count(t, `SELECT count(*) FROM personal_playlists WHERE user_id=$1`, u) != 1000 {
		t.Fatal("quota race")
	}
	// 取消等待者不留下收据，锁释放后同键仍可完成。
	other := f.user(t)
	key = uuid.New()
	tx, err = f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = lockPersonal(ctx, tx, personalKeyLock, other+":"+key); err != nil {
		t.Fatal(err)
	}
	waitCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, e := f.service.Execute(waitCtx, other, key, cmd); done <- e }()
	sum = sha256.Sum256([]byte(other + ":" + key))
	waitPersonalLocks(t, f, ctx, personalKeyLock, binary.BigEndian.Uint32(sum[:4]), 1)
	stop()
	if e := <-done; e == nil {
		t.Fatal("cancel succeeded")
	}
	tx.Rollback()
	if f.count(t, `SELECT count(*) FROM personal_playlist_requests WHERE user_id=$1`, other) != 0 {
		t.Fatal("cancel receipt")
	}
	if _, err = f.service.Execute(ctx, other, key, cmd); err != nil {
		t.Fatal(err)
	}
}
func waitPersonalLocks(t *testing.T, f *personalFixture, ctx context.Context, namespace int32, key uint32, n int) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := f.database.QueryRowContext(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND classid=$1::oid AND objid=$2::oid AND NOT granted`, uint32(namespace), key).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count >= n {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestPostgreSQLPersonalSchemaLifecycleAndRevisionExhaustion(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	u, v := f.user(t), f.user(t)
	r := f.release(t, "约束")
	id := f.track(t, r, "曲目", 1)
	p := createPersonal(t, f, u, id).Playlist
	refs := playlistRefs(t, f, u, p.ID)
	for _, tc := range []struct {
		user, code string
	}{
		{v, "23503"},
		{u, "23505"},
	} {
		// 连续占位符避免参数类型推断失败冒充外键/唯一约束生效。
		_, err := f.database.Exec(`INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key) VALUES(gen_random_uuid(),$1::uuid,$2::uuid,$3::uuid,2)`, tc.user, p.ID, id)
		requirePlaylistSQLState(t, err, tc.code)
	}
	_, err := f.service.SetFavorite(ctx, u, "track", id, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec(`DELETE FROM tracks WHERE id=$1`, id); err == nil {
		t.Fatal("track reference not restricted")
	}
	removed := executePersonal[RemoveResult](t, f, u, Command{Kind: "remove_item", PlaylistID: p.ID, ItemID: refs[0].ID, ExpectedRevision: p.Revision})
	p = removed.Playlist
	f.exec(t, `UPDATE personal_playlists SET revision=$2 WHERE id=$1`, p.ID, MaxRevision)
	if _, err = f.service.Execute(ctx, u, uuid.New(), Command{Kind: "rename", PlaylistID: p.ID, ExpectedRevision: MaxRevision, Name: "溢出"}); err == nil {
		t.Fatal("revision overflow")
	}
	f.exec(t, `UPDATE personal_playlists SET revision=$2 WHERE id=$1`, p.ID, p.Revision)
	executePersonal[RestoreResult](t, f, u, Command{Kind: "restore_item", PlaylistID: p.ID, Token: removed.Undo.Token})
	f.exec(t, `DELETE FROM users WHERE id=$1`, u)
	for _, table := range []string{"personal_favorite_state", "favorite_tracks", "favorite_albums", "personal_playlists", "personal_playlist_items", "personal_playlist_undo", "personal_playlist_requests"} {
		if f.count(t, `SELECT count(*) FROM `+table+` WHERE user_id=$1`, u) != 0 {
			t.Fatalf("cascade %s", table)
		}
	}
	// 完整迁移重复启动不能重放或漂移 checksum。
	var before string
	if err = f.database.QueryRow(`SELECT string_agg(version::text||checksum||applied_at::text,',' ORDER BY version) FROM schema_migrations`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	reopened, openErr := postgres.Open(ctx, f.databaseURL)
	if openErr != nil {
		t.Fatal(openErr)
	}
	reopened.Close()
	var after string
	if err = f.database.QueryRow(`SELECT string_agg(version::text||checksum||applied_at::text,',' ORDER BY version) FROM schema_migrations`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("migration rerun changed metadata")
	}
}

func TestPersonalNamesAndOrderIntervals(t *testing.T) {
	for _, name := range []string{"", " \t ", strings.Repeat("曲", 101), "有\x00控制", "有\n换行"} {
		if _, err := NormalizeName(name); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, name := range []string{strings.Repeat("曲", 100), strings.Repeat("𝄞", 100), "  名字  "} {
		if _, err := NormalizeName(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]int64{{math.MinInt64, math.MaxInt64}, {-5, -2}, {0, 2}, {math.MaxInt64 - 2, math.MaxInt64}} {
		k, ok := intervalKey(&playlistItem{OrderKey: pair[0]}, &playlistItem{OrderKey: pair[1]})
		if !ok || k <= pair[0] || k >= pair[1] {
			t.Fatalf("midpoint %v => %d", pair, k)
		}
	}
	if _, ok := intervalKey(&playlistItem{OrderKey: 1}, &playlistItem{OrderKey: 2}); ok {
		t.Fatal("adjacent interval")
	}
	if personalKeyLock == personalQuotaLock || personalKeyLock == 0x4f504552 || personalQuotaLock == 0x524f4f54 {
		t.Fatal("lock namespaces collide")
	}
}
