package personal

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLPlaylistReadsStatisticsPaginationAndIsolation(t *testing.T) {
	f := newPersonalFixture(t)
	f.database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	user, other, admin := f.user(t), f.user(t), f.user(t)
	f.exec(t, `UPDATE users SET role='admin' WHERE id=$1::uuid`, admin)
	ids := make([]string, 6)
	groups := make([]string, 6)
	for i := range ids {
		r := f.release(t, "专辑")
		groups[i] = r.groupID
		ids[i] = f.track(t, r, "曲目", float64(i))
	}
	f.setStatus(t, ids[0], "missing")
	f.setStatus(t, ids[2], "missing")
	playlist := readFixturePlaylist(t, f, user, ids)
	empty := readFixturePlaylist(t, f, user, nil)
	foreign := readFixturePlaylist(t, f, other, ids[:1])
	detail, err := f.service.Playlist(ctx, user, playlist)
	if err != nil || detail.Playlist.DurationSeconds != 15 || detail.Playlist.UnknownDurationCount != 1 || detail.Playlist.MissingCount != 2 || len(detail.Playlist.CoverGroups) != 4 || detail.Playlist.TrackCount != 6 || detail.Playlist.TrackLimit != 10000 || detail.Playlist.OverLimitCount != 0 {
		t.Fatalf("detail: %+v %v", detail, err)
	}
	for i, cover := range detail.Playlist.CoverGroups {
		if cover.GroupID != groups[i] {
			t.Fatalf("cover groups: %+v", detail.Playlist.CoverGroups)
		}
	}
	if detail.Playlist.CoverGroups[0].Artwork != nil {
		t.Fatal("missing album acquired artwork")
	}
	first, err := f.service.PlaylistItems(ctx, user, playlist, Page{Number: 1, Size: 2})
	if err != nil || first.PageAfterItemID == nil || len(first.Items) != 2 || first.Items[0].Position != 1 || first.Items[0].Track.Present || first.Pagination.Total != 6 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	next, err := f.service.PlaylistItems(ctx, user, playlist, Page{Number: 2, Size: 2, Revision: &first.Playlist.Revision})
	if err != nil || next.Items[0].Position != 3 || next.Items[0].ID != *first.PageAfterItemID {
		t.Fatalf("next page: %+v %v", next, err)
	}
	last, err := f.service.PlaylistItems(ctx, user, playlist, Page{Number: 3, Size: 2})
	if err != nil || last.PageAfterItemID != nil || last.Items[1].Position != 6 {
		t.Fatalf("last page: %+v %v", last, err)
	}
	beyond, err := f.service.PlaylistItems(ctx, user, playlist, Page{Number: 99, Size: 2})
	if err != nil || beyond.Items == nil || len(beyond.Items) != 0 || beyond.Pagination.Total != 6 || beyond.PageAfterItemID != nil {
		t.Fatalf("beyond: %+v %v", beyond, err)
	}
	revision := int64(2)
	if _, err := f.service.PlaylistItems(ctx, user, playlist, Page{Number: 1, Size: 2, Revision: &revision}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("revision: %v", err)
	}
	list, err := f.service.Playlists(ctx, user, Page{Number: 1, Size: 20})
	if err != nil || list.Pagination.Total != 2 || len(list.Items) != 2 {
		t.Fatalf("list: %+v %v", list, err)
	}
	wantIDs := []string{playlist, empty}
	sort.Strings(wantIDs)
	if list.Items[0].ID != wantIDs[0] || list.Items[1].ID != wantIDs[1] {
		t.Fatal("updated-at tie order")
	}
	emptyDetail, err := f.service.Playlist(ctx, user, empty)
	if err != nil || emptyDetail.Playlist.CoverGroups == nil || emptyDetail.Playlist.DurationSeconds != 0 || emptyDetail.Playlist.UnknownDurationCount != 0 || emptyDetail.Playlist.MissingCount != 0 {
		t.Fatalf("empty detail: %+v %v", emptyDetail, err)
	}
	emptyQueue, err := f.service.PlaylistQueue(ctx, user, empty)
	if err != nil || emptyQueue.Items == nil || emptyQueue.Total != 0 || emptyQueue.Limit != 10000 || emptyQueue.Truncated {
		t.Fatalf("empty queue: %+v %v", emptyQueue, err)
	}
	membership, err := f.service.ContainingTrack(ctx, user, ids[0])
	if err != nil || len(membership.Items) != 1 || membership.Items[0].PlaylistID != playlist || membership.Items[0].Revision != 1 {
		t.Fatalf("membership: %+v %v", membership, err)
	}
	unknown, err := f.service.ContainingTrack(ctx, user, uuid.New())
	if err != nil || unknown.Items == nil || len(unknown.Items) != 0 {
		t.Fatalf("unknown membership: %+v %v", unknown, err)
	}
	for _, actor := range []string{other, admin} {
		if _, err := f.service.Playlist(ctx, actor, playlist); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign detail: %v", err)
		}
		if _, err := f.service.PlaylistItems(ctx, actor, playlist, Page{Number: 1, Size: 50}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign items: %v", err)
		}
		if _, err := f.service.PlaylistQueue(ctx, actor, playlist); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign queue: %v", err)
		}
		got, err := f.service.ContainingTrack(ctx, actor, ids[0])
		if err != nil {
			t.Fatal(err)
		}
		if actor == other && (len(got.Items) != 1 || got.Items[0].PlaylistID != foreign) {
			t.Fatalf("other membership: %+v", got)
		}
		if actor == admin && len(got.Items) != 0 {
			t.Fatalf("admin membership: %+v", got)
		}
	}
	if _, err := f.service.Playlist(ctx, user, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown playlist: %v", err)
	}
	// 文件恢复与曲目换组均反映当前事实，而非歌单创建时的摘要。
	f.setStatus(t, ids[0], "present")
	moved := f.release(t, "新专辑")
	f.exec(t, `UPDATE tracks SET medium_id=$2::uuid,duration_seconds=9 WHERE id=$1::uuid`, ids[0], moved.mediumID)
	updated, err := f.service.Playlist(ctx, user, playlist)
	if err != nil || updated.Playlist.DurationSeconds != 24 || updated.Playlist.UnknownDurationCount != 0 || updated.Playlist.MissingCount != 1 || updated.Playlist.CoverGroups[0].GroupID != moved.groupID || updated.Playlist.Revision != 1 {
		t.Fatalf("current facts: %+v %v", updated, err)
	}
}

