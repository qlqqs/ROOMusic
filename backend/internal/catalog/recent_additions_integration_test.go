package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLRecentAdditionsProjectionAndSort(t *testing.T) {
	f := newEffectiveFixture(t)
	ctx := context.Background()
	empty, err := f.store.ListRecentlyAddedReleaseGroups(ctx)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty recent = %#v, %v", empty, err)
	}
	old := f.visibleRelease("旧版")
	newer := f.visibleRelease("检索命中新版")
	hidden := f.release("不可见更早版本")
	f.track(hidden, "missing", "missing", 100)
	f.exec(`UPDATE releases SET created_at='2020-01-01T00:00:00Z' WHERE id=$1::uuid`, old.releaseID)
	f.exec(`UPDATE releases SET group_id=$1::uuid,created_at='2025-01-01T00:00:00Z' WHERE id=$2::uuid`, old.groupID, newer.releaseID)
	f.exec(`UPDATE releases SET group_id=$1::uuid,created_at='2010-01-01T00:00:00Z' WHERE id=$2::uuid`, old.groupID, hidden.releaseID)
	f.exec(`UPDATE release_groups SET default_release_id=$2::uuid WHERE id=$1::uuid`, old.groupID, newer.releaseID)
	detail, err := f.store.LoadReleaseGroup(ctx, old.groupID)
	wantDate := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err != nil || !detail.AddedAt.Equal(wantDate) || detail.Representative.ID != newer.releaseID || detail.ReleaseCount != 2 {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	searched, err := f.store.ListReleaseGroups(ctx, ListParams{Search: "检索命中新版", Sort: ReleaseGroupSortAddedAtDesc, Page: 1, PageSize: 20})
	if err != nil || len(searched.Items) != 1 || !searched.Items[0].AddedAt.Equal(wantDate) {
		t.Fatalf("searched = %+v, %v", searched, err)
	}
	// 禁用目录不改变已有 catalog 的可见性；十张同时间专辑按组 UUID 倒序稳定分页。
	f.exec(`UPDATE library_roots SET status='disabled' WHERE id=$1::uuid`, f.rootID)
	for i := 0; i < 10; i++ {
		r := f.visibleRelease(fmt.Sprintf("新专辑%02d", i))
		f.exec(`UPDATE releases SET created_at='2026-01-01T00:00:00Z' WHERE id=$1::uuid`, r.releaseID)
	}
	recent, err := f.store.ListRecentlyAddedReleaseGroups(ctx)
	if err != nil || len(recent) != 8 {
		t.Fatalf("recent = %+v, %v", recent, err)
	}
	page, err := f.store.ListReleaseGroups(ctx, ListParams{Sort: ReleaseGroupSortAddedAtDesc, Page: 1, PageSize: 8})
	if err != nil || page.Total != 11 || !reflect.DeepEqual(page.Items, recent) {
		t.Fatalf("page differs from recent: %+v, %v", page, err)
	}
	for i := 1; i < len(recent); i++ {
		if recent[i-1].ID <= recent[i].ID {
			t.Fatal("equal-time group ordering unstable")
		}
	}
	second, err := f.store.ListReleaseGroups(ctx, ListParams{Sort: ReleaseGroupSortAddedAtDesc, Page: 2, PageSize: 8})
	if err != nil || len(second.Items) != 3 || second.Items[2].ID != old.groupID {
		t.Fatalf("second = %+v, %v", second, err)
	}
	// 原版不可见后加入时间变新；恢复后回到原时间，而非恢复时刻。
	f.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, old.mediumID)
	detail, err = f.store.LoadReleaseGroup(ctx, old.groupID)
	if err != nil || detail.AddedAt.Year() != 2025 {
		t.Fatalf("hidden oldest: %+v, %v", detail, err)
	}
	f.exec(`UPDATE tracks SET source_status='present' WHERE medium_id=$1::uuid`, old.mediumID)
	detail, err = f.store.LoadReleaseGroup(ctx, old.groupID)
	if err != nil || !detail.AddedAt.Equal(wantDate) {
		t.Fatalf("restored oldest: %+v, %v", detail, err)
	}
}

func TestPostgreSQLScanAddedAlbumsHistoryAndSnapshot(t *testing.T) {
	f := newEffectiveFixture(t)
	ctx := context.Background()
	scanID := uuid.New()
	f.exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'succeeded',NOW())`, scanID)
	a, b := f.visibleRelease("新增甲"), f.visibleRelease("新增乙")
	for _, r := range []placementRelease{a, b} {
		f.exec(`INSERT INTO scan_added_releases(scan_run_id,release_id,group_id_at_creation,added_at,title_at_creation,artist_at_creation)
   VALUES($1::uuid,$2::uuid,$3::uuid,'2026-01-01T00:00:00Z','扫描标题','扫描艺人')`, scanID, r.releaseID, r.groupID)
	}
	read := func(store *Store, page, size int) ScanAddedAlbumPage {
		t.Helper()
		result, err := store.ListScanAddedAlbums(ctx, scanID, page, size)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := read(f.store, 1, 20)
	if before.Total != 2 || len(before.Items) != 2 || before.Items[0].ReleaseID <= before.Items[1].ReleaseID {
		t.Fatalf("history = %+v", before)
	}
	if one, two := read(f.store, 1, 1), read(f.store, 2, 1); one.Total != 2 || two.Total != 2 || one.Items[0].ReleaseID != before.Items[0].ReleaseID || two.Items[0].ReleaseID != before.Items[1].ReleaseID {
		t.Fatal("history pagination changed")
	}
	if beyond := read(f.store, 3, 1); beyond.Total != 2 || beyond.Items == nil || len(beyond.Items) != 0 {
		t.Fatalf("beyond = %+v", beyond)
	}
	tx, err := f.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshot := read(NewStore(tx), 1, 20)
	f.exec(`UPDATE releases SET group_id=$1::uuid,title='当前标题' WHERE id=$2::uuid`, a.groupID, b.releaseID)
	if got := read(NewStore(tx), 1, 20); !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("snapshot changed: %+v", got)
	}
	after := read(f.store, 1, 20)
	for _, item := range after.Items {
		if item.CurrentGroupID == nil || *item.CurrentGroupID != a.groupID || item.Album == nil || item.Album.ReleaseCount != 2 || item.TitleAtScan != "扫描标题" || item.ArtistAtScan != "扫描艺人" {
			t.Fatalf("dynamic item = %+v", item)
		}
		if item.ReleaseID == b.releaseID && item.GroupIDAtScan != b.groupID {
			t.Fatal("historical group overwritten")
		}
	}
	// 某个新增版本失去可见曲目后，即使同组仍可见，也不能给它错误的播放卡片。
	f.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1::uuid`, b.mediumID)
	after = read(f.store, 1, 20)
	if after.Total != 2 {
		t.Fatal("invisible item removed from history")
	}
	for _, item := range after.Items {
		if item.ReleaseID == b.releaseID && (item.CurrentGroupID != nil || item.Album != nil) {
			t.Fatalf("invisible item has card: %+v", item)
		}
	}
}
