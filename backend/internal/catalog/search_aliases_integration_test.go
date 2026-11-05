package catalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

// 别名与原始字段命中同时存在，拆开来源后仍保留角色、实际发行、完整分页与代表排序。
func TestPostgreSQLSearchAliasSubjectBranches(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	representative := f.release("Z representative")
	f.track(representative, "unmatched", "present", nil)
	release := f.release("A release")
	f.exec(`UPDATE releases SET artist='周杰伦',album_artist='周杰伦',group_id=$2::uuid WHERE id=$1::uuid`, release.releaseID, representative.groupID)
	f.exec(`UPDATE release_groups SET default_release_id=$2::uuid WHERE id=$1::uuid`, representative.groupID, representative.releaseID)
	first := f.track(release, "A first", "present", nil)
	second := f.track(release, "A second", "present", nil)
	f.track(release, "A missing", "missing", nil)
	creditRelease := f.release("B credit")
	credit := f.track(creditRelease, "B first", "present", nil)
	f.track(creditRelease, "B sibling", "present", nil)
	f.exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','周杰倫',1)`, credit)
	missing := f.release("C invisible")
	f.exec(`UPDATE releases SET artist='周杰伦',album_artist='周杰伦' WHERE id=$1::uuid`, missing.releaseID)
	f.track(missing, "C missing", "missing", nil)
	publishArtistReadFixture(t, f)
	postgrestest.AnalyzeSchema(t, ctx, f.database)
	artist := findArtistReadFixture(t, f, "周杰伦")

	for _, tc := range []struct{ query, rank string }{{"周杰倫", "exact"}, {"Jay Chou", "exact"}, {"Jay", "prefix"}, {"Chou", "contains"}} {
		t.Run(tc.query, func(t *testing.T) {
			gotTracks := []string{}
			for page := 1; page <= 4; page++ {
				result, err := f.store.Search(ctx, SearchParams{Query: tc.query, Type: "tracks", Page: page, PageSize: 1})
				if err != nil {
					t.Fatal(err)
				}
				if result.Pagination.Total != 3 || result.Pagination.TotalRelation != "eq" || result.Pagination.HasMore != (page < 3) {
					t.Fatalf("曲目分页：%+v", result.Pagination)
				}
				for _, item := range result.Items.([]SearchTrackItem) {
					gotTracks = append(gotTracks, item.TrackID)
					match := item.Match
					if match.Kind != tc.rank || match.ArtistID == nil || *match.ArtistID != artist {
						t.Fatalf("命中身份或等级：%+v", match)
					}
					if tc.query != "周杰倫" && (match.Field != "artist_alias" || match.Alias == nil || *match.Alias != "Jay Chou") {
						t.Fatalf("别名说明：%+v", match)
					}
					if tc.query == "周杰倫" && match.Alias != nil {
						t.Fatalf("原始字段应优先于别名：%+v", match)
					}
					if item.TrackID == credit && (match.Role == nil || *match.Role != "composer" || match.TrackID == nil || *match.TrackID != credit || match.ReleaseID == nil || *match.ReleaseID != creditRelease.releaseID) {
						t.Fatalf("单轨署名来源：%+v", match)
					}
				}
			}
			if !reflect.DeepEqual(gotTracks, []string{first, second, credit}) {
				t.Fatalf("曲目排序/兄弟传播错误：%v", gotTracks)
			}
			albums := searchFixturePage(t, f.store, "albums", tc.query)
			items := albums.Items.([]SearchAlbumItem)
			if albums.Pagination.Total != 2 || len(items) != 2 || items[0].Group.ID != creditRelease.groupID || items[1].Group.ID != representative.groupID || items[1].Group.Representative.ID != representative.releaseID || items[1].MatchedReleaseID != release.releaseID {
				t.Fatalf("专辑身份/代表/命中来源：%+v", albums)
			}
			for _, attention := range []bool{false, true} {
				want := int64(2)
				if attention {
					want = 1
				}
				params := ListParams{Search: tc.query, Page: 1, PageSize: 50, AttentionRequired: attention}
				releases, err := f.store.ListReleases(ctx, params)
				if err != nil || releases.Total != want || int64(len(releases.Items)) != want {
					t.Fatalf("发行精确计数 attention=%v：%+v %v", attention, releases, err)
				}
				groups, err := f.store.ListReleaseGroups(ctx, params)
				if err != nil || groups.Total != want || int64(len(groups.Items)) != want {
					t.Fatalf("专辑精确计数 attention=%v：%+v %v", attention, groups, err)
				}
			}
		})
	}
}