func TestPostgreSQLPlaylistQueueOverLimitAndLargeCardPage(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	user := f.user(t)
	ids := readFixtureTracks(t, f, f.release(t, "超限歌单"), 12006)
	large := readFixturePlaylist(t, f, user, ids)
	queue, err := f.service.PlaylistQueue(ctx, user, large)
	if err != nil || queue.Total != 12006 || queue.MissingCount != 2001 || queue.EligibleCount != 10005 || queue.ReturnedCount != 10000 || queue.OmittedCount != 5 || !queue.Truncated || queue.Playlist.OverLimitCount != 2006 {
		t.Fatalf("over-limit queue total=%d missing=%d omitted=%d over=%d err=%v", queue.Total, queue.MissingCount, queue.OmittedCount, queue.Playlist.OverLimitCount, err)
	}
	if queue.Items[0].TrackID != ids[0] || queue.Items[9999].TrackID != ids[11998] {
		t.Fatal("playlist queue order")
	}
	for i := 0; i < 19; i++ {
		readFixturePlaylist(t, f, user, ids[:10001])
	}
	outside := readFixturePlaylist(t, f, user, ids[:1])
	f.exec(t, `UPDATE personal_playlists SET updated_at='2000-01-01T00:00:00Z' WHERE id=$1::uuid`, outside)
	var batchSizes []int
	var groupCalls, summaryCalls int
	f.service.catalogFor = func(tx *sql.Tx) Catalog {
		source := catalog.NewStore(tx)
		return projectionCatalog{Catalog: source,
			statistics: func(ctx context.Context, ids []string) (catalog.TrackCollectionStats, error) {
				batchSizes = append(batchSizes, len(ids))
				return source.LoadTrackCollectionStats(ctx, ids)
			},
			summaries: func(context.Context, []string) (map[string]catalog.TrackSummary, error) {
				summaryCalls++
				return nil, errors.New("cards must not load full tracks")
			},
			groups: func(ctx context.Context, ids []string) (map[string]catalog.PersonalReleaseGroupSummary, error) {
				groupCalls++
				if len(ids) != 1 {
					t.Errorf("shared group not deduplicated: %d", len(ids))
				}
				return source.LoadReleaseGroupSummaries(ctx, ids)
			},
		}
	}
	start := time.Now()
	page, err := f.service.Playlists(ctx, user, Page{Number: 1, Size: 20})
	if err != nil || len(page.Items) != 20 || page.Pagination.Total != 21 {
		t.Fatalf("large card page items=%d total=%d err=%v", len(page.Items), page.Pagination.Total, err)
	}
	for _, item := range page.Items {
		count := int64(10001)
		if item.ID == large {
			count = 12006
		}
		if item.ID == outside || item.TrackCount != count || item.OverLimitCount != count-10000 || item.MissingCount != count/6 || item.UnknownDurationCount != count/7 || item.DurationSeconds != float64(count-count/7)*2 || len(item.CoverGroups) != 1 {
			t.Fatalf("card exact stats: %+v", item)
		}
	}
	if len(batchSizes) < 21 || groupCalls != 1 || summaryCalls != 0 {
		t.Fatalf("bounded calls batches=%v groups=%d summaries=%d", batchSizes, groupCalls, summaryCalls)
	}
	for _, size := range batchSizes {
		if size < 1 || size > TrackLimit {
			t.Fatalf("aggregate reference batch=%d", size)
		}
	}
	t.Logf("20 张卡片、202025 条引用：%s；目录聚合 %d 批、组摘要 %d 批、完整曲目摘要 %d 批", time.Since(start), len(batchSizes), groupCalls, summaryCalls)
}

