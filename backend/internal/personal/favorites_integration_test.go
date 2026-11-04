package personal

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLFavoritesToggleIsolationAndAvailability(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, b, admin := f.user(t), f.user(t), f.user(t)
	f.exec(t, `UPDATE users SET role='admin' WHERE id=$1::uuid`, admin)
	release := f.release(t, "收藏专辑")
	track := f.track(t, release, "收藏曲目", 45)
	f.setStatus(t, track, "missing")
	initial, err := f.service.LookupFavorites(ctx, a, nil, nil)
	if err != nil || initial.Revision != 0 || initial.Tracks == nil || initial.Albums == nil {
		t.Fatalf("empty lookup: %+v %v", initial, err)
	}
	if f.count(t, `SELECT COUNT(*) FROM personal_favorite_state`) != 0 {
		t.Fatal("GET created state")
	}
	first, err := f.service.SetFavorite(ctx, a, "track", strings.ToUpper(track), true)
	if err != nil || first.Revision != 1 || first.CreatedAt == nil || first.TargetID != track {
		t.Fatalf("favorite missing: %+v %v", first, err)
	}
	originalFactory := f.service.catalogFor
	f.service.catalogFor = func(*sql.Tx) Catalog { return projectionCatalog{} }
	repeated, err := f.service.SetFavorite(ctx, a, "track", track, true)
	if err != nil || !reflect.DeepEqual(first, repeated) {
		t.Fatalf("repeated favorite: %+v %v", repeated, err)
	}
	deleted, err := f.service.SetFavorite(ctx, a, "track", track, false)
	if err != nil || deleted.Favorited || deleted.CreatedAt != nil || deleted.Revision != 2 {
		t.Fatalf("delete: %+v %v", deleted, err)
	}
	repeated, err = f.service.SetFavorite(ctx, a, "track", track, false)
	if err != nil || !reflect.DeepEqual(deleted, repeated) {
		t.Fatalf("repeat delete: %+v %v", repeated, err)
	}
	repeated, err = f.service.SetFavorite(ctx, a, "album", uuid.New(), false)
	if err != nil || repeated.Revision != 2 {
		t.Fatalf("unknown delete: %+v %v", repeated, err)
	}
	f.service.catalogFor = originalFactory
	if _, err := f.service.SetFavorite(ctx, a, "track", track, true); err != nil {
		t.Fatal(err)
	}
	// 无可见发行的已存在组仍允许收藏；未知身份才返回 404。
	emptyGroup := uuid.New()
	f.exec(t, `INSERT INTO release_groups(id) VALUES($1::uuid)`, emptyGroup)
	if got, err := f.service.SetFavorite(ctx, a, "album", emptyGroup, true); err != nil || got.Revision != 4 {
		t.Fatalf("empty group: %+v %v", got, err)
	}
	for _, kind := range []string{"track", "album"} {
		if _, err := f.service.SetFavorite(ctx, a, kind, uuid.New(), true); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown %s: %v", kind, err)
		}
	}
	state, err := f.service.LookupFavorites(ctx, a, []string{track, strings.ToUpper(track), uuid.New()}, []string{emptyGroup})
	if err != nil || state.Revision != 4 || len(state.Tracks) != 2 || !state.Tracks[0].Favorited || state.Tracks[1].Favorited || !state.Albums[0].Favorited {
		t.Fatalf("lookup: %+v %v", state, err)
	}
	for _, other := range []string{b, admin} {
		state, err := f.service.LookupFavorites(ctx, other, []string{track}, []string{emptyGroup})
		if err != nil || state.Revision != 0 || state.Tracks[0].Favorited || state.Albums[0].Favorited {
			t.Fatalf("other favorites: %+v %v", state, err)
		}
		tracks, err := f.service.FavoriteTracks(ctx, other, Page{Number: 1, Size: 50})
		if err != nil || tracks.Pagination.Total != 0 || tracks.Items == nil {
			t.Fatalf("other tracks: %+v %v", tracks, err)
		}
		albums, err := f.service.FavoriteAlbums(ctx, other, Page{Number: 1, Size: 50})
		if err != nil || albums.Pagination.Total != 0 || albums.Items == nil {
			t.Fatalf("other albums: %+v %v", albums, err)
		}
		queue, err := f.service.FavoriteQueue(ctx, other)
		if err != nil || queue.Total != 0 || queue.Items == nil {
			t.Fatalf("other queue: %+v %v", queue, err)
		}
		if _, err := f.service.SetFavorite(ctx, other, "track", track, false); err != nil {
			t.Fatal(err)
		}
	}
	if f.count(t, `SELECT COUNT(*) FROM favorite_tracks WHERE user_id=$1::uuid`, a) != 1 {
		t.Fatal("other actor changed favorites")
	}
	albums, err := f.service.FavoriteAlbums(ctx, a, Page{Number: 1, Size: 50})
	if err != nil || len(albums.Items) != 1 || albums.Items[0].Album.Available || albums.Items[0].Album.Title != "暂时不可用的专辑" || albums.Items[0].Album.Representative != nil {
		t.Fatalf("empty group projection: %+v %v", albums, err)
	}
}

