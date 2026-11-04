package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
)

// 测试专用的显式发布：只有合成夹具使用，不绕过生产后台的完整性校验。
func publishSearchFixture(t *testing.T, fixture *placementFixture) {
	t.Helper()
	ctx := context.Background()
	var releaseIDs, trackIDs []string
	for _, target := range []struct {
		query string
		ids   *[]string
	}{{"SELECT id::text FROM releases", &releaseIDs}, {"SELECT id::text FROM tracks", &trackIDs}} {
		rows, err := fixture.database.QueryContext(ctx, target.query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			*target.ids = append(*target.ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	tx, err := fixture.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, id := range releaseIDs {
		if err := searchindex.RefreshRelease(ctx, tx, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range trackIDs {
		if err := searchindex.RefreshTrack(ctx, tx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,
	 published_generation=target_generation,published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version,catalog_epoch=catalog_epoch+1 WHERE capability='search'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func searchFixturePage(t *testing.T, store *Store, kind, query string) SearchResult {
	t.Helper()
	result, err := store.Search(context.Background(), SearchParams{Type: kind, Query: query, Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("search %s/%q: %v", kind, query, err)
	}
	return result
}

func TestPostgreSQLSearchLegacyAndNormalizedPropagation(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	representative := f.release("ZZ 代表")
	f.track(representative, "代表曲目", "present", 120)
	member := f.release("繁體聲音")
	f.exec(`UPDATE releases SET group_id=$1::uuid,label='廠牌',catalog_number='SRCL-1234' WHERE id=$2::uuid`, representative.groupID, member.releaseID)
	f.exec(`UPDATE release_groups SET default_release_id=$1::uuid WHERE id=$2::uuid`, representative.releaseID, representative.groupID)
	track := f.track(member, "Beyoncé ＡＢＣ サユリ", "present", 183.5)
	sibling := f.track(member, "另一首", "present", nil)
	missing := f.track(member, "隐藏词", "missing", 100)
	f.exec(`UPDATE tracks SET artist='周杰倫',codec='flac',bit_depth=16,sample_rate=44100,channels=2 WHERE id=$1::uuid`, track)
	for _, role := range []string{"composer", "conductor", "performer", "producer"} {
		f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,$2,$3,1)`, track, role, "角色"+role)
	}
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','缺失作曲',1)`, missing)
	legacy := searchFixturePage(t, f.store, "tracks", "周杰倫")
	if legacy.CatalogState.SearchMode != "legacy" || legacy.Pagination.Total != 1 || legacy.Items.([]SearchTrackItem)[0].Match.Field != "track_artist" {
		t.Fatalf("legacy tracks: %+v", legacy)
	}
	for _, q := range []string{"周杰倫", "廠牌", "SRCL1234", "角色composer", "隐藏词", "缺失作曲"} {
		if got := searchFixturePage(t, f.store, "albums", q); got.Pagination.Total != 0 {
			t.Errorf("legacy albums expanded %s: %+v", q, got)
		}
	}
	publishSearchFixture(t, f)
	for _, q := range []string{"beyonce", "abc", "さゆり", "周杰伦"} {
		got := searchFixturePage(t, f.store, "tracks", q)
		items := got.Items.([]SearchTrackItem)
		if got.CatalogState.SearchMode != "normalized" || len(items) != 1 || items[0].TrackID != track || items[0].Source.Codec == nil || *items[0].Source.Codec != "flac" || items[0].DurationSeconds == nil || *items[0].DurationSeconds != 183.5 {
			t.Fatalf("normalized %s: %+v", q, got)
		}
	}
	for _, role := range []string{"composer", "conductor", "performer", "producer"} {
		q := "角色" + role
		got := searchFixturePage(t, f.store, "tracks", q)
		items := got.Items.([]SearchTrackItem)
		if len(items) != 1 || items[0].TrackID != track || items[0].Match.Role == nil || *items[0].Match.Role != role || items[0].Match.Original != q || items[0].Match.Kind != "exact" {
			t.Fatalf("credit propagated to sibling %s: %+v", sibling, got)
		}
		albums := searchFixturePage(t, f.store, "albums", q).Items.([]SearchAlbumItem)
		if len(albums) != 1 || albums[0].Group.ID != representative.groupID || albums[0].Group.Representative.ID != representative.releaseID || albums[0].Group.ReleaseCount != 2 || albums[0].MatchedReleaseID != member.releaseID {
			t.Fatalf("wrong representative/matched member: %+v", albums)
		}
	}
	for _, q := range []string{"厂牌", "srcl1234", "srcl 1234", "繁体声音"} {
		got := searchFixturePage(t, f.store, "tracks", q)
		if got.Pagination.Total != 2 {
			t.Fatalf("release field %s must reach both present tracks: %+v", q, got)
		}
	}
	for _, q := range []string{"隐藏词", "缺失作曲"} {
		for _, kind := range []string{"albums", "tracks"} {
			if got := searchFixturePage(t, f.store, kind, q); got.Pagination.Total != 0 {
				t.Fatalf("missing leak: %+v", got)
			}
		}
	}
	suggestions, err := f.store.SearchSuggestions(ctx, "艺术", 1)
	if err != nil || len(suggestions.Albums.Items) != 1 || !suggestions.Tracks.HasMore || len(suggestions.Artists.Items) != 0 || !reflect.DeepEqual(suggestions.AvailableTypes, []string{"albums", "tracks"}) {
		t.Fatalf("suggestions: %+v %v", suggestions, err)
	}
	// 安全摘要出错不能静默丢曲目、返回半页或泄露路径。
	f.artwork(member, "../unsafe.jpg")
	if _, err := f.store.Search(ctx, SearchParams{Type: "tracks", Query: "beyonce", Page: 1, PageSize: 50}); !errors.Is(err, ErrQuery) {
		t.Fatalf("unsafe projection: %v", err)
	}
}

func TestPostgreSQLSearchRankingLegacyListsAndEmpty(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	var releases []placementRelease
	for _, title := range []string{"aecho", "Echoes", "ECHO"} {
		r := f.release(title)
		f.track(r, title, "present", nil)
		releases = append(releases, r)
	}
	literal := f.release("100%_~")
	f.track(literal, "100%_~", "present", nil)
	before, err := f.store.ListReleases(ctx, ListParams{Page: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	legacy := searchFixturePage(t, f.store, "albums", "echo").Items.([]SearchAlbumItem)
	if legacy[0].Group.Representative.ID != releases[0].releaseID {
		t.Fatal("legacy order changed")
	}
	for _, q := range []string{"%", "_", "~"} {
		if got := searchFixturePage(t, f.store, "tracks", q); got.Pagination.Total != 1 {
			t.Fatalf("wildcard %s: %+v", q, got)
		}
	}
	publishSearchFixture(t, f)
	for _, kind := range []string{"albums", "tracks"} {
		got := searchFixturePage(t, f.store, kind, "echo")
		data, _ := json.Marshal(got.Items)
		var items []struct {
			Match SearchMatch `json:"match"`
		}
		if err := json.Unmarshal(data, &items); err != nil {
			t.Fatal(err)
		}
		if len(items) != 3 || items[0].Match.Kind != "exact" || items[1].Match.Kind != "prefix" || items[2].Match.Kind != "contains" {
			t.Fatalf("rank %s: %s", kind, data)
		}
		if got := searchFixturePage(t, f.store, kind, ""); got.Pagination.Total != 0 || got.Pagination.TotalRelation != "eq" || got.Pagination.HasMore {
			t.Fatalf("empty query: %+v", got)
		}
	}
	for _, q := range []string{"%", "_", "~"} {
		if got := searchFixturePage(t, f.store, "tracks", q); got.Pagination.Total != 1 {
			t.Fatalf("normalized wildcard %s: %+v", q, got)
		}
	}
	listed, err := f.store.ListReleases(ctx, ListParams{Search: "echo", Page: 1, PageSize: 100})
	if err != nil || listed.Total != 3 || listed.Items[0].ID != releases[2].releaseID {
		t.Fatalf("legacy release route relevance: %+v %v", listed, err)
	}
	groups, err := f.store.ListReleaseGroups(ctx, ListParams{Search: "echo", Page: 1, PageSize: 100})
	if err != nil || groups.Total != 3 || groups.Items[0].Representative.ID != releases[2].releaseID {
		t.Fatalf("legacy group route relevance: %+v %v", groups, err)
	}
	after, err := f.store.ListReleases(ctx, ListParams{Page: 1, PageSize: 100})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("no-query browsing changed: %+v %+v %v", before, after, err)
	}
	if _, err := f.store.Search(ctx, SearchParams{Type: "artists", Page: 1, PageSize: 50}); !errors.Is(err, ErrUnsupportedSearchType) {
		t.Fatal(err)
	}
}

func TestPostgreSQLSearchBoundedCountAndBeyondThousand(t *testing.T) {
	f := newPlacementFixture(t)
	r := f.release("needle")
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind)
	 SELECT gen_random_uuid(),$1::uuid,n,'needle '||n,'needle',1,$2::uuid,'fixture/'||n||'.flac','present',NOW(),'physical' FROM generate_series(1,1001) n`, r.mediumID, f.rootID)
	publishSearchFixture(t, f)
	ctx := context.Background()
	for _, n := range []int{1001, 1000, 999} {
		f.exec(`UPDATE tracks SET source_status=CASE WHEN position<=$1 THEN 'present' ELSE 'missing' END WHERE medium_id=$2::uuid`, n, r.mediumID)
		for _, page := range []int{1, 21, 99} {
			got, err := f.store.Search(ctx, SearchParams{Type: "tracks", Query: "needle", Page: page, PageSize: 50})
			if err != nil {
				t.Fatal(err)
			}
			want, relation := boundedSearchTotal(int64(n))
			items := got.Items.([]SearchTrackItem)
			wantMore := (page * 50) < n
			if got.Pagination.Total != want || got.Pagination.TotalRelation != relation || got.Pagination.HasMore != wantMore {
				t.Fatalf("%d/%d: %+v", n, page, got.Pagination)
			}
			if page == 21 && len(items) != max(n-1000, 0) {
				t.Fatalf("page beyond 1000: %d", len(items))
			}
			if page == 99 && len(items) != 0 {
				t.Fatal("out-of-range page not empty")
			}
		}
	}
	got := searchFixturePage(t, f.store, "albums", "needle")
	if got.Pagination.Total != 1 {
		t.Fatalf("album dedup after track/release propagation: %+v", got)
	}
}

func TestPostgreSQLSearchSnapshotPublicationAndFacts(t *testing.T) {
	f := newPlacementFixture(t)
	r := f.release("before")
	track := f.track(r, "before", "present", 100)
	ctx := context.Background()
	var poolJIT string
	if err := f.database.QueryRowContext(ctx, `SHOW jit`).Scan(&poolJIT); err != nil {
		t.Fatal(err)
	}
	_, err := searchSnapshot(ctx, f.store, func(snapshot *Store, state CatalogState, generation int64) (bool, error) {
		if state.SearchMode != "legacy" {
			return false, fmt.Errorf("unexpected initial state")
		}
		// 另一连接提交发布和事实变更；页、计数、摘要仍属于状态读取时的旧快照。
		publishSearchFixture(t, f)
		f.exec(`UPDATE tracks SET title='after',source_status='missing' WHERE id=$1::uuid`, track)
		f.exec(`UPDATE releases SET title='after' WHERE id=$1::uuid`, r.releaseID)
		plan := newSearchPlan("before", "tracks", state.SearchMode, generation)
		count, err := snapshot.countSearch(ctx, plan, true)
		if err != nil {
			return false, err
		}
		hits, _, err := snapshot.searchPage(ctx, plan, 50, 0)
		if err != nil {
			return false, err
		}
		items, err := snapshot.completeSearch(ctx, "tracks", hits)
		if err != nil {
			return false, err
		}
		if count != 1 || len(hits) != 1 || items.([]SearchTrackItem)[0].Title != "before" || items.([]SearchTrackItem)[0].Release.Title != "before" {
			return false, fmt.Errorf("mixed snapshot: %d %+v", count, items)
		}
		var isolation string
		if err := snapshot.database.QueryRowContext(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil {
			return false, err
		}
		if isolation != "repeatable read" {
			return false, fmt.Errorf("isolation: %s", isolation)
		}
		var jit string
		if err := snapshot.database.QueryRowContext(ctx, `SHOW jit`).Scan(&jit); err != nil {
			return false, err
		}
		if jit != "off" {
			return false, fmt.Errorf("search snapshot JIT = %q, want off", jit)
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var poolJITAfter string
	if err := f.database.QueryRowContext(ctx, `SHOW jit`).Scan(&poolJITAfter); err != nil {
		t.Fatal(err)
	}
	if poolJITAfter != poolJIT {
		t.Fatalf("SET LOCAL changed pooled-session default: before=%q after=%q", poolJIT, poolJITAfter)
	}
	state, err := f.store.LoadCatalogState(ctx)
	if err != nil || state.SearchMode != "normalized" {
		t.Fatalf("next snapshot state: %+v %v", state, err)
	}
	if got := searchFixturePage(t, f.store, "tracks", "before"); got.Pagination.Total != 0 {
		t.Fatalf("stale present track: %+v", got)
	}
	// 真正的数据库错误不能冒充正常回填状态。
	f.exec(`DROP TABLE catalog_projection_state`)
	if _, err := f.store.LoadCatalogState(ctx); !errors.Is(err, ErrQuery) && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("state failure: %v", err)
	}
}

func TestPostgreSQLSearchEffectiveFieldsAndGroupAttention(t *testing.T) {
	f := newEffectiveFixture(t)
	ctx := context.Background()
	representative := f.visibleRelease("代表")
	member := f.visibleRelease("旧标题")
	f.exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, representative.groupID, member.releaseID)
	f.exec(`UPDATE release_groups SET default_release_id=$1::uuid WHERE id=$2::uuid`, representative.releaseID, representative.groupID)
	f.decision(representative.releaseID, "source_type", "uncertain_apply")
	f.correct(member.releaseID, "title", "修正标题")
	f.correct(member.releaseID, "album_artist", "修正艺人")
	for _, query := range []string{"修正标题", "修正艺人"} {
		if got := searchFixturePage(t, f.store, "albums", query); got.Pagination.Total != 1 {
			t.Fatalf("legacy effective %s: %+v", query, got)
		}
	}
	publishSearchFixture(t, f.placementFixture)
	for _, query := range []string{"修正标题", "修正艺人"} {
		result, err := f.store.ListReleaseGroups(ctx, ListParams{Search: query, AttentionRequired: true, Page: 1, PageSize: 50})
		if err != nil || result.Total != 1 || len(result.Items) != 1 || result.Items[0].AttentionCount != 1 || result.Items[0].Representative.ID != representative.releaseID {
			t.Fatalf("nonmatching member attention: %+v %v", result, err)
		}
		// 版本列表只对命中的版本求 attention，不借用同组其他版本的问题。
		releases, err := f.store.ListReleases(ctx, ListParams{Search: query, AttentionRequired: true, Page: 1, PageSize: 50})
		if err != nil || releases.Total != 0 || len(releases.Items) != 0 {
			t.Fatalf("release attention leaked: %+v %v", releases, err)
		}
		if got := searchFixturePage(t, f.store, "tracks", query); got.Pagination.Total != 1 {
			t.Fatalf("normalized effective %s: %+v", query, got)
		}
	}
	if got := searchFixturePage(t, f.store, "albums", "旧标题"); got.Pagination.Total != 0 {
		t.Fatalf("scanned value leaked: %+v", got)
	}
}