func TestPostgreSQLPlaylistReadProjectionFailures(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	user := f.user(t)
	track := f.track(t, f.release(t, "失败"), "曲目", 2)
	id := readFixturePlaylist(t, f, user, []string{track})
	service := &Service{database: f.database, catalogFor: func(tx *sql.Tx) Catalog {
		return projectionCatalog{Catalog: catalog.NewStore(tx),
			statistics: func(context.Context, []string) (catalog.TrackCollectionStats, error) {
				return catalog.TrackCollectionStats{}, nil
			},
			facts: func(context.Context, []string) (map[string]catalog.TrackFact, error) {
				return map[string]catalog.TrackFact{}, nil
			},
		}
	}}
	if _, err := service.Playlist(ctx, user, id); err == nil {
		t.Fatal("missing facts on card")
	}
	if _, err := service.PlaylistQueue(ctx, user, id); err == nil {
		t.Fatal("missing facts in queue")
	}
	service.catalogFor = func(tx *sql.Tx) Catalog {
		return projectionCatalog{Catalog: catalog.NewStore(tx), groups: func(context.Context, []string) (map[string]catalog.PersonalReleaseGroupSummary, error) {
			return map[string]catalog.PersonalReleaseGroupSummary{}, nil
		}}
	}
	if _, err := service.Playlist(ctx, user, id); err == nil {
		t.Fatal("missing cover group silently dropped")
	}
	service.catalogFor = func(tx *sql.Tx) Catalog {
		return projectionCatalog{Catalog: catalog.NewStore(tx), summaries: func(context.Context, []string) (map[string]catalog.TrackSummary, error) {
			return map[string]catalog.TrackSummary{}, nil
		}}
	}
	if _, err := service.PlaylistItems(ctx, user, id, Page{Number: 1, Size: 50}); err == nil {
		t.Fatal("missing item summary silently dropped")
	}
	f.exec(t, `UPDATE personal_playlists SET track_count=2 WHERE id=$1::uuid`, id)
	if _, err := f.service.Playlist(ctx, user, id); err == nil {
		t.Fatal("count mismatch accepted in card")
	}
	if _, err := f.service.PlaylistQueue(ctx, user, id); err == nil {
		t.Fatal("count mismatch accepted in queue")
	}
	f.exec(t, `UPDATE personal_playlists SET track_count=1 WHERE id=$1::uuid`, id)
	good, err := f.service.Playlist(ctx, user, id)
	if err != nil || !reflect.DeepEqual(good.Playlist.CoverGroups, []CoverGroup{{GroupID: good.Playlist.CoverGroups[0].GroupID}}) {
		t.Fatalf("read after rollback: %+v %v", good, err)
	}
}