func TestPostgreSQLFavoritesPaginationCurrentPlacementAndFallback(t *testing.T) {
	f := newPersonalFixture(t)
	f.database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	user := f.user(t)
	release := f.release(t, "保存的专辑")
	ids := []string{f.track(t, release, "甲", 1), f.track(t, release, "乙", 2), f.track(t, release, "丙", 3)}
	sort.Strings(ids)
	for _, id := range ids {
		if _, err := f.service.SetFavorite(ctx, user, "track", id, true); err != nil {
			t.Fatal(err)
		}
	}
	f.exec(t, `UPDATE favorite_tracks SET created_at='2026-11-04T00:00:00Z' WHERE user_id=$1::uuid`, user)
	f.setStatus(t, ids[0], "missing")
	revision := int64(3)
	var seen []string
	for page := 1; page <= 2; page++ {
		got, err := f.service.FavoriteTracks(ctx, user, Page{Number: page, Size: 2, Revision: &revision})
		if err != nil || got.Pagination.Total != 3 || got.Revision != revision {
			t.Fatalf("page: %+v %v", got, err)
		}
		for _, item := range got.Items {
			seen = append(seen, item.Track.TrackID)
		}
		if page == 1 && got.Items[0].Track.Present {
			t.Fatal("missing disappeared")
		}
	}
	if !reflect.DeepEqual(seen, ids) {
		t.Fatalf("order: %v want %v", seen, ids)
	}
	last, err := f.service.FavoriteTracks(ctx, user, Page{Number: 99, Size: 2})
	if err != nil || last.Items == nil || len(last.Items) != 0 || last.Pagination.Total != 3 {
		t.Fatalf("out of range: %+v %v", last, err)
	}
	if _, err := f.service.SetFavorite(ctx, user, "album", release.groupID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.FavoriteTracks(ctx, user, Page{Number: 2, Size: 2, Revision: &revision}); !errors.Is(err, ErrFavoritesChanged) {
		t.Fatalf("stale revision: %v", err)
	}
	// 同一个曲目身份换发行，收藏随读取展示新位置；原组收藏不跟随移动。
	destination := f.release(t, "当前专辑")
	f.exec(t, `UPDATE tracks SET medium_id=$2::uuid WHERE id=$1::uuid`, ids[0], destination.mediumID)
	got, err := f.service.FavoriteTracks(ctx, user, Page{Number: 1, Size: 50})
	if err != nil || got.Items[0].Track.GroupID != destination.groupID || got.Items[0].Track.Release.ID != destination.releaseID {
		t.Fatalf("current placement: %+v %v", got, err)
	}
	f.setStatus(t, ids[0], "present")
	for _, id := range ids[1:] {
		f.setStatus(t, id, "missing")
	}
	albums, err := f.service.FavoriteAlbums(ctx, user, Page{Number: 1, Size: 1})
	if err != nil || albums.Items[0].Album.GroupID != release.groupID || albums.Items[0].Album.Title != "保存的专辑" || albums.Items[0].Album.Available || albums.Items[0].Album.Representative != nil || albums.Items[0].Album.ReleaseCount != 0 {
		t.Fatalf("fallback: %+v %v", albums, err)
	}
	f.setStatus(t, ids[1], "present")
	f.exec(t, `UPDATE releases SET title='更新的专辑' WHERE id=$1::uuid`, release.releaseID)
	albums, err = f.service.FavoriteAlbums(ctx, user, Page{Number: 1, Size: 1})
	if err != nil || !albums.Items[0].Album.Available || albums.Items[0].Album.Title != "更新的专辑" {
		t.Fatalf("restored album: %+v %v", albums, err)
	}
}

func TestPostgreSQLFavoritesConcurrentToggleAndRevisionExhaustion(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	user := f.user(t)
	track := f.track(t, f.release(t, "并发"), "曲目", 1)
	const workers = 8
	start := make(chan struct{})
	results := make(chan FavoriteResult, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := f.service.SetFavorite(ctx, user, "track", track, true)
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var created *time.Time
	for result := range results {
		if result.Revision != 1 || result.CreatedAt == nil {
			t.Fatalf("concurrent result: %+v", result)
		}
		if created != nil && !created.Equal(*result.CreatedAt) {
			t.Fatal("duplicate changed created time")
		}
		created = result.CreatedAt
	}
	if f.count(t, `SELECT COUNT(*) FROM favorite_tracks WHERE user_id=$1::uuid`, user) != 1 {
		t.Fatal("duplicate rows")
	}
	f.exec(t, `UPDATE personal_favorite_state SET revision=$2 WHERE user_id=$1::uuid`, user, MaxRevision)
	if _, err := f.service.SetFavorite(ctx, user, "track", track, false); err == nil || errors.Is(err, ErrInvalidInput) {
		t.Fatalf("overflow did not fail closed: %v", err)
	}
	if got, err := f.service.SetFavorite(ctx, user, "track", track, true); err != nil || got.Revision != MaxRevision {
		t.Fatalf("overflow no-op: %+v %v", got, err)
	}
	if f.count(t, `SELECT COUNT(*) FROM favorite_tracks WHERE user_id=$1::uuid`, user) != 1 {
		t.Fatal("overflow left partial delete")
	}
}

func TestPostgreSQLFavoritesProjectionFailureRollsBack(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	user := f.user(t)
	track := f.track(t, f.release(t, "失败"), "曲目", 1)
	failure := errors.New("synthetic projection failure")
	broken := &Service{database: f.database, catalogFor: func(*sql.Tx) Catalog {
		return projectionCatalog{facts: func(context.Context, []string) (map[string]catalog.TrackFact, error) { return nil, failure }}
	}}
	if _, err := broken.SetFavorite(ctx, user, "track", track, true); !errors.Is(err, failure) {
		t.Fatalf("projection failure: %v", err)
	}
	if f.count(t, `SELECT COUNT(*) FROM personal_favorite_state WHERE user_id=$1::uuid`, user) != 0 || f.count(t, `SELECT COUNT(*) FROM favorite_tracks WHERE user_id=$1::uuid`, user) != 0 {
		t.Fatal("failed write leaked state")
	}
	if _, err := f.service.SetFavorite(ctx, user, "track", track, true); err != nil {
		t.Fatal(err)
	}
	broken.catalogFor = func(*sql.Tx) Catalog {
		return projectionCatalog{summaries: func(context.Context, []string) (map[string]catalog.TrackSummary, error) {
			return map[string]catalog.TrackSummary{}, nil
		}, facts: func(context.Context, []string) (map[string]catalog.TrackFact, error) {
			return map[string]catalog.TrackFact{}, nil
		}}
	}
	if _, err := broken.FavoriteTracks(ctx, user, Page{Number: 1, Size: 50}); err == nil {
		t.Fatal("missing summary discarded")
	}
	if _, err := broken.FavoriteQueue(ctx, user); err == nil {
		t.Fatal("missing fact discarded")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.service.FavoriteQueue(canceled, user); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := f.service.FavoriteQueue(ctx, user); err != nil {
		t.Fatalf("connection after cancel: %v", err)
	}
}