// 超过原始命中策略阈值后仍能从别名命中遍历深页；同连接重复读取覆盖准备语句复用。
func TestPostgreSQLSearchDenseAliasesKeepDeepPages(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	release := f.release("Album")
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at,source_kind)
 SELECT md5('search-dense-alias:'||i)::uuid,$1::uuid,i,'Track '||lpad(i::text,4,'0'),'周杰伦',$2::uuid,
 'dense-alias/'||i||'.flac','present',now(),'physical' FROM generate_series(1,1500)i`, release.mediumID, f.rootID)
	f.track(release, "missing", "missing", nil)
	publishArtistReadFixture(t, f)
	postgrestest.AnalyzeSchema(t, ctx, f.database)
	f.database.SetMaxOpenConns(1)
	for _, query := range []string{"周杰伦", "Jay Chou"} {
		_, err := searchSnapshot(ctx, f.store, func(snapshot *Store, state CatalogState, generation int64) (bool, error) {
			plan := newSearchPlan(query, "tracks", state.SearchMode, generation)
			plan.artistAliases = true
			plan, err := snapshot.prepareSearchPlan(ctx, plan)
			if err != nil {
				return false, err
			}
			var rawHits int
			probe := plan.withArtistAliases("WITH probe AS (" + rawHitProbeSQL(plan.matchPredicate+" AND ("+normalizedSearchRank+")=0") + ") SELECT n FROM probe")
			if err := snapshot.database.QueryRowContext(ctx, probe, plan.args...).Scan(&rawHits); err != nil {
				return false, err
			}
			if !plan.artistAliases || (query == "周杰伦" && rawHits <= rawHitProbeLimit) || (query == "Jay Chou" && rawHits > rawHitProbeLimit) {
				t.Fatalf("夹具必须覆盖别名的密集/稀疏路径：%s raw=%d aliases=%v", query, rawHits, plan.artistAliases)
			}
			return true, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for attempt := 0; attempt < 7; attempt++ {
			result, err := f.store.Search(ctx, SearchParams{Query: query, Type: "tracks", Page: 30, PageSize: 50})
			if err != nil {
				t.Fatal(err)
			}
			items := result.Items.([]SearchTrackItem)
			if result.Pagination.Total != 1000 || result.Pagination.TotalRelation != "gte" || result.Pagination.HasMore || len(items) != 50 || items[0].Title != "Track 1451" || items[49].Title != "Track 1500" {
				t.Fatalf("复用%d/%s深页：%+v items=%d", attempt, query, result.Pagination, len(items))
			}
		}
		result, err := f.store.Search(ctx, SearchParams{Query: query, Type: "tracks", Page: 31, PageSize: 50})
		if err != nil || result.Pagination.Total != 1000 || result.Pagination.TotalRelation != "gte" || result.Pagination.HasMore || len(result.Items.([]SearchTrackItem)) != 0 {
			t.Fatalf("深空页保留全局下界：%+v %v", result, err)
		}
		groups, err := f.store.ListReleaseGroups(ctx, ListParams{Search: query, Page: 1, PageSize: 50})
		if err != nil || groups.Total != 1 || len(groups.Items) != 1 {
			t.Fatalf("多曲目重复命中只计一组：%+v %v", groups, err)
		}
	}
}
