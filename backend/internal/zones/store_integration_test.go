package zones

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

// 每个用例独占 schema；目录和曲目只有合成数据库行，不访问音乐文件。
type zonesFixture struct {
	database *sql.DB
	tracks   []string
}

func newZonesFixture(t *testing.T) *zonesFixture {
	t.Helper()
	scopedURL := postgrestest.NewSchema(t, "roomusic_test_zones_")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, scopedURL)
	if err != nil {
		t.Fatalf("打开隔离数据库：%v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("关闭数据库：%v", err)
		}
	})
	f := &zonesFixture{database: database}
	root, group, release, medium := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,'/synthetic/zones','active',1)`, root)
	f.exec(t, `INSERT INTO release_groups(id) VALUES($1)`, group)
	f.exec(t, `INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'测试发行','测试艺人')`, release, group)
	f.exec(t, `INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, medium, release)
	for i := 0; i < 4; i++ {
		id := uuid.New()
		f.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at)
   VALUES($1,$2,$3,'测试曲目','测试艺人',$4,$5,NOW())`, id, medium, i+1, root, fmt.Sprintf("track-%d.flac", i))
		f.tracks = append(f.tracks, id)
	}
	return f
}

func (f *zonesFixture) exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	if _, err := f.database.Exec(statement, args...); err != nil {
		t.Fatalf("建立夹具：%v", err)
	}
}

func (f *zonesFixture) user(t *testing.T) string {
	t.Helper()
	id := uuid.New()
	f.exec(t, `INSERT INTO users(id,username,password_hash,role) VALUES($1,$2,'test-only','user')`, id, "zones-"+id)
	return id
}

func (f *zonesFixture) zone(t *testing.T, userID string, createdAt time.Time) (string, string) {
	t.Helper()
	deviceID, zoneID := uuid.New(), uuid.New()
	f.exec(t, `INSERT INTO playback_devices(id,user_id,name,created_at,last_seen_at) VALUES($1,$2,'测试设备',$3,$3)`, deviceID, userID, createdAt)
	f.exec(t, `INSERT INTO playback_zones(id,user_id,device_id,position_saved_at) VALUES($1,$2,$3,$4)`, zoneID, userID, deviceID, createdAt)
	return zoneID, deviceID
}

func (f *zonesFixture) entry(track int) queue.Entry {
	return queue.Entry{EntryID: uuid.New(), TrackID: f.tracks[track]}
}

func (f *zonesFixture) begin(t *testing.T) *sql.Tx {
	t.Helper()
	tx, err := f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func (f *zonesFixture) write(t *testing.T, change func(*Store)) {
	t.Helper()
	tx := f.begin(t)
	change(NewStore(tx))
	if err := tx.Commit(); err != nil {
		t.Fatalf("提交播放区事务：%v", err)
	}
}

func (f *zonesFixture) load(t *testing.T, userID string) UserState {
	t.Helper()
	state, err := LoadUser(context.Background(), f.database, userID)
	if err != nil {
		t.Fatalf("加载用户播放区：%v", err)
	}
	return state
}

func (f *zonesFixture) positions(t *testing.T, zoneID string) map[string]int {
	t.Helper()
	rows, err := f.database.Query(`SELECT entry_id,position FROM playback_queue_entries WHERE zone_id=$1`, zoneID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	positions := map[string]int{}
	for rows.Next() {
		var id string
		var position int
		if err := rows.Scan(&id, &position); err != nil {
			t.Fatal(err)
		}
		positions[id] = position
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return positions
}

func requireZonesOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func requireZonesChanged(t *testing.T, changed bool, err error) {
	t.Helper()
	requireZonesOK(t, err)
	if !changed {
		t.Fatal("期望条目发生变化")
	}
}

func requireZoneEntries(t *testing.T, got PersistedZone, want ...queue.Entry) {
	t.Helper()
	if !slices.Equal(got.Queue.Entries, want) {
		t.Fatalf("条目顺序 = %+v，期望 %+v", got.Queue.Entries, want)
	}
}

func TestPostgreSQLZonesStoreRoundTrip(t *testing.T) {
	f := newZonesFixture(t)
	ctx := context.Background()
	user := f.user(t)
	at := time.Date(2026, 11, 9, 12, 0, 0, 123000000, time.UTC)
	// 反向创建两个设备，验证 LoadUser 的设备与播放区顺序一致。
	laterZone, laterDevice := f.zone(t, user, at.Add(time.Hour))
	zoneID, deviceID := f.zone(t, user, at)
	initial := f.load(t, user)
	if len(initial.Devices) != 2 || len(initial.Zones) != 2 ||
		initial.Devices[0].DeviceID != deviceID || initial.Devices[1].DeviceID != laterDevice ||
		initial.Zones[0].ZoneID != zoneID || initial.Zones[1].ZoneID != laterZone {
		t.Fatalf("设备与播放区排序错误：%+v", initial)
	}
	zone := initial.Zones[0]
	if zone.Queue.CurrentEntryID != "" || zone.ImportedAt != nil ||
		zone.Queue.History == nil || len(zone.Queue.History) != 0 || zone.Queue.Bag == nil || len(zone.Queue.Bag) != 0 {
		t.Fatalf("默认数组/null字段错误：%+v", zone)
	}
	if !zone.Settings.Gapless || zone.Settings.Quality != "lossless" || zone.Settings.SampleRate != DefaultSampleRateDocument() {
		t.Fatalf("默认设置错误：%+v", zone.Settings)
	}
	a, b, c, x, y := f.entry(0), f.entry(1), f.entry(2), f.entry(3), f.entry(0)
	f.write(t, func(store *Store) {
		requireZonesOK(t, store.InsertEntries(ctx, user, zoneID, "", []queue.Entry{a, b, c}))
		requireZonesOK(t, store.InsertEntries(ctx, user, zoneID, a.EntryID, []queue.Entry{x}))
	})
	requireZoneEntries(t, f.load(t, user).Zones[0], a, x, b, c)
	f.write(t, func(store *Store) {
		changed, err := store.DeleteEntry(ctx, user, zoneID, x.EntryID)
		requireZonesChanged(t, changed, err)
	})
	wantPositions := map[string]int{a.EntryID: 0, b.EntryID: 2, c.EntryID: 3}
	if got := f.positions(t, zoneID); !reflect.DeepEqual(got, wantPositions) {
		t.Fatalf("删除应保留空隙：%v", got)
	}
	f.write(t, func(store *Store) {
		requireZonesOK(t, store.InsertEntries(ctx, user, zoneID, b.EntryID, []queue.Entry{y}))
		// a 与 b 在逻辑列表里相邻，但实际位置中间有空隙。
		changed, err := store.SwapEntries(ctx, user, zoneID, a.EntryID, b.EntryID)
		requireZonesChanged(t, changed, err)
		zone.Queue.CurrentEntryID, zone.Queue.Mode = y.EntryID, queue.ModeShuffle
		zone.Queue.History, zone.Queue.Bag = []string{b.EntryID, a.EntryID, b.EntryID}, []string{c.EntryID, a.EntryID}
		zone.PositionSeconds, zone.PositionSavedAt = 12.5, at.Add(2*time.Minute)
		sampleRate, err := ParseSampleRateDocument([]byte(`{"strategy":"custom","custom_rate":96000,"filter":"minimum_smooth"}`))
		requireZonesOK(t, err)
		zone.Settings = PlaybackSettings{Leveling: "album", Crossfade: true, CrossfadeSeconds: 8, Gapless: false, Quality: "lossy", SampleRate: sampleRate, Downmix: DefaultDownmixDocument()}
		zone.Revision, zone.QueueRevision, zone.ImportedAt = 7, 4, &at
		requireZonesOK(t, store.UpdateZone(ctx, user, zone))
	})
	loaded := f.load(t, user).Zones[0]
	requireZoneEntries(t, loaded, b, a, y, c)
	zone.Queue.Entries = []queue.Entry{b, a, y, c}
	// PostgreSQL 保留时间点；不依赖驱动返回的时区指针身份。
	loaded.PositionSavedAt = loaded.PositionSavedAt.UTC()
	if loaded.ImportedAt != nil {
		utc := loaded.ImportedAt.UTC()
		loaded.ImportedAt = &utc
	}
	if !reflect.DeepEqual(loaded, zone) {
		t.Fatalf("播放区行往返：实际 %+v，期望 %+v", loaded, zone)
	}
	replacement := []queue.Entry{f.entry(2), f.entry(1)}
	f.write(t, func(store *Store) {
		requireZonesOK(t, store.ReplaceEntries(ctx, user, zoneID, replacement))
		zone.Queue.CurrentEntryID, zone.Queue.History, zone.Queue.Bag = "", nil, nil
		zone.ImportedAt = nil
		zone.QueueRevision++
		requireZonesOK(t, store.UpdateZone(ctx, user, zone))
	})
	loaded = f.load(t, user).Zones[0]
	requireZoneEntries(t, loaded, replacement...)
	if loaded.Queue.CurrentEntryID != "" || loaded.ImportedAt != nil ||
		loaded.Queue.History == nil || len(loaded.Queue.History) != 0 || loaded.Queue.Bag == nil || len(loaded.Queue.Bag) != 0 {
		t.Fatalf("清除后的数组/null字段错误：%+v", loaded)
	}
}

func TestPostgreSQLZonesUserIsolation(t *testing.T) {
	f := newZonesFixture(t)
	ctx := context.Background()
	userA, userB := f.user(t), f.user(t)
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	zoneA, _ := f.zone(t, userA, at)
	zoneB, _ := f.zone(t, userB, at)
	a, a2, b, b2 := f.entry(0), f.entry(2), f.entry(1), f.entry(3)
	f.write(t, func(store *Store) {
		requireZonesOK(t, store.InsertEntries(ctx, userA, zoneA, "", []queue.Entry{a, a2}))
		requireZonesOK(t, store.InsertEntries(ctx, userB, zoneB, "", []queue.Entry{b, b2}))
	})
	beforeA, beforeB := f.load(t, userA), f.load(t, userB)
	for _, tc := range []struct {
		state   UserState
		zoneID  string
		entries []queue.Entry
	}{{beforeA, zoneA, []queue.Entry{a, a2}}, {beforeB, zoneB, []queue.Entry{b, b2}}} {
		if len(tc.state.Devices) != 1 || len(tc.state.Zones) != 1 || tc.state.Zones[0].ZoneID != tc.zoneID {
			t.Fatalf("读取泄漏其他用户设备/播放区：%+v", tc.state)
		}
		requireZoneEntries(t, tc.state.Zones[0], tc.entries...)
	}
	// 双向借用另一用户的区 ID，所有写原语都必须拒绝且可以安全提交无操作事务。
	for _, tc := range []struct {
		actor         string
		target        PersistedZone
		entry, second queue.Entry
	}{
		{userB, beforeA.Zones[0], a, a2}, {userA, beforeB.Zones[0], b, b2},
	} {
		f.write(t, func(store *Store) {
			tc.target.PositionSeconds = 42
			for _, err := range []error{
				store.InsertEntries(ctx, tc.actor, tc.target.ZoneID, "", []queue.Entry{f.entry(2)}),
				store.ReplaceEntries(ctx, tc.actor, tc.target.ZoneID, []queue.Entry{f.entry(3)}),
				store.UpdateZone(ctx, tc.actor, tc.target),
			} {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("越权写入错误 = %v，期望 ErrNotFound", err)
				}
			}
			changed, err := store.DeleteEntry(ctx, tc.actor, tc.target.ZoneID, tc.entry.EntryID)
			requireZonesOK(t, err)
			if changed {
				t.Fatal("删除了其他用户的条目")
			}
			changed, err = store.SwapEntries(ctx, tc.actor, tc.target.ZoneID, tc.entry.EntryID, tc.second.EntryID)
			requireZonesOK(t, err)
			if changed {
				t.Fatal("交换了其他用户的条目")
			}
		})
	}
	// A 的合法区里混入 B 的条目 ID 作为锚点、删除目标或交换目标，同样不改变两边队列。
	f.write(t, func(store *Store) {
		if err := store.InsertEntries(ctx, userA, zoneA, b.EntryID, []queue.Entry{f.entry(2)}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("外区锚点错误 = %v", err)
		}
		changed, err := store.DeleteEntry(ctx, userA, zoneA, b.EntryID)
		requireZonesOK(t, err)
		if changed {
			t.Fatal("跨区删除成功")
		}
		changed, err = store.SwapEntries(ctx, userA, zoneA, a.EntryID, b.EntryID)
		requireZonesOK(t, err)
		if changed {
			t.Fatal("跨区交换成功")
		}
	})
	if !reflect.DeepEqual(f.load(t, userA), beforeA) || !reflect.DeepEqual(f.load(t, userB), beforeB) {
		t.Fatal("越权尝试改变了用户状态")
	}
}

func TestPostgreSQLZonesTransactionRollback(t *testing.T) {
	f := newZonesFixture(t)
	ctx := context.Background()
	user := f.user(t)
	zoneID, _ := f.zone(t, user, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	a, b := f.entry(0), f.entry(1)
	f.write(t, func(store *Store) {
		requireZonesOK(t, store.InsertEntries(ctx, user, zoneID, "", []queue.Entry{a, b}))
	})
	before := f.load(t, user)
	positions := f.positions(t, zoneID)
	tx := f.begin(t)
	store := NewStore(tx)
	added := f.entry(2)
	requireZonesOK(t, store.InsertEntries(ctx, user, zoneID, a.EntryID, []queue.Entry{added}))
	zone := before.Zones[0]
	zone.Queue.CurrentEntryID, zone.Queue.History = added.EntryID, []string{a.EntryID}
	zone.PositionSeconds, zone.Revision, zone.QueueRevision = 23, 1, 1
	requireZonesOK(t, store.UpdateZone(ctx, user, zone))
	// 先成功写入条目与区行，再让同事务后续写入失败；调用方必须整体回滚。
	zone.PositionSeconds = -1
	if err := store.UpdateZone(ctx, user, zone); err == nil {
		t.Fatal("非法位置应写入失败")
	}
	requireZonesOK(t, tx.Rollback())
	if after := f.load(t, user); !reflect.DeepEqual(after, before) {
		t.Fatalf("回滚后状态改变：实际 %+v，期望 %+v", after, before)
	}
	if after := f.positions(t, zoneID); !reflect.DeepEqual(after, positions) {
		t.Fatalf("回滚后条目位置改变：%v", after)
	}
}
