package personal

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 仅测试夹具构造合成身份与投影；生产 personal 始终经 catalog 公开端口读取。
func (f *personalFixture) artist(t *testing.T, name string) string {
	t.Helper()
	id := uuid.New()
	f.exec(t, `INSERT INTO artists(id,initial_name) VALUES($1::uuid,$2)`, id, name)
	f.exec(t, `INSERT INTO artist_profiles(artist_id,auto_display_name,effective_display_name,sort_key,initial_group,version) VALUES($1::uuid,$2,$2,$2,'#',1)`, id, name)
	return id
}
func (f *personalFixture) artistsReady(t *testing.T) {
	t.Helper()
	f.exec(t, `UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`)
}
func (f *personalFixture) artistRoute(t *testing.T, original, target string) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if original == target {
		_, err = tx.ExecContext(ctx, `DELETE FROM artist_overrides WHERE artist_id=$1::uuid`, original)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO artist_overrides(artist_id,redirect_artist_id) VALUES($1::uuid,$2::uuid) ON CONFLICT(artist_id) DO UPDATE SET redirect_artist_id=EXCLUDED.redirect_artist_id`, original, target)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := RefreshArtistResolution(ctx, tx, map[string]string{original: target}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLArtistFavoritesAnchorsMergeSplitAndPagination(t *testing.T) {
	f := newPersonalFixture(t)
	f.database.SetMaxOpenConns(1)
	ctx := context.Background()
	user, other, newUser := f.user(t), f.user(t), f.user(t)
	a, b, c := f.artist(t, "甲"), f.artist(t, "乙"), f.artist(t, "丙")
	f.artistsReady(t)
	for _, owner := range []string{user, other} {
		for _, id := range []string{a, b, c} {
			if _, err := f.service.SetFavorite(ctx, owner, "artist", id, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	first := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	latest := first.Add(2 * time.Hour)
	f.exec(t, `UPDATE artist_favorites SET created_at=$2 WHERE artist_id=$1::uuid`, a, first)
	f.exec(t, `UPDATE artist_favorites SET created_at=$2 WHERE artist_id=$1::uuid`, b, latest)
	f.exec(t, `UPDATE artist_favorites SET created_at=$2 WHERE artist_id=$1::uuid`, c, first.Add(time.Hour))
	f.artistRoute(t, a, b)
	page, err := f.service.FavoriteArtists(ctx, user, Page{Number: 1, Size: 1})
	if err != nil || page.Pagination.Total != 2 || page.Revision != 4 || len(page.Items) != 1 || page.Items[0].Artist.ID != b || !page.Items[0].CreatedAt.Equal(latest) || page.Items[0].Available || page.Items[0].Artist.Available {
		t.Fatalf("merged first page: %+v %v", page, err)
	}
	second, err := f.service.FavoriteArtists(ctx, user, Page{Number: 2, Size: 1, Revision: &page.Revision})
	if err != nil || second.Pagination.Total != 2 || len(second.Items) != 1 || second.Items[0].Artist.ID != c {
		t.Fatalf("merged second page: %+v %v", second, err)
	}
	empty, err := f.service.FavoriteArtists(ctx, user, Page{Number: 99, Size: 1})
	if err != nil || empty.Pagination.Total != 2 || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("past end: %+v %v", empty, err)
	}
	unknown := uuid.New()
	lookup, err := f.service.LookupFavorites(ctx, user, nil, nil, []string{strings.ToUpper(a), a, b, unknown})
	if err != nil || len(lookup.Artists) != 3 || lookup.Artists[0].ArtistID != a || lookup.Artists[0].ResolvedArtistID == nil || *lookup.Artists[0].ResolvedArtistID != b || !lookup.Artists[0].Favorited || !lookup.Artists[1].Favorited || lookup.Artists[2].ResolvedArtistID != nil || lookup.Artists[2].Favorited {
		t.Fatalf("lookup: %+v %v", lookup, err)
	}
	for _, id := range []string{a, b} {
		result, err := f.service.SetFavorite(ctx, user, "artist", id, true)
		if err != nil || result.TargetID != b || result.Revision != 4 || result.CreatedAt == nil || !result.CreatedAt.Equal(latest) {
			t.Fatalf("equivalent PUT: %+v %v", result, err)
		}
	}
	if f.count(t, `SELECT COUNT(*) FROM artist_favorites WHERE user_id=$1::uuid`, user) != 3 {
		t.Fatal("merge changed anchors")
	}
	added, err := f.service.SetFavorite(ctx, newUser, "artist", a, true)
	if err != nil || added.TargetID != b || added.Revision != 1 {
		t.Fatalf("new canonical favorite: %+v %v", added, err)
	}
	deleted, err := f.service.SetFavorite(ctx, other, "artist", a, false)
	if err != nil || deleted.Favorited || deleted.TargetID != b || deleted.CreatedAt != nil || deleted.Revision != 5 {
		t.Fatalf("equivalent DELETE: %+v %v", deleted, err)
	}
	if f.count(t, `SELECT COUNT(*) FROM artist_favorites WHERE user_id=$1::uuid`, other) != 1 {
		t.Fatal("delete did not remove all equivalent anchors")
	}
	f.artistRoute(t, a, a)
	if _, err := f.service.FavoriteArtists(ctx, user, Page{Number: 1, Size: 1, Revision: &page.Revision}); !errors.Is(err, ErrFavoritesChanged) {
		t.Fatalf("stale page: %v", err)
	}
	for _, tc := range []struct {
		owner           string
		total, revision int64
	}{{user, 3, 5}, {other, 1, 5}, {newUser, 1, 1}} {
		got, err := f.service.FavoriteArtists(ctx, tc.owner, Page{Number: 1, Size: 50})
		if err != nil || got.Pagination.Total != tc.total || got.Revision != tc.revision {
			t.Fatalf("split %s: %+v %v", tc.owner, got, err)
		}
	}
	if _, err := f.service.SetFavorite(ctx, user, "artist", unknown, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown PUT: %v", err)
	}
	noOp, err := f.service.SetFavorite(ctx, user, "artist", unknown, false)
	if err != nil || noOp.TargetID != unknown || noOp.Favorited || noOp.CreatedAt != nil || noOp.Revision != 5 {
		t.Fatalf("unknown DELETE: %+v %v", noOp, err)
	}
	f.exec(t, `UPDATE users SET disabled_at=NOW() WHERE id=$1::uuid`, user)
	if f.count(t, `SELECT COUNT(*) FROM artist_favorites WHERE user_id=$1::uuid`, user) != 3 {
		t.Fatal("disabled user lost favorites")
	}
	f.exec(t, `DELETE FROM users WHERE id=$1::uuid`, user)
	if f.count(t, `SELECT COUNT(*) FROM artist_favorites WHERE user_id=$1::uuid`, user) != 0 {
		t.Fatal("user deletion did not cascade")
	}
}

func TestPostgreSQLArtistFavoritesPreparingAndConcurrentWrites(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	user := f.user(t)
	artist := f.artist(t, "整理中的艺人")
	for _, favorited := range []bool{true, false} {
		if _, err := f.service.SetFavorite(ctx, user, "artist", artist, favorited); !errors.Is(err, ErrArtistCatalogNotReady) {
			t.Fatalf("write preparing: %v", err)
		}
	}
	if f.count(t, `SELECT COUNT(*) FROM personal_favorite_state WHERE user_id=$1::uuid`, user) != 0 {
		t.Fatal("preparing write created state")
	}
	if _, err := f.service.FavoriteArtists(ctx, user, Page{Number: 1, Size: 50}); !errors.Is(err, ErrArtistCatalogPreparing) {
		t.Fatalf("empty preparing list: %v", err)
	}
	if _, err := f.service.LookupFavorites(ctx, user, nil, nil, []string{artist}); !errors.Is(err, ErrArtistCatalogPreparing) {
		t.Fatalf("preparing lookup: %v", err)
	}
	if got, err := f.service.LookupFavorites(ctx, user, nil, nil); err != nil || got.Artists == nil {
		t.Fatalf("legacy lookup blocked: %+v %v", got, err)
	}
	f.artistsReady(t)
	var group sync.WaitGroup
	results := make(chan FavoriteResult, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := f.service.SetFavorite(ctx, user, "artist", artist, true)
			results <- result
			failures <- err
		}()
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *FavoriteResult
	for result := range results {
		if first == nil {
			first = &result
		}
		if !reflect.DeepEqual(*first, result) || result.Revision != 1 {
			t.Fatalf("concurrent PUT: %+v / %+v", first, result)
		}
	}
	if f.count(t, `SELECT COUNT(*) FROM artist_favorites WHERE user_id=$1::uuid`, user) != 1 {
		t.Fatal("concurrent PUT duplicated intent")
	}
	// 等待 CATP 的艺人写不能先占有本人 state，曲目收藏仍可独立前进。
	hold, err := f.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback()
	if err := catalog.AcquireArtistWriteGate(ctx, hold); err != nil {
		t.Fatal(err)
	}
	waitDB, err := sql.Open("pgx", f.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer waitDB.Close()
	waitDB.SetMaxOpenConns(1)
	var pid int
	if err := waitDB.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := NewService(waitDB).SetFavorite(waiting, user, "artist", artist, false)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var blocked bool
		if err := f.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("artist write did not reach CATP wait")
		}
		time.Sleep(time.Millisecond)
	}
	ordinary, ordinaryCancel := context.WithTimeout(ctx, time.Second)
	defer ordinaryCancel()
	if _, err := f.service.SetFavorite(ordinary, user, "track", uuid.New(), false); err != nil {
		t.Fatalf("ordinary favorite blocked: %v", err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("gate wait unexpectedly succeeded")
	}
	if err := hold.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := f.service.LookupFavorites(ctx, user, nil, nil, []string{artist}); err != nil || !got.Artists[0].Favorited || got.Revision != 1 {
		t.Fatalf("cancel changed state: %+v %v", got, err)
	}
}

func TestPostgreSQLArtistResolutionBatchRevisionAndRollback(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	user, other := f.user(t), f.user(t)
	target := f.artist(t, "合并目标")
	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = uuid.New()
	}
	// 合成夹具批量建立强 FK 身份，避免逐条准备大型映射。
	f.exec(t, `INSERT INTO artists(id,initial_name) SELECT id,'原艺人' FROM unnest($1::text[]::uuid[]) AS id`, ids)
	f.exec(t, `INSERT INTO personal_favorite_state(user_id,revision) VALUES($1::uuid,7),($2::uuid,2)`, user, other)
	f.exec(t, `INSERT INTO artist_favorites(user_id,artist_id,resolved_artist_id,created_at) SELECT $1::uuid,id,id,'2026-11-01T00:00:00Z' FROM unnest($2::text[]::uuid[]) AS id`, user, ids)
	f.exec(t, `INSERT INTO artist_favorites(user_id,artist_id,resolved_artist_id) VALUES($1::uuid,$2::uuid,$2::uuid)`, other, ids[1000])
	mapping := map[string]string{}
	for _, id := range ids {
		mapping[id] = target
	}
	refresh := func(mapping map[string]string, commit bool) error {
		tx, err := f.database.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
			return err
		}
		if err := RefreshArtistResolution(ctx, tx, mapping); err != nil {
			return err
		}
		if commit {
			return tx.Commit()
		}
		return nil
	}
	if err := refresh(mapping, false); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT COUNT(*) FROM artist_favorites WHERE artist_id=resolved_artist_id`) != 1002 || f.count(t, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, user) != 7 {
		t.Fatal("caller rollback did not restore projection")
	}
	if err := refresh(mapping, true); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, user) != 8 || f.count(t, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, other) != 3 {
		t.Fatal("revision advanced once per batch instead of once per user")
	}
	if f.count(t, `SELECT COUNT(*) FROM artist_favorites WHERE resolved_artist_id=$1::uuid`, target) != 1002 {
		t.Fatal("incomplete refresh")
	}
	if f.count(t, `SELECT COUNT(*) FROM artist_favorites WHERE user_id=$1::uuid AND created_at='2026-11-01T00:00:00Z'`, user) != 1001 {
		t.Fatal("refresh changed original time")
	}
	if err := refresh(mapping, true); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, user) != 8 {
		t.Fatal("unchanged refresh advanced revision")
	}
	// 派生目标的 FK 失败与版本耗尽都必须使同事务刷新完全回滚。
	if err := refresh(map[string]string{ids[0]: uuid.New()}, true); err == nil {
		t.Fatal("dangling derived reference accepted")
	}
	f.exec(t, `UPDATE personal_favorite_state SET revision=$2 WHERE user_id=$1::uuid`, user, MaxRevision)
	if err := refresh(map[string]string{ids[0]: ids[0]}, true); err == nil {
		t.Fatal("exhausted state accepted")
	}
	if f.count(t, `SELECT COUNT(*) FROM artist_favorites WHERE resolved_artist_id=$1::uuid`, target) != 1002 {
		t.Fatal("failed refresh partially committed")
	}
	// 原锚点和解析目标两类外键都阻止物理删除身份。
	for _, id := range []string{ids[0], target} {
		if _, err := f.database.Exec(`DELETE FROM artists WHERE id=$1::uuid`, id); err == nil {
			t.Fatal("referenced artist deleted")
		}
	}
}

// 编译时保证跨能力端口由真实 catalog.Store 实现，不依赖私有 adapter。
var _ ArtistCatalog = (*catalog.Store)(nil)

func TestPostgreSQLArtistFavoritesSnapshotTiesAndProjectionFailure(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	user := f.user(t)
	a, b := f.artist(t, "甲"), f.artist(t, "乙")
	f.artistsReady(t)
	if _, err := f.service.SetFavorite(ctx, user, "artist", a, true); err != nil {
		t.Fatal(err)
	}
	started, resume := make(chan struct{}), make(chan struct{})
	reader := NewService(f.database)
	reader.artistCatalogFor = func(tx *sql.Tx) ArtistCatalog {
		source := catalog.NewStore(tx)
		return artistCatalogStub{
			resolve: func(ctx context.Context, ids []string) (map[string]string, error) {
				resolved, err := source.ResolveArtists(ctx, ids)
				close(started)
				select {
				case <-resume:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return resolved, err
			},
			summaries: source.LoadArtistSummaries,
		}
	}
	type response struct {
		page FavoriteArtistsPage
		err  error
	}
	done := make(chan response, 1)
	go func() {
		page, err := reader.FavoriteArtists(ctx, user, Page{Number: 1, Size: 50})
		done <- response{page, err}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := f.service.SetFavorite(ctx, user, "artist", b, true); err != nil {
		t.Fatal(err)
	}
	close(resume)
	got := <-done
	if got.err != nil || got.page.Revision != 1 || got.page.Pagination.Total != 1 || len(got.page.Items) != 1 || got.page.Items[0].Artist.ID != a {
		t.Fatalf("snapshot mixed revisions: %+v %v", got.page, got.err)
	}
	f.exec(t, `UPDATE artist_favorites SET created_at='2026-11-01T00:00:00Z' WHERE user_id=$1::uuid`, user)
	first, err := f.service.FavoriteArtists(ctx, user, Page{Number: 1, Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.FavoriteArtists(ctx, user, Page{Number: 2, Size: 1})
	if err != nil || len(first.Items) != 1 || len(second.Items) != 1 || first.Items[0].Artist.ID >= second.Items[0].Artist.ID || first.Pagination.Total != 2 || second.Pagination.Total != 2 {
		t.Fatalf("UUID tie order: %+v %+v %v", first, second, err)
	}
	reader.artistCatalogFor = func(tx *sql.Tx) ArtistCatalog {
		return artistCatalogStub{resolve: catalog.NewStore(tx).ResolveArtists, summaries: func(context.Context, []string) (map[string]catalog.ArtistSummary, error) {
			return map[string]catalog.ArtistSummary{}, nil
		}}
	}
	if _, err := reader.FavoriteArtists(ctx, user, Page{Number: 1, Size: 50}); err == nil {
		t.Fatal("missing safe projection silently removed favorite")
	}
}
